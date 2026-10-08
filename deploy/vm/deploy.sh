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
  etcd | postgres)
    # Never restarted by a deploy, only started: a new database image is applied by a
    # Patroni rolling restart (replica, switchover, old primary), not here.
    compose "$role" up -d --no-recreate
    if [ "$role" = postgres ]; then
      for _ in $(seq 60); do compose postgres exec -T patroni pg_isready -qh localhost && break || sleep 2; done
      compose postgres exec -T patroni pg_isready -h localhost
    fi
    ;;
  ops)
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
