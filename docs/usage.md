# Using task-queue

This guide covers how to use the scheduler once it is running: signing in, submitting and managing jobs, cron schedules, the dashboard, writing your own job handlers, and running the checks. For why it works the way it does, see the [design doc](design.md).

- [1. Where it runs](#1-where-it-runs)
- [2. Sign in](#2-sign-in)
- [3. Submit a job](#3-submit-a-job)
- [4. Follow a job through its life](#4-follow-a-job-through-its-life)
- [5. Manage jobs](#5-manage-jobs)
- [6. Cron schedules](#6-cron-schedules)
- [7. Built-in job types](#7-built-in-job-types)
- [8. Write your own job handler](#8-write-your-own-job-handler)
- [9. The dashboard, Grafana and Prometheus](#9-the-dashboard-grafana-and-prometheus)
- [10. Users and admins](#10-users-and-admins)
- [11. Check the guarantees yourself](#11-check-the-guarantees-yourself)
- [12. Deploy to the cloud](#12-deploy-to-the-cloud)
- [13. API reference](#13-api-reference)
- [14. Troubleshooting](#14-troubleshooting)
- [Appendix: run it locally](#appendix-run-it-locally)

```mermaid
flowchart LR
  C["You: curl, script, browser"] -->|"HTTPS + token"| G[gateway]
  G --> A["auth<br/>(login, users)"]
  G --> J["jobs<br/>(job + schedule API)"]
  J --> DB[("PostgreSQL<br/>(Patroni)")]
  S["scheduler x2<br/>(leader: reaper + cron)"] --> DB
  D["dispatch<br/>(claims, leases)"] --> DB
  W["workers<br/>(your handlers)"] -->|"claim / heartbeat / result"| D
  W -->|"effect key"| X["destinations<br/>(ledger, webhook, email)"]
```

You only ever talk to the **gateway**. Workers talk only to **dispatch**. Everything inside uses mTLS.

## 1. Where it runs

The live system is at **https://tq.aniruddha81.tech** (until Dec 14, 2026). It runs in AWS Mumbai and Azure Central India, with a real Let's Encrypt certificate, so you need nothing installed but `curl` (and `jq` for the one-liners).

| What | Where |
| --- | --- |
| API and dashboard | https://tq.aniruddha81.tech |
| Grafana | http://10.99.0.14:3000 (over the WireGuard mesh only) |
| Prometheus | http://10.99.0.14:9090 (mesh only) |
| Mailpit (`email.send` mail to `@example.com` addresses) | http://10.99.0.14:8025 (mesh only) |

The API and dashboard are public. Grafana, Prometheus and Mailpit run on the `ops-a` VM and have no public port; [section 9](#9-the-dashboard-grafana-and-prometheus) shows how to reach them.

### Shell setup used in this guide

```sh
G=https://tq.aniruddha81.tech
```

Every command in this guide is bash. On Windows, run them in Git Bash or WSL. If Git Bash's curl fails with `CRYPT_E_NO_REVOCATION_CHECK`, add `--ssl-no-revoke` to each call, or once for the session:

```sh
alias curl='curl --ssl-no-revoke'
```

To run your own copy on your laptop instead, see [Appendix: run it locally](#appendix-run-it-locally).

## 2. Sign in

Every API call needs a token. Log in to get one; it lasts **60 minutes**.

```sh
curl $G/v1/auth/login -d '{"email":"demo@example.com","password":"<password>"}'
# {"token":"eyJ...","expires_at":"2026-10-12T11:00:00Z"}

TOKEN=eyJ...
H="Authorization: Bearer $TOKEN"
```

With `jq` you can do it in one step:

```sh
TOKEN=$(curl -s $G/v1/auth/login -d '{"email":"demo@example.com","password":"<password>"}' | jq -r .token)
```

Production has three seeded accounts: `admin@example.com` (admin), `demo@example.com`, and `smoke@example.com` (used by the release smoke test). Their passwords are random. Whoever runs the deployment reads them with:

```sh
terraform -chdir=deploy/terraform/session output users
```

Anyone else gets an account from an admin ([section 10](#10-users-and-admins)).

Notes:

- When the token expires you get `401`. Log in again.
- Too many failed logins for one email gives `429`. Wait and retry.
- Browsers get the token as an `HttpOnly` cookie (`tq_session`) instead, so the dashboard needs no copying. `POST /v1/auth/logout` clears it.
- Each user sees only their own jobs and schedules. Another user's job is `404`, the same as one that doesn't exist.

### For programs: API keys

A service shouldn't sign in with a person's password. Give it an API key instead, and it exchanges the key for a token whenever the last one runs out:

```sh
# once, signed in as yourself (or in the dashboard: Account → API keys → Create key)
curl $G/v1/auth/keys -H "$H" -d '{"name":"billing service"}'
# {"id":"...","name":"billing service","key":"tq_...", ...}   <- shown once: store it as a secret

# in the service, every time it needs a token
TOKEN=$(curl -s -X POST $G/v1/auth/token -H "Authorization: Bearer tq_..." | jq -r .token)
```

The token acts as the key's owner and lasts 60 minutes. Only a hash of the key is stored, so a lost key can't be shown again: revoke it and make a new one.

```sh
curl $G/v1/auth/keys -H "$H"                          # your keys, with when each was last used
curl -X DELETE $G/v1/auth/keys/<id> -H "$H"           # revoke: no new tokens; issued ones run out within 60 minutes
```

## 3. Submit a job

**In the browser:** sign in at https://tq.aniruddha81.tech and open **New job**. Pick a type, and a sample payload is filled in for you. Edit it if you like, then press **Submit job**; the job's page opens so you can watch it run. Priority, run at, max attempts, timeout and cloud are under **More options**. The page makes the Idempotency-Key for you, so pressing Submit twice still creates one job.

**From bash:**

```sh
curl $G/v1/jobs -H "$H" -H "Idempotency-Key: nap-1" \
  -d '{"queue":"default","type":"chaos.sleep","payload":{"ms":500}}'
```

The response is the job, with `"state":"available"` and status `201 Created`.

### The Idempotency-Key header (required)

Every submit needs an `Idempotency-Key`: any string of 1 to 200 characters that you choose, unique per job you mean to create. Use something from your own domain, such as `order-1042-shipped`.

- Send the same key with the same body again and you get the **same job back** with `200 OK`. No second job is made. That makes retrying always safe.
- A key is remembered for as long as its job exists: 30 days after the job finishes ([retention](#retention)), the job is deleted and the key can be used again.
- Send the same key with a **different** body and you get `409 Conflict`.
- If you get `503`, the outcome is unknown: retry with the **same** key. You will either get the job that was created or it will be created now.
- Keys starting with `cron:` are reserved for schedules.

### Job fields

| Field | Required | Default | Rules | What it does |
| --- | --- | --- | --- | --- |
| `queue` | yes | | `^[a-z0-9._-]{1,64}$` | Which queue. Workers subscribe to queues. The local workers serve `default`. |
| `type` | yes | | same as `queue` | Which handler runs it, such as `ledger.transfer`. |
| `payload` | no | `{}` | any JSON | Input for the handler. |
| `priority` | no | `0` | -1000 to 1000 | Higher runs first. |
| `run_at` | no | now | RFC 3339 time | Don't start before this time (on the database's clock). |
| `max_attempts` | no | `5` | 1 to 50 | Attempts before the job goes to dead letter. |
| `timeout_seconds` | no | `60` | 1 to 3600 | Deadline for one attempt. The handler's context ends then. |
| `affinity` | no | none | `"aws"` or `"azure"` | Prefer workers in that cloud. After 30 s with no taker, any cloud's worker may take it. |

The whole body is at most 64 KB. Unknown fields are rejected with `400`.

A fuller example: a delayed, high-priority webhook with a tight retry budget.

```sh
curl $G/v1/jobs -H "$H" -H "Idempotency-Key: order-1042-shipped" -d '{
  "queue": "default",
  "type": "webhook.deliver",
  "payload": {"url": "https://ops-a:8090/webhook", "body": {"order": 1042, "status": "shipped"}},
  "priority": 10,
  "run_at": "2026-10-12T18:30:00+05:30",
  "max_attempts": 3,
  "timeout_seconds": 15,
  "affinity": "aws"
}'
```

## 4. Follow a job through its life

```mermaid
flowchart TB
  start(("start")) -->|you submit a job| available
  available -->|claim| running
  running -->|success| succeeded
  running -->|gave up| dead
  running -.->|retry| available
  dead -.->|redrive| available
```

Cancelling moves an `available` or `running` job to `cancelled`.

| State | Meaning |
| --- | --- |
| `available` | Waiting to be claimed (possibly until its `run_at`, or a retry's backoff). |
| `running` | A worker holds a 30-second lease on it and keeps renewing it. |
| `succeeded` | Done. |
| `dead` | Gave up: used all `max_attempts`, or the handler returned a permanent error. |
| `cancelled` | You cancelled it. |

### Retention

Finished jobs (`succeeded`, `dead` or `cancelled`) are deleted **30 days after they finish**, with their attempt history, so the database doesn't grow forever. Jobs that are waiting or running are never deleted, and a redriven job counts from when it finishes again. The leader scheduler does it, a batch at a time. Change the period with `RETENTION_DAYS` on the `scheduler` service; `0` keeps every job forever.

What happens on failure:

- **The handler returns an error.** The job goes back to `available` with a backoff: a random wait up to 1 s, 2 s, 4 s, ... doubling per attempt, capped at 10 minutes.
- **The worker crashes or hangs.** It stops renewing the lease. After 30 s the leader scheduler's reaper takes the job back, and another worker runs it.
- **The old worker comes back and reports late.** Its result is refused (it is "fenced"), because its lease is no longer valid. The job's attempt history shows this as a late result.
- **The handler returns a permanent error,** such as bad input, or there's no handler for the type. The job goes straight to `dead`.

Watch a job:

```sh
curl $G/v1/jobs/<id> -H "$H"            # current state, attempt, last_error
curl $G/v1/jobs/<id>/attempts -H "$H"   # every attempt: worker, times, outcome, error, late result
```

An attempt's `outcome` is `succeeded`, `failed`, `lease_expired` or `cancelled`.

## 5. Manage jobs

### List

```sh
curl "$G/v1/jobs" -H "$H"                                # newest first, 50 per page
curl "$G/v1/jobs?state=dead&queue=default&limit=200" -H "$H"
```

Filters: `state` (one of the five states), `queue`, `limit` (1 to 200). The response is `{"jobs": [...], "next": "<id>"}`. When `next` isn't null, pass it as `after` for the next page:

```sh
curl "$G/v1/jobs?after=<next>" -H "$H"
```

### Cancel

```sh
curl -X POST $G/v1/jobs/<id>/cancel -H "$H"
```

- An `available` job becomes `cancelled` at once.
- A `running` job gets `cancel_requested: true`. The worker sees this on its next heartbeat (within about 10 s), stops the handler, and the job becomes `cancelled`. If the handler finishes first, it may still end `succeeded`.
- Cancelling a job that is already `succeeded` or `dead` gives `409`. Cancelling twice is fine.

### Redrive a dead job

```sh
curl -X POST $G/v1/jobs/<id>/redrive -H "$H"
```

The job goes back to `available` with a fresh budget of its original `max_attempts`. Attempt numbers keep counting up, so the history stays intact, and the effect key stays the same, so anything an earlier attempt already did isn't done again. Redriving a job that isn't `dead` gives `409`.

Redrive every dead job at once, or just one queue's (up to 1000 per call; call again if there are more):

```sh
curl -X POST $G/v1/jobs/redrive -H "$H"                              # {"redriven": 12}
curl -X POST $G/v1/jobs/redrive -H "$H" -d '{"queue":"default"}'
```

### Queue counts

```sh
curl $G/v1/queues -H "$H"
# {"queues":[{"queue":"default","state":"succeeded","count":120}, ...]}
```

## 6. Cron schedules

A schedule creates one job on every tick of a cron expression.

```sh
curl $G/v1/schedules -H "$H" -d '{
  "name": "daily-report",
  "cron": "0 9 * * *",
  "timezone": "Asia/Kolkata",
  "job": {"queue": "default", "type": "report.daily"}
}'
```

| Field | Rules |
| --- | --- |
| `name` | `^[a-z0-9._-]{1,64}$`, unique per user (`409` if taken) |
| `cron` | Standard 5 fields: minute, hour, day of month, month, day of week |
| `timezone` | Any IANA zone, such as `Asia/Kolkata`, `UTC`, `America/New_York` |
| `job` | The same fields as a submit, except `run_at`, which the schedule sets |

The response includes `next_tick_at`. The first tick is the first one after now.

Manage schedules:

```sh
curl $G/v1/schedules -H "$H"                          # list
curl $G/v1/schedules/<id> -H "$H"                     # get
curl -X POST $G/v1/schedules/<id>/pause -H "$H"       # ticks while paused never fire
curl -X POST $G/v1/schedules/<id>/resume -H "$H"      # continues from the next tick after now
curl -X DELETE $G/v1/schedules/<id> -H "$H"           # 204; jobs it already made stay
```

What you can rely on:

- **Exactly one job per tick**, even with two schedulers running. Only the elected leader ticks, and the database refuses a second job for the same tick.
- **No tick is lost to downtime.** If every scheduler was down for an hour, the missed ticks are all created when one comes back.
- **Daylight saving.** In zones with DST, times inside the skipped spring-forward hour don't exist, so they don't fire that day. Zones without DST, such as `Asia/Kolkata`, are unaffected.

## 7. Built-in job types

The bundled worker (`cmd/worker`) handles these. Field names in payloads are case-insensitive.

| Type | Payload | What it does | Effect happens |
| --- | --- | --- | --- |
| `ledger.transfer` | `{"from":"alice","to":"bob","amount":500}` | Moves `amount` between two accounts in the sinks ledger | Exactly once |
| `webhook.deliver` | `{"url":"https://ops-a:8090/webhook","body":{...}}` | POSTs `body` to `url` with an `Idempotency-Key` header | Exactly once, if the receiver checks the key |
| `report.daily` | none | Records one daily digest | Exactly once |
| `email.send` | `{"to":"a@example.com","subject":"Hi","body":"Hello!"}` | Sends a plain-text UTF-8 email from `Task Queue <jobs@aniruddha81.tech>` through Amazon SES. Addresses at `example.com` go to Mailpit instead. The SES account has production access (since Oct 2026), so any address gets mail; one SES refuses (a 5xx, such as an address on its suppression list) fails the job permanently. `body` is optional; without it the email says which job sent it. A line break in `to` or `subject` is refused | **At least once**: SMTP can't refuse a duplicate |
| `chaos.sleep` | `{"ms":500}` | Sleeps, then succeeds | |
| `chaos.fail` | `{"p":0.5}` | Fails with probability `p`, to show retries | |
| `chaos.overrun` | `{"ms":45000}` | Ignores its deadline, so its late result is fenced | |
| `chaos.crash` | `{"p":0.1}` | Kills the whole worker process with probability `p` | |
| `demo.freeze` | like `ledger.transfer` | The live page's frozen payment: a `ledger.transfer` whose first attempt pays, then stalls for 10 s ignoring its deadline, so another attempt takes over and the stalled one's late result is fenced. Give it a short `timeout_seconds` | Exactly once |

`webhook.deliver` only calls URLs in the worker's `WEBHOOK_ALLOW` list. In production that is `https://ops-a:8090/webhook`, the test receiver, which deduplicates on the effect key. Locally it is `https://sinks:8090/webhook`. Any other URL is a permanent error, and the job goes to `dead`.

A handler that gets a 2xx counts as success (including "already applied"). A 4xx other than 429 is permanent. Anything else, including 429, 5xx and network errors, is retried.

Try a few:

```sh
curl $G/v1/jobs -H "$H" -H "Idempotency-Key: pay-1" \
  -d '{"queue":"default","type":"ledger.transfer","payload":{"from":"alice","to":"bob","amount":500}}'

curl $G/v1/jobs -H "$H" -H "Idempotency-Key: mail-1" \
  -d '{"queue":"default","type":"email.send","payload":{"to":"you@gmail.com","subject":"Hello"}}'
# arrives in your inbox; to "you@example.com" it lands in Mailpit, http://10.99.0.14:8025, over the mesh

curl $G/v1/jobs -H "$H" -H "Idempotency-Key: flaky-1" \
  -d '{"queue":"default","type":"chaos.fail","payload":{"p":0.7},"max_attempts":3}'
# watch it retry, and maybe end up in dead letter
```

## 8. Write your own job handler

Handlers are Go functions run by a worker built on the SDK in [sdk/go/worker](../sdk/go/worker/worker.go).

```go
w := worker.New(worker.Config{
	Dispatchers: []string{"https://dispatch:8081"}, // nearest first; it fails over down the list
	Cloud:       "aws",                             // "aws" or "azure", for affinity
	Queues:      []string{"default"},
	Concurrency: 8,                                 // jobs at once, default 4
	HTTPClient:  &http.Client{Transport: serve.Transport(certs)}, // mTLS to dispatch
})

w.Handle("invoice.render", func(ctx context.Context, job worker.Job) error {
	var p struct{ OrderID string }
	if err := json.Unmarshal(job.Payload, &p); err != nil {
		return worker.Permanent(err) // retrying won't fix bad input: straight to dead letter
	}
	// Pass job.EffectKey to wherever the effect lands, so a second run is refused there.
	return renderAndStore(ctx, p.OrderID, job.EffectKey)
})

w.Run(ctx) // returns after ctx ends and running jobs finish
```

The rules a handler must follow:

1. **Expect to run more than once.** A worker can crash after doing the work but before reporting it. Make the effect happen once by sending `job.EffectKey` to the destination and having the destination ignore a key it has already seen (a unique column, or an `Idempotency-Key` header). `job.EffectKey` is the same across every attempt and every redrive.
2. **Stop when `ctx` ends.** It ends when the deadline passes, the job is cancelled, or the lease is lost. A handler that ignores it still can't do harm: its result is fenced. But it wastes a worker slot.
3. **Return `nil` for success**, an ordinary error to retry, or `worker.Permanent(err)` to give up now.
4. **Panics are safe.** A panic becomes a failed attempt and the worker keeps going.

The worker claims only the types it has handlers for, from the queues it lists. A job whose type no worker handles stays `available` until one appears.

The SDK does the rest: long-poll claims, a heartbeat every 10 s for every held lease, retrying result reports through every dispatcher for up to a minute, and a graceful drain on shutdown (stop claiming, finish running jobs).

### Running it

dispatch is internal and needs mTLS, so your worker runs inside the deployment with a `worker` certificate. The simplest path:

1. Add your handler to [internal/handlers/handlers.go](../internal/handlers/handlers.go) and register it in `Register`.
2. Try it locally: `docker compose -f deploy/local/compose.yml up --build worker`.
3. Ship it: `git push origin main:release`. The release rolls the new worker out one VM at a time, and running jobs finish first.

To run a separate worker binary instead, copy [cmd/worker/main.go](../cmd/worker/main.go), swap `handlers.Register` for your own `w.Handle` calls, and add it to [deploy/vm/worker.compose.yml](../deploy/vm/worker.compose.yml) with the same environment as the `worker` service:

| Variable | Meaning |
| --- | --- |
| `DISPATCHERS` | Comma-separated dispatch URLs, nearest first |
| `CLOUD` | `aws` or `azure` |
| `QUEUES` | Comma-separated queues, default `default` |
| `CONCURRENCY` | Jobs at once, default 4 |
| `TLS_DIR` | Directory holding the CA and `worker` certificate |
| `SINKS_URL`, `SMTP_ADDR`, `WEBHOOK_ALLOW` | Only for the built-in handlers |

Give it a stop grace period longer than your longest job (the bundled worker uses 1 minute), so a deploy lets running jobs finish.

## 9. The dashboard, Grafana and Prometheus

### Dashboard

Open https://tq.aniruddha81.tech and sign in. It refreshes every 2 seconds.

| Page | Shows |
| --- | --- |
| **Jobs** (home) | Your jobs, filterable by state and queue |
| **New job** | A form to submit a job: type (with a sample payload for each built-in type), queue, payload and every option |
| **Job** (click an ID) | One job and every attempt: which worker, when, the outcome, and any late result that fencing refused |
| **Dead** | Dead-lettered jobs, each with a **Redrive** button, and **Redrive all** |
| **Queues** | Counts per queue and state |
| **Workers** | Every worker per cloud: queues, types, version, running jobs, live or not |
| **Schedules** | Your schedules and their next tick; pause, resume and delete them; a form to create one, with cron examples and the same job fields as New job |
| **Account** | Your email and role; change your password; create and revoke API keys |
| **Users** (admins only) | Every user; add a user or an admin; delete a user |
| Header | The current leader scheduler and its epoch; who you're signed in as; Sign out |
| **Live** (`/live/`, public) | For visitors without an account, and where a signed-out visitor lands: workers online per cloud, the last 24 hours (jobs finished, jobs that recovered after a failure, late results fenced), the leader's epoch, four demos anyone can run and watch step by step (a payment, a payment whose server freezes, a service that fails half the time, and a job booked 20 s ahead), and a real reminder by email ([below](#reminders-from-the-live-page)) |

The Jobs page shows 100 jobs at a time, newest first; **Older →** and **← Newer** page through the rest.

### Reminders from the live page

Anyone can ask the live page to email them a reminder, up to a week ahead. It is the system doing something useful in public, so it is built not to be usable for spam:

```mermaid
sequenceDiagram
  participant V as Visitor
  participant J as jobs
  participant W as worker
  participant M as Their inbox
  V->>J: POST /v1/public/remind {email, label, at, tz}
  J->>J: store it in reminders (a random token)
  J->>W: email.send job "confirm:<token>"
  W->>M: "Confirm your reminder" with a link, and no text from the visitor
  V->>J: opens the link, presses Confirm
  J->>J: email.send job "remind:<token>" at its time, then delete the row
  W->>M: at the time: "Reminder: <label>"
```

- **Only the confirmation link goes to an unproven address**, and it carries no text from the visitor. The visitor's words reach an address only after someone with access to it confirms.
- **Limits.** An address gets at most 3 reminders waiting for confirmation, and everyone together 300 (unconfirmed ones are deleted after a day). Booking and confirming share the demo's rate limits at the gateway. Labels are 1 to 80 characters on one line.
- **Confirming twice makes one job.** The job's ID is chosen at booking and its Idempotency-Key is `remind:<token>`. Confirming takes a button press on the page, not just opening the link, because mail scanners open links on their own.
- **Addresses aren't kept.** The `reminders` row is deleted when confirmed or after a day. The job keeps the address until [retention](#retention) deletes it, 30 days after it is sent. The public view of a job (`/v1/public/try/{id}`) never shows its payload, or an email job's errors.
- **The links name the site from the gateway's own configuration.** The gateway sends the visitor's site name to `jobs` in `X-Public-Origin`, but only one of its `PUBLIC_ORIGIN`s, never what the client sent.
- **At least once.** It is plain email, so a crash just after sending can send it twice; the email says so.

Locally, use an `@example.com` address: both emails land in Mailpit at http://localhost:8025.

### Grafana, Prometheus and Mailpit

These run on `ops-a` (mesh address `10.99.0.14`) and have no public port. Join the WireGuard mesh from your laptop, once:

1. Install WireGuard (https://www.wireguard.com/install/) and make a key pair:

   ```sh
   mkdir -p ~/.wireguard && cd ~/.wireguard
   umask 077                                 # the key files are readable by you only
   WG=wg                                     # Git Bash on Windows: WG="/c/Program Files/WireGuard/wg.exe"
   "$WG" genkey > laptop.key                 # private: never share or commit it
   "$WG" pubkey < laptop.key > laptop.pub
   ```

2. Give the servers your public key (the file is git-ignored):

   ```sh
   echo "admin_wg_public_key = \"$(cat ~/.wireguard/laptop.pub)\"" > deploy/terraform/session/terraform.tfvars
   ```

3. Apply it. On a running system use `infra.sh` (what `make infra` runs), not `make up`: it applies the change and has every VM reload its mesh. It also opens the WireGuard port to your current public IP only.

   ```sh
   STATE=tq-tfstate-$(aws sts get-caller-identity --query Account --output text)
   terraform -chdir=deploy/terraform/session init -input=false -backend-config=bucket=$STATE
   bash deploy/release/infra.sh
   ```

   The plan should update secrets and firewall rules only. If it would replace or destroy a VM, stop.

4. Write your laptop's config, with your private key filled in:

   ```sh
   terraform -chdir=deploy/terraform/session output -raw admin_wg_conf \
     | sed "s|<your laptop's private key>|$(cat ~/.wireguard/laptop.key)|" > ~/.wireguard/tq.conf
   ```

5. Import `~/.wireguard/tq.conf` into the WireGuard app and activate it (on Linux: `sudo wg-quick up ~/.wireguard/tq.conf`). Your laptop becomes `10.99.0.2`. Check with `curl -s -o /dev/null -w "%{http_code}\n" http://10.99.0.14:3000`; `200` means you're in.

If your public IP changes, the tunnel stops working: run step 3 again. If the VMs are rebuilt (`make down`, `make up`), they get new keys: redo steps 4 and 5.

Then, with the tunnel up:

| Tool | Address | Shows |
| --- | --- | --- |
| Grafana | http://10.99.0.14:3000 | No login (read-only). Claims per second, queue depth, submit-to-start latency (p50, p95, p99), lease expiries, retries and fenced results, the leader's epoch |
| Prometheus | http://10.99.0.14:9090 | Raw metrics from dispatch and both schedulers |
| Mailpit | http://10.99.0.14:8025 | Every email the `email.send` jobs sent to `@example.com` |

## 10. Users and admins

In the browser: **Account** (top right) changes your password; admins also get **Users**, to list, add and delete users.

Change your own password. You need the current one, so a stolen session can't lock you out:

```sh
curl $G/v1/auth/password -H "$H" -d '{"current":"<current password>","new":"at-least-12-chars"}'   # 204
curl $G/v1/auth/me -H "$H"                                                                        # who am I
```

Only an admin can create, list or delete users. Passwords need at least 12 characters.

```sh
ADMIN_TOKEN=$(curl -s $G/v1/auth/login -d '{"email":"admin@example.com","password":"<admin password>"}' | jq -r .token)

curl $G/v1/auth/users -H "Authorization: Bearer $ADMIN_TOKEN" \
  -d '{"email":"you@example.com","password":"at-least-12-chars"}'

# another admin:
curl $G/v1/auth/users -H "Authorization: Bearer $ADMIN_TOKEN" \
  -d '{"email":"ops@example.com","password":"at-least-12-chars","admin":true}'
```

`201` returns the new user's ID. `403` means the token isn't an admin's, `409` means the email is taken.

```sh
curl $G/v1/auth/users -H "Authorization: Bearer $ADMIN_TOKEN"                     # list
curl -X DELETE $G/v1/auth/users/<id> -H "Authorization: Bearer $ADMIN_TOKEN"      # 204
```

A deleted user can't sign in again, but a session they already have keeps working until its token expires (at most 60 minutes). An admin can't delete their own account. Their jobs stay until retention removes them.

Seed users come from the `auth` service's `SEED_USERS` variable: `email:password[:admin],...`. They are created at startup if they don't exist yet. In production Terraform sets it, with random passwords.

## 11. Check the guarantees yourself

The chaos test kills containers and reads the databases directly, so it never goes through the public site. On your laptop, with the [local stack](#appendix-run-it-locally) running:

```sh
go test ./...                                   # unit tests
TEST_DATABASE_URL="postgres://postgres:superuser-local-only@localhost:5432,localhost:5434/postgres?sslmode=require&target_session_attrs=read-write" \
  go test -race ./...                           # plus the database tests

go run ./cmd/torture -duration 5m               # chaos test: load + faults, then checks G1-G8
go run ./cmd/torture -seed <seed>               # repeat an earlier run's fault timetable
bash deploy/local/mutants.sh                     # six broken builds; each must be caught
go run ./cmd/bench                               # throughput and latency vs worker count
```

`torture` submits a mix of jobs while it kills, pauses and partitions services and crashes PostgreSQL. Then it checks every guarantee against its own log of acknowledged submits, the jobs database and the destinations. It writes a report to `docs/results/` and exits non-zero if any guarantee fails, or if a fault a guarantee depends on never fired.

Useful `torture` flags: `-rate` (jobs per second, default 8), `-faults=false` (load only), `-gateway` and `-ca` (point it elsewhere), `-out` (report directory).

Against the cloud, the same harness runs on `ops-a`, one fault at a time, while an outside probe watches the public name:

```sh
bash deploy/experiments/act1.sh cut-clouds    # or kill-gateway, stop-azure, stop-aws
bash deploy/experiments/stop-vm.sh svc-a       # stop one VM under the probe
```

## 12. Deploy to the cloud

The short version (details in the [README](../README.md#run-it-in-the-cloud) and [deployment_plan.md](../deployment_plan.md)):

```sh
make bootstrap         # once: state bucket and the persistent stack
make up                # VMs, mesh and firewalls in AWS Mumbai and Azure Central India
git push origin main:release   # verify, build, roll out one VM at a time, smoke test
make stop / make start # park the VMs overnight
make down              # destroy the session and prove nothing is left
```

A release does only what its changes need, judged against the version that is running:

| Changed since the running release | What the release does | About |
| --- | --- | --- |
| Only Markdown (docs) | Checks only; nothing is built or deployed | 2 min |
| Only `web/` (the dashboard), plus any Markdown | Checks and build, no chaos run; rolls out the two gateways only | 7 min |
| Anything else | Everything: chaos run, build, every VM | 22 min |

A release that fails any step is rolled back automatically, always with a full redeploy of the previous release. To redeploy an older commit, run the `release` workflow by hand with its SHA; that is always a full release.

## 13. API reference

The full machine-readable spec is at **https://tq.aniruddha81.tech/openapi.yaml** (OpenAPI 3.1). Feed it to any OpenAPI client generator, or open it in an OpenAPI viewer.

All routes go through the gateway and need a token, except login, logout, the API key exchange and `/v1/public/` (the live page's). Starting a demo job is limited to 5 at once per address (one more every 3 s) and 10 at once for everyone together (one more every 2 s). Each user gets 20 requests per second, with bursts up to 40; over that is `429` with a `Retry-After` header. Errors are JSON: `{"error": "..."}`.

| Method | Path | Does |
| --- | --- | --- |
| `POST` | `/v1/auth/login` | `{email, password}` → `{token, expires_at}` and the session cookie |
| `POST` | `/v1/auth/logout` | Clears the session cookie |
| `POST` | `/v1/auth/token` | API key as the bearer token → `{token, expires_at}` |
| `GET` | `/v1/auth/me` | The caller: `{id, email, admin, created_at}` |
| `POST` | `/v1/auth/keys` | `{name}` → a new API key, shown once |
| `GET` | `/v1/auth/keys` | Your API keys (without the keys themselves) |
| `DELETE` | `/v1/auth/keys/{id}` | Revoke an API key |
| `POST` | `/v1/auth/password` | `{current, new}`: change your password |
| `POST` | `/v1/auth/users` | Admin only: `{email, password, admin?}` |
| `GET` | `/v1/auth/users` | Admin only: every user |
| `DELETE` | `/v1/auth/users/{id}` | Admin only: delete a user |
| `POST` | `/v1/jobs` | Submit (needs `Idempotency-Key`) |
| `GET` | `/v1/jobs` | List: `?state=&queue=&limit=&after=` |
| `GET` | `/v1/jobs/{id}` | Get one job |
| `GET` | `/v1/jobs/{id}/attempts` | Its attempt history |
| `POST` | `/v1/jobs/{id}/cancel` | Cancel |
| `POST` | `/v1/jobs/{id}/redrive` | Retry a dead job |
| `POST` | `/v1/jobs/redrive` | Retry every dead job, or `{queue}`'s, up to 1000 per call |
| `GET` | `/v1/queues` | Your job counts by queue and state |
| `POST` | `/v1/schedules` | Create `{name, cron, timezone, job}` |
| `GET` | `/v1/schedules` | List |
| `GET` | `/v1/schedules/{id}` | Get |
| `POST` | `/v1/schedules/{id}/pause` | Pause |
| `POST` | `/v1/schedules/{id}/resume` | Resume |
| `DELETE` | `/v1/schedules/{id}` | Delete |
| `GET` | `/v1/workers` | Every worker (system-wide, read-only) |
| `GET` | `/v1/leader` | The leader scheduler: `{holder, epoch, expires_at, held}` |
| `GET` | `/v1/public/stats` | No token: `{clouds: [{cloud, workers, running}], epoch, leader, day: {succeeded, recovered, fenced}}`, cached 5 s |
| `POST` | `/v1/public/try` | No token: `{"scenario": "pay" \| "freeze" \| "flaky" \| "later"}` runs that fixed demo job → the job |
| `GET` | `/v1/public/try/{id}` | No token: a demo job and its attempts, `{job, attempts}`, without its payload |
| `POST` | `/v1/public/remind` | No token: `{email, label, at, tz}` books a reminder and emails a confirmation link → `{email}` |
| `POST` | `/v1/public/remind/{token}/confirm` | No token: the reminder becomes an `email.send` job at its time → the job; `404` once used or after a day |
| `GET` | `/.well-known/jwks.json` | Public keys that verify tokens |

| Status | Meaning |
| --- | --- |
| `200` | OK, or a repeated submit returning the original job |
| `201` | Created |
| `204` | Deleted, or logged out |
| `400` | Invalid input; the message says which field |
| `401` | Missing, expired or invalid token |
| `403` | Not an admin, the current password is wrong, or a browser request from another site |
| `404` | No such job or schedule, or it belongs to someone else |
| `409` | Key reused with a different body, name taken, or the job is in the wrong state |
| `413` | Body over 64 KB |
| `429` | Rate limited; wait for `Retry-After` |
| `503` | Outcome unknown; retry with the same `Idempotency-Key` |

A job looks like this:

```json
{
  "id": "0199d3c2-...",
  "queue": "default",
  "type": "ledger.transfer",
  "payload": {"from": "alice", "to": "bob", "amount": 500},
  "priority": 0,
  "run_at": "2026-10-12T10:00:00Z",
  "state": "succeeded",
  "cancel_requested": false,
  "attempt": 1,
  "max_attempts": 5,
  "timeout_seconds": 60,
  "affinity": null,
  "last_error": null,
  "created_at": "2026-10-12T10:00:00Z",
  "updated_at": "2026-10-12T10:00:01Z",
  "finished_at": "2026-10-12T10:00:01Z"
}
```

## 14. Troubleshooting

| Symptom | Likely cause and fix |
| --- | --- |
| `curl: (35) ... CRYPT_E_NO_REVOCATION_CHECK` in Git Bash on Windows | Add `--ssl-no-revoke`. |
| Grafana or Mailpit doesn't load | They are mesh-only. Bring up the WireGuard tunnel ([section 9](#9-the-dashboard-grafana-and-prometheus)). |
| The site doesn't resolve after Dec 14, 2026 | The deployment was destroyed as planned. [Run it locally](#appendix-run-it-locally). |
| `401` on every call | Token expired (60 min) or the header is missing. Log in again. |
| `400 Idempotency-Key header is required` | Add `-H "Idempotency-Key: <something unique>"`. |
| `409` on submit | You reused a key with a different body. Use a new key. |
| Job stays `available` forever | No worker handles that `type` on that `queue`, or its `run_at` is in the future. Check the Workers page. |
| Job went straight to `dead` | Permanent error: bad payload, a webhook URL not in `WEBHOOK_ALLOW`, or no handler. See `last_error`. |
| `affinity` job ran in the other cloud | Expected: after 30 s with no worker in its own cloud, any worker may take it. |
| A schedule didn't fire at 02:30 one night | That time didn't exist that day (DST spring-forward) in its time zone. |
| Database tests are skipped | Set `TEST_DATABASE_URL` (see [section 11](#11-check-the-guarantees-yourself)). |

## Appendix: run it locally

Everything in this guide also works against your own copy. You need Go 1.27 and Docker.

```sh
go run ./cmd/devcerts                                  # once: dev CA, certificates, JWT key
docker compose -f deploy/local/compose.yml up --build
```

Then change the setup to:

```sh
G=https://localhost:8443
alias curl='curl --cacert deploy/local/certs/ca.crt'   # the local certificate comes from the dev CA
# Git Bash on Windows: alias curl='curl --ssl-no-revoke --cacert deploy/local/certs/ca.crt'
```

| | Production | Local |
| --- | --- | --- |
| Address | https://tq.aniruddha81.tech | https://localhost:8443 |
| Logins | from `terraform output users` | `demo@example.com` / `demo-password-1`, `admin@example.com` / `admin-password-1` |
| Webhook URL allowed | `https://ops-a:8090/webhook` | `https://sinks:8090/webhook` |
| Grafana | http://10.99.0.14:3000 (mesh) | http://localhost:3000 |
| Prometheus | http://10.99.0.14:9090 (mesh) | http://localhost:9090 |
| Mailpit | http://10.99.0.14:8025 (mesh) | http://localhost:8025 |

If services exit at startup with a certificate error, run `go run ./cmd/devcerts` first.

Stop it:

```sh
docker compose -f deploy/local/compose.yml down      # keep the data
docker compose -f deploy/local/compose.yml down -v   # also delete the databases
```
