# Probe: prod-pause-work-z

The release probe, outside both clouds, through https://tq-aniruddha81.trafficmanager.net: about 10 requests a second (`/version`, an authenticated list, an idempotent submit), each on a new connection and never retried. Fault applied at +90s and held for 158s, from the start of load.

**probe: 3783 requests in 6m19s, 0 failed**

