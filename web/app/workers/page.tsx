"use client";

import { ago, usePoll } from "../lib/api";

type Worker = {
  id: string;
  name: string;
  cloud: string;
  queues: string[];
  types: string[];
  version: string;
  last_seen_at: string;
  live: boolean;
  running: number;
};

export default function Workers() {
  const { data, error } = usePoll<{ workers: Worker[] }>("/v1/workers");
  const clouds = new Map<string, Worker[]>();
  for (const w of data?.workers ?? []) clouds.set(w.cloud, [...(clouds.get(w.cloud) ?? []), w]);
  return (
    <>
      <h1>Workers</h1>
      <p className="muted">Live means the worker polled dispatch in the last 45 seconds.</p>
      {error && <p className="error">{error}</p>}
      {data && clouds.size === 0 && <p className="muted">No workers seen in the last hour.</p>}
      {[...clouds].map(([cloud, ws]) => (
        <section key={cloud}>
          <h2>{cloud.toUpperCase()}: {ws.filter((w) => w.live).length} live</h2>
          <table>
            <thead>
              <tr><th>Name</th><th>Status</th><th>Running</th><th>Last seen</th><th>Queues</th><th>Types</th></tr>
            </thead>
            <tbody>
              {ws.map((w) => (
                <tr key={w.id}>
                  <td>{w.name || <span className="mono">{w.id.slice(0, 8)}</span>}</td>
                  <td className={w.live ? "ok" : "bad"}>{w.live ? "live" : "gone"}</td>
                  <td>{w.running}</td>
                  <td className="muted">{ago(w.last_seen_at)}</td>
                  <td>{w.queues.join(", ")}</td>
                  <td className="muted">{w.types.join(", ")}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </section>
      ))}
    </>
  );
}
