# Probe: prod-stop-aws

The release probe, outside both clouds, through https://tq-aniruddha81.trafficmanager.net: about 10 requests a second (`/version`, an authenticated list, an idempotent submit), each on a new connection and never retried. Fault applied at +90s and held for 203s, from the start of load.

**probe: 3931 requests in 6m34s, 181 failed**

| Request | From the fault | Error |
| --- | --- | --- |
| GET /version | +2.9s | Get "https://tq-aniruddha81.trafficmanager.net/version": EOF |
| GET /v1/jobs | +12.3s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| POST /v1/jobs | +12.4s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| GET /v1/jobs | +12.6s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| POST /v1/jobs | +12.7s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| GET /v1/jobs | +12.9s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| POST /v1/jobs | +13.0s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| GET /v1/jobs | +13.2s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| POST /v1/jobs | +13.3s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| GET /v1/jobs | +13.5s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| POST /v1/jobs | +13.6s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| GET /v1/jobs | +13.8s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| POST /v1/jobs | +13.9s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| GET /v1/jobs | +14.1s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| POST /v1/jobs | +14.2s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| GET /v1/jobs | +14.4s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| POST /v1/jobs | +14.5s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| GET /v1/jobs | +14.7s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| POST /v1/jobs | +14.8s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| GET /v1/jobs | +15.0s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| POST /v1/jobs | +15.1s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| GET /v1/jobs | +15.3s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| POST /v1/jobs | +15.4s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| GET /v1/jobs | +15.6s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| POST /v1/jobs | +15.7s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| GET /v1/jobs | +15.9s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| POST /v1/jobs | +16.0s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| GET /v1/jobs | +16.2s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| POST /v1/jobs | +16.3s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| GET /v1/jobs | +16.5s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| POST /v1/jobs | +16.6s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| GET /v1/jobs | +16.8s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| POST /v1/jobs | +16.9s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| GET /v1/jobs | +17.1s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| POST /v1/jobs | +17.2s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| GET /v1/jobs | +17.4s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| POST /v1/jobs | +17.5s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| GET /v1/jobs | +17.7s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| POST /v1/jobs | +17.8s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| GET /v1/jobs | +18.0s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| POST /v1/jobs | +18.1s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| GET /v1/jobs | +18.3s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| POST /v1/jobs | +18.4s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| GET /v1/jobs | +18.6s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| POST /v1/jobs | +18.7s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| GET /v1/jobs | +18.9s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| POST /v1/jobs | +19.0s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| GET /v1/jobs | +19.2s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| POST /v1/jobs | +19.3s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| GET /v1/jobs | +19.5s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| POST /v1/jobs | +19.6s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| GET /v1/jobs | +19.8s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| POST /v1/jobs | +19.9s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| GET /v1/jobs | +20.1s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| POST /v1/jobs | +20.2s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| GET /v1/jobs | +20.4s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| POST /v1/jobs | +20.5s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| GET /v1/jobs | +20.7s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| POST /v1/jobs | +20.8s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| GET /v1/jobs | +21.0s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| POST /v1/jobs | +21.1s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| GET /v1/jobs | +21.3s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| POST /v1/jobs | +21.4s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| GET /v1/jobs | +21.6s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| POST /v1/jobs | +21.7s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| GET /v1/jobs | +21.9s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| POST /v1/jobs | +22.0s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| GET /v1/jobs | +22.2s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| POST /v1/jobs | +22.3s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| GET /v1/jobs | +22.5s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| POST /v1/jobs | +22.6s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| GET /v1/jobs | +22.8s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| POST /v1/jobs | +22.9s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| GET /v1/jobs | +23.1s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| POST /v1/jobs | +23.2s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| GET /v1/jobs | +23.4s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| POST /v1/jobs | +23.5s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| GET /v1/jobs | +23.7s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| POST /v1/jobs | +23.8s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| GET /v1/jobs | +24.0s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": context deadline exceeded (Client.Timeout exceeded while awaiting headers) |
| GET /version | +28.9s | Get "https://tq-aniruddha81.trafficmanager.net/version": dial tcp 13.205.52.183:443: i/o timeout |
| GET /v1/jobs | +29.0s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": dial tcp 13.205.52.183:443: i/o timeout |
| POST /v1/jobs | +29.1s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": dial tcp 13.205.52.183:443: i/o timeout |
| GET /version | +29.2s | Get "https://tq-aniruddha81.trafficmanager.net/version": dial tcp 13.205.52.183:443: i/o timeout |
| GET /v1/jobs | +29.3s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": dial tcp 13.205.52.183:443: i/o timeout |
| POST /v1/jobs | +29.4s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": dial tcp 13.205.52.183:443: i/o timeout |
| GET /version | +29.5s | Get "https://tq-aniruddha81.trafficmanager.net/version": dial tcp 13.205.52.183:443: i/o timeout |
| GET /v1/jobs | +29.6s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": dial tcp 13.205.52.183:443: i/o timeout |
| POST /v1/jobs | +29.7s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": dial tcp 13.205.52.183:443: i/o timeout |
| GET /version | +29.8s | Get "https://tq-aniruddha81.trafficmanager.net/version": dial tcp 13.205.52.183:443: i/o timeout |
| GET /v1/jobs | +29.9s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": dial tcp 13.205.52.183:443: i/o timeout |
| POST /v1/jobs | +30.0s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": dial tcp 13.205.52.183:443: i/o timeout |
| GET /version | +30.1s | Get "https://tq-aniruddha81.trafficmanager.net/version": dial tcp 13.205.52.183:443: i/o timeout |
| GET /v1/jobs | +30.2s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": dial tcp 13.205.52.183:443: i/o timeout |
| POST /v1/jobs | +30.3s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": dial tcp 13.205.52.183:443: i/o timeout |
| GET /version | +30.4s | Get "https://tq-aniruddha81.trafficmanager.net/version": dial tcp 13.205.52.183:443: i/o timeout |
| GET /v1/jobs | +30.5s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": dial tcp 13.205.52.183:443: i/o timeout |
| POST /v1/jobs | +30.6s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": dial tcp 13.205.52.183:443: i/o timeout |
| GET /version | +30.7s | Get "https://tq-aniruddha81.trafficmanager.net/version": dial tcp 13.205.52.183:443: i/o timeout |
| GET /v1/jobs | +30.8s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": dial tcp 13.205.52.183:443: i/o timeout |
| POST /v1/jobs | +30.9s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": dial tcp 13.205.52.183:443: i/o timeout |
| GET /version | +31.0s | Get "https://tq-aniruddha81.trafficmanager.net/version": dial tcp 13.205.52.183:443: i/o timeout |
| GET /v1/jobs | +31.1s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": dial tcp 13.205.52.183:443: i/o timeout |
| POST /v1/jobs | +31.2s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": dial tcp 13.205.52.183:443: i/o timeout |
| GET /version | +31.3s | Get "https://tq-aniruddha81.trafficmanager.net/version": dial tcp 13.205.52.183:443: i/o timeout |
| GET /v1/jobs | +31.4s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": dial tcp 13.205.52.183:443: i/o timeout |
| POST /v1/jobs | +31.5s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": dial tcp 13.205.52.183:443: i/o timeout |
| GET /version | +31.6s | Get "https://tq-aniruddha81.trafficmanager.net/version": dial tcp 13.205.52.183:443: i/o timeout |
| GET /v1/jobs | +31.7s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": dial tcp 13.205.52.183:443: i/o timeout |
| POST /v1/jobs | +31.8s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": dial tcp 13.205.52.183:443: i/o timeout |
| GET /version | +31.9s | Get "https://tq-aniruddha81.trafficmanager.net/version": dial tcp 13.205.52.183:443: i/o timeout |
| GET /v1/jobs | +32.0s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": dial tcp 13.205.52.183:443: i/o timeout |
| POST /v1/jobs | +32.1s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": dial tcp 13.205.52.183:443: i/o timeout |
| GET /version | +32.2s | Get "https://tq-aniruddha81.trafficmanager.net/version": dial tcp 13.205.52.183:443: i/o timeout |
| GET /v1/jobs | +32.3s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": dial tcp 13.205.52.183:443: i/o timeout |
| POST /v1/jobs | +32.4s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": dial tcp 13.205.52.183:443: i/o timeout |
| GET /version | +32.5s | Get "https://tq-aniruddha81.trafficmanager.net/version": dial tcp 13.205.52.183:443: i/o timeout |
| GET /v1/jobs | +32.6s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": dial tcp 13.205.52.183:443: i/o timeout |
| POST /v1/jobs | +32.7s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": dial tcp 13.205.52.183:443: i/o timeout |
| GET /version | +32.8s | Get "https://tq-aniruddha81.trafficmanager.net/version": dial tcp 13.205.52.183:443: i/o timeout |
| GET /v1/jobs | +32.9s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": dial tcp 13.205.52.183:443: i/o timeout |
| POST /v1/jobs | +33.0s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": dial tcp 13.205.52.183:443: i/o timeout |
| GET /version | +33.1s | Get "https://tq-aniruddha81.trafficmanager.net/version": dial tcp 13.205.52.183:443: i/o timeout |
| GET /v1/jobs | +33.2s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": dial tcp 13.205.52.183:443: i/o timeout |
| POST /v1/jobs | +33.3s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": dial tcp 13.205.52.183:443: i/o timeout |
| GET /version | +33.4s | Get "https://tq-aniruddha81.trafficmanager.net/version": dial tcp 13.205.52.183:443: i/o timeout |
| GET /v1/jobs | +33.5s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": dial tcp 13.205.52.183:443: i/o timeout |
| POST /v1/jobs | +33.6s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": dial tcp 13.205.52.183:443: i/o timeout |
| GET /version | +33.7s | Get "https://tq-aniruddha81.trafficmanager.net/version": dial tcp 13.205.52.183:443: i/o timeout |
| GET /v1/jobs | +33.8s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": dial tcp 13.205.52.183:443: i/o timeout |
| POST /v1/jobs | +33.9s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": dial tcp 13.205.52.183:443: i/o timeout |
| GET /version | +34.0s | Get "https://tq-aniruddha81.trafficmanager.net/version": dial tcp 13.205.52.183:443: i/o timeout |
| GET /v1/jobs | +34.1s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": dial tcp 13.205.52.183:443: i/o timeout |
| POST /v1/jobs | +34.2s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": dial tcp 13.205.52.183:443: i/o timeout |
| GET /version | +34.3s | Get "https://tq-aniruddha81.trafficmanager.net/version": dial tcp 13.205.52.183:443: i/o timeout |
| GET /v1/jobs | +34.4s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": dial tcp 13.205.52.183:443: i/o timeout |
| POST /v1/jobs | +34.5s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": dial tcp 13.205.52.183:443: i/o timeout |
| GET /version | +34.6s | Get "https://tq-aniruddha81.trafficmanager.net/version": dial tcp 13.205.52.183:443: i/o timeout |
| GET /v1/jobs | +34.7s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": dial tcp 13.205.52.183:443: i/o timeout |
| POST /v1/jobs | +34.8s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": dial tcp 13.205.52.183:443: i/o timeout |
| GET /version | +34.9s | Get "https://tq-aniruddha81.trafficmanager.net/version": dial tcp 13.205.52.183:443: i/o timeout |
| GET /v1/jobs | +35.0s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": dial tcp 13.205.52.183:443: i/o timeout |
| POST /v1/jobs | +35.1s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": dial tcp 13.205.52.183:443: i/o timeout |
| GET /version | +35.2s | Get "https://tq-aniruddha81.trafficmanager.net/version": dial tcp 13.205.52.183:443: i/o timeout |
| GET /v1/jobs | +35.3s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": dial tcp 13.205.52.183:443: i/o timeout |
| POST /v1/jobs | +35.4s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": dial tcp 13.205.52.183:443: i/o timeout |
| GET /version | +35.5s | Get "https://tq-aniruddha81.trafficmanager.net/version": dial tcp 13.205.52.183:443: i/o timeout |
| GET /v1/jobs | +35.6s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": dial tcp 13.205.52.183:443: i/o timeout |
| POST /v1/jobs | +35.7s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": dial tcp 13.205.52.183:443: i/o timeout |
| GET /version | +35.8s | Get "https://tq-aniruddha81.trafficmanager.net/version": dial tcp 13.205.52.183:443: i/o timeout |
| GET /v1/jobs | +35.9s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": dial tcp 13.205.52.183:443: i/o timeout |
| POST /v1/jobs | +36.0s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": dial tcp 13.205.52.183:443: i/o timeout |
| GET /version | +36.1s | Get "https://tq-aniruddha81.trafficmanager.net/version": dial tcp 13.205.52.183:443: i/o timeout |
| GET /v1/jobs | +36.2s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": dial tcp 13.205.52.183:443: i/o timeout |
| POST /v1/jobs | +36.3s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": dial tcp 13.205.52.183:443: i/o timeout |
| GET /version | +36.4s | Get "https://tq-aniruddha81.trafficmanager.net/version": dial tcp 13.205.52.183:443: i/o timeout |
| GET /v1/jobs | +36.5s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": dial tcp 13.205.52.183:443: i/o timeout |
| POST /v1/jobs | +36.6s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": dial tcp 13.205.52.183:443: i/o timeout |
| GET /version | +36.7s | Get "https://tq-aniruddha81.trafficmanager.net/version": dial tcp 13.205.52.183:443: i/o timeout |
| GET /v1/jobs | +36.8s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": dial tcp 13.205.52.183:443: i/o timeout |
| POST /v1/jobs | +36.9s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": dial tcp 13.205.52.183:443: i/o timeout |
| GET /version | +37.0s | Get "https://tq-aniruddha81.trafficmanager.net/version": dial tcp 13.205.52.183:443: i/o timeout |
| GET /v1/jobs | +37.1s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": dial tcp 13.205.52.183:443: i/o timeout |
| POST /v1/jobs | +37.2s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": dial tcp 13.205.52.183:443: i/o timeout |
| GET /version | +37.3s | Get "https://tq-aniruddha81.trafficmanager.net/version": dial tcp 13.205.52.183:443: i/o timeout |
| GET /v1/jobs | +37.4s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": dial tcp 13.205.52.183:443: i/o timeout |
| POST /v1/jobs | +37.5s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": dial tcp 13.205.52.183:443: i/o timeout |
| GET /version | +37.6s | Get "https://tq-aniruddha81.trafficmanager.net/version": dial tcp 13.205.52.183:443: i/o timeout |
| GET /v1/jobs | +37.7s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": dial tcp 13.205.52.183:443: i/o timeout |
| POST /v1/jobs | +37.8s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": dial tcp 13.205.52.183:443: i/o timeout |
| GET /version | +37.9s | Get "https://tq-aniruddha81.trafficmanager.net/version": dial tcp 13.205.52.183:443: i/o timeout |
| GET /v1/jobs | +38.0s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": dial tcp 13.205.52.183:443: i/o timeout |
| POST /v1/jobs | +38.1s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": dial tcp 13.205.52.183:443: i/o timeout |
| GET /version | +38.2s | Get "https://tq-aniruddha81.trafficmanager.net/version": dial tcp 13.205.52.183:443: i/o timeout |
| GET /v1/jobs | +38.3s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": dial tcp 13.205.52.183:443: i/o timeout |
| POST /v1/jobs | +38.4s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": dial tcp 13.205.52.183:443: i/o timeout |
| GET /version | +38.5s | Get "https://tq-aniruddha81.trafficmanager.net/version": dial tcp 13.205.52.183:443: i/o timeout |
| GET /v1/jobs | +38.6s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": dial tcp 13.205.52.183:443: i/o timeout |
| POST /v1/jobs | +38.7s | Post "https://tq-aniruddha81.trafficmanager.net/v1/jobs": dial tcp 13.205.52.183:443: i/o timeout |
| GET /version | +38.8s | Get "https://tq-aniruddha81.trafficmanager.net/version": dial tcp 13.205.52.183:443: i/o timeout |
| GET /v1/jobs | +38.9s | Get "https://tq-aniruddha81.trafficmanager.net/v1/jobs?limit=1": dial tcp 13.205.52.183:443: i/o timeout |
