# Load (cloud)

Measured 2026-10-11 in production: the database on pg-a (AWS Mumbai) and pg-z (Azure Central India), 2 worker VMs with 8 slots each, one dispatch per cloud, the bench on ops-a. Jobs are `chaos.sleep` with 0 ms, so this measures the scheduler's own overhead.

| Sync replication | Workers | Drain throughput | Latency p50 | p95 | p99 | Dead tuples after |
| --- | --- | --- | --- | --- | --- | --- |
| on | 2 | 101 jobs/s | 22 ms | 40 ms | 49 ms | 2092 |
| off | 2 | 307 jobs/s | 4 ms | 15 ms | 18 ms | 24682 |

Throughput is a backlog of jobs drained; latency is from `run_at` to the first attempt's start, under a steady load.

## What the database waited on during the drains

Sampled every 200 ms from `pg_stat_activity` (active client backends):

- sync, 2 workers: IPC:SyncRep 80%, CPU 7%, LWLock:WALWrite 5%, IO:WalSync 4%
- async, 2 workers: CPU 38%, LWLock:WALWrite 30%, IO:WalSync 22%, IPC:BgworkerShutdown 3%

## Traffic between the clouds

The primary was `pg-z`. During the whole run (232 s, both modes, including ordinary traffic and the probe), 156254904 bytes (149 MiB) crossed between the clouds, counted by WireGuard on the Azure VMs for their AWS peers. Most of it is the write-ahead log streamed to the standby.
