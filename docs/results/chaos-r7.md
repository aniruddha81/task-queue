# Chaos run r7: PASS

Seed `7` · 5m0s of load and faults

- 2372 jobs submitted, 2650 acknowledgements, 2381 jobs in the database: map[dead:133 succeeded:2248]
- email: 190 messages, 0 duplicate deliveries (expected: at-least-once)

## Faults injected

| Fault | Times |
| --- | --- |
| crash-postgres | 6 |
| cut-network | 6 |
| kill | 3 |
| pause-dispatch | 7 |
| pause-scheduler | 5 |
| pause-worker | 5 |

## Guarantees

| | Guarantee | Result | Exercised | Violations |
| --- | --- | --- | --- | --- |
| G1 | An acknowledged job is never lost | PASS | 9 kills of jobs/gateway/auth and Postgres crashes | 0 |
| G2 | Same key, same job; a changed request gets 409 | PASS | 331 repeated keys and conflicting resubmits | 0 |
| G3 | Only the current lease holder records a result | PASS | 190 late results rejected by fencing | 0 |
| G4 | Effects at most once, exactly once on success | PASS | 72 duplicate deliveries absorbed by the sinks | 0 |
| Email | At-least-once delivery (SMTP can't deduplicate) | PASS | 190 succeeded email jobs | 0 |
| G5 | Every job ends succeeded, dead or cancelled | PASS | 2381 jobs that finished | 0 |
| G6 | Cron: one job per tick, none skipped | PASS | 3 leader changes while ticking | 0 |
| G7 | One leader per epoch: chores never overlap | PASS | 3 leader changes | 0 |
| G8 | No attempt starts before run_at | PASS | 83 delayed jobs | 0 |
