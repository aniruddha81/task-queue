# Probe: prod-stop-work-a

The release probe, outside both clouds, through https://tq-aniruddha81.trafficmanager.net: about 10 requests a second (`/version`, an authenticated list, an idempotent submit), each on a new connection and never retried. Fault applied at +90s and held for 203s, from the start of load.

**probe: 4227 requests in 7m3s, 0 failed**

