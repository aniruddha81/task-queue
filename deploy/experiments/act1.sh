#!/usr/bin/env bash
# act1.sh <scenario>: one Act 1 fragility experiment (week 11), run from an admin machine
# signed in to the aws and az CLIs (with jq).
#
#   cut-clouds     drop all mesh traffic between Azure and AWS for 2 minutes
#   stop-azure     stop every Azure VM for 2 minutes
#   stop-aws       stop every AWS VM except ops-a (never faulted) for 2 minutes
#   kill-gateway   kill the gateway; it stays down for 1 minute
#
# The chaos harness runs on ops-a with the scenario's name and no faults of its own: load
# through the public name, then quiesce, then the checker. 90 s into the load this script
# applies the scenario, and reverts it after the hold. Faults applied on a VM revert
# themselves (systemd-run timers), so a crashed script can't leave one behind. Meanwhile a
# probe outside both clouds records what the public entry still serves. Results: the checker's
# report in docs/results/, the probe's timeline beside it.
#
# HARNESS_SHA picks the harness code (default: HEAD; it must be on GitHub).
set -euo pipefail
cd "$(dirname "$0")/../.."
scenario=${1:?usage: act1.sh cut-clouds|stop-azure|stop-aws|kill-gateway}
. deploy/release/vmrun.sh
sha=${HARNESS_SHA:-$(git rev-parse HEAD)}
hold=120
aws_vms=$(jq -r --arg ops "$ops" '.vms[] | select(.cloud == "aws" and .name != $ops) | .name' <<<"$topo")
azure_vms=$(jq -r '.vms[] | select(.cloud == "azure") | .name' <<<"$topo")
gateways=$(jq -r '.vms[] | select(.roles | index("gateway")) | .name' <<<"$topo")
azure_ids() { az vm list -g "$(vm svc-z rg)" --query '[].id' -o tsv; }
results=docs/results
probe_log=$(mktemp)
log() { echo "$(date -u +%T) $*"; }

# ---------- the harness, on ops-a ----------
harness='set -e
. /etc/tq/tq.env
d=/opt/tq-harness/@SHA@
if [ ! -d $d ]; then
  rm -rf $d.tmp && mkdir -p $d.tmp
  curl -fsSL https://codeload.github.com/aniruddha81/task-queue/tar.gz/@SHA@ | tar xz -C $d.tmp --strip-components=1
  mv $d.tmp $d
fi
mkdir -p /opt/tq-harness/results
docker rm -f torture >/dev/null 2>&1 || true
docker run -d --name torture --network host -w /src -v $d:/src:ro -v tq-gomod:/go/pkg/mod -v tq-gocache:/root/.cache/go-build \
  -v /etc/tq/certs:/certs:ro -v /opt/tq-harness/results:/out golang:1.27 \
  go run ./cmd/torture -gateway https://@FQDN@ -ca "" -password "$HARNESS_PASSWORD" \
    -jobs-db "$JOBS_DB_URL" -sinks-db "$SINKS_DB_URL" -mailpit http://localhost:8025 -webhook https://@OPS@:8090/webhook \
    -faults=false -scenario @SCENARIO@ -duration 6m -quiesce 10m -out /out'
harness=${harness//@SHA@/$sha}
harness=${harness//@FQDN@/$fqdn}
harness=${harness//@OPS@/$ops}
harness=${harness//@SCENARIO@/$scenario}

# ---------- scenarios ----------
on_all() { # on_all <vms> <command>: in parallel; fails if any fails
  local pids=() v
  for v in $1; do
    run "$v" "$2" >/dev/null &
    pids+=($!)
  done
  for p in "${pids[@]}"; do wait "$p"; done
}

apply() {
  case $scenario in
  cut-clouds)
    # On each Azure VM, drop mesh traffic to and from every AWS VM; a timer removes the rules.
    local hosts cmd
    hosts=$(jq -r '[.vms[] | select(.cloud == "aws") | .name] | join(" ")' <<<"$topo")
    # Run Command uses plain sh: the cut is a script file, applied with -I, reverted with -D.
    cmd='cat >/run/tq-cut <<"CUT"
for h in @HOSTS@; do
  ip=$(getent hosts $h | cut -d" " -f1)
  iptables $1 INPUT -i wg0 -s $ip -j DROP
  iptables $1 OUTPUT -o wg0 -d $ip -j DROP
done
CUT
sh /run/tq-cut -I
systemctl reset-failed tq-fault-revert 2>/dev/null || true
systemd-run --on-active=@HOLD@s --unit tq-fault-revert sh /run/tq-cut -D'
    cmd=${cmd//@HOSTS@/$hosts}
    on_all "$azure_vms" "${cmd//@HOLD@/$hold}"
    ;;
  stop-azure) az vm stop --ids $(azure_ids) -o none ;;
  stop-aws)
    aws ec2 stop-instances --region "$region" --instance-ids $(for v in $aws_vms; do vm "$v" id; done) --output text >/dev/null
    aws ec2 wait instance-stopped --region "$region" --instance-ids $(for v in $aws_vms; do vm "$v" id; done)
    ;;
  kill-gateway)
    on_all "$gateways" "docker update --restart=no tq-gateway-gateway-1 >/dev/null && docker kill tq-gateway-gateway-1 >/dev/null
      systemctl reset-failed tq-fault-revert 2>/dev/null || true
      systemd-run --on-active=60s --unit tq-fault-revert sh -c 'docker update --restart=unless-stopped tq-gateway-gateway-1 && docker start tq-gateway-gateway-1'"
    ;;
  *) echo "unknown scenario $scenario" >&2; exit 2 ;;
  esac
}

revert() { # VM-side faults revert themselves; stopped VMs are started here
  case $scenario in
  stop-azure) az vm start --ids $(azure_ids) -o none ;;
  stop-aws) aws ec2 start-instances --region "$region" --instance-ids $(for v in $aws_vms; do vm "$v" id; done) --output text >/dev/null ;;
  esac
}

# ---------- the probe: what an outside client sees, every 2 s ----------
probe() {
  local pw token t v l s n=0
  pw=$(aws ssm get-parameter --region "$region" --name /tq/ci/smoke-password --with-decryption --query Parameter.Value --output text)
  login() { curl -s -o /tmp/tq-probe-login -w '%{http_code}' --max-time 5 "https://$fqdn/v1/auth/login" -d "{\"email\":\"smoke@example.com\",\"password\":\"$pw\"}"; }
  login >/dev/null && token=$(jq -r .token </tmp/tq-probe-login)
  while :; do
    t=$(date -u +%s)
    v=$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 "https://$fqdn/version")
    l=-
    if [ $((n % 5)) = 0 ]; then l=$(login); fi # the login limiter allows one every 2 s
    s=$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 "https://$fqdn/v1/jobs" -H "Authorization: Bearer $token" \
      -H "Idempotency-Key: probe-$scenario-$t-$n" -d '{"queue":"default","type":"chaos.sleep","payload":{"ms":10}}')
    echo "$t $v $l $s" >>"$probe_log"
    n=$((n + 1))
    sleep 2
  done
}

# ---------- run ----------
log "harness $(git rev-parse --short "$sha") on $ops, scenario $scenario"
run "$ops" "$harness" >/dev/null
until run "$ops" "docker logs torture 2>&1 | grep -q 'of load at'" >/dev/null 2>&1; do sleep 10; done
load_start=$(date -u +%s)
log "load started; probing"
probe &
probe_pid=$!
trap 'kill $probe_pid 2>/dev/null || true' EXIT

sleep 90
fault_start=$(date -u +%s)
log "apply $scenario"
apply
log "applied; holding ${hold}s"
sleep $hold
revert
fault_end=$(date -u +%s)
log "reverted"

until [ "$(run "$ops" "docker inspect -f '{{.State.Status}}' torture" 2>/dev/null | head -1 | tr -d '[:space:]')" = exited ]; do sleep 20; done
kill $probe_pid 2>/dev/null || true
code=$(run "$ops" "docker inspect -f '{{.State.ExitCode}}' torture" | head -1 | tr -d '[:space:]')
report=$(run "$ops" "ls -t /opt/tq-harness/results/*-$scenario.md | head -1" | head -1 | tr -d '[:space:]')
run "$ops" "cat $report" | sed '/^\s*$/N;/^\s*\n$/D' >"$results/$(basename "$report")"
log "checker exit $code; report $results/$(basename "$report")"

# The probe's timeline: share of 2xx answers before, during and after the fault window.
{
  echo "# Probe: $scenario"
  echo
  echo "An outside client, every 2 s, through https://$fqdn: \`/version\` (the gateway), login every 10 s (auth), and a submit (jobs and the database). Fault applied at +$((fault_start - load_start))s and reverted at +$((fault_end - load_start))s from the start of load."
  echo
  echo "| Window | /version | login | submit |"
  echo "| --- | --- | --- | --- |"
  awk -v a="$fault_start" -v b="$fault_end" '
    function w(t) { return t < a ? "before" : (t < b ? "during" : "after") }
    { k = w($1); for (i = 2; i <= 4; i++) if ($i != "-") { n[k, i]++; if ($i ~ /^2/) ok[k, i]++ } }
    END { split("before during after", ws, " ")
      for (j = 1; j <= 3; j++) { k = ws[j]; printf "| %s |", k
        for (i = 2; i <= 4; i++) printf " %d/%d |", ok[k, i], n[k, i]; print "" } }' "$probe_log"
  echo
  echo "First and last failed submit, relative to the fault: $(awk -v a="$fault_start" '$4 !~ /^2/ { if (!f) f = $1; l = $1 } END { if (f) printf "+%ds … +%ds", f - a, l - a; else printf "none" }' "$probe_log")."
} >"$results/act1-$scenario-probe.md"
log "probe summary $results/act1-$scenario-probe.md"
exit "$code"
