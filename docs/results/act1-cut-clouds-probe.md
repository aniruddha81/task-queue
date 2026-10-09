# Probe: cut-clouds

An outside client, every 2 s, through https://tq-aniruddha81.trafficmanager.net: `/version` (the gateway), login every 10 s (auth), and a submit (jobs and the database). Fault applied at +90s and reverted at +245s from the start of load.

| Window | /version | login | submit |
| --- | --- | --- | --- |
| before | 34/34 | 7/7 | 34/34 |
| during | 47/47 | 3/10 | 46/47 |
| after | 59/59 | 11/11 | 59/59 |

First and last failed submit, relative to the fault: +26s … +26s.
