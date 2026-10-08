#!/usr/bin/env bash
# vms.sh stop|start|sweep
#   stop   nightly before go-live: stop AWS instances, deallocate Azure VMs (a VM that is only
#          stopped, not deallocated, still bills on Azure)
#   start  the next session: VMs converge to the current release at boot
#   sweep  after make down: fail if anything billable is left outside the persistent stack
set -euo pipefail
regions=(ap-south-1 ap-south-2)
rg=tq-session

instances() { # instances <region> <states>
  aws ec2 describe-instances --region "$1" --filters Name=tag:stack,Values=session "Name=instance-state-name,Values=$2" \
    --query 'Reservations[].Instances[].InstanceId' --output text
}

case ${1:-} in
stop)
  for r in "${regions[@]}"; do
    ids=$(instances "$r" pending,running)
    [ -z "$ids" ] || aws ec2 stop-instances --region "$r" --instance-ids $ids --output text >/dev/null
  done
  ids=$(az vm list -g $rg --query '[].id' -o tsv)
  [ -z "$ids" ] || az vm deallocate --ids $ids -o none
  echo stopped
  ;;
start)
  for r in "${regions[@]}"; do
    ids=$(instances "$r" stopped,stopping)
    [ -z "$ids" ] || aws ec2 start-instances --region "$r" --instance-ids $ids --output text >/dev/null
  done
  ids=$(az vm list -g $rg --query '[].id' -o tsv)
  [ -z "$ids" ] || az vm start --ids $ids -o none
  echo started
  ;;
sweep)
  left=0
  # report <what> <command...>: a query that fails proves nothing, so it fails the sweep.
  report() {
    local out
    out=$("${@:2}") || { echo "sweep: query failed: $1" >&2; exit 2; }
    [ -z "$out" ] || { echo "LEFT: $1: $(echo $out)"; left=1; }
  }
  for r in "${regions[@]}"; do
    report "$r instances" instances "$r" pending,running,stopping,stopped
    report "$r elastic IPs" aws ec2 describe-addresses --region "$r" --query 'Addresses[].PublicIp' --output text
    report "$r unattached volumes" aws ec2 describe-volumes --region "$r" --filters Name=status,Values=available --query 'Volumes[].VolumeId' --output text
    report "$r snapshots" aws ec2 describe-snapshots --region "$r" --owner-ids self --query 'Snapshots[].SnapshotId' --output text
    report "$r VPCs" aws ec2 describe-vpcs --region "$r" --filters Name=tag:stack,Values=session --query 'Vpcs[].VpcId' --output text
  done
  report "Azure $rg" az resource list -g $rg --query '[].name' -o tsv
  report "Azure disks" az resource list --resource-type Microsoft.Compute/disks --query '[].name' -o tsv
  [ $left = 0 ] && echo "sweep: nothing left"
  exit $left
  ;;
*)
  echo "usage: $0 stop|start|sweep" >&2
  exit 2
  ;;
esac
