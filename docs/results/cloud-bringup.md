# Cloud bring-up — week 10

Oct 8, 2026. Act 1 layout from the role map: `svc-a`, `work-a`, `pg-a` and `ops-a` on AWS Mumbai (`t4g.small`, arm64); `svc-z` and `work-z` on Azure Central India (`Standard_B2ats_v2`, amd64). The public name is https://tq-aniruddha81.trafficmanager.net.

## Done-when checklist

| Check | Result |
| --- | --- |
| `make up` and `make down` each work from scratch, twice in a row | ✅ two full rounds (below) |
| `ping -M do -s 1352` across the mesh (MTU 1380 fits) | ✅ every VM to every VM, 0% loss |
| The sweep finds nothing left after `make down` | ✅ both rounds, after fixing the sweep itself ([bug diary](../bugs.md)) |
| A push to `release` deploys in place | ✅ releases `34a614a` and `f72a5ad`: verify, chaos run, 9 multi-arch images, rolling deploy, smoke test |
| A deliberately broken release rolls back on its own | ⏳ not yet run: pushing a broken release needs the owner's go-ahead |

## Rebuild from scratch

The VMs' boot config holds no version. After `make up`, each VM fetches its own files and converges to the release recorded in SSM and Key Vault, with no deploy running. "Serving" means one job with `affinity=aws` and one with `affinity=azure` succeeded through the public name, as a seeded user.

| Round | `make down` (139 resources) | Sweep | `make up` (139 resources) | Apply done → serving |
| --- | --- | --- | --- | --- |
| 1 | 2 m 37 s | nothing left | 1 m 45 s | 3 m 43 s |
| 2 | 2 m 24 s | nothing left | 1 m 39 s | 3 m 00 s |

During convergence, services that start before their dependencies fail and retry: `auth` restarts until `ops-a` has run the migrations, and `tq-converge.service` retries every 20 s. No ordering between VMs is needed.

## Mesh (WireGuard, MTU 1380)

Round-trip time of a 1,352-byte ping with don't-fragment set, which proves the MTU fits:

| | within a cloud | AWS ↔ Azure |
| --- | --- | --- |
| RTT | 0.5–1.6 ms | 5.1–6.6 ms (a few samples up to 14 ms) |

The week 1 public-internet probe measured 5.47 ms between the clouds, so WireGuard adds well under a millisecond.

## The public entry

- Traffic Manager (MultiValue) returns `svc-a`'s address; the endpoint reports `Online`.
- The gateway obtained its own Let's Encrypt certificate (`CN=tq-aniruddha81.trafficmanager.net`, issuer `YE2`) over TLS-ALPN-01 on 443, so no port 80 is open.
- `/version` reports the release commit; the release record (`current`) matches in SSM and Key Vault.

## Found on the way

Three bugs, all in the bug diary: GitHub's OIDC subject now carries immutable IDs, which broke the cloud sign-in; the CI chaos run could start before `jobs` had auth's keys; and the sweep reported "nothing left" when a query failed.

One risk to watch: each fresh session's gateway requests a new certificate. Let's Encrypt allows 5 duplicate certificates a week for the same name, and this week used 3. Week 12's shared certificate storage should outlive sessions, so that `make up` reuses the certificate.
