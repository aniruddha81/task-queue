# Demo video: shot list

The plan calls for a 2–3 minute video with six shots. Each one shows a guarantee working, not a slide about it. The cloud experiments take about 10 minutes each, so record them in full and cut them down. A spoken line is given for each shot.

**Before recording:**

- Start the local stack: `go run ./cmd/devcerts && docker compose -f deploy/local/compose.yml up --build -d`.
- Sign in to the dashboard at `https://tq-aniruddha81.trafficmanager.net`, and locally at `https://localhost:8443` (`demo@example.com` / `demo-password-1`).
- Check that no rollout is running, because experiments share its concurrency group: `gh run list --workflow release.yml -L 1`.

---

## 1. Jobs flowing in both clouds (≈20 s)

```sh
gh workflow run experiment.yml --ref release -f scenario=watch -f label=demo-watch   # 6 min of harness load, no fault
```

Show the dashboard's **Workers** page (workers grouped by cloud), then **Jobs** filling and finishing.

> "Jobs submitted through one public name, run by workers in AWS Mumbai and Azure Central India. Postgres is the only queue."

## 2. A paused worker's late result is rejected (≈30 s, local)

```sh
TOKEN=...   # from POST /v1/auth/login, see the README
curl --cacert deploy/local/certs/ca.crt https://localhost:8443/v1/jobs -H "Authorization: Bearer $TOKEN" \
  -H "Idempotency-Key: demo-zombie-1" -d '{"queue":"default","type":"chaos.sleep","payload":{"ms":40000}}'
docker compose -f deploy/local/compose.yml logs worker | grep <job id>   # which worker took it
docker pause taskqueue-worker-1                                         # that one; hold about 45 s
docker unpause taskqueue-worker-1
```

Show the job's detail page. Attempt 1 is `lease_expired`, and attempt 2, on the other worker, `succeeded`. Once the paused worker wakes, attempt 1's **Late result** column reads "rejected".

> "This worker froze past its 30-second lease. Another worker took the job over. When the first one woke up and reported, its lease token was stale, so the database rejected it. One recorded result."

## 3. The clouds cut apart (≈30 s)

```sh
gh workflow run experiment.yml --ref release -f scenario=cut-clouds -f label=demo-cut-clouds
```

Show the dashboard staying up through the cut. Then show the run's artifacts: the probe's timeline (failures only in the first seconds, then full service) and the checker's report (**PASS** on G1–G8).

> "Now the network between AWS and Azure is cut for two minutes. The side holding the etcd majority keeps the database. The other side's workers idle. Nothing is lost and nothing is duplicated: that's the checker, not me."

## 4. A deploy with zero failed requests (≈20 s)

```sh
git push origin main:release
```

Show the release run's rollout step: each gateway drained, one VM at a time, ending with `probe: N requests, 0 failed`. A recorded release run works just as well (`gh run view <id> --log`).

> "Every release deploys one VM at a time, with the gateway drained first, while a probe outside both clouds sends ten requests a second and never retries. One failure would roll the release back. Sixteen in a row: zero."

## 5. Automatic database failover with zero loss (≈30 s)

```sh
gh workflow run experiment.yml --ref release -f scenario=stop-leader -f label=demo-failover
```

Show the dashboard's leader epoch moving, the probe timeline (about 17 s with no primary, then normal), and the checker's G1 line: **PASS, 0 violations**.

> "This crashes the database primary with no warning. Patroni promotes the synchronous standby in the other cloud on its own. Every job the system had acknowledged is still there."

## 6. The checker passes, then fails against a mutant (≈30 s, local)

```sh
go run ./cmd/torture -duration 3m                                  # PASS
MUTANT=mutant_nofence docker compose -f deploy/local/compose.yml up --build -d
go run ./cmd/torture -duration 3m                                  # FAIL: G3
docker compose -f deploy/local/compose.yml up --build -d           # back to the clean build
```

Show the two reports side by side: all PASS, then G3 **FAIL** with its violations ("attempt 1 succeeded but attempt 2 exists").

> "A test that always passes proves nothing. This build removes lease fencing, and the same checker catches it immediately. Every safety mechanism has a broken build like this one."

---

**Closing card:** the claim in one sentence, the repository URL, and links to [design.md](design.md) and [results/act2-resilience.md](results/act2-resilience.md).
