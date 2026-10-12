# 0008. Three etcd sites, with a witness in AWS Hyderabad

**Status:** accepted · **Decided:** week 13

## Context

Automatic failover needs someone to decide which database node is primary. With only two sites (AWS and Azure), a cut between them leaves each side unable to tell whether the other is dead or only unreachable. If both promoted, there would be two primaries (split brain). If neither could, every failover would need a human.

## Decision

etcd, Patroni's consensus store, runs on `pg-a` (AWS Mumbai), `pg-z` (Azure Central India) and `wit`, a small VM in AWS Hyderabad that runs etcd only. A promotion needs two of the three votes, and a primary that loses etcd demotes itself. Members join a running cluster as learners and are promoted once caught up. Only a fresh session founds a cluster, so a member that can't reach its peers never starts a second one.

## Consequences

- **Good:** losing any one site, or a cut between the clouds, is handled automatically. The side with the majority keeps or takes the primary, and the other side can't promote.
- **Good:** the witness is cheap. It runs only etcd and holds no data.
- **Bad:** the witness is in AWS. An outage across all of AWS removes two of the three votes, so Azure can't promote itself. A witness on a third provider would fix this, and is a stretch goal.
- **Bad:** membership changes are fragile. An etcd leader that wrongly believes a peer is gone refuses every change ([bugs.md](../bugs.md), "A replaced database VM couldn't rejoin etcd for 48 minutes"). `deploy.sh` now asks each member in turn.

## Evidence

- Stopping `wit` failed 0 probe requests in week 13 and again in production. A cut with the primary on the majority side caused no leader change. A cut with the primary on the minority side moved it to `pg-a` in about 22 s, with no acknowledged job lost ([act2-database.md](../results/act2-database.md), [act2-resilience.md](../results/act2-resilience.md)).
