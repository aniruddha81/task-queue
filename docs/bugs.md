# Bug diary

Bugs found by the project's own tests, and what each one taught. Newest first.

## 2026-10-08 · The experiment probe died silently and reported an empty table (tooling bug)

**Found by:** the first `kill-gateway` experiment (week 11). The checker passed, but the probe's table read 0/0 in every window.

**Cause:** the probe ran under the runner's `set -u`. On Windows, curl's TLS (Schannel) couldn't check certificate revocation, so the first login failed, the token stayed unset, and the next line's `$token` killed the probe on its first iteration with nothing logged. A table of zeros could be read as "nothing failed".

**Fix:** the probe turns `set -eu` off for itself, adds `--ssl-no-revoke` when curl uses Schannel, logs in again whenever it has no token, and reads curl's body and status from one call (with path conversion off, Windows curl and bash also disagreed on where `/tmp` is). The run was repeated; its numbers are in [act1-fragility.md](results/act1-fragility.md).

**Lesson:** an observer that can fail must fail loudly. Zero observations is a result to question, not a quiet window.

## 2026-10-08 · The sweep reported "nothing left" when it couldn't look (tooling bug)

**Found by:** the first `make down` in the cloud (week 10). The sweep printed an Azure CLI usage error, `the following arguments are required: --resource-group`, and then `sweep: nothing left`.

**Cause:** each check captured a query's output and treated empty output as "clean". A query that failed printed nothing to stdout, so it counted as a pass.

**Fix:** each query runs inside the check, and a failed query fails the sweep with exit code 2. The disk check now uses a query that needs no resource group. With the CLIs taken off `PATH`, the sweep now fails, where it used to pass.

**Lesson:** this is the checker problem again: a check that can't see must not report what it didn't see. It applies to cleanup scripts as much as to the chaos checker.

## 2026-10-08 · The first cloud deploy couldn't sign in: GitHub's OIDC subject now carries IDs

**Found by:** the first push to `release` (week 10). Verify and build passed, then `configure-aws-credentials` failed with `Not authorized to perform sts:AssumeRoleWithWebIdentity`.

**Cause:** the trust policies (the AWS role and the Azure federated credential) expected the documented subject `repo:aniruddha81/task-queue:environment:cloud`. CloudTrail's refused request showed what GitHub actually sent: `repo:aniruddha81@53252451/task-queue@1409923352:environment:cloud`. GitHub now qualifies the owner and the repository with their immutable IDs.

**Fix:** both trusts use the ID-qualified subject. This is also safer: a deleted and re-created repository with the same name can't inherit the trust.

**Lesson:** read the refused request, not the documentation. CloudTrail recorded the exact claim; guessing would have meant loosening the condition to a wildcard.

## 2026-10-08 · The chaos run in CI could start before `jobs` had auth's keys (harness bug)

**Found by:** the second release run (week 10). The chaos step failed within one second, before any load, after an identical run had passed an hour earlier.

**Cause:** CI waited for the gateway's `/readyz`, which covers only the gateway. In Compose, `jobs` starts before `auth` and retries its JWKS fetch every second until it gets the keys, rejecting every request meanwhile. The gateway starts after both, so it can be ready while `jobs` is not, and the harness's first call (creating its cron schedule) wasn't retried. Neither the job log (sign-in only) nor a local run (where `jobs` gets the keys at once) showed it directly; this is the only path that fails in under a second.

**Fix:** the harness retries schedule creation for up to 30 s, but only on 502 and 401. Neither creates a schedule, so a retry can't make a duplicate. CI also turns the harness's failure lines into workflow annotations, which can be read without signing in. The next release's chaos run passed.

**Lesson:** "ready" has to mean ready for the caller's first request, not ready at the front door.

## 2026-10-08 · The chaos harness's own network fault never fully healed (harness bug)

**Found by:** the first chaos run after the HTTP/2 fix. 1,501 jobs were still unfinished after quiescing, and the workers logged `lookup dispatch: no such host`, although the dispatch container was running and attached to the network.

**Cause:** the harness, not the system under test. Reverting a network cut used a plain `docker network connect`, which re-attaches a container under its container name (`taskqueue-dispatch-1`) but not its Compose service alias (`dispatch`). Every client that finds dispatch by service name lost it permanently. Earlier runs passed only because dispatch happened not to be cut.

**Fix:** the revert, and the final heal, reconnect with `--alias <service>`. After the alias was restored by hand, all 1,498 stuck jobs drained in about 2 minutes, confirming the diagnosis. Nothing was lost: G1–G4 held throughout.

**Lesson:** a fault's revert must restore exactly the state from before the fault. Otherwise the harness injects a permanent fault it never reports. The two G5 failures in the first mutant runs (`noeffectkey`, `ackfirst`) were this bug; each mutant was still caught by the guarantee it targets.

## 2026-10-08 · Workers stopped claiming for minutes after a network partition healed

**Found by:** the chaos test (week 7). In two runs, hundreds of jobs stayed `available` for more than six minutes after the faults stopped. They finished eventually, so nothing was lost, but G5 (liveness) failed the quiesce limit.

**Reproduction:** disconnect both workers from the network for 20 s and reconnect them. A new job then waited **more than 2 minutes**, and the workers logged one `claim: deadline_exceeded` every 30 s.

**Cause:** a network cut silently kills open TCP connections; on reconnect a container can even get a new IP address. Go's HTTP/2 client kept sending each claim into the same dead connection. Each claim timed out after 30 s, the retry picked the same connection, and that continued until the operating system gave up on the socket, which can take many minutes.

**Fix:** HTTP/2 health pings on every internal client (`serve.Transport` and the SDK's default client). A connection silent for 10 s gets a ping, and an unanswered ping closes it after 5 s. After the fix, the same reproduction runs its job **1 s** after the network returns.

**Lesson:** a network partition isn't over when the cable is plugged back in. Every long-lived connection needs its own liveness check.

## 2026-10-08 · The chaos test can't catch a missing leader epoch fence (mutant `noepoch`)

**Found by:** `deploy/local/mutants.sh` (week 7). Four of the five mutant builds failed the checker, each on the guarantee it removes. `mutant_noepoch` passed, despite 5 scheduler pauses and 3 leader changes.

**Why:** a leader that resumes from a pause first checks whether its lease is due for renewal. After any pause longer than a third of the lease (5 s), the renewal fails, and the leader steps down *before* running a chore. The epoch fence only matters when a pause lands in the short gap between a successful renewal and the start of a chore transaction. A GC pause or a VM freeze can land there; a randomly timed `SIGSTOP` almost never does.

**Decision:** keep the fence. It is the only protection for that gap, and `TestPausedLeaderIsFenced` (internal/scheduler) checks it deterministically: it runs a chore with a stale epoch after a takeover. In week 5, the same mutation failed that test 3 runs out of 3. The chaos test's G7 check stays, but this mechanism is proven by the unit test, not by chaos.

## 2026-10-08 · `retry_backoff` overflowed from about attempt 44

**Found by:** `TestBackoff` (week 4), which checks the backoff for attempts 1 to 1000.

**Cause:** `interval '1 second' * power(2, attempt - 1)` leaves Postgres's interval range at about 2^43 seconds. Users may set `max_attempts` up to 50, so a job's 44th failure would have made `Fail` and the reaper error out, leaving the job stuck in `running` forever.

**Fix:** migration `00005` caps the exponent at 20 before multiplying. Every delay that worked before is unchanged.

## 2026-10-08 · Cron fired twice when clocks fell back

**Found by:** probing the cron library before using it (week 5).

**Cause:** in a zone with DST, a daily `01:30` schedule matches twice on the night clocks fall back, because 01:30 happens twice. The library returned both times, so a daily report would have run twice.

**Fix:** a tick is a local wall-clock time and fires at most once (`queue.Cron.Next`). The spring-forward case, a time that doesn't exist that day, stays skipped, and this is documented. Asia/Kolkata, the project's zone, has no DST.
