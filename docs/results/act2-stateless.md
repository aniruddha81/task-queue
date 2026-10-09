# Act 2, step 1: no single point of failure in the stateless tier — week 12

Oct 9, 2026. Every stateless service now runs in both clouds: `svc-a` (AWS Mumbai) and `svc-z` (Azure Central India) each run the gateway, `auth`, `jobs`, `dispatch` and a scheduler. Traffic Manager returns both gateways' addresses, and each gateway prefers its own cloud's `auth` and `jobs` and falls back to the other cloud's. The database is still one node, `pg-a` (week 13 moves it to both clouds).

## Done-when checklist

| Check | Result |
| --- | --- |
| Five rollouts in a row with zero failed probe requests | ✅ seven in a row: 25,713 requests, 0 failed (below) |
| Stopping any one stateless VM fails no request beyond its in-flight ones | ✅ `svc-a` and `svc-z`: only requests in flight on the VM at the moment it stopped failed (below) |

## How a release reaches both clouds with nothing dropped

`rollout.sh` deploys one VM at a time. For a VM with a gateway:

1. **Drain.** Its Traffic Manager endpoint is disabled, and the rollout waits 60 s (six times the 10 s DNS TTL), so clients move to the twin.
2. **Deploy.** Services restart one at a time, with the gateway last. Each one finishes its in-flight requests on `SIGTERM` (Go's `Server.Shutdown`, with a 35 s stop grace period) and must report the new version and ready before the next one starts.
3. **Undrain.** The endpoint is enabled again, and the rollout waits until Traffic Manager's own probe sees it Online. A gateway reports ready only when its nearest `jobs` and `auth` do.

A gateway is drained only when another one is Online. Otherwise it is deployed in place and the rollout says so. That happened exactly once, in the release that brought `svc-z`'s gateway up.

Behind the gateways, a request that can't reach its nearest `jobs` or `auth` is retried on the twin in the other cloud: always if the connection was never made, and otherwise only for a read or a write carrying an `Idempotency-Key`, which makes a repeat return the original result.

## The external probe

`deploy/release/probe` runs on the GitHub-hosted runner, outside both clouds, from before the first VM changes until after the smoke test. It sends about 10 requests a second through `https://tq-aniruddha81.trafficmanager.net`: `/version`, an authenticated list and an idempotent submit, each on a new connection (so DNS is resolved afresh) and **never retried**. One failed request fails the release and rolls it back. `probe.yml` runs the same probe for 2 minutes every 15 minutes as the uptime monitor.

## Rollouts

| # | Release | What it carried | Probe |
| --- | --- | --- | --- |
| 1 | `869dfc1` | shared-cache fix; gateway converges last | 3,691 requests, **0 failed** |
| 2 | `5926391` | probe summary as an annotation | 3,578 requests, **0 failed** |
| 3 | `11950c2` | stop-VM experiment script | 4,127 requests, **0 failed** |
| 4 | `dadd8ee` | README | 3,637 requests, **0 failed** |
| 5 | `b2e2fb9` | deployment plan | 3,673 requests, **0 failed** |
| 6 | `d78db54` | gateway ready only with its upstreams | 3,622 requests, **0 failed** |
| 7 | `7dc66ab` | experiment workflow; CI actions on Node 24 | 3,385 requests, **0 failed** |

Each rollout restarts every service in both clouds, and each passed the full CI and a 5-minute chaos run first (every checker verdict: PASS). The next release, `3fcba90`, was stopped by its chaos run; see below.

## Stopping a stateless VM

`experiment.yml` stops one VM for 2 minutes while the probe runs beside it, both on a GitHub-hosted runner. The VM is stopped through the cloud's API, the way a crash or an outage takes it, with no drain.

| VM | Stopped | Started | Probe | Failed requests |
| --- | --- | --- | --- | --- |
| `svc-a` (AWS) | 11:39:19 | 11:42:09 | 4,792 requests, 1 failed | 11:39:22, connection reset by `svc-a` |
| `svc-z` (Azure) | 11:51:23 | 11:54:31 | 4,792 requests, 2 failed | 11:51:28, connection reset and EOF from `svc-z` |

Every failure came from the stopping VM's own address within 5 s of the stop: requests in flight as its services shut down. Nothing failed while it was down: Traffic Manager stopped returning it, and a client given both addresses fell through to the other. Nothing failed after it came back either: it rejoined DNS only once its gateway, `jobs` and `auth` were all ready. Clients retry such in-flight failures with the same idempotency key, so no work is lost; that is the stated limit of "zero downtime".

## What it took to get here

- **The first stop-VM attempt failed the check:** after `svc-a` restarted, two requests hung for 10 s. A gateway then reported ready as soon as it had signing keys, so Traffic Manager returned it while its VM's `jobs` couldn't yet serve. Since `d78db54` a gateway is ready only when its nearest `jobs` and `auth` are.
- **Running the experiment from the admin laptop was unreliable:** its home connection dropped requests to a VM that was up, and it slept for 6 minutes during one run. The experiments moved to a GitHub runner, which is also where the release probe runs.
- **The chaos gate caught a real bug** once the `pipefail` fix let it fail: in release `3fcba90`'s CI, after the harness cut both schedulers off the network in turn, neither led again, because their database calls had no deadlines, and 66 jobs were left unreaped. Leadership steps now have deadlines, with a test that reproduces the dead connection (bug diary).
- **Three more bugs**, in the [bug diary](../bugs.md): the second gateway issued its own certificate instead of sharing the first's; a failing chaos run or probe could pass a workflow (`| tee` without `pipefail`; no chaos run had actually failed); and new HTTP/2 advisories against Go 1.27.1 (fixed by Go 1.27.2 and `x/net` 0.60.0).

## Still single

- **The database:** `pg-a` alone. Stopping it stops all writes; week 13 adds the standby in Azure, the etcd witness, and automatic failover.
- **Traffic Manager** itself, and DNS caching by clients that keep one address and ignore the TTL: these are in the plan's known limits.
