# Chaos run r1791464152144801900: FAIL

Seed `1791464152144801900` · 3m0s of load and faults · **mutant build `mutant_ackfirst`** (expected to FAIL)

- 1413 jobs submitted, 1552 acknowledgements, 1338 jobs in the database: map[available:588 dead:33 succeeded:717]
- email: 54 messages, 0 duplicate deliveries (expected: at-least-once)

## Faults injected

| Fault | Times |
| --- | --- |
| crash-postgres | 3 |
| cut-network | 2 |
| kill | 3 |
| pause-dispatch | 4 |
| pause-scheduler | 3 |
| pause-worker | 4 |

## Guarantees

| | Guarantee | Result | Exercised | Violations |
| --- | --- | --- | --- | --- |
| G1 | An acknowledged job is never lost | **FAIL** | 6 kills of jobs/gateway/auth and Postgres crashes | 222 |
| G2 | Same key, same job; a changed request gets 409 | **FAIL** | 139 repeated keys and conflicting resubmits | 157 |
| G3 | Only the current lease holder records a result | PASS | 59 late results rejected by fencing | 0 |
| G4 | Effects at most once, exactly once on success | PASS | 20 duplicate deliveries absorbed by the sinks | 0 |
| Email | At-least-once delivery (SMTP can't deduplicate) | PASS | 54 succeeded email jobs | 0 |
| G5 | Every job ends succeeded, dead or cancelled | **FAIL** | 1338 jobs that finished | 588 |
| G6 | Cron: one job per tick, none skipped | PASS | 3 leader changes while ticking | 0 |
| G7 | One leader per epoch: chores never overlap | PASS | 3 leader changes | 0 |
| G8 | No attempt starts before run_at | PASS | 19 delayed jobs | 0 |

### G1

- acknowledged 01a11b95-a036-7a7a-8e60-18973e97d9ba (key r1791464152144801900-5) is not in the database
- acknowledged 01a11b95-a3a0-7336-a2bc-dc7f5f7bd31f (key r1791464152144801900-12) is not in the database
- acknowledged 01a11b95-a41d-796d-9c0f-3c2630ce65f5 (key r1791464152144801900-13) is not in the database
- acknowledged 01a11b95-ace7-7c62-b0c5-a71fbb5318ba (key r1791464152144801900-31) is not in the database
- acknowledged 01a11b95-ad64-7bfe-ac40-7181a68f97de (key r1791464152144801900-32) is not in the database
- acknowledged 01a11b95-afd5-77e4-8c2c-9ac3e3f3788b (key r1791464152144801900-37) is not in the database
- acknowledged 01a11b95-c7c3-719e-9bed-5a40dc76cd41 (key r1791464152144801900-86) is not in the database
- acknowledged 01a11b95-c840-70dc-8045-a31a85760de3 (key r1791464152144801900-87) is not in the database
- acknowledged 01a11b95-cbac-717b-a729-afba72321315 (key r1791464152144801900-94) is not in the database
- acknowledged 01a11b95-d27f-7402-8860-8cb93093bc41 (key r1791464152144801900-108) is not in the database
- … and 212 more

### G2

- key r1791464152144801900-1010 acknowledged as 2 different jobs
- key r1791464152144801900-748 acknowledged as 2 different jobs
- key r1791464152144801900-379 acknowledged as 2 different jobs
- key r1791464152144801900-1187 acknowledged as 2 different jobs
- key r1791464152144801900-1009 acknowledged as 2 different jobs
- key r1791464152144801900-909 acknowledged as 2 different jobs
- key r1791464152144801900-32 acknowledged as 2 different jobs
- key r1791464152144801900-1227 acknowledged as 2 different jobs
- key r1791464152144801900-973 acknowledged as 2 different jobs
- key r1791464152144801900-1279 acknowledged as 2 different jobs
- … and 147 more

### G5

- job 01a11b96-90ef-71fb-a84b-1491323c8af8 still available after quiescing
- job 01a11b96-fbdc-7b53-9c5b-98164d9d728a still available after quiescing
- job 01a11b98-3f7a-7b19-971f-5985922eca5e still available after quiescing
- job 01a11b97-d98b-7a5d-a979-343ebb89258d still available after quiescing
- job 01a11b98-3f79-79de-bd14-e9dd6d61962f still available after quiescing
- job 01a11b98-5497-7419-b4bc-dc28c666e3b7 still available after quiescing
- job 01a11b96-cb85-7460-92fa-2b444383f353 still available after quiescing
- job 01a11b98-0dca-7d30-80dc-75c0eaf6bf75 still available after quiescing
- job 01a11b97-77e3-7ee4-8a60-c835a5fd4be4 still available after quiescing
- job 01a11b96-e17e-7e46-b73f-d652f7dd99f7 still available after quiescing
- … and 578 more
