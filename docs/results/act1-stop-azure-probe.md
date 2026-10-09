# Probe: stop-azure

An outside client, every 2 s, through https://tq-aniruddha81.trafficmanager.net: `/version` (the gateway), login every 10 s (auth), and a submit (jobs and the database). Fault applied at +90s and reverted at +322s from the start of load.

| Window | /version | login | submit |
| --- | --- | --- | --- |
| before | 34/34 | 7/7 | 34/34 |
| during | 67/67 | 1/14 | 67/67 |
| after | 48/48 | 9/9 | 48/48 |

First and last failed submit, relative to the fault: none.
