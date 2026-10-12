# 0005. One clock: the database's

**Status:** accepted · **Decided:** week 1 · **Guarantees:** G5, G8 (timing only)

## Context

Leases, deadlines, delayed jobs, backoff and cron all need "now". Services and workers run on VMs in two clouds whose clocks drift apart. If each component used its own clock, a fast worker clock could start a job before its `run_at`, and a slow one could keep a lease alive after the database considers it expired.

## Decision

Every time-based decision uses Postgres `now()` inside the statement that acts on it: claim, heartbeat, lease expiry, backoff, `run_at` and cron ticks. No VM's clock decides anything about a job. Just as important, **no safety guarantee depends on any clock**. Fencing tokens, epochs and unique tick keys make sure a wrong clock changes only *when* things happen.

The only two other uses of time:

- JWT expiry, checked on the VM's clock with 60 s of leeway and chrony running everywhere.
- After a failover, the new primary's clock. The skew between the two hosts is milliseconds, and leases are measured in seconds.

## Consequences

- **Good:** an entire class of clock-skew bugs can't happen, and nothing needs synchronised clocks to stay correct.
- **Good:** the chaos harness never has to make timing "right". Only the order of commits matters.
- **Bad:** every time comparison has to be done in SQL, and the claim query can't be served from a cache.
- **Bad:** a badly wrong database clock still delays or hurries work (liveness), even though it can't break safety.

## Evidence

- G8 (`started_at ≥ run_at` for every attempt) held in every chaos run, including across failovers between two database hosts.
- Discussion: [design.md, section 6](../design.md#6-one-clock-and-why-safety-doesnt-need-it).
