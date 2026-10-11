#!/usr/bin/env bash
# infra.sh [vm...]: apply the session stack, handling each kind of change so the service stays
# up (make infra; make infra REPLACE="pg-a" also replaces the VMs named). It runs on the admin
# machine, signed in to the aws and az CLIs (with jq), so the release role never holds the
# rights to change infrastructure.
#   no change                          nothing
#   create, or update in place         terraform apply
#   change one existing VM             through cycle: drained or handed over, applied, converged
#     (replace, or update in place,    to the current release, then back in service
#     which may restart it)
#   change several existing VMs        refused: one at a time keeps a twin serving
#   remove a database or etcd VM       refused: deleting data is never automatic
# After an apply every VM refreshes its files and mesh (a new or replaced VM has new peers or a
# new private address), and a new database VM must be streaming before this finishes.
set -euo pipefail
cd "$(dirname "$0")/../.."
. deploy/release/vmrun.sh
export ARM_SUBSCRIPTION_ID
ARM_SUBSCRIPTION_ID=$(az account show --query id -o tsv)
tf() { terraform -chdir=deploy/terraform/session "$@"; }

addr() { # addr <vm>: its VM resource in the session stack
  case "$(vm "$1" cloud)/$(vm "$1" region)" in
  aws/ap-south-1) echo "module.mumbai.aws_instance.vm[\"$1\"]" ;;
  aws/ap-south-2) echo "module.hyderabad.aws_instance.vm[\"$1\"]" ;;
  azure/*) echo "azurerm_linux_virtual_machine.vm[\"$1\"]" ;;
  *) echo "no VM named $1" >&2; return 1 ;;
  esac
}

apply() { # the saved plan, then every VM refreshes its files and mesh (tq-converge none)
  local m
  tf apply -input=false tfplan
  load_topo
  for m in $(jq -r '.vms[].name' <<<"$topo"); do
    run "$m" "tq-converge none" >/dev/null 2>&1 || echo "::warning::$m did not refresh its mesh (a new VM does at boot)"
  done
}

args=(-input=false -out=tfplan -var "admin_cidr=$(curl -fsS https://checkip.amazonaws.com)/32")
for name in "$@"; do args+=("-replace=$(addr "$name")"); done
tf plan "${args[@]}"

# Changes to VMs that exist: anything but a create (or nothing).
vms='[(.resource_changes // [])[] | select(.type == "aws_instance" or .type == "azurerm_linux_virtual_machine")]'
plan=$(tf show -json tfplan)
changed=$(jq -r "$vms | map(select(.change.actions != [\"create\"] and .change.actions != [\"no-op\"] and .change.actions != [\"read\"])) | .[].index" <<<"$plan")
created=$(jq -r "$vms | map(select(.change.actions == [\"create\"])) | .[].index" <<<"$plan")
removed=$(jq -r "$vms | map(select(.change.actions == [\"delete\"])) | .[].index" <<<"$plan")

for name in $removed; do
  if has "$name" postgres || has "$name" etcd; then
    echo "refused: the plan removes $name, a database VM; do that by hand if you mean it" >&2
    exit 1
  fi
done
if [ "$(wc -w <<<"$changed")" -gt 1 ]; then
  echo "refused: the plan changes$(printf ' %s' $changed) at once; change one VM at a time (-target)" >&2
  exit 1
fi

if [ -z "$(jq '(.resource_changes // [])[] | select(.change.actions != ["no-op"] and .change.actions != ["read"])' <<<"$plan")" ]; then
  echo "no change"
elif [ -n "$changed" ] && [ -z "$removed" ]; then
  cycle "$changed" apply
else
  if [ -n "$removed" ] && has "$removed" gateway; then drain "$removed" || true; fi
  apply
fi

for name in $created; do # a new database or etcd VM has joined once the cluster is whole
  if has "$name" postgres || has "$name" etcd; then
    echo "waiting for $name to join the database cluster"
    for i in $(seq 60); do
      pg_healthy && etcd_healthy && break
      [ "$i" = 60 ] && { echo "the database cluster is not whole 15 minutes after $name was created" >&2; exit 1; }
      sleep 15
    done
  fi
done
echo "infra: done"
