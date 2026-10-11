#!/usr/bin/env bash
# maintain.sh sweep|reboot: automatic maintenance, run by maintenance.yml.
#   sweep   every VM answers, every gateway is online in Traffic Manager, Patroni has a leader
#           with every replica streaming, and every etcd member is healthy. It changes nothing:
#           it fails (and GitHub emails) when the service has lost a twin, which the public
#           probe can't see while the other twin serves. AWS and Azure restart a VM whose host
#           fails; anything else needs a person (make infra REPLACE=<vm> replaces one).
#   reboot  each VM whose OS updates need a reboot (/var/run/reboot-required) is rebooted on
#           its own, through cycle: drained or handed over first, back in service before the
#           next. The Patroni leader goes last, so there is one switchover at most.
set -euo pipefail
. "$(dirname "$0")/vmrun.sh"

restart_vm() { # restart_vm <vm>: reboot it and wait until it is back up (a new boot ID)
  local boot now
  boot=$(run "$1" "cat /proc/sys/kernel/random/boot_id")
  run "$1" "systemd-run --on-active=5 systemctl reboot" >/dev/null
  for _ in $(seq 40); do
    sleep 15
    now=$(run "$1" "cat /proc/sys/kernel/random/boot_id" 2>/dev/null) || continue
    [ -n "$now" ] && [ "$now" != "$boot" ] && return 0
  done
  echo "$1 did not come back from its reboot in 10 minutes"
  return 1
}

case ${1:-} in
sweep)
  bad=
  for name in $(jq -r '.vms[].name' <<<"$topo"); do
    run "$name" "docker info >/dev/null" >/dev/null 2>&1 || bad="$bad $name"
  done
  for name in $(jq -r '.vms[] | select(.roles | index("gateway")) | .name' <<<"$topo"); do
    [ -n "$(online " && name=='$name'")" ] || bad="$bad $name-in-DNS"
  done
  pg_healthy || bad="$bad Patroni"
  etcd_healthy || bad="$bad etcd"
  [ -z "$bad" ] || { echo "::error::unhealthy:$bad"; exit 1; }
  echo "::notice::healthy: every VM, gateway, database node and etcd member"
  ;;
reboot)
  leader=$(pg_leader)
  for name in $(jq -r --arg l "$leader" '[.vms[].name] | map(select(. != $l)) + [$l] | .[]' <<<"$topo"); do
    run "$name" "test -f /var/run/reboot-required" >/dev/null 2>&1 || continue
    echo "::group::reboot $name"
    cycle "$name" restart_vm "$name" || { echo "::endgroup::"; exit 1; }
    echo "::endgroup::"
  done
  echo "every VM that needed a reboot has had one"
  ;;
*)
  echo "usage: $0 sweep|reboot" >&2
  exit 2
  ;;
esac
