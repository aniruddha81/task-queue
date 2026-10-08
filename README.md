# task-queue

A distributed job scheduler in Go, running across AWS and Azure. It never loses a job it has acknowledged, and it never applies a job's effect twice: through crashes, pauses, network splits and the loss of a whole cloud. An automated chaos test checks every guarantee.

It is built on PostgreSQL with `FOR UPDATE SKIP LOCKED`, with no message broker.

> **Status:** early. Jobs are submitted, run by workers through `dispatch` with fenced leases, retried with backoff, recovered by the lease reaper when a worker dies, and dead-lettered after `max_attempts` (redrive brings them back). Cron schedules create exactly one job per tick, run by an elected, epoch-fenced leader scheduler. Everything runs over TLS behind an authenticating gateway, and the chaos test checks every guarantee while killing, pausing and partitioning the stack. The dashboard comes next.

## Use cases

Use it for any work that has to happen **later**, **reliably**, and **exactly once in effect**, even when servers crash or a whole cloud goes down.

| Use case | Example | What the scheduler guarantees |
| --- | --- | --- |
| **Payments and ledgers** | Move ₹500 from one wallet to another | A transfer is never lost and never applied twice. The ledger rejects a second attempt that carries the same effect key. |
| **Webhook delivery** | Notify a customer's server when an order ships | Retries with backoff while their server is down. After `max_attempts` failures the job goes to dead letter, and you can redrive it later. |
| **Scheduled jobs (cron)** | Build the daily sales report at 09:00 IST | Exactly one job per tick, even with two schedulers running, and no tick is skipped across DST changes or downtime. |
| **Delayed jobs** | Send a reminder 24 hours after signup | Nothing starts before its `run_at`, measured on the database's clock, not a server's. |
| **Retrying flaky APIs** | Call a third-party API that times out sometimes | Failed attempts retry with backoff. A worker that crashes mid-call doesn't lose the job: its lease expires and another worker takes over. |
| **Background processing** | Generate a PDF invoice or an export after a request returns | The request returns at once. The work runs on any free worker in either cloud, and jobs can prefer workers in a specific cloud. |
| **Priority work** | Handle password-reset emails before marketing emails | Higher-priority jobs are claimed first. |
| **Multi-cloud availability** | Keep processing when AWS (or Azure) has an outage | The surviving cloud keeps accepting and running jobs. The database fails over automatically. |

**Exactly once, with one honest caveat.** A job may *run* more than once: for example, when a worker crashes after doing the work but before reporting it. Its *effect* still happens once, as long as the destination checks the job's effect key, the way the built-in ledger and webhook receiver do. Destinations that can't check it, such as plain SMTP email, get at-least-once delivery. The project uses email to demonstrate this case.

**Not a good fit for:**

- event streaming or analytics pipelines (use Kafka or similar);
- sub-millisecond latency;
- millions of jobs per second. Claiming with Postgres `SKIP LOCKED` has a ceiling, and the project measures where it is.

## Run it locally

You need Go 1.27 and Docker.

```sh
go run ./cmd/devcerts                                   # dev CA, service certificates, local JWT key (git-ignored)
docker compose -f deploy/local/compose.yml up --build
```

This starts PostgreSQL 18 (TLS only, one role per service), `auth`, `jobs`, `dispatch`, two `scheduler`s (one elected leader runs the lease reaper and cron), a `worker`, and the `gateway`. The gateway at **https://localhost:8443** is the only public port; every internal call uses mTLS.

### Try the API

Log in as a seeded demo user to get a token (60 minutes). Browsers get it as an `HttpOnly` cookie instead.

```sh
K="--cacert deploy/local/certs/ca.crt"   # on Windows' built-in curl, add --ssl-no-revoke
G=https://localhost:8443

curl $K $G/v1/auth/login -d '{"email":"demo@example.com","password":"demo-password-1"}'
TOKEN=<token from the response>

# Submit. The Idempotency-Key makes retries safe: repeating it returns the same job.
# The demo worker runs chaos.sleep: it sleeps for "ms", then succeeds.
curl $K $G/v1/jobs -H "Authorization: Bearer $TOKEN" -H "Idempotency-Key: nap-1"   -d '{"queue":"default","type":"chaos.sleep","payload":{"ms":500}}'

curl $K $G/v1/jobs -H "Authorization: Bearer $TOKEN"                        # list, newest first
curl $K $G/v1/jobs/<id> -H "Authorization: Bearer $TOKEN"                   # get
curl $K -X POST $G/v1/jobs/<id>/cancel -H "Authorization: Bearer $TOKEN"    # cancel
curl $K -X POST $G/v1/jobs/<id>/redrive -H "Authorization: Bearer $TOKEN"   # retry a dead job

# Cron: a job on every tick (5-field cron, any IANA time zone).
curl $K $G/v1/schedules -H "Authorization: Bearer $TOKEN"   -d '{"name":"nightly","cron":"0 2 * * *","timezone":"Asia/Kolkata","job":{"queue":"default","type":"chaos.sleep","payload":{"ms":100}}}'
```

| Status | Meaning |
| --- | --- |
| `201` | Created |
| `200` | A repeat of an earlier submit: the original job is returned |
| `401` | Missing, expired or invalid token |
| `404` | No such job, or it belongs to someone else |
| `409` | Key reused with a different request, or the job is in the wrong state |
| `429` | Rate limit; retry after the `Retry-After` header |
| `503` | The outcome is unknown; retry with the same key |

Only admins create users (`admin@example.com` / `admin-password-1` locally):

```sh
curl $K $G/v1/auth/users -H "Authorization: Bearer $ADMIN_TOKEN" -d '{"email":"you@example.com","password":"at-least-12-chars"}'
```

Stop and remove everything, including the database volume:

```sh
docker compose -f deploy/local/compose.yml down -v
```

## Develop

```sh
go build ./...
go vet ./...
go test ./...
```

The database tests are skipped unless `TEST_DATABASE_URL` points at a PostgreSQL 18 server. They create and drop their own throwaway databases, so the local stack's server is safe to use:

```sh
TEST_DATABASE_URL="postgres://postgres:superuser-local-only@localhost:5432/postgres?sslmode=require" go test -race ./...
```

After editing anything in `proto/`, regenerate the Go code. This also lints the proto files:

```sh
go generate .
```

## Chaos test

With the local stack running:

```sh
go run ./cmd/torture -duration 5m            # load + seeded faults, then checks G1–G8
bash deploy/local/mutants.sh                  # each mutant build must FAIL the checker
```

`torture` submits a mix of jobs through the gateway while killing, pausing and cutting off services and crashing Postgres. It then checks every guarantee against its own fsynced log of acknowledgements, the jobs database, and the sinks (stand-ins for external systems, plus Mailpit for email). A guarantee whose fault never fired fails too: nothing was proven. Reports land in [docs/results/](docs/results/); rerun with `-seed` to repeat a fault timetable.

`mutants.sh` builds five deliberately broken variants (lease fencing, idempotency keys, effect keys, the leader's epoch fence, and acknowledging before commit each removed in turn) and requires the checker to catch every one.

## Release

Work on `main`. Nothing runs when you push `main`. When `main` is ready, send it to the `release` branch:

```sh
git push origin main:release
```

That push runs CI ([.github/workflows/ci.yml](.github/workflows/ci.yml)). Later in the plan, it will also deploy (see [deployment_plan.md](deployment_plan.md)). The push only fast-forwards, so `release` can never jump to a commit that isn't on `main`.

Shortcut, after a one-time `git config alias.release "push origin main:release"`:

```sh
git release
```

## Layout

```text
cmd/gateway/      the only public entry: TLS, JWT check, rate limit, dashboard
cmd/auth/         login, admin-only users, JWKS
cmd/jobs/         public job API service
cmd/dispatch/     worker API: claim, heartbeat, complete, fail
cmd/worker/       runs job handlers (demo: chaos.sleep)
cmd/scheduler/    leader-elected chores: lease reaper and cron ticks
cmd/sinks/        test destinations that deduplicate on the effect key
cmd/torture/      the chaos test and its checker
internal/scheduler/ leader election (lease + epoch) and fenced chores
cmd/devcerts/     writes the local dev CA, certificates and JWT key
internal/queue/   jobs database: submit, get, list, cancel
internal/jobsapi/ REST handlers and input validation
internal/dispatch/ worker API server, long-poll claims
internal/serve/   shared HTTP server: health endpoints, graceful drain
sdk/go/worker/    worker SDK: claim, heartbeat, drain, report results
internal/authn/   JWT signing and verification (EdDSA, JWKS)
internal/auth/    auth service: argon2id passwords, login rate limit
internal/gateway/ gateway routing, cookie CSRF check, per-user rate limit
internal/tlsconf/ mTLS configs from a CA
internal/handlers/ demo workloads: ledger, webhook, email, cron digest, chaos.*
internal/mutant/  build-tag switches that remove one safety mechanism each
internal/pgtest/  throwaway databases for tests
cmd/migrate/      one-shot migration runner: migrate <database>
migrations/       SQL migrations, one folder per database
proto/            worker API (Protobuf)
gen/              Go code generated from proto/ (do not edit)
deploy/local/     Docker Compose for local development
web/              Next.js dashboard (static export; skeleton so far)
docs/results/     measurements and checks (e.g. cloud accounts)
```

## Docs

- [full_plan(v4).md](full_plan(v4).md): the design, the guarantees, and the week-by-week plan
- [deployment_plan.md](deployment_plan.md): CI/CD and zero-downtime deploys
