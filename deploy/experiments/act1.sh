#!/usr/bin/env bash
# act1.sh <scenario>: one fault experiment against the running cloud: Act 1's fragility
# experiments (week 11) and Act 2's go-live gate (week 13). Runs on a GitHub runner through
# experiment.yml, or from an admin machine signed in to the aws and az CLIs (with jq and Go).
#
#   cut-clouds     drop all mesh traffic between Azure and AWS for 2 minutes
#   stop-azure     stop every Azure VM for 2 minutes
#   stop-aws       stop every AWS VM in Mumbai except ops-a (never faulted) for 2 minutes: a
#                  region outage; the witness, in Hyderabad, stays up
#   kill-gateway   kill the gateway; it stays down for 1 minute
#   stop-leader    stop the Patroni leader's VM for 2 minutes: an automatic failover to the
#                  other cloud; run it again to fail back
#   stop-<vm>      stop that VM (stop-wit: the etcd witness, stop-work-a, ...) for 2 minutes
#   pause-<vm>     freeze every service container on that VM (docker pause), past every lease,
#                  then thaw it: a long garbage-collection pause; its late writes must be fenced
#   watch          no fault: load, probe and checker while someone else changes the cloud
#                  (make infra REPLACE=pg-z); set LOAD and HOLD to cover it
#
# The chaos harness runs on ops-a with the scenario's name and no faults of its own: load
# through the public name (LOAD, default 6m), then quiesce, then the checker. 90 s into the
# load this script applies the scenario, and reverts it after HOLD seconds (default 120).
# Faults applied on a VM revert themselves (systemd-run timers), so a crashed script can't
# leave one behind. Meanwhile the release probe, outside both clouds, sends ~10 requests a
# second through the public name and never retries. Results: the checker's report in
# $RESULTS (default docs/results/), and the probe's failures, timed from the fault, beside it
# in $LABEL-probe.md (LABEL defaults to act1-<scenario>).
#
# HARNESS_SHA picks the harness code (default: HEAD; it must be on GitHub).
set -euo pipefail
cd "$(dirname "$0")/../.."
scenario=${1:?usage: act1.sh cut-clouds|stop-azure|stop-aws|kill-gateway|stop-leader|stop-<vm>|pause-<vm>|watch}
. deploy/release/vmrun.sh
sha=${HARNESS_SHA:-$(git rev-parse HEAD)}
hold=${HOLD:-120}
label=${LABEL:-act1-$scenario}
aws_vms=$(jq -r --arg ops "$ops" --arg r "$region" '.vms[] | select(.cloud == "aws" and .region == $r and .name != $ops) | .name' <<<"$topo")
azure_vms=$(jq -r '.vms[] | select(.cloud == "azure") | .name' <<<"$topo")
gateways=$(jq -r '.vms[] | select(.roles | index("gateway")) | .name' <<<"$topo")
azure_ids() { az vm list -g "$(vm svc-z rg)" --query '[].id' -o tsv; }
results=${RESULTS:-docs/results}
mkdir -p "$results"
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
  -v /etc/tq/certs:/certs:ro -v /opt/tq-harness/results:/out golang:@GO@ \
  go run ./cmd/torture -gateway https://@FQDN@ -ca "" -password "$HARNESS_PASSWORD" \
    -jobs-db "$JOBS_DB_URL" -sinks-db "$SINKS_DB_URL" -mailpit http://localhost:8025 -webhook https://@OPS@:8090/webhook \
    -faults=false -scenario @SCENARIO@ -duration @LOAD@ -quiesce 10m -out /out'
harness=${harness//@SHA@/$sha}
harness=${harness//@FQDN@/$fqdn}
harness=${harness//@OPS@/$ops}
harness=${harness//@SCENARIO@/$scenario}
harness=${harness//@LOAD@/${LOAD:-6m}}
harness=${harness//@GO@/$(sed -n 's/^go //p' go.mod)} # exactly go.mod's: a cached golang:1.27 can be older

# ---------- scenarios ----------
on_all() { # on_all <vms> <command>: in parallel; fails if any fails
  local pids=() v
  for v in $1; do
    run "$v" "$2" >/dev/null &
    pids+=($!)
  done
  for p in "${pids[@]}"; do wait "$p"; done
}

# A stopped VM, the way a crash takes it: no drain, no handover, no clean shutdown.
stop_vm() {
  if [ "$(vm "$1" cloud)" = aws ]; then
    aws ec2 stop-instances --force --region "$(vm "$1" region)" --instance-ids "$(vm "$1" id)" --output text >/dev/null
    aws ec2 wait instance-stopped --region "$(vm "$1" region)" --instance-ids "$(vm "$1" id)"
  else
    az vm stop --skip-shutdown -g "$(vm "$1" rg)" -n "$(vm "$1" id)" -o none
  fi
}
start_vm() {
  if [ "$(vm "$1" cloud)" = aws ]; then
    aws ec2 start-instances --region "$(vm "$1" region)" --instance-ids "$(vm "$1" id)" --output text >/dev/null
  else
    az vm start -g "$(vm "$1" rg)" -n "$(vm "$1" id)" -o none
  fi
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
  stop-leader)
    victim=$(pg_leader)
    log "the Patroni leader is $victim"
    stop_vm "$victim"
    ;;
  stop-?* | pause-?*)
    victim=${scenario#*-}
    [ -n "$(vm "$victim" id)" ] && [ "$victim" != "$ops" ] || { echo "no VM $victim to fault (and $ops never is)" >&2; exit 2; }
    if [ "${scenario%%-*}" = stop ]; then
      stop_vm "$victim"
    else
      run "$victim" "ids=\$(docker ps -q --filter name=tq-)
        docker pause \$ids >/dev/null
        systemctl reset-failed tq-fault-revert 2>/dev/null || true
        systemd-run --on-active=${hold}s --unit tq-fault-revert docker unpause \$ids" >/dev/null
    fi
    ;;
  watch) ;;
  *) echo "unknown scenario $scenario" >&2; exit 2 ;;
  esac
}

revert() { # VM-side faults revert themselves; stopped VMs are started here
  case $scenario in
  stop-azure) az vm start --ids $(azure_ids) -o none ;;
  stop-aws) aws ec2 start-instances --region "$region" --instance-ids $(for v in $aws_vms; do vm "$v" id; done) --output text >/dev/null ;;
  stop-?*) start_vm "$victim" ;; # stop-leader too
  esac
}

# ---------- run ----------
probe_bin="./tq-probe$(go env GOEXE)" # relative: Windows Go and bash disagree on /tmp
go build -o "$probe_bin" ./deploy/release/probe
log "harness $(git rev-parse --short "$sha") on $ops, scenario $scenario"
run "$ops" "tq-converge" >/dev/null # fresh files: Terraform may have changed ops-a's settings
run "$ops" "$harness" >/dev/null
until run "$ops" "docker logs torture 2>&1 | grep -q 'load running'" >/dev/null 2>&1; do
  if [ "$(run "$ops" "docker inspect -f '{{.State.Status}}' torture" 2>/dev/null | head -1)" = exited ]; then
    run "$ops" "docker logs --tail 20 torture" >&2
    echo "the harness exited before its load started" >&2
    exit 1
  fi
  sleep 10
done
load_start=$(date -u +%s)
log "load started; probing"
PROBE_PASSWORD=$(aws ssm get-parameter --region "$region" --name /tq/ci/smoke-password --with-decryption --query Parameter.Value --output text)   "$probe_bin" -url "https://$fqdn" >"$probe_log" 2>&1 &
probe_pid=$!
trap 'kill $probe_pid 2>/dev/null || true; rm -f "$probe_bin"' EXIT

sleep 90
fault_start=$(date -u +%s)
log "apply $scenario"
apply
log "applied; holding ${hold}s"
sleep $hold
revert
fault_end=$(date -u +%s)
log "reverted"

deadline=$(($(date +%s) + 40 * 60)) # 6 min of load + up to 10 min of quiesce, with margin
until [ "$(run "$ops" "docker inspect -f '{{.State.Status}}' torture" 2>/dev/null | head -1 | tr -d '[:space:]')" = exited ]; do
  [ "$(date +%s)" -lt $deadline ] || { echo "the harness is still running after 40 minutes" >&2; exit 1; }
  sleep 20
done
kill -TERM $probe_pid # it prints its summary on SIGTERM (on Linux; experiment.yml runs there)
wait $probe_pid || true
code=$(run "$ops" "docker inspect -f '{{.State.ExitCode}}' torture" | head -1 | tr -d '[:space:]')
report=$(run "$ops" "ls -t /opt/tq-harness/results/*-$scenario.md | head -1" | head -1 | tr -d '[:space:]')
run "$ops" "base64 -w0 $report" | tr -d '[:space:]' | base64 -d >"$results/$(basename "$report")" # bytes intact
log "checker exit $code; report $results/$(basename "$report")"

# The probe's verdict, and each failed request timed from the fault.
{
  echo "# Probe: $label"
  echo
  echo "The release probe, outside both clouds, through https://$fqdn: about 10 requests a second (\`/version\`, an authenticated list, an idempotent submit), each on a new connection and never retried. Fault applied at +$((fault_start - load_start))s and held for $((fault_end - fault_start))s, from the start of load."
  echo
  echo "**$(grep -E '^probe: [0-9]+ requests' "$probe_log" || echo 'probe: no summary')**"
  echo
  if grep -q '^probe: FAILED' "$probe_log"; then
    echo "| Request | From the fault | Error |"
    echo "| --- | --- | --- |"
    awk -v a="$((fault_start % 86400))" '$2 == "FAILED" {
      split($6, t, ":"); e = $0; sub(/^[^:]*:[^:]*:[^:]*:[^:]*: /, "", e)
      printf "| %s %s | %+.1fs | %s |\n", $3, $4, t[1] * 3600 + t[2] * 60 + t[3] - a, e }' "$probe_log"
  fi
} >"$results/$label-probe.md"
log "probe summary $results/$label-probe.md"
exit "$code"
