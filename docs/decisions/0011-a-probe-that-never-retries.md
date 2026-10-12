# 0011. Zero downtime is measured by an external probe that never retries

**Status:** accepted · **Decided:** week 12

## Context

"Zero-downtime deploys" is easy to claim and hard to check. Health checks from inside the clouds can't see DNS, Traffic Manager or the client's view. A client that retries hides exactly the failures the check exists to find.

## Decision

Every rollout runs `deploy/release/probe` on a GitHub runner, outside both clouds. The probe sends about 10 requests a second through the public name: `/version`, an authenticated list, and an idempotent submit. Each request resolves DNS afresh, opens a new connection, has a 10 s timeout, and is **never retried**. A single failed request fails the release and triggers rollback. The same probe watches every chaos experiment, and runs on a schedule as an uptime monitor.

## Consequences

- **Good:** "zero failed requests" is measured on every deploy, not argued. A regression in the drain rolls itself back.
- **Good:** in experiments, the probe's timeline shows exactly when and why requests failed.
- **Bad:** it is strict enough to catch the vantage point. Probing from a home network gave false failures, so experiments run on GitHub runners too.
- **Bad:** GitHub runs the uptime schedule irregularly, every 3–6 hours rather than every 15 minutes, so between deploys the probe is a sample, not continuous coverage.

## Evidence

- 16 releases in a row from week 12 to week 15: 59,599 requests, 0 failed ([act2-resilience.md](../results/act2-resilience.md)).
- A probe that died on its first request once reported an empty table as success. It now fails loudly ([bugs.md](../bugs.md)).
- Discussion: [design.md, section 8](../design.md#8-zero-downtime-deploys-and-the-probe-that-proves-them).
