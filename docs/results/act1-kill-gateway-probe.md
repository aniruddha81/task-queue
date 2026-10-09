# Probe: kill-gateway

An outside client, every 2 s, through https://tq-aniruddha81.trafficmanager.net: `/version` (the gateway), login every 10 s (auth), and a submit (jobs and the database). Fault applied at +90s and reverted at +219s from the start of load.

| Window | /version | login | submit |
| --- | --- | --- | --- |
| before | 34/34 | 7/7 | 34/34 |
| during | 24/33 | 5/7 | 24/33 |
| after | 69/69 | 14/14 | 69/69 |

First and last failed submit, relative to the fault: +3s … +62s.
