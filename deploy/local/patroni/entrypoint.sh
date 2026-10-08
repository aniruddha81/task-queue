#!/bin/sh
# Runs as root: install the TLS key the way PostgreSQL insists (private, owned by postgres),
# add the simulated cross-cloud latency, then run Patroni as postgres.
set -e
install -o postgres -g postgres -m 600 /certs/postgres.key /var/lib/postgresql/server.key
install -o postgres -g postgres -m 644 /certs/postgres.crt /var/lib/postgresql/server.crt
install -o postgres -g postgres -m 644 /certs/ca.crt /var/lib/postgresql/ca.crt
mkdir -p /var/lib/postgresql/data && chown postgres:postgres /var/lib/postgresql/data && chmod 700 /var/lib/postgresql/data

# Half the measured AWS Mumbai <-> Azure Central India round trip (5.5 ms) on each node's
# egress, so replication between the two nodes sees the real RTT (docs/results/accounts.md).
if [ -n "${NETEM_DELAY:-}" ]; then
  tc qdisc add dev eth0 root netem delay "$NETEM_DELAY" || echo "netem not applied (needs NET_ADMIN)"
fi

exec gosu postgres patroni /etc/patroni.yml
