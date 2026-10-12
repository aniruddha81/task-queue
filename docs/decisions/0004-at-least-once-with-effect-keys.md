# 0004. At-least-once execution, with required idempotency keys and effect keys

**Status:** accepted · **Decided:** week 1, built in weeks 2 (submit) and 7 (sinks) · **Guarantees:** G2, G4

## Context

Exactly-once execution is impossible. A worker can crash after doing the work and before reporting it, and the scheduler can't tell that from a crash before the work, so it must run the job again. The same doubt arises at submission: a client whose request timed out doesn't know whether its job was created.

## Decision

The scheduler promises at-least-once execution and makes every repeat safe:

- **The idempotency key is required on every submit.** `UNIQUE (owner_id, idempotency_key)` makes a retried submit return the original job. The stored request (`jsonb`) gives `409` to a key reused for something different. Any error after `COMMIT` was sent returns `503 outcome unknown`, telling the client to retry with the same key.
- **Each job gets an effect key,** `owner_id:idempotency_key`. It stays the same across attempts, redrives and resubmissions, and each handler passes it to the destination. A destination stores the key in the same transaction as the effect, and treats "already applied" as success.
- **Email is kept as the counterexample.** SMTP can't deduplicate, so `email.send` is at-least-once, and the checker counts its duplicates instead of hiding them.

## Consequences

- **Good:** "I don't know if it worked" always has the same answer: do it again with the same key. Clients, the gateway's failover to the other cloud, and the services' database retries all rely on this.
- **Bad:** effect-once holds only at destinations that check the key. The scheduler can't make a third party idempotent.
- **Bad:** keys never expire, because jobs are never purged in this project.

## Evidence

- 50 concurrent identical submits create exactly one job.
- In production runs the sinks absorbed 59–96 duplicates per run, with no effect applied twice. Across 30 recorded chaos runs, 6 duplicate emails got through, as expected.
- `mutant_nokey` failed G2 and `mutant_noeffectkey` failed G4 ([results/](../results/)).
- Discussion: [design.md, section 4](../design.md#4-at-least-once-execution-effects-once).
