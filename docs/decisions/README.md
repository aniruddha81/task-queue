# Decision records

One short page per major decision: the context, what was decided, what it costs, and the evidence. The full argument is in [design.md](../design.md).

| # | Decision | Decided |
| --- | --- | --- |
| [0001](0001-postgres-skip-locked-no-broker.md) | Postgres with `SKIP LOCKED` is the queue; no message broker | week 1 |
| [0002](0002-one-database-for-jobs-dispatch-scheduler.md) | `jobs`, `dispatch` and `scheduler` share one database | week 1 |
| [0003](0003-fencing-tokens.md) | Fencing tokens: a lease token per attempt, an epoch per leader | weeks 3, 5 |
| [0004](0004-at-least-once-with-effect-keys.md) | At-least-once execution, with required idempotency keys and effect keys | week 1 |
| [0005](0005-the-database-clock.md) | One clock: the database's | week 1 |
| [0006](0006-workers-talk-only-to-dispatch.md) | Workers talk only to `dispatch` | week 1 |
| [0007](0007-synchronous-when-possible-replication.md) | Synchronous replication when possible, not strict | weeks 9, 13 |
| [0008](0008-three-etcd-sites-with-a-witness.md) | Three etcd sites, with a witness in AWS Hyderabad | week 13 |
| [0009](0009-traffic-manager-multivalue.md) | Azure Traffic Manager with MultiValue routing as the public entry point | weeks 10, 12 |
| [0010](0010-vms-hold-no-version.md) | VMs hold no version; they converge to the current release at boot | week 10 |
| [0011](0011-a-probe-that-never-retries.md) | Zero downtime is measured by an external probe that never retries | week 12 |
| [0012](0012-every-check-must-be-shown-to-fail.md) | Every check must be shown to fail | week 7 |
