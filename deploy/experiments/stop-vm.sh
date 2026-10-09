#!/usr/bin/env bash
# stop-vm.sh <vm>: stop one VM for 2 minutes while the external probe runs on a GitHub-hosted
# runner (probe.yml), outside both clouds, as every rollout's probe does. Week 12's check:
# stopping any one stateless VM fails no request beyond those in flight on it. Prints the stop
# and start times and the probe's verdict, with each failed request. Run from an admin machine
# signed in to the aws, az and gh CLIs (with jq).
set -euo pipefail
cd "$(dirname "$0")/../.."
name=${1:?usage: stop-vm.sh <vm>}
. deploy/release/vmrun.sh
id=$(vm "$name" id)
log() { echo "$(date -u +%T) $*"; }

gh workflow run probe.yml --ref main -f for=8m
sleep 10
run_id=$(gh run list --workflow probe.yml --event workflow_dispatch --limit 1 --json databaseId -q '.[0].databaseId')
log "probe run $run_id: waiting for it to start probing"
until [ "$(gh run view "$run_id" --json jobs -q '.jobs[0].steps[] | select(.name == "probe") | .status')" = in_progress ]; do sleep 5; done
sleep 90 # compiling (about 30 s), then a minute of probing before the stop

log "stop $name"
if [ "$(vm "$name" cloud)" = aws ]; then
  aws ec2 stop-instances --region "$(vm "$name" region)" --instance-ids "$id" --output text >/dev/null
  aws ec2 wait instance-stopped --region "$(vm "$name" region)" --instance-ids "$id"
else
  az vm stop -g "$(vm "$name" rg)" -n "$id" -o none
fi
log "stopped; holding 2 minutes"
sleep 120
log "start $name"
if [ "$(vm "$name" cloud)" = aws ]; then
  aws ec2 start-instances --region "$(vm "$name" region)" --instance-ids "$id" --output text >/dev/null
else
  az vm start -g "$(vm "$name" rg)" -n "$id" -o none
fi
log "started; the probe runs on to its 8 minutes"
rc=0
gh run watch "$run_id" --exit-status >/dev/null 2>&1 || rc=$?
gh run view "$run_id" --log | grep -oE 'probe: .*' | sort -u
log "probe run $run_id: $([ $rc = 0 ] && echo passed || echo failed)"
exit "$rc"
