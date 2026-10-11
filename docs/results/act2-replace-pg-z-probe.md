# Probe: act2-replace-pg-z

The release probe, outside both clouds, through https://tq-aniruddha81.trafficmanager.net: about 10 requests a second (`/version`, an authenticated list, an idempotent submit), each on a new connection and never retried. Fault applied at +90s and held for 1080s, from the start of load.

**probe: 12076 requests in 20m9s, 0 failed**

