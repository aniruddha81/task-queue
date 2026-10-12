# 0006. Workers talk only to `dispatch`

**Status:** accepted · **Decided:** week 1, built in week 3

## Context

Workers run user handlers on separate VMs in both clouds. If they claimed jobs directly from Postgres, every worker would hold database credentials. Every worker would also need a database connection, and each of its claims would be a round trip across the clouds whenever the primary is in the other cloud.

## Decision

Workers speak only to `dispatch`, using gRPC over mTLS (connect-go). `dispatch` offers a long-poll claim, batched heartbeats, complete and fail, and it keeps the `nodes` registry. Each worker has a list of dispatchers, nearest first, and falls back to the next one on errors (`sdk/go/worker`). Any dispatcher in either cloud can claim; only the scheduler's singleton chores need a leader.

## Consequences

- **Good:** database credentials never leave the service tier. A compromised worker can report results only for jobs it holds a lease token for.
- **Good:** the number of database connections follows the number of dispatchers, not the number of worker slots. The worker API is versioned Protobuf, and CI runs `buf lint` and `buf breaking` on it.
- **Bad:** one more hop. A worker in the cloud without the primary still pays the cross-cloud round trip, inside `dispatch`.
- **Bad:** workers still count as trusted code. A worker with a valid certificate can report success without doing the work.

## Evidence

- Stopping or pausing either worker VM in production failed 0 probe requests, and its jobs were claimed again elsewhere ([act2-resilience.md](../results/act2-resilience.md)).
- A bug in this path: after a network cut, HTTP/2 kept sending claims into a dead connection. Health pings now close such connections ([bugs.md](../bugs.md)).
