# 0009. Azure Traffic Manager with MultiValue routing as the public entry point

**Status:** accepted · **Decided:** week 10, used with two gateways from week 12

## Context

The public name has to reach a gateway in either cloud and stop pointing at one that is down or being deployed. The original plan used Route 53 DNS failover with a 30 s TTL and a purchased domain. A load balancer can't span the two clouds, and a single entry VM would be a single point of failure.

## Decision

Azure Traffic Manager publishes `tq-<name>.trafficmanager.net` with **MultiValue** routing. It returns up to two healthy gateway IPs, with a 10 s TTL and an HTTPS health check on `/readyz` every 10 s (one failure tolerated). Clients that receive both IPs try the second as soon as the first fails, as browsers and Go's dialer do. A rollout disables a gateway's endpoint and waits 60 s before restarting the gateway. Both gateways share one Let's Encrypt certificate through `auth`'s database.

## Consequences

- **Good:** no domain to buy, and a crash costs only the requests in flight, because new connections go to the live gateway at once.
- **Good:** cheap. It was billed at $0.13 a day in production.
- **Bad:** the public name depends on Azure even while Azure's VMs are down. A Traffic Manager outage is a known limit.
- **Bad:** pulling an unhealthy gateway takes 20–30 s. In one production run, DNS returned only the dead gateway for about 10 s ([act2-resilience.md](../results/act2-resilience.md), stop AWS Mumbai).
- **Bad:** a gateway's readiness has to reflect its upstreams. Otherwise Traffic Manager keeps sending traffic to a gateway that can't serve it.

## Evidence

- Stopping either `svc-*` VM in production failed 2 probe requests each, both in flight on the dying VM.
- 16 drained rollouts in a row with 0 failed requests ([act2-resilience.md](../results/act2-resilience.md)).
