# Load baseline (local)

Measured 2026-10-08 on one laptop (Docker Desktop, Windows), with the full local stack: two Patroni nodes with 5.5 ms of simulated cross-cloud round trip between them, one dispatch, workers with 8 slots each. Jobs are `chaos.sleep` with 0 ms, so this measures the scheduler's own overhead. Absolute numbers depend on the machine; the shape is the point.

| Sync replication | Workers | Drain throughput | Latency p50 | p95 | p99 | Dead tuples after |
| --- | --- | --- | --- | --- | --- | --- |
| on | 1 | 137 jobs/s | 12 ms | 36 ms | 37 ms | 162 |
| on | 2 | 257 jobs/s | 12 ms | 12 ms | 13 ms | 23358 |
| on | 4 | 436 jobs/s | 12 ms | 13 ms | 17 ms | 2654 |
| on | 8 | 537 jobs/s | 12 ms | 14 ms | 15 ms | 25628 |
| off | 1 | 281 jobs/s | 5 ms | 15 ms | 16 ms | 14864 |
| off | 2 | 555 jobs/s | 5 ms | 5 ms | 6 ms | 37635 |
| off | 4 | 1083 jobs/s | 5 ms | 5 ms | 6 ms | 15995 |
| off | 8 | 1271 jobs/s | 5 ms | 5 ms | 6 ms | 39817 |

Throughput is a backlog of jobs drained; latency is from `run_at` to the first attempt's start, under a steady load.

## What the database waited on during the drains

Sampled every 200 ms from `pg_stat_activity` (active client backends):

- sync, 1 workers: IPC:SyncRep 79%, CPU 14%, IO:WalSync 3%, LWLock:WALWrite 2%
- sync, 2 workers: IPC:SyncRep 68%, CPU 20%, LWLock:WALWrite 6%, IO:WalSync 3%
- sync, 4 workers: IPC:SyncRep 66%, CPU 16%, LWLock:WALWrite 6%, Lock:transactionid 4%
- sync, 8 workers: IPC:SyncRep 63%, CPU 21%, LWLock:WALWrite 5%, IO:WalSync 4%
- async, 1 workers: CPU 50%, LWLock:WALWrite 26%, IO:WalSync 17%, IPC:BgworkerShutdown 2%
- async, 2 workers: CPU 46%, LWLock:WALWrite 38%, IO:WalSync 10%, Client:ClientRead 5%
- async, 4 workers: LWLock:WALWrite 48%, CPU 33%, IO:WalSync 12%, Client:ClientRead 3%
- async, 8 workers: CPU 44%, LWLock:WALWrite 33%, IO:WalSync 14%, Client:ClientRead 5%

## Where it stops scaling, and why

**It scales almost linearly up to 4 workers, then flattens.** From 4 to 8 workers, throughput grows only 23% (synchronous) and 17% (asynchronous). On this machine, the ceiling is about 540 jobs/s with synchronous replication and about 1,270 jobs/s without.

**It isn't `SKIP LOCKED`.** Row-lock waits (`Lock:transactionid`) stay at about 4% or less in every run, so concurrent claimers almost never wait for each other. Skipping locked rows instead of queueing behind them is doing exactly its job.

**It's the commit path.** Every job costs at least two commits: the claim, which is batched across jobs, and the completion, which is one per job.
- **Synchronous replication:** each commit waits for the standby to confirm over the 5.5 ms round trip. `IPC:SyncRep` is 63–79% of all waiting, and latency has a floor of about 12 ms (two round trips). More workers help only because Postgres groups concurrent commits into one flush, and that grouping saturates.
- **Asynchronous replication:** the wait moves to writing and flushing the write-ahead log (`LWLock:WALWrite` plus `IO:WalSync`, 45–60%), with the rest CPU on one laptop running the whole stack.

**Synchronous replication costs about half the throughput, and about 7 ms of latency.** That is the price of never losing an acknowledged job in a failover (G1). The cloud phase re-measures it against a real network.

**Dead tuples pile up.** Each job's row is updated at least twice, and every update changes `state`, a column the claim index depends on. So Postgres can't update the row in place (no HOT updates): up to 40,000 dead tuples remained after a 10,000-job drain. Autovacuum kept up at these rates, but under sustained load the table and its claim index will grow until it can't.

**What would raise the ceiling, in order of payoff:**
1. **Complete in batches.** Report several results per call, the way heartbeats already work: one commit for many jobs instead of one per job.
2. **Tune autovacuum for `jobs`** (a lower `autovacuum_vacuum_scale_factor`), and delete or archive finished jobs.
3. **More `dispatch` instances.** One dispatcher serves every worker here.
