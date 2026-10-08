# Cloud accounts — week 1 check

Checked Oct 8, 2026, from the AWS and Azure CLIs.

## AWS (account 573693339820)

| Item | Value | Plan needs | OK? |
| --- | --- | --- | --- |
| Account plan | Free plan, active | — | ✅ |
| Credits remaining | $193.07 | ~$110 through week 16 | ✅ |
| Plan expires | 2027-03-22 | after week 16 (late Jan 2027) | ✅ about 8 weeks of margin |
| vCPU quota, ap-south-1 Mumbai | 16 | 8 | ✅ |
| vCPU quota, ap-south-2 Hyderabad | 5 | 2 (etcd witness) | ✅ |
| ap-south-2 region | Enabled | — | ✅ |
| Budget | "My Monthly Cost Budget", $100/month; email alerts at 25/50/80/85/100% actual, 100% forecast | alerts at 25/50/80% | ✅ |
| **Allowed instance types (free plan)** | only free-tier eligible: `t3`/`t4g`/`t8i` `.micro`/`.small`, `c7i-flex.large`, `m7i-flex.large` | `t4g.small`; witness `t4g.micro` (not `nano`) | ⚠️ the plan's `t4g.nano` witness is **not allowed**; use `t4g.micro` |
| Default VPC, ap-south-1 | created for the probe (`vpc-06b11aac61b8fc57e`); free | — | ✅ |

## Azure (subscription "Azure for Students", f6066207-…)

| Item | Value | Plan needs | OK? |
| --- | --- | --- | --- |
| Subscription | Enabled | — | ✅ |
| Allowed regions | koreacentral, uaenorth, indonesiacentral, eastasia, **centralindia** | Central India | ✅ |
| Total regional vCPUs, Central India | 6 | 6 (3 VMs × 2 vCPU) | ✅ exactly at the limit |
| B-series (BS family) vCPUs | 4 | — | ⚠️ too few for 3 × 2-vCPU VMs |
| Bsv2 family vCPUs | 10 | 6 | ✅ **use Bsv2 sizes** (e.g. `Standard_B2ats_v2`) |
| Credits remaining | **$86.18** ($13.82 used of $100), from the portal | ~$65 through week 16 | ✅ but tight: ~$21 margin |
| **Credit expires** | **2026-12-21** (74 days from today) | through week 16 (late Jan 2027) | ❌ **expires in week 11, before go-live** |
| Spending limit | not readable from the CLI; always on for Azure for Students | on | ✅ by offer type |
| Budget | `tq-credits`, $86/year, alerts to subscription owners at 25/50/80% | alerts at 25/50/80% | ✅ |

## Cross-cloud round-trip time (Mumbai ↔ Central India)

Measured Oct 8, 2026: Azure Central India VM (`Standard_B2ats_v2`) pinging an AWS ap-south-1 VM (`t4g.micro`) over the public internet, 180 pings at 1 s.

| min | avg | max | mdev | loss |
| --- | --- | --- | --- | --- |
| 5.32 ms | **5.47 ms** | 9.13 ms | 0.36 ms | 0% |

Synchronous replication will add about 5.5 ms per commit (plus WireGuard overhead). Week 9's local `tc netem` delay uses **5.5 ms**.

Also learned: `Standard_B1ls` is capacity-restricted in Central India; `Standard_B2ats_v2` deployed fine.

## Actions

- [x] Azure: budget `tq-credits` with alerts at 25/50/80%.
- [x] AWS: 25/50/80% alerts added to the existing budget.
- [x] Measure the AWS Mumbai ↔ Azure Central India round-trip time: 5.47 ms.
- [ ] **Decide what to do about Azure credit expiring 2026-12-21** (see below).
- Terraform (week 10): the AWS witness uses `t4g.micro`; only free-tier eligible types can launch on the free plan.
- [x] Azure remaining credit recorded: $86.18.
- Terraform (week 10): Azure VMs must use **Bsv2** sizes; the plain B-series quota (4 vCPUs) is too small.

## Open problem: Azure credit ends before go-live

Azure for Students credit expires **2026-12-21**, which is week 11. The plan's cloud phases run weeks 10–16, with 24/7 production from week 14.
After expiry the subscription is disabled unless renewed. Options, to decide before week 10:

1. **Renew Azure for Students** (allowed yearly while you are a verified student). Check eligibility in the portal → Education, close to the expiry date.
2. **Move the cloud phases earlier** so the Azure-dependent work (weeks 10–13) finishes before Dec 21.
3. **Shrink Act 2 on Azure**: run production on AWS only after Dec 21. The plan already survives losing Azure, but the cross-cloud claim would hold only until then.
