# 0007. Synchronous replication when possible, not strict

**Status:** accepted · **Decided:** week 9 (locally), week 13 (across the clouds) · **Guarantees:** G1

## Context

With Postgres in both clouds, the system has to decide what a commit means while the standby can't be reached:

- **Asynchronous:** commits never wait, but a failover can lose acknowledged jobs.
- **Strict synchronous:** nothing acknowledged is ever lost, but every write stops while the standby is gone. That includes every cut between the clouds and every standby reboot.

## Decision

Patroni with `synchronous_mode: true` and `synchronous_mode_strict: false`. While the standby is healthy, every commit waits for it. If it stops answering, `wal_sender_timeout` (5 s, down from 60 s) drops it, and Patroni turns synchronous replication off so that writes continue. Synchronous mode returns when the standby catches up. Patroni promotes only a standby that was in sync. Services list both nodes (`target_session_attrs=read-write`) and retry database calls for up to 30 s, so a failover shows up as a slow request.

## Consequences

- **Good:** a failover never loses an acknowledged job, and a cut or a standby reboot never stops writes for more than a few seconds.
- **Bad: the double failure.** If the primary's disk is lost while the standby is out of sync, the jobs acknowledged in that window are lost. G1 is stated with that exception.
- **Bad:** synchronous commit costs about half the throughput locally and two-thirds in production.
- **Bad:** a crashed primary leaves about 20 s with no primary, because Patroni won't accept a `ttl` below 20 s.

## Evidence

- Failover in both directions, a cut, the witness lost, and both database VMs replaced: no acknowledged job lost ([act2-database.md](../results/act2-database.md)). The same faults in production all passed too ([act2-resilience.md](../results/act2-resilience.md)).
- 101 against 307 jobs/s in production; `IPC:SyncRep` was 80% of the database's waiting ([load-cloud.md](../results/load-cloud.md)).
- Discussion: [design.md, section 7](../design.md#7-replication-synchronous-when-possible).
