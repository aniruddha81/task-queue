# Chaos run r1791463184018549800: FAIL

Seed `1791463184018549800` · 3m0s of load and faults · **mutant build `mutant_noeffectkey`** (expected to FAIL)

- 1440 jobs submitted, 1578 acknowledgements, 1448 jobs in the database: map[available:448 dead:42 succeeded:958]
- email: 87 messages, 0 duplicate deliveries (expected: at-least-once)

## Faults injected

| Fault | Times |
| --- | --- |
| crash-postgres | 2 |
| cut-network | 3 |
| kill | 7 |
| pause-dispatch | 3 |
| pause-scheduler | 5 |
| pause-worker | 2 |

## Guarantees

| | Guarantee | Result | Exercised | Violations |
| --- | --- | --- | --- | --- |
| G1 | An acknowledged job is never lost | PASS | 9 kills of jobs/gateway/auth and Postgres crashes | 0 |
| G2 | Same key, same job; a changed request gets 409 | PASS | 168 repeated keys and conflicting resubmits | 0 |
| G3 | Only the current lease holder records a result | PASS | 57 late results rejected by fencing | 0 |
| G4 | Effects at most once, exactly once on success | **FAIL** | 0 duplicate deliveries absorbed by the sinks | 27 |
| Email | At-least-once delivery (SMTP can't deduplicate) | PASS | 87 succeeded email jobs | 0 |
| G5 | Every job ends succeeded, dead or cancelled | **FAIL** | 1448 jobs that finished | 448 |
| G6 | Cron: one job per tick, none skipped | PASS | 5 leader changes while ticking | 0 |
| G7 | One leader per epoch: chores never overlap | PASS | 5 leader changes | 0 |
| G8 | No attempt starts before run_at | PASS | 30 delayed jobs | 0 |

### G4

- Not exercised: its fault never happened, so nothing was proven.
- webhook.deliver job 01a11b86-e522-73d5-8721-78abae9bbb4c (succeeded): 2 effects
- webhook.deliver job 01a11b88-3be9-7541-8736-85b24f54ea27 (succeeded): 2 effects
- webhook.deliver job 01a11b88-13dd-7d3e-bced-1021c383e655 (succeeded): 4 effects
- webhook.deliver job 01a11b88-1bad-75ba-a406-927c3fbb0914 (succeeded): 2 effects
- webhook.deliver job 01a11b88-9b1f-7765-8a40-bc5445b98651 (succeeded): 4 effects
- webhook.deliver job 01a11b86-e986-7c5e-b3d3-896923048c5b (succeeded): 2 effects
- webhook.deliver job 01a11b88-acb2-7ec4-b00b-75e11f2b8b10 (succeeded): 2 effects
- webhook.deliver job 01a11b87-ff1c-7e4f-ab7e-ed02bc497611 (succeeded): 3 effects
- webhook.deliver job 01a11b88-a2ef-73bf-86f9-eee74d62dd67 (succeeded): 2 effects
- webhook.deliver job 01a11b88-06af-7f6e-8b0a-8b25b43c96cd (succeeded): 3 effects
- … and 17 more

### G5

- job 01a11b89-1b0c-7ea0-be77-1e0fca223504 still available after quiescing
- job 01a11b89-6aa4-77b7-93a3-182a4e8e0cc5 still available after quiescing
- job 01a11b88-a271-75f1-9b3e-a07b530a9b5e still available after quiescing
- job 01a11b89-12bf-77e9-affa-a3dd869a4996 still available after quiescing
- job 01a11b89-87ef-79db-af4e-ae82f993452a still available after quiescing
- job 01a11b89-90ba-7a23-8806-ac37d8568bad still available after quiescing
- job 01a11b88-627b-7bd4-92fa-8bb39ddc4464 still available after quiescing
- job 01a11b89-5b81-78e6-acbb-ebefa0c4dae8 still available after quiescing
- job 01a11b89-88e9-7e3f-86a6-20fd70b9f4a0 still available after quiescing
- job 01a11b88-aabf-70c6-9d4d-780a7e2c6459 still available after quiescing
- … and 438 more
