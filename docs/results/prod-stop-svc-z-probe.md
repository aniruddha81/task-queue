# Probe: prod-stop-svc-z

The release probe, outside both clouds, through https://tq-aniruddha81.trafficmanager.net: about 10 requests a second (`/version`, an authenticated list, an idempotent submit), each on a new connection and never retried. Fault applied at +90s and held for 143s, from the start of load.

**probe: 3947 requests in 6m35s, 2 failed**

| Request | From the fault | Error |
| --- | --- | --- |
| GET /v1/jobs | +16.2s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| POST /v1/jobs | +16.3s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
