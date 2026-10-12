# 0012. Every check must be shown to fail

**Status:** accepted · **Decided:** week 7

## Context

The whole claim rests on the chaos checker. A checker that passes broken code, or passes because the fault it needs never happened, proves nothing while looking like proof.

## Decision

- **A guarantee whose fault never fired fails.** Each line of the report states how many times its fault was exercised, such as late results rejected or duplicates absorbed.
- **Each mechanism has a mutant build** (`-tags mutant_<name>`, `internal/mutant`) that removes it: lease fencing, idempotency-key uniqueness, the sinks' effect-key check, tick uniqueness, the leader epoch, and acknowledging only after commit. `deploy/local/mutants.sh` runs them, and every one must be caught. A mutant the chaos test can't catch is either covered by a deterministic test or its mechanism is deleted, and `bugs.md` records which.
- The same rule applies to tooling. The cleanup sweep, the experiment probe and CI's chaos gate must fail when they can't see.

## Consequences

- **Good:** each guarantee's check has been seen to fail at least once, on the mechanism it guards.
- **Good:** it found real bugs in the tooling: a sweep that reported "nothing left" when its query failed, a probe that reported zeros, and a `| tee` that let a failing chaos run pass CI.
- **Bad:** mutants are code paths in production source behind build tags, which have to be kept in step with the real mechanism.

## Evidence

- `nofence` failed G3, `nokey` failed G2, `noeffectkey` failed G4, and `ackfirst` failed G1 ([results/](../results/)).
- `noepoch` and `notick` passed the chaos test. Each is caught by a deterministic test instead (`TestPausedLeaderIsFenced` and `TestTickSchedulesConcurrentlyNoDuplicates`), because other layers stop a random fault from reaching the gap it opens ([bugs.md](../bugs.md)). The concurrent tick test itself first had to be fixed to count jobs, not tick rows.
- Discussion: [design.md, section 9](../design.md#9-how-the-claim-is-checked).
