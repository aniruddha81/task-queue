# PostgreSQL 18 managed by Patroni (automatic failover through etcd).
FROM postgres:18
RUN apt-get update \
 && apt-get install -y --no-install-recommends patroni python3-etcd iproute2 \
 && rm -rf /var/lib/apt/lists/*
COPY patroni.yml /etc/patroni.yml
COPY entrypoint.sh init-dbs.sh /usr/local/bin/
COPY init.sql /init.sql
RUN chmod +x /usr/local/bin/entrypoint.sh /usr/local/bin/init-dbs.sh
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
