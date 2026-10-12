# Cost report — week 16

Oct 12, 2026. Figures come from AWS Cost Explorer, the AWS free-plan API, Azure Cost Management, and each provider's public price list. They are amounts **before credits**: the credits cover every one of them, so the cash spent is $0 so far.

## Credits and their dates

| | Remaining | Expires | What happens then |
| --- | --- | --- | --- |
| AWS (free plan) | **$190.66** (from $193.07 on Oct 8) | 2027-03-22 | A free-plan account is closed when its credits or its plan run out, unless it is upgraded to a paid plan |
| Azure for Students | about **$84** ($86.18 on Oct 8, minus about $2.2 since) | **2026-12-21** | The subscription is disabled and its VMs stop, unless the offer is renewed |

## What the project has cost so far

The cloud phase began on Oct 8 (week 10). Charges on the AWS account from Oct 1–5 (a `t3.micro`, a load balancer and an RDS instance) are from something else on the account, ended by Oct 6, and are excluded.

| | Oct 8–11 | Of which |
| --- | --- | --- |
| AWS | about $2.0 | public IPv4 addresses about $1.4, disks about $0.5, the witness `t4g.micro` in Hyderabad $0.03 |
| Azure | about $2.2 | public IPs $0.92, disks and disk operations $0.85, Traffic Manager health checks $0.43 |
| **Total** | **about $4.2** | the VMs' compute: $0 in both clouds (below) |

Oct 11 isn't fully reported yet, because billing data lags by about a day.

**One full day with all eight VMs running 24/7 (Oct 10) cost $1.23:** $0.67 on AWS and $0.56 on Azure. Every cent of it is IP addresses, disks and Traffic Manager.

## Why compute is $0, and how long that lasts

Both clouds bill these VMs at $0 against a free allowance of hours, and this layout uses those hours faster than one VM would:

| | Hours this month (to Oct 11) | Billed | Allowance | This layout uses |
| --- | --- | --- | --- | --- |
| AWS `t4g.small` (`svc-a`, `work-a`, `pg-a`, `ops-a`) | 273.6 | $0 | AWS's `t4g.small` free trial (750 hours a month, if it applies as published; the free-plan API doesn't list it) | 96 hours a day |
| Azure `B2ats v2` (`svc-z`, `work-z`, `pg-z`) | 177 (meter "B2ats v2 - Free") | $0 | 750 hours a month | 72 hours a day |

At these rates, a 750-hour allowance runs out around **Oct 15 on AWS and Oct 20 on Azure**, and every month after that. From then on, compute is billed at list price for the rest of each month. **Check the next few days of billing to confirm.**

## Projected monthly cost, 24/7, after the allowances

The rates below are list prices: `t4g.small` $0.0112 an hour in Mumbai, and `B2ats v2` (Linux) $0.00615 an hour in Central India.

| Item | Per month | With a 750-hour allowance |
| --- | --- | --- |
| AWS: 4 × `t4g.small` | $32.7 | $24.3 |
| AWS: `wit` (`t4g.micro`, Hyderabad) | $4.0 | $4.0 |
| AWS: 5 public IPv4 addresses | $18.3 | $18.3 |
| AWS: disks | $6.0 | $6.0 |
| **AWS** | **$61** | **$53** |
| Azure: 3 × `B2ats v2` | $13.5 | $8.9 |
| Azure: 3 public IPs | $7.3 | $7.3 |
| Azure: disks | $5.8 | $5.8 |
| Azure: Traffic Manager | $3.9 | $3.9 |
| **Azure** | **$30.5** | **$26** |
| **Total** | **about $92** | **about $79** |

That is about half of `deployment_plan.md`'s estimate of $140–180 a month, mainly because the VMs are the smallest sizes the free plans allow.

**Data transfer between the clouds** is billed as internet egress, and both clouds include 100 GB a month free. The load test measured at most 6.8 GB per million jobs ([act2-resilience.md](act2-resilience.md#load-cost-and-traffic-between-the-clouds)). At the harness's rate this stays far inside the free 100 GB. Past it, it would add about $0.6–0.8 per million jobs.

## Cost per million jobs

From the [cloud load test](load-cloud.md): at the measured synchronous capacity of 101 jobs a second (about 8.7 million jobs a day):

| | Per day | Per million jobs |
| --- | --- | --- |
| Today's bill | $1.23 | **$0.14** |
| After the allowances | $2.6–3.0 | **$0.30–0.35** |

A system that runs 24/7 is priced by the hour, not by the job. Running idle costs the same as running full.

## Credit runway

- **AWS:** at $2–2.6 a day once the trial hours run out, $190 lasts until about **January 2027**, before the plan's own expiry on 2027-03-22.
- **Azure:** at about $1 a day, $84 would last into January, but **the credit expires on 2026-12-21**. Azure is the binding date.

Through the end of the project, the cash cost is $0 either way.

## Decision: keep it running until Dec 14, then destroy it

**Kept running**, on credits, until **Dec 14, 2026**, one week before the Azure credit expires:

- The cash cost is $0. Credits cover everything, with AWS's $190 and Azure's $84 both comfortably ahead of the spend.
- The live system is the demo. The video needs it, and so does anyone who wants to try the public name.
- Every week it keeps running, the probe and the 15-minute health sweep add to the uptime record.

**Destroyed on Dec 14,** and not left to lapse. When Azure disables the subscription, the system loses `svc-z`, `work-z` and `pg-z` without warning. It would keep serving from AWS (this was tested), but with no standby, and with its public name depending on a Traffic Manager profile inside a disabled subscription. Shutting down on purpose is cheaper and cleaner than finding out what a disabled subscription does to it.

**Tripwires before then:**

- Azure's budget alert at 80% (`tq-credits`), or any month's AWS usage above $70: shrink first, by stopping `work-z` and then `ops-a` outside experiments, rather than lose the system.
- AWS budget alerts at 25/50/80%. The EC2-stop budget action stays in place.

**Teardown, in this order:**

1. `make down`: destroys the session stack (VMs, IPs, mesh, firewalls), then sweeps. The sweep must report nothing left.
2. `terraform -chdir=deploy/terraform/persistent destroy`: Traffic Manager, Key Vault, OIDC roles and budgets. **Persistent last**, because the session's Traffic Manager endpoints and secrets depend on it.
3. Purge the soft-deleted Key Vault. Then delete every object version in the state bucket, and the bucket itself. `make bootstrap` created the bucket outside Terraform, and it holds both stacks' state, so it goes last.
4. Check billing the next day in both consoles. Anything still billed is a leak, and the sweep should be fixed to catch it.
