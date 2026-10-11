#!/usr/bin/env bash
# deploy.sh <sha>: converge this VM's roles to release <sha>. Run by tq-converge, at every
# boot and by rollout.sh. Each role is one Compose project (deploy/vm/<role>.compose.yml),
# and each service must answer /readyz on the new version before the next role starts.
#   deploy.sh <sha> migrate   on the ops VM: apply the jobs and auth migrations instead.
set -euo pipefail
sha=$1
here=$(cd "$(dirname "$0")" && pwd)
set -a
. /etc/tq/tq.env # ROLES, IMAGE, URLs and this VM's secrets (rendered by Terraform)
TAG=$sha
VERSION=$sha
set +a

compose() { docker compose -p "tq-$1" -f "$here/$1.compose.yml" "${@:2}"; }
migrate() { docker run --rm --network host -v /etc/tq/certs:/certs:ro -e TLS_DIR=/certs -e DATABASE_URL="$2" "$IMAGE/migrate:$TAG" "$1"; }

# ready <service> <port>: /readyz passes and /version reports this release, within 2 minutes.
cert=$(ls /etc/tq/certs/*.crt | grep -v /ca.crt | head -1 || true)
ready() {
  local c=(-fsS --max-time 5 --cacert /etc/tq/certs/ca.crt --cert "$cert" --key "${cert%.crt}.key")
  for _ in $(seq 60); do
    if [ "$(curl "${c[@]}" "https://localhost:$2/version" 2>/dev/null)" = "$sha" ] &&
      curl "${c[@]}" -o /dev/null "https://localhost:$2/readyz" 2>/dev/null; then
      echo "$1 ready"
      return 0
    fi
    sleep 2
  done
  echo "$1 is not ready on :$2 at ${sha:0:7}" >&2
  return 1
}

# etcd membership, through the other members (a learner can't answer for the cluster).
etcdctl() {
  local peers
  peers=$(tr , '\n' <<<"$ETCD_INITIAL_CLUSTER" | grep -v "^$VM_NAME=" | sed 's/^[^=]*=//; s/2380$/2379/' | paste -sd, -)
  docker run --rm --network host "$(compose etcd config --images)" etcdctl --endpoints="$peers" --dial-timeout=3s --command-timeout=10s "$@"
}
me() { # this VM's member ID, from `member list` (ID, status, name, peer URL, client URL, learner);
  # by peer URL, since a member that hasn't started has no name yet. Members first created
  # before week 13 advertise the VM's name rather than its mesh IP.
  awk -F', ' -v n="http://$VM_NAME:2380" -v u="http://$MESH_IP:2380" -v l="$1" '($4 == u || $4 == n) && (l == "" || $6 == l) {print $1}'
}
# A new or replaced member joins the running cluster as a learner: it doesn't count toward
# quorum, so adding it can't stall the cluster. It never founds a cluster because it can't
# reach one: in a running session that would be a second cluster, and Patroni would
# bootstrap a second primary on it. Only a fresh session (ETCD_BOOTSTRAP) founds one.
etcd_join() {
  local list old out id
  list=$(etcdctl member list) || { echo "etcd: no running cluster reachable to join yet" >&2; return 1; }
  old=$(me "" <<<"$list") # a replaced VM keeps its name: its dead incarnation goes first
  for id in $old; do etcdctl member remove "$id"; done
  docker volume rm -f tq-etcd_etcd >/dev/null # a removed member's data can't rejoin
  out=$(etcdctl member add "$VM_NAME" --learner --peer-urls="http://$MESH_IP:2380")
  ETCD_CLUSTER=$(sed -n 's/^ETCD_INITIAL_CLUSTER="\(.*\)"$/\1/p' <<<"$out") ETCD_STATE=existing compose etcd up -d
}
# A learner becomes a voter once it has caught up with the leader; until then promote fails.
etcd_promote() {
  local id
  id=$(etcdctl member list 2>/dev/null | me true) || return 0 # can't tell: a later converge will
  [ -n "$id" ] || return 0
  for _ in $(seq 60); do
    etcdctl member promote "$id" 2>/dev/null && return 0
    sleep 2
  done
  echo "etcd: $VM_NAME is still a learner" >&2
  return 1
}

if [ "${2:-}" = migrate ]; then
  migrate jobs "$JOBS_DB_URL"
  migrate auth "$AUTH_DB_URL"
  exit 0
fi

touch "$here/../.." # this release is the newest, even when it is a rollback to an older one
for role in $ROLES; do
  echo "--- $role"
  compose "$role" pull -q
  case $role in
  etcd)
    # Never restarted by a deploy, only created and started (as postgres below).
    if [ -z "$(compose etcd ps -aq)" ]; then
      if [ "${ETCD_BOOTSTRAP:-}" = true ]; then
        ETCD_CLUSTER=$ETCD_INITIAL_CLUSTER ETCD_STATE=new compose etcd up -d
      else
        etcd_join
      fi
    fi
    compose etcd up -d --no-recreate
    etcd_promote
    ;;
  postgres)
    # Never restarted by a deploy, only started: a new database image is applied by a
    # Patroni rolling restart (replica, switchover, old primary), not here. A new node with
    # no data clones the primary.
    compose postgres up -d --no-recreate
    for _ in $(seq 150); do compose postgres exec -T patroni pg_isready -qh localhost && break || sleep 2; done
    compose postgres exec -T patroni pg_isready -h localhost
    ;;
  ops)
    # Every migration, not just the sinks': a VM that boots into a fresh session (make up)
    # converges with no rollout to run them. They are idempotent, and until the database
    # is up this fails and tq-converge.service retries.
    migrate jobs "$JOBS_DB_URL"
    migrate auth "$AUTH_DB_URL"
    compose ops up -d --wait sinks-postgres
    migrate sinks "$SINKS_DB_URL"
    compose ops up -d --remove-orphans
    ready sinks 8090
    ;;
  *)
    compose "$role" up -d --remove-orphans
    case $role in
    gateway) ready gateway 443 ;;
    auth) ready auth 8083 ;;
    jobs) ready jobs 8080 ;;
    dispatch) ready dispatch 8081 ;;
    scheduler) ready scheduler 8082 ;;
    esac
    ;;
  esac
done

# Roles the role map moved off this VM.
for p in $(docker compose ls -aq); do
  [[ " $ROLES " == *" ${p#tq-} "* ]] || docker compose -p "$p" down
done

# Keep the three newest releases, and images used in the last week.
ls -1dt /opt/tq/*/ | tail -n +4 | xargs -r rm -rf
docker image prune -af --filter until=168h >/dev/null
echo "converged to ${sha:0:7}: $ROLES"
