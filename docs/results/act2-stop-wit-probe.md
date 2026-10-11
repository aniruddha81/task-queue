# Probe: act2-stop-wit

The release probe, outside both clouds, through https://tq-aniruddha81.trafficmanager.net: about 10 requests a second (`/version`, an authenticated list, an idempotent submit), each on a new connection and never retried. Fault applied at +90s and held for 141s, from the start of load.

**probe: 4211 requests in 7m2s, 0 failed**

