# vmrun.sh: sourced by rollout.sh and the experiments. Reads the session's topology (written
# by Terraform to SSM) and runs commands on its VMs through AWS Run Command and Azure Run
# Command, never SSH. Needs the aws, az and jq CLIs, signed in.
export MSYS_NO_PATHCONV=1 # Git Bash on Windows would rewrite /tq/... into a file path
region=ap-south-1         # where the release record and topology live

load_topo() { topo=$(aws ssm get-parameter --region $region --name /tq/release/topology --query Parameter.Value --output text); }
load_topo # again after an apply: a replaced VM has a new instance ID
fqdn=$(jq -r .fqdn <<<"$topo")
vault=$(jq -r .vault <<<"$topo")
ops=$(jq -r .ops <<<"$topo")
vm() { jq -r --arg n "$1" ".vms[] | select(.name == \$n) | .$2" <<<"$topo"; }

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
    # The command's stdout to stdout, its stderr to stderr (each truncated at 24 KB by SSM).
    aws ssm get-command-invocation --region "$r" --command-id "$cid" --instance-id "$id" --query StandardOutputContent --output text
    aws ssm get-command-invocation --region "$r" --command-id "$cid" --instance-id "$id" --query StandardErrorContent --output text >&2
    [ "$status" = Success ]
  else
    # Run Command doesn't return the script's exit status, so success prints a marker.
    local out
    out=$(az vm run-command invoke -g "$(vm "$name" rg)" -n "$id" --command-id RunShellScript \
      --scripts "( $cmd ) && echo TQ-RUN-OK" --query 'value[0].message' -o tsv)
    # Its message is "Enable succeeded:", then [stdout] and [stderr] sections.
    sed -n '/^\[stdout\]$/,/^\[stderr\]$/{//!p}' <<<"$out" | grep -v '^TQ-RUN-OK$' || true
    sed -n '/^\[stderr\]$/,${//!p}' <<<"$out" >&2
    grep -q TQ-RUN-OK <<<"$out"
  fi
}

# ---------- Taking a VM out of service and back (rollout.sh, maintain.sh, infra.sh) ----------

has() { jq -e --arg n "$1" --arg r "$2" '.vms[] | select(.name == $n) | .roles | index($r)' <<<"$topo" >/dev/null; } # has <vm> <role>

tm_rg=$(cut -d/ -f5 <<<"$(jq -r .tm <<<"$topo")")
tm_profile=$(jq -r '.tm | split("/") | last' <<<"$topo")
endpoint() { # endpoint <vm> Enabled|Disabled
  az network traffic-manager endpoint update -g "$tm_rg" --profile-name "$tm_profile" --type externalEndpoints -n "$1" --endpoint-status "$2" -o none
}
online() { # online <query>: names of endpoints that are enabled and pass Traffic Manager's health probe
  az network traffic-manager endpoint list -g "$tm_rg" --profile-name "$tm_profile" --query "[?endpointStatus=='Enabled' && endpointMonitorStatus=='Online'$1].name" -o tsv
}
drain() { # drain <gateway>: out of DNS, if another gateway is online to serve meanwhile
  [ -n "$(online " && name!='$1'")" ] || return 1
  endpoint "$1" Disabled
  echo "drained $1; waiting 60 s (six DNS TTLs) for clients to move to its twin"
  sleep 60
}
undrain() { # undrain <gateway>: back in DNS once Traffic Manager's probe (every 10 s) sees it
  endpoint "$1" Enabled
  for _ in $(seq 30); do
    [ -n "$(online " && name=='$1'")" ] && { echo "$1 is back in DNS"; return 0; }
    sleep 10
  done
  echo "$1 did not come back online in Traffic Manager"
  return 1
}

any() { # any <role> <command>: run it on the first VM with the role that answers
  local m
  for m in $(jq -r --arg r "$1" '.vms[] | select(.roles | index($r)) | .name' <<<"$topo"); do
    run "$m" "$2" 2>/dev/null && return 0
  done
  return 1
}
pg_cluster() { any postgres "curl -fsS --max-time 5 http://localhost:8008/cluster"; }
pg_leader() { pg_cluster | jq -r '.members[] | select(.role == "leader") | .name'; }
pg_healthy() { # one running leader, and every other database VM streaming from it
  local c n
  n=$(jq '[.vms[] | select(.roles | index("postgres"))] | length - 1' <<<"$topo")
  c=$(pg_cluster) && jq -e --argjson n "$n" '([.members[] | select(.role == "leader" and .state == "running")] | length == 1)
    and ([.members[] | select(.state == "streaming")] | length == $n)' <<<"$c" >/dev/null
}
etcd_healthy() { # every member of the role map is in the cluster and healthy
  local n
  n=$(jq '[.vms[] | select(.roles | index("etcd"))] | length' <<<"$topo")
  any etcd "docker exec tq-etcd-etcd-1 etcdctl endpoint health --cluster 2>&1" | grep -c 'is healthy' | grep -qx "$n"
}
# switchover <vm>: hand Patroni's leader role from <vm> to a streaming replica (synchronous,
# so no acknowledged write is lost), and wait until the old leader streams from the new one.
switchover() {
  local to
  to=$(pg_cluster | jq -r --arg f "$1" '[.members[] | select(.name != $f and .state == "streaming")][0].name // empty')
  [ -n "$to" ] || { echo "no streaming replica to hand $1's leader role to"; return 1; }
  run "$1" "curl -fsS --max-time 30 -X POST http://localhost:8008/switchover -d '{\"leader\": \"$1\", \"candidate\": \"$to\"}'" || return 1
  for _ in $(seq 24); do
    [ "$(pg_leader)" = "$to" ] && pg_healthy && { echo "switchover: $to leads"; return 0; }
    sleep 5
  done
  echo "switchover from $1 to $to did not finish in 2 minutes"
  return 1
}

# cycle <vm> <command...>: take a VM out of service, run the command (a reboot, a replacement),
# then wait until the VM has converged to the current release and put it back. A gateway is
# drained first and a Patroni leader hands over first, so its twin serves meanwhile; with no
# twin ready (or etcd short of a member), nothing is done. Afterwards a database VM must be
# streaming again, and every etcd member healthy, before the next VM may go.
cycle() {
  local name=$1 drained=
  shift
  if has "$name" postgres || has "$name" etcd; then
    pg_healthy && etcd_healthy || { echo "::error::the database cluster is degraded; $name is left alone"; return 1; }
  fi
  if has "$name" gateway; then
    drain "$name" || { echo "::error::no other gateway is online; $name is left alone"; return 1; }
    drained=1
  fi
  if has "$name" postgres && [ "$(pg_leader)" = "$name" ]; then
    switchover "$name" || return 1
  fi
  "$@" || return 1
  load_topo
  echo "waiting for $name to converge"
  for i in $(seq 60); do
    run "$name" tq-converge >/dev/null 2>&1 && break
    [ "$i" = 60 ] && { echo "::error::$name did not converge in 15 minutes"; return 1; }
    sleep 15
  done
  if has "$name" postgres || has "$name" etcd; then
    for i in $(seq 40); do
      pg_healthy && etcd_healthy && break
      [ "$i" = 40 ] && { echo "::error::the database cluster did not recover after $name"; return 1; }
      sleep 15
    done
  fi
  [ -z "$drained" ] || undrain "$name"
  echo "$name is back in service"
}
