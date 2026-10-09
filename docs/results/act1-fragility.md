# Act 1: the fragile split — week 11

Oct 9, 2026. Act 1 layout (role map in `deploy/terraform/session/main.tf`):

| VM | Cloud | Roles |
| --- | --- | --- |
| `svc-a` | AWS Mumbai | gateway (the only one), jobs, dispatch-a, scheduler-a |
| `work-a` | AWS | worker |
| `pg-a` | AWS | PostgreSQL primary (the only database) |
| `ops-a` | AWS | harness, sinks, Mailpit, Prometheus, Grafana (never faulted) |
| `svc-z` | Azure Central India | auth (the only one), dispatch-z, scheduler-z |
| `work-z` | Azure | worker |

Each experiment is `deploy/experiments/act1.sh <scenario>`. The chaos harness runs on `ops-a`: 6 minutes of mixed load through the public name (about 8 jobs/s, including `chaos.sleep` jobs with an affinity for either cloud), then it quiesces and runs the checker. 90 s into the load the scenario is applied and held for 2 minutes. A probe outside both clouds hits the public name every 2 s: `/version` (the gateway), a login every 10 s (auth) and a submit (jobs and the database).

## What kept working, what stopped

| Scenario | Probe: share of 2xx answers during the fault | What stopped | What kept working | Checker |
| --- | --- | --- | --- | --- |
| **kill-gateway** (down 60 s) | `/version` 24/33, login 5/7, submit 24/33; failures +3 s … +62 s | **Everything public**, for exactly as long as the gateway was down | Workers kept finishing claimed jobs; nothing behind the gateway noticed | [PASS](chaos-r1791518695764074271t1791518695-kill-gateway.md) |
| **cut-clouds** (2 min) | `/version` 47/47, **login 3/10**, submit 46/47 | **New logins** (the AWS gateway can't reach auth in Azure); Azure's workers (their dispatcher can't reach the database in AWS) | Submits with a token already held (the gateway and jobs verify tokens locally with cached keys); AWS workers ran everything | [PASS](chaos-r1791519158282152368t1791519158-cut-clouds.md) |
| **stop-azure** (2 min) | `/version` 67/67, **login 1/14**, submit 67/67 | **New logins** (the only auth is in Azure); Azure's worker | Submits with a token held; the AWS worker took Azure-pinned jobs after their 30 s wait | [PASS](chaos-r1791519617581529403t1791519617-stop-azure.md) |
| **stop-aws** (2 min, plus stop and start) | **`/version` 1/17, login 0/4, submit 1/17**; failures +3 s … +207 s | **Everything**: the gateway, jobs and the only database are in AWS | Nothing public. Azure's dispatcher, scheduler and worker were up but useless without the database | [PASS](chaos-r1791520130015106659t1791520130-stop-aws.md) |

Every run passed the checker: **no acknowledged job was lost, no result was recorded twice, and no effect was applied twice**, through every scenario, including stopping the database's VM. Safety held in all four, and liveness did not. G6 and G7 (cron and leader epochs) were exercised only in `stop-aws`, where the leader moved (one epoch change, no tick duplicated or skipped). In the other three, the leader (`scheduler-a`, in AWS) never lost its lease, so those rows say "not exercised" rather than pass.

One `cut-clouds` submit failed, at +26 s; the probe keeps only status codes, so its cause is not known. Every other submit during the cut succeeded.

Full probe timelines: [kill-gateway](act1-kill-gateway-probe.md), [cut-clouds](act1-cut-clouds-probe.md), [stop-azure](act1-stop-azure-probe.md), [stop-aws](act1-stop-aws-probe.md).

## Affinity, and the 30 s steal

First attempts of the `chaos.sleep` jobs that asked for a cloud, by where they actually ran (seconds from submit to start):

| Run | Wants | Ran on | Jobs | Min | Median | Max |
| --- | --- | --- | --- | --- | --- | --- |
| kill-gateway (both clouds up) | aws | aws | 63 | 0.0 | 0.0 | 6.7 |
| | azure | azure | 59 | 0.0 | 0.0 | 1.1 |
| stop-azure | aws | aws | 60 | 0.0 | 0.0 | 235.6 |
| | azure | **aws** | 47 | **30.0** | 96.1 | 262.5 |
| | azure | azure | 48 | 0.0 | 0.0 | 266.1 |
| | aws | **azure** | 21 | 63.9 | 92.7 | 229.8 |

With both clouds up, every job ran where it asked to. With Azure stopped, no Azure-pinned job was taken by AWS before **exactly 30.0 s**; that is the steal rule, enforced by the claim query on the database's clock. The medians are far above 30 s because Act 1 has one worker per cloud: with Azure gone, a single 8-slot worker carried all the load, so even AWS-pinned jobs queued for up to 4 minutes. When Azure came back, its worker helped drain that backlog and took 21 AWS-pinned jobs that had waited past 30 s.

## Per-hop latency

Median of 19 requests on one kept-alive connection (`deploy/experiments/hops.sh`): `GET /healthz` over mTLS, or `SELECT 1` in one session.

| Hop | Clouds | Median ms |
| --- | --- | --- |
| client (ops-a) → gateway, through the Traffic Manager name | AWS → AWS | 0.36 |
| gateway → jobs (svc-a) | AWS, same VM | 0.22 |
| gateway → auth (svc-z) | AWS → Azure | 5.87 |
| jobs, dispatch-a → Postgres primary (pg-a) | AWS → AWS | 0.35 |
| dispatch-z → Postgres primary (pg-a) | Azure → AWS | 5.44 |
| worker (work-a) → dispatch-a | AWS → AWS | 0.44 |
| worker (work-z) → dispatch-z | Azure → Azure | 1.01 |
| worker (work-z) → sinks (ops-a) | Azure → AWS | 6.32 |

Crossing the clouds costs about 5.5 ms a hop: the 5.47 ms internet round trip (week 1) plus WireGuard. Every claim by an Azure worker pays it once (dispatch-z → database), and every login pays it once (gateway → auth).

## Metrics from both clouds

Prometheus on `ops-a` scrapes `dispatch` and `scheduler` in both clouds over the mesh, labelled by cloud: 4 of 4 targets up (`up` counts 2 for `aws`, 2 for `azure`).

## Availability: Act 1 needs everything at once

Each request path is a chain, and a chain is up only when every link is:

| Path | Needs | If each part is up 99.5% of the time |
| --- | --- | --- |
| submit, with a token held | `svc-a` and `pg-a` | 0.995² = **99.0%** (about 7 h down a month) |
| new login | `svc-a`, `pg-a`, `svc-z` and the link between the clouds | 0.995⁴ = **98.0%** (about 15 h down a month) |
| a job pinned to Azure, run in Azure | the above, plus `work-z` | 0.995⁵ = **97.5%** |

99.5% is the published single-instance figure for both an EC2 instance and an Azure VM on Standard SSD. Every part is a single point of failure, and the experiments show each one: the gateway (`kill-gateway`), the database and the AWS side (`stop-aws`), auth and the Azure side (`stop-azure`), and the link (`cut-clouds`). **Act 1 needs AWS, Azure and the link between them all at once**, so adding a cloud made it less available than either cloud alone. That is what Act 2 removes: two of every stateless service (week 12) and an automatic database failover across the clouds (week 13), so that any one of these failures is survived.

## How the experiments were run

- The harness runs in a `golang:1.27` container on `ops-a`, at the commit under test, with `-faults=false -scenario <name>`. The scenario counts as G1's fault; a guarantee the scenario can't exercise is reported "not exercised" instead of failing, and any violation still fails the run.
- Faults on a VM revert themselves through a `systemd-run` timer (the cut's `iptables` rules, the gateway's restart), so a crashed runner can't leave one behind. Stopped VMs are started by the runner.
- Bugs in this tooling found while building it are in the [bug diary](../bugs.md) only where they could have hidden a result; the rest are in the commit history.
