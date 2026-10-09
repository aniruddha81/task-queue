# Probe: stop-aws

An outside client, every 2 s, through https://tq-aniruddha81.trafficmanager.net: `/version` (the gateway), login every 10 s (auth), and a submit (jobs and the database). Fault applied at +90s and reverted at +291s from the start of load.

| Window | /version | login | submit |
| --- | --- | --- | --- |
| before | 34/34 | 7/7 | 34/34 |
| during | 1/17 | 0/4 | 1/17 |
| after | 29/30 | 6/6 | 29/30 |

First and last failed submit, relative to the fault: +3s … +207s.
