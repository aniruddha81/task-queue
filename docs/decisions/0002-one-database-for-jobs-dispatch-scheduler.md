# 0002. `jobs`, `dispatch` and `scheduler` share one database

**Status:** accepted · **Decided:** week 1 · **Guarantees:** G1, G3, G5, G6

## Context

The usual microservice rule is one database per service. Here, three services work on the same jobs. `jobs` creates them, `dispatch` claims and completes them, and `scheduler` reaps expired leases and inserts cron jobs. A claim, a lease expiry and a cron tick each have to be atomic with the job row they change.

## Decision

`jobs`, `dispatch` and `scheduler` are three processes of **one bounded context**, and they share the `jobs` database on purpose. They are separate processes because they scale and fail differently: public API, worker-facing gRPC, and singleton chores. `auth` is a separate context with its own database and credentials. Both databases live in one Postgres cluster, so there is a single failover story, and each service's database role can reach only its own database.

## Consequences

- **Good:** every operation that has to be atomic is one transaction. No distributed protocol, saga or outbox exists anywhere in the core.
- **Good:** one cluster means one replication setup and one failover to test.
- **Bad:** the three services share a schema. A migration has to keep all of them working, so migrations are expand/contract only, and a service reports not ready until the schema matches its version.
- **Bad:** they can't be split into separate databases later without redesigning the claim path.

## Evidence

- The chaos checker reads G1, G3, G5 and G6 straight from this database. All of them held in every run, locally and in production ([act2-resilience.md](../results/act2-resilience.md)).
- Discussion: [design.md, section 1](../design.md#1-the-system-in-one-picture).
