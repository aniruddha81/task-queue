#!/usr/bin/env bash
# rollout.sh <sha>: deploy release <sha> to every VM, one at a time, then smoke-test it through
# the public name. If any step fails, the same procedure redeploys the previous release and
# the script fails. Runs in release.yml with OIDC credentials for both clouds (aws and az
# CLIs); VMs are reached through AWS Run Command and Azure Run Command, never SSH.
set -euo pipefail
sha=$1
region=ap-south-1 # where the release record and topology live

topo=$(aws ssm get-parameter --region $region --name /tq/release/topology --query Parameter.Value --output text)
fqdn=$(jq -r .fqdn <<<"$topo")
vault=$(jq -r .vault <<<"$topo")
ops=$(jq -r .ops <<<"$topo")
vm() { jq -r --arg n "$1" ".vms[] | select(.name == \$n) | .$2" <<<"$topo"; }

param() { aws ssm get-parameter --region $region --name "/tq/release/$1" --query Parameter.Value --output text; }
record() { # in both clouds: each VM reads `current` from its own cloud at boot
  aws ssm put-parameter --region $region --name "/tq/release/$1" --value "$2" --type String --overwrite >/dev/null
  az keyvault secret set --vault-name "$vault" --name "release-$1" --value "$2" -o none
}

# run <vm> <command>: run a shell command as root on a VM; fails if the command fails.
run() {
  local name=$1 cmd=$2 id cid status
  id=$(vm "$name" id)
  if [ "$(vm "$name" cloud)" = aws ]; then
    local r
    r=$(vm "$name" region)
    cid=$(aws ssm send-command --region "$r" --instance-ids "$id" --document-name AWS-RunShellScript \
      --parameters "$(jq -nc --arg c "$cmd" '{commands: [$c], executionTimeout: ["900"]}')" \
      --query Command.CommandId --output text)
    while :; do
      sleep 5
      status=$(aws ssm get-command-invocation --region "$r" --command-id "$cid" --instance-id "$id" --query Status --output text 2>/dev/null || echo Pending)
      case $status in Pending | InProgress | Delayed) ;; *) break ;; esac
    done
    aws ssm get-command-invocation --region "$r" --command-id "$cid" --instance-id "$id" \
      --query '[StandardOutputContent, StandardErrorContent]' --output text
    [ "$status" = Success ]
  else
    # Run Command doesn't return the script's exit status, so success prints a marker.
    local out
    out=$(az vm run-command invoke -g "$(vm "$name" rg)" -n "$id" --command-id RunShellScript \
      --scripts "$cmd && echo TQ-RUN-OK" --query 'value[0].message' -o tsv)
    echo "$out"
    grep -q TQ-RUN-OK <<<"$out"
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
    run "$name" "tq-converge $s" || { echo "::endgroup::"; return 1; }
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

# 2-6. Record the target, deploy, smoke-test, and make it current.
previous=$(param current)
record target "$sha"
if deploy "$sha" && smoke "$sha"; then
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
