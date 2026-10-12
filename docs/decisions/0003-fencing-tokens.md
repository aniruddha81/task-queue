# 0003. Fencing tokens: a lease token per attempt, an epoch per leader

**Status:** accepted · **Decided:** weeks 3 (lease tokens) and 5 (epochs) · **Guarantees:** G3, G7

## Context

Leases decide *when* a worker or a leader should give up. They can't stop one that doesn't know its lease has expired. A process paused past its lease (GC, `SIGSTOP`, a frozen VM) wakes up and acts as if nothing happened. No timeout fixes this, because a pause can last longer than any timeout.

## Decision

- **Jobs:** each claim gets a random `lease_token`. Heartbeat, complete and fail all run `UPDATE … WHERE id = $1 AND lease_token = $2 AND state = 'running'`. A stale token matches no row and changes nothing. A retried complete that already committed is recognised by its token in `job_attempts` and returns OK.
- **The scheduler leader:** a lease row with `holder`, `epoch` and `expires_at`, where each takeover increments `epoch`. Every chore transaction starts with `SELECT … FOR SHARE` on that row at its own epoch. A stale leader finds no row and steps down. `FOR SHARE` makes a takeover wait until a running chore commits, so chores of two epochs never overlap.

## Consequences

- **Good:** a pause of any length, a slow network or a wrong clock costs time, never correctness.
- **Good:** the check is in the same statement as the write, so there is no gap between checking and writing.
- **Bad:** every write path has to carry the token. A heartbeat that matches no row has to cancel the handler (the SDK does this).
- **Note:** the epoch fence protects only a narrow gap, between a renewal and the start of a chore. Cron ticks have a unique key and the reaper is token-fenced, so G1–G6 don't depend on the epoch.

## Evidence

- Production runs rejected 290–410 late results each, and none of them was recorded ([act2-resilience.md](../results/act2-resilience.md)).
- `mutant_nofence` failed G3 with 32 violations ([report](../results/chaos-r1791462536690885800-mutant_nofence.md)).
- The chaos test couldn't catch `mutant_noepoch`, because a random pause almost never lands in its gap. `TestPausedLeaderIsFenced` covers that gap deterministically instead ([bugs.md](../bugs.md)).
- Discussion: [design.md, section 5](../design.md#5-fencing-tokens).
