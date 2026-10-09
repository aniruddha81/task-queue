#!/usr/bin/env bash
# stop-vm.sh <vm>: stop one VM for 2 minutes while the external probe (the same one every
# rollout runs) sends requests through the public name from this machine, outside both clouds.
# Week 12's check: stopping any one stateless VM fails no request beyond those in flight on it.
# Prints the probe's failures with the stop and start times. Run from an admin machine signed
# in to the aws and az CLIs (with jq and Go).
set -euo pipefail
cd "$(dirname "$0")/../.."
name=${1:?usage: stop-vm.sh <vm>}
. deploy/release/vmrun.sh
id=$(vm "$name" id)
log() { echo "$(date -u +%T) $*"; }

probe_bin="./tq-probe$(go env GOEXE)" # relative: Windows Go and bash disagree on /tmp
trap 'rm -f "$probe_bin"' EXIT
go build -o "$probe_bin" ./deploy/release/probe
PROBE_PASSWORD=$(aws ssm get-parameter --region "$region" --name /tq/ci/smoke-password --with-decryption --query Parameter.Value --output text) \
  "$probe_bin" -url "https://$fqdn" -for 8m &
probe=$!
sleep 60

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
log "started; the probe runs on until 8 minutes"
rc=0
wait "$probe" || rc=$?
log "probe exit $rc"
exit "$rc"
