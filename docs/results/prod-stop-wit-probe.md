# Probe: prod-stop-wit

The release probe, outside both clouds, through https://tq-aniruddha81.trafficmanager.net: about 10 requests a second (`/version`, an authenticated list, an idempotent submit), each on a new connection and never retried. Fault applied at +90s and held for 142s, from the start of load.

**probe: 3935 requests in 6m34s, 0 failed**

