# task-queue

A distributed job scheduler in Go, running across AWS and Azure. It never loses a job it has acknowledged, and it never applies a job's effect twice: through crashes, pauses, network splits and the loss of a whole cloud. An automated chaos test checks every guarantee.

It is built on PostgreSQL with `FOR UPDATE SKIP LOCKED`, with no message broker.

> **Status:** early. The local stack, the database schema and the worker API contract are in place. Submitting and running jobs is not built yet.

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

## Layout

```text
cmd/jobs/         public job API (health endpoints so far)
cmd/migrate/      one-shot migration runner: migrate <database>
migrations/       SQL migrations, one folder per database
proto/            worker API (Protobuf)
gen/              Go code generated from proto/ (do not edit)
deploy/local/     Docker Compose for local development
```

## Docs

- [full_plan(v4).md](full_plan(v4).md): the design, the guarantees, and the week-by-week plan
- [deployment_plan.md](deployment_plan.md): CI/CD and zero-downtime deploys
