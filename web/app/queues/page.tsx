"use client";

import { usePoll } from "../lib/api";

type Count = { queue: string; state: string; count: number };
const STATES = ["available", "running", "succeeded", "dead", "cancelled"];

export default function Queues() {
  const { data, error } = usePoll<{ queues: Count[] }>("/v1/queues");
  const byQueue = new Map<string, Record<string, number>>();
  for (const c of data?.queues ?? []) {
    byQueue.set(c.queue, { ...byQueue.get(c.queue), [c.state]: c.count });
  }
  return (
    <>
      <h1>Queues</h1>
      {error && <p className="error">{error}</p>}
      {data && byQueue.size === 0 && <p className="muted">No jobs yet.</p>}
      {byQueue.size > 0 && (
        <table>
          <thead>
            <tr><th>Queue</th>{STATES.map((s) => <th key={s}>{s}</th>)}</tr>
          </thead>
          <tbody>
            {[...byQueue].map(([q, counts]) => (
              <tr key={q}>
                <td>{q}</td>
                {STATES.map((s) => <td key={s}>{counts[s] ?? 0}</td>)}
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </>
  );
}
