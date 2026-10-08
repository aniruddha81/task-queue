# task-queue

A distributed job scheduler in Go, running across AWS and Azure. It never loses a job it has acknowledged, and it never applies a job's effect twice: through crashes, pauses, network splits and the loss of a whole cloud. An automated chaos test checks every guarantee.

It is built on PostgreSQL with `FOR UPDATE SKIP LOCKED`, with no message broker.

> **Status:** early. The local stack, the database schema and the worker API contract are in place. Submitting and running jobs is not built yet.

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
docker compose -f deploy/local/compose.yml up --build
```

This starts PostgreSQL 18, applies the migrations, and starts the `jobs` service on port 8080:

```sh
curl localhost:8080/healthz   # the process is alive
curl localhost:8080/readyz    # it can reach the database
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

The database tests are skipped unless `TEST_DATABASE_URL` points at a PostgreSQL 18 server. They create and drop their own throwaway database, so the local stack's server is safe to use:

```sh
TEST_DATABASE_URL="postgres://taskqueue:taskqueue@localhost:5432/jobs?sslmode=disable" go test ./...
```

After editing anything in `proto/`, regenerate the Go code. This also lints the proto files:

```sh
go generate .
```

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
cmd/jobs/         public job API (health endpoints so far)
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
