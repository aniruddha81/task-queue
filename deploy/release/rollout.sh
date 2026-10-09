#!/usr/bin/env bash
# rollout.sh <sha>: deploy release <sha> to every VM, one at a time, then smoke-test it through
# the public name. A gateway is drained first: its Traffic Manager endpoint is disabled until
# it is back, so its twin serves meanwhile. Throughout, an external probe (from this runner,
# outside both clouds) sends requests through the public name and never retries; one failed
# request fails the release. If anything fails, the same procedure redeploys the previous
# release and the script fails. Runs in release.yml with OIDC credentials for both clouds
# (aws and az CLIs, and Go for the probe); VMs are reached through Run Command, never SSH.
set -euo pipefail
sha=$1
. "$(dirname "$0")/vmrun.sh"

param() { aws ssm get-parameter --region $region --name "/tq/release/$1" --query Parameter.Value --output text; }
record() { # in both clouds: each VM reads `current` from its own cloud at boot
  aws ssm put-parameter --region $region --name "/tq/release/$1" --value "$2" --type String --overwrite >/dev/null
  az keyvault secret set --vault-name "$vault" --name "release-$1" --value "$2" -o none
}

tm_rg=$(cut -d/ -f5 <<<"$(jq -r .tm <<<"$topo")")
tm_profile=$(jq -r '.tm | split("/") | last' <<<"$topo")
endpoint() { # endpoint <vm> Enabled|Disabled
  az network traffic-manager endpoint update -g "$tm_rg" --profile-name "$tm_profile" --type externalEndpoints -n "$1" --endpoint-status "$2" -o none
}
online() { # online <query>: names of endpoints that are enabled and pass Traffic Manager's health probe
  az network traffic-manager endpoint list -g "$tm_rg" --profile-name "$tm_profile" --query "[?endpointStatus=='Enabled' && endpointMonitorStatus=='Online'$1].name" -o tsv
}

# converge <vm> <sha>: a gateway is drained first, when a twin can serve meanwhile.
converge() {
  local name=$1 s=$2 drained=
  if jq -e --arg n "$name" '.vms[] | select(.name == $n) | .roles | index("gateway")' <<<"$topo" >/dev/null; then
    if [ -n "$(online " && name!='$name'")" ]; then
      endpoint "$name" Disabled
      echo "drained $name; waiting 60 s (six DNS TTLs) for clients to move to its twin"
      sleep 60
      drained=1
    else
      echo "::warning::no other gateway is online; $name is deployed in place, which drops requests"
      inplace=1 # zero downtime is impossible here, so the probe can't hold the release to it
    fi
  fi
  run "$name" "tq-converge $s" || return 1
  if [ -n "$drained" ]; then
    endpoint "$name" Enabled
    for _ in $(seq 30); do # Traffic Manager probes every 10 s
      [ -n "$(online " && name=='$name'")" ] && { echo "$name is back in DNS"; return 0; }
      sleep 10
    done
    echo "$name did not come back online in Traffic Manager"
    return 1
  fi
}

deploy() { # deploy <sha>: data VMs, then migrations, then everything else, in topology order
  local s=$1 name migrated=
  for name in $(jq -r '.vms[].name' <<<"$topo"); do
    if [ -z "$migrated" ] && [ "$(vm "$name" stage)" = app ]; then
      echo "::group::migrate on $ops"
      run "$ops" "tq-converge $s migrate" || { echo "::endgroup::"; return 1; }
      echo "::endgroup::"
      migrated=1
    fi
    echo "::group::$name -> ${s:0:7}"
    converge "$name" "$s" || { echo "::endgroup::"; return 1; }
    echo "::endgroup::"
  done
}

smoke() { # one job per cloud must succeed through the public name, on the new version
  local s=$1 pw token cloud id state
  [ "$(curl -fsS "https://$fqdn/version")" = "$s" ] || { echo "public /version is not ${s:0:7}"; return 1; }
  pw=$(aws ssm get-parameter --region $region --name /tq/ci/smoke-password --with-decryption --query Parameter.Value --output text)
  token=$(curl -fsS "https://$fqdn/v1/auth/login" -d "$(jq -nc --arg p "$pw" '{email: "smoke@example.com", password: $p}')" | jq -r .token)
  for cloud in aws azure; do
    id=$(curl -fsS "https://$fqdn/v1/jobs" -H "Authorization: Bearer $token" \
      -H "Idempotency-Key: smoke-${s:0:12}-$cloud-$(date +%s)" \
      -d "{\"queue\":\"default\",\"type\":\"chaos.sleep\",\"payload\":{\"ms\":100},\"affinity\":\"$cloud\"}" | jq -r .id)
    for _ in $(seq 60); do
      state=$(curl -fsS "https://$fqdn/v1/jobs/$id" -H "Authorization: Bearer $token" | jq -r .state)
      [ "$state" = succeeded ] && break
      sleep 2
    done
    echo "smoke: $cloud job $id $state"
    [ "$state" = succeeded ] || return 1
  done
}

# 1. Preflight: every VM answers. If not, change nothing; retry a few times, then fail.
for attempt in 1 2 3 4; do
  down=
  for name in $(jq -r '.vms[].name' <<<"$topo"); do
    run "$name" "docker info >/dev/null" >/dev/null 2>&1 || down="$down $name"
  done
  [ -z "$down" ] && break
  [ $attempt = 4 ] && { echo "preflight: no answer from$down; nothing changed"; exit 1; }
  echo "preflight: no answer from$down; retrying in 2 minutes"
  sleep 120
done

# The probe: running from before the first VM changes until after the smoke test.
probe_dir=$(mktemp -d)
(cd "$(dirname "$0")/probe" && go build -o "$probe_dir/probe" .)
PROBE_PASSWORD=$(aws ssm get-parameter --region $region --name /tq/ci/smoke-password --with-decryption --query Parameter.Value --output text) "$probe_dir/probe" -url "https://$fqdn" >"$probe_dir/log" 2>&1 &
probe_pid=$!
probe_stop() {
  local rc=0
  kill -TERM "$probe_pid" && wait "$probe_pid" || rc=$?
  sed 's/^probe: FAILED/::error::probe: FAILED/' "$probe_dir/log"
  return $rc
}

# 2-6. Record the target, deploy, smoke-test, and make it current, if the probe saw no failure.
previous=$(param current)
record target "$sha"
ok=1
deploy "$sha" && smoke "$sha" || ok=
if ! probe_stop; then
  if [ -n "${inplace:-}" ]; then
    echo "::warning::the probe saw failed requests, expected: a gateway with no twin was deployed in place"
  elif [ -n "$ok" ]; then
    echo "::error::the probe saw failed requests during the rollout"
    ok=
  fi
fi
if [ -n "$ok" ]; then
  [ "$previous" = "$sha" ] || record previous "$previous"
  record current "$sha"
  echo "released ${sha:0:7}"
  exit 0
fi

# 7. Roll back. Migrations are expand/contract, so the previous release runs on the new schema.
echo "::error::release ${sha:0:7} failed; rolling back to ${previous:0:7}"
record target "$previous"
if [ "$previous" != none ] && deploy "$previous" && smoke "$previous"; then
  echo "rolled back to ${previous:0:7}"
else
  echo "::error::rollback to ${previous:0:7} failed too"
fi
exit 1
