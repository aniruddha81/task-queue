# Act 2 in production: survivable chaos under the probe — weeks 14–15

Oct 11, 2026, 19:02–20:42 UTC. Production since go-live: eight VMs running 24/7.

| VM | Cloud | Roles |
| --- | --- | --- |
| `svc-a` | AWS Mumbai | gateway, auth, jobs, dispatch, scheduler |
| `work-a` | AWS Mumbai | worker |
| `pg-a` | AWS Mumbai | Postgres + Patroni, etcd |
| `ops-a` | AWS Mumbai | harness, sinks, monitoring (never faulted) |
| `svc-z` | Azure Central India | gateway, auth, jobs, dispatch, scheduler |
| `work-z` | Azure Central India | worker |
| `pg-z` | Azure Central India | Postgres + Patroni, etcd |
| `wit` | AWS Hyderabad | etcd (the witness) |

Each experiment is `act1.sh <scenario>` run through `experiment.yml`, the same way as weeks 11 and 13. The chaos harness on `ops-a` loads the system for 6 minutes through the public name, then quiesces and runs the checker. The fault starts 90 s into the load and is held for 2 minutes. A VM is stopped the way a crash takes it: forced, with no clean shutdown. Meanwhile the release probe, outside both clouds, sends about 10 requests a second (`/version`, an authenticated list, an idempotent submit). Each one opens a new connection, has a 10 s timeout and is **never retried**. The eleven experiments ran back to back, each starting on whatever the previous one left behind.

## Done-when checklist

| Check | Result |
| --- | --- |
| Every survivable fault, in production, under the probe | ✅ 11 experiments, **every checker PASS**: kill any VM, pause each worker, fail the database over both ways, cut the clouds apart, stop all of Azure, lose the witness |
| Before-and-after numbers against Act 1, with availability measured by the probe | ✅ below |
| Zero failed probe requests during deploys | ✅ 15 rollouts in a row since week 12: 55,833 requests, **0 failed** |
| Stop AWS Mumbai | ⏳ not run yet: `stop-aws` included the witness and couldn't have worked ([bug diary](../bugs.md)); fixed in this release |
| Cloud load test, cost per million jobs, cross-cloud transfer | ⏳ not run yet: `load.sh` died before it started ([bug diary](../bugs.md)); fixed in this release |

## Act 1 → Act 2

Act 1's probe asked every 2 s; Act 2's asks about 10 times a second. "Answered" is the share of probe requests that succeeded while the fault was held. For Act 2 it's estimated from the probe's steady rate, since its summary doesn't split before, during and after.

| Fault | Act 1 (week 11) | Act 2 in production |
| --- | --- | --- |
| **Lose the gateway** (Act 1: kill the only gateway; Act 2: stop `svc-a`, which runs a gateway and every other stateless service) | **Everything public down for 59 s**; 73% answered | **2 failed** of about 1,700, both in flight on `svc-a` as it died; 99.9% answered |
| **Cut the clouds apart** | **New logins down for the whole cut** (3/10); Azure's workers idle; 92% answered | 177 failed, sent over 39 s, then full service; 88.6% answered. The primary was on the cut-off side, so this run included a failover (below) |
| **Stop all of Azure** | **New logins down for the whole stop** (1/14); 91% answered | **5 failed** of about 2,300, all in flight on Azure's VMs as they died; 99.8% answered |
| **Stop all of AWS Mumbai** | **Everything down for 204 s**; 5% answered | ⏳ not run yet (see the checklist) |
| **Lose the database primary** | Not survivable (one database) | 113 and 114 failed, sent over ~17 s while there was no primary; 93% answered |

Act 1 needed AWS, Azure and the link between them all at once. In Act 2, losing a VM, Azure, or the link costs at most the requests sent in the first half-minute, and losing a stateless VM or a whole cloud costs only the requests in flight.

## Every experiment

| Scenario | Probe failed | Failures, from the fault | Answered during the fault | Checker | Reports |
| --- | --- | --- | --- | --- | --- |
| Stop `svc-a` | 2 of 3,926 | +2.6 s, +2.7 s: `EOF`, in flight | 99.9% | PASS | [probe](prod-stop-svc-a-probe.md) · [checker](chaos-r1791745429027603040t1791745429-stop-svc-a.md) |
| Stop `svc-z` | 2 of 3,947 | +16.2 s, +16.3 s: timeouts, sent at +6 s, in flight | 99.9% | PASS | [probe](prod-stop-svc-z-probe.md) · [checker](chaos-r1791745964170632843t1791745964-stop-svc-z.md) |
| Stop `work-a` | **0 of 4,227** | — | 100% | PASS | [probe](prod-stop-work-a-probe.md) · [checker](chaos-r1791746498929100998t1791746498-stop-work-a.md) |
| Stop `work-z` | **0 of 3,963** | — | 100% | PASS | [probe](prod-stop-work-z-probe.md) · [checker](chaos-r1791747072257856084t1791747072-stop-work-z.md) |
| Pause `work-a` (every container frozen for 2 minutes, past every lease) | **0 of 4,111** | — | 100% | PASS | [probe](prod-pause-work-a-probe.md) · [checker](chaos-r1791747607957036650t1791747607-pause-work-a.md) |
| Pause `work-z` | **0 of 3,783** | — | 100% | PASS | [probe](prod-pause-work-z-probe.md) · [checker](chaos-r1791748180923216818t1791748180-pause-work-z.md) |
| Crash the primary `pg-z`: failover Azure → AWS | 113 of 4,372 | +23.7 … +40.5 s: timeouts | 92.7% | PASS | [probe](prod-failover-azure-to-aws-probe.md) · [checker](chaos-r1791748711931684264t1791748711-stop-leader.md) |
| Crash the primary `pg-a`: failover AWS → Azure | 114 of 4,153 | +22.0 … +39.1 s: timeouts | 93.1% | PASS | [probe](prod-failover-aws-to-azure-probe.md) · [checker](chaos-r1791749275553055303t1791749275-stop-leader.md) |
| Cut the clouds apart (primary on `pg-z`, the cut-off side) | 177 of 3,802 | +13.8 … +52.7 s: timeouts | 88.6% | PASS | [probe](prod-cut-clouds-probe.md) · [checker](chaos-r1791749822432227205t1791749822-cut-clouds.md) |
| Stop all of Azure (primary on `pg-a`) | 5 of 3,630 | +6.5 … +16.9 s: `EOF`, reset, timeouts, all sent by +7 s, in flight | 99.8% | PASS | [probe](prod-stop-azure-probe.md) · [checker](chaos-r1791750353609484297t1791750353-stop-azure.md) |
| Stop the witness `wit` | **0 of 3,935** | — | 100% | PASS | [probe](prod-stop-wit-probe.md) · [checker](chaos-r1791750854405640489t1791750854-stop-wit.md) |
| **Total** | **413 of 43,849 (0.94%)** over 1 h 40 min of back-to-back faults | | | **11 / 11** | |

"From the fault" is measured from the moment the script began applying it; stopping a VM takes several seconds, so the crash itself comes a little later. A timeout is reported 10 s after its request was sent.

**Safety held everywhere.** In all eleven runs the checker found no acknowledged job lost (G1), no job with two successful attempts or a success that wasn't the last attempt (G3), no effect applied twice (G4), and every job finished (G5). Leadership moved in six runs (both `svc-*` stops, both failovers, the cut, Azure's stop), and each time G6 and G7 were exercised and passed: one leader change, no cron tick duplicated or skipped, no chore by a stale leader. In the pause runs, the frozen worker's leases expired and its jobs were claimed again; G3 shows that nothing it finished afterwards was recorded. The G3 evidence count (late results the database rejected) doesn't single out the paused worker's late results: the workload produces 290–410 of them in every run, whatever the fault.

## Where the failed requests came from

There are three causes, and every failed request fits one of them:

- **In flight on a VM as it died** (stop `svc-a`, stop `svc-z`, stop Azure). A request whose connection was already open to the dying gateway gets `EOF`, a reset, or no answer at all. New connections were fine from the start: Traffic Manager returns both gateways' addresses, and the client's dialer moves on to the live one. Stopping a worker or the witness failed nothing.
- **No primary until the replica is promoted** (both failovers). Patroni's history puts the promotions at 20:00:59.6 and 20:10:22.2 UTC, about 26 s after the first failed request was sent each time (Patroni's 20 s `ttl`, plus its loop and the crash itself). The services hold requests and retry the database for up to 30 s, so requests sent in the last ~10 s before the promotion succeeded slowly; only the earlier ones outlived the probe's 10 s timeout. The old primary rejoined as the synchronous standby on its own both times.
- **A cut with the primary on the cut-off side.** The two failovers left the primary on `pg-z`. When the cut isolated Azure, `pg-z` lost the etcd majority and stepped down, and `pg-a` (with the witness) was promoted at 20:19:17.5, about 22 s after the first failed request was sent. Requests kept failing until 20:19:35, while DNS still returned `svc-z`, whose services could no longer reach any primary, until Traffic Manager pulled it. That gave a window of about 39 s, against 26 s in week 13, when the primary was on the majority side and the cut cost only the DNS delay. No acknowledged job was lost: Patroni promoted only the synchronous standby.

Patroni's history after the run: timelines 5 (`pg-a`), 6 (`pg-z`) and 7 (`pg-a`), one per forced promotion. `pg-a` leads with `pg-z` as the streaming synchronous standby, with zero lag.

## Availability outside the experiments

- **Deploys:** every release since week 12 deployed under the external probe with **zero failed requests**: week 12's eight (29,150 requests), week 13's five (18,932), and `19a00ee` (3,802) and `9de38de` (3,949) since then. That's 15 in a row and 55,833 requests. (`4fcc718` failed CI and never deployed; two queued releases were cancelled before any job ran, replaced by the next push.)
- **Uptime monitor** (`probe.yml`, 2 minutes of the same probe): 15 runs since Oct 9, **20,285 requests, 0 failed**. GitHub ran the "every 15 minutes" schedule only every 3–6 hours, so this is a sample, not continuous coverage; a gap between runs could hide a short outage. One run (20:39 UTC) landed during the witness experiment.
- **The health sweep** (`maintenance.yml`) covers what the public probe can't see, such as a gateway still answering while its twin is gone.

## Not run, by design

These would cause downtime, so the plan keeps them out of production:

- **Both gateways down at once** (`kill-gateway` now kills both): there is nothing left to answer.
- **All of AWS**, including the witness: two of three etcd votes gone, so Azure can't promote itself (a known limit in `full_plan(v4).md`). Losing AWS Mumbai alone is survivable, and is the pending `stop-aws` run.
