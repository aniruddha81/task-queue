#!/usr/bin/env bash
# load.sh: the cloud load test (weeks 14-15). Runs cmd/bench on ops-a against production: a
# 10,000-job backlog drained, then a steady trickle for latency, with synchronous replication on
# and then off (back on at the end, even on failure), with the workers as deployed. It also
# counts the bytes that crossed between the clouds meanwhile (WireGuard's counters on the Azure
# VMs, for their AWS peers): cross-cloud traffic is billed as internet egress.
# Runs on a GitHub runner through experiment.yml (bench=true), or from an admin machine signed
# in to the aws and az CLIs (with jq). Writes $RESULTS/load-cloud.md (default docs/results/).
# HARNESS_SHA picks the bench code (default: HEAD; it must be on GitHub).
set -euo pipefail
cd "$(dirname "$0")/../.."
. deploy/release/vmrun.sh
sha=${HARNESS_SHA:-$(git rev-parse HEAD)}
results=${RESULTS:-docs/results}
mkdir -p "$results"
log() { echo "$(date -u +%T) $*"; }
azure_vms=$(jq -r '.vms[] | select(.cloud == "azure") | .name' <<<"$topo")
workers=$(jq '[.vms[] | select(.roles | index("worker"))] | length' <<<"$topo")
patroni=$(jq -r '[.vms[] | select(.roles | index("postgres")) | "http://\(.name):8008"] | join(",")' <<<"$topo")

cross() { # bytes so far between the clouds; mesh IPs 10.99.0.2x are Azure, 10.99.0.2 the admin
  local v n t=0
  for v in $azure_vms; do
    n=$(run "$v" "wg show wg0 dump | awk 'NR > 1 && \$4 !~ /^10[.]99[.]0[.]2[0-9]?[/]32\$/ { s += \$6 + \$7 } END { printf \"%.0f\", s }'" | tr -d '[:space:]')
    t=$((t + n))
  done
  echo $t
}

bench='set -e
. /etc/tq/tq.env
d=/opt/tq-harness/@SHA@
if [ ! -d $d ]; then
  rm -rf $d.tmp && mkdir -p $d.tmp
  curl -fsSL https://codeload.github.com/aniruddha81/task-queue/tar.gz/@SHA@ | tar xz -C $d.tmp --strip-components=1
  mv $d.tmp $d
fi
mkdir -p /opt/tq-harness/results
docker run --rm --network host -w /src -v $d:/src:ro -v tq-gomod:/go/pkg/mod -v tq-gocache:/root/.cache/go-build \
  -v /etc/tq/certs:/certs:ro -v /opt/tq-harness/results:/out golang:@GO@ \
  go run ./cmd/bench -cloud "@CLOUD@" -workers @WORKERS@ -db "$JOBS_DB_URL" -patroni @PATRONI@ -out /out/load-cloud.md >/dev/null 2>&1'
bench=${bench//@SHA@/$sha}
bench=${bench//@GO@/$(sed -n 's/^go //p' go.mod)}
bench=${bench//@WORKERS@/$workers}
bench=${bench//@PATRONI@/$patroni}
bench=${bench//@CLOUD@/the database on pg-a (AWS Mumbai) and pg-z (Azure Central India), $workers worker VMs with 8 slots each, one dispatch per cloud, the bench on ops-a}

trap 'sync_mode true || echo "::error::synchronous_mode is not back on; run sync_mode true from vmrun.sh"' EXIT
leader=$(pg_leader)
before=$(cross)
start=$(date -u +%s)
log "bench $(git rev-parse --short "$sha") on $ops; the primary is $leader"
run "$ops" "$bench" >/dev/null
secs=$(($(date -u +%s) - start))
bytes=$(($(cross) - before))
log "done in ${secs}s; ${bytes} bytes between the clouds"

run "$ops" "base64 -w0 /opt/tq-harness/results/load-cloud.md" | tr -d '[:space:]' | base64 -d >"$results/load-cloud.md"
cat >>"$results/load-cloud.md" <<EOF

## Traffic between the clouds

The primary was \`$leader\`. During the whole run ($secs s, both modes, including ordinary traffic and the probe), $bytes bytes ($((bytes / 1048576)) MiB) crossed between the clouds, counted by WireGuard on the Azure VMs for their AWS peers. Most of it is the write-ahead log streamed to the standby.
EOF
log "wrote $results/load-cloud.md"
