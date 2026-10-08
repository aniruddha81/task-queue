#!/usr/bin/env bash
# hops.sh: per-hop latency of the Act 1 layout (week 11), measured from the VMs themselves.
# Each hop is 20 requests on one kept-alive connection; the median leaves out the first
# request's handshake. SQL hops time `SELECT 1` in one psql session. Prints a Markdown table.
set -euo pipefail
cd "$(dirname "$0")/../.."
. deploy/release/vmrun.sh

# https <from-vm> <url>: median ms of 20 GETs over one mTLS connection.
https() {
  run "$1" "cert=\$(ls /etc/tq/certs/*.crt | grep -v /ca.crt | head -1)
    urls=''; for i in \$(seq 20); do urls=\"\$urls $2\"; done
    curl -s -o /dev/null --cacert /etc/tq/certs/ca.crt --cert \$cert --key \${cert%.crt}.key -w '%{time_total}\n' \$urls |
      tail -19 | sort -n | awk '{a[NR]=\$1} END {printf \"%.2f\n\", a[int((NR+1)/2)]*1000}'" | head -1
}

# sql <from-vm>: median ms of 20 `SELECT 1` on one connection to the primary.
sql() {
  run "$1" ". /etc/tq/tq.env
    for i in \$(seq 20); do echo 'SELECT 1;'; done |
      docker run --rm -i --network host -v /etc/tq/certs:/certs:ro postgres:18 psql \"\$JOBS_DB_URL\" -qAt -c '\timing on' -f - 2>&1 |
      sed -n 's/^Time: \([0-9.]*\) ms.*/\1/p' | tail -19 | sort -n | awk '{a[NR]=\$1} END {printf \"%.2f\n\", a[int((NR+1)/2)]}'" | head -1
}

# public: from ops-a, the public name's full request, as an in-region client sees it.
public() {
  run "$ops" "urls=''; for i in \$(seq 20); do urls=\"\$urls https://$fqdn/version\"; done
    curl -s -o /dev/null -w '%{time_total}\n' \$urls | tail -19 | sort -n | awk '{a[NR]=\$1} END {printf \"%.2f\n\", a[int((NR+1)/2)]*1000}'" | head -1
}

echo "| Hop | Clouds | Median ms |"
echo "| --- | --- | --- |"
echo "| client (ops-a) → gateway, through Traffic Manager's name | AWS → AWS | $(public) |"
echo "| gateway → jobs (svc-a) | AWS, same VM | $(https svc-a https://svc-a:8080/readyz) |"
echo "| gateway → auth (svc-z) | AWS → Azure | $(https svc-a https://svc-z:8083/readyz) |"
echo "| jobs, dispatch-a → Postgres primary (pg-a) | AWS → AWS | $(sql svc-a) |"
echo "| dispatch-z → Postgres primary (pg-a) | Azure → AWS | $(sql svc-z) |"
echo "| worker (work-a) → dispatch-a | AWS → AWS | $(https work-a https://svc-a:8081/readyz) |"
echo "| worker (work-z) → dispatch-z | Azure → Azure | $(https work-z https://svc-z:8081/readyz) |"
echo "| worker (work-z) → sinks (ops-a) | Azure → AWS | $(https work-z https://ops-a:8090/readyz) |"
