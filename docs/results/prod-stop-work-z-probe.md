# Probe: prod-stop-work-z

The release probe, outside both clouds, through https://tq-aniruddha81.trafficmanager.net: about 10 requests a second (`/version`, an authenticated list, an idempotent submit), each on a new connection and never retried. Fault applied at +90s and held for 146s, from the start of load.

**probe: 3963 requests in 6m37s, 0 failed**

