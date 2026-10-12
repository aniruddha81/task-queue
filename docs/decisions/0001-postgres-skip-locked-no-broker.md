# 0001. Postgres with `SKIP LOCKED` is the queue; no message broker

**Status:** accepted · **Decided:** week 1, built in week 3 · **Guarantees:** G1–G5, G8

## Context

A job is more than a message. Besides its payload, it has an owner, an idempotency key, a state, a lease, a log of attempts, and for cron jobs a tick. A claim has to change several of these together. With a broker, delivery would live in one system and this state in another, so every claim and completion would span two systems that fail independently.

## Decision

Jobs are rows in Postgres. Workers claim them with one statement: `SELECT … FOR UPDATE SKIP LOCKED` picks the rows, an `UPDATE` leases them, and an `INSERT` logs the attempts (`internal/queue/lease.go`). Heartbeat, complete, fail and the reaper are single statements too.

## Consequences

- **Good:** each step is one transaction, and correctness lives in constraints and `WHERE` clauses that a crash can't skip. There is no outbox, no consumer-side dedup table, and only one failover story.
- **Good:** `SKIP LOCKED` lets any number of claimers work at once without queueing behind each other's row locks.
- **Bad:** throughput is bounded by commits. Every job costs at least two (the claim and the completion), and with synchronous replication each commit waits one round trip between the clouds.
- **Bad:** every update changes `state`, which the claim index covers, so updates are never HOT and dead tuples pile up under sustained load.
- **When to revisit:** past tens of thousands of jobs a second, or for fan-out and event streams. Those are broker workloads.

## Evidence

- About 540 jobs/s (synchronous) and 1,270 (asynchronous) on a laptop; 101 and 307 in production. Row-lock waits stayed at or below 4%, while commit waits (`IPC:SyncRep`) took 63–80% of the waiting. See [load-local.md](../results/load-local.md) and [load-cloud.md](../results/load-cloud.md).
- 5 workers and 1,000 jobs produce exactly one attempt per job, 20 runs in a row under `-race`.
- Discussion: [design.md, section 3](../design.md#3-postgres-as-the-queue-skip-locked-instead-of-a-broker).
