# Probe: act2-replace-pg-a

The release probe, outside both clouds, through https://tq-aniruddha81.trafficmanager.net: about 10 requests a second (`/version`, an authenticated list, an idempotent submit), each on a new connection and never retried. Fault applied at +90s and held for 2280s, from the start of load.

**probe: 24385 requests in 40m40s, 0 failed**

