# Probe: prod-stop-svc-a

The release probe, outside both clouds, through https://tq-aniruddha81.trafficmanager.net: about 10 requests a second (`/version`, an authenticated list, an idempotent submit), each on a new connection and never retried. Fault applied at +90s and held for 172s, from the start of load.

**probe: 3926 requests in 6m33s, 2 failed**

| Request | From the fault | Error |
| --- | --- | --- |
| POST /v1/jobs | +2.6s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": EOF |
| GET /version | +2.7s | Get "https://tq-aniruddha81.trafficmanager.net/version": EOF |
