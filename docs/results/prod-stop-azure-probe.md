# Probe: prod-stop-azure

The release probe, outside both clouds, through https://tq-aniruddha81.trafficmanager.net: about 10 requests a second (`/version`, an authenticated list, an idempotent submit), each on a new connection and never retried. Fault applied at +90s and held for 234s, from the start of load.

**probe: 3630 requests in 6m4s, 5 failed**

| Request | From the fault | Error |
| --- | --- | --- |
| GET /version | +6.5s | Get "https://tq-aniruddha81.trafficmanager.net/version": EOF |
| POST /v1/jobs | +6.5s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": read tcp 10.1.0.110:32976->20.235.67.41:443: read: connection reset by peer |
| POST /v1/jobs | +16.3s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| POST /v1/jobs | +16.6s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| POST /v1/jobs | +16.9s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
