"use client";

import { useState } from "react";
import { ago, post, usePoll } from "../lib/api";

type Schedule = {
  id: string;
  name: string;
  cron: string;
  timezone: string;
  job: { type: string; queue: string };
  paused: boolean;
  next_tick_at: string;
};

export default function Schedules() {
  const { data, error, refresh } = usePoll<{ schedules: Schedule[] }>("/v1/schedules");
  const [actionError, setActionError] = useState<string | null>(null);
  const toggle = (s: Schedule) =>
    post(`/v1/schedules/${s.id}/${s.paused ? "resume" : "pause"}`)
      .then(() => (setActionError(null), refresh()))
      .catch((e) => setActionError(e.message));
  return (
    <>
      <h1>Schedules</h1>
      {(error || actionError) && <p className="error">{error ?? actionError}</p>}
      {data && data.schedules.length === 0 && <p className="muted">No schedules.</p>}
      {data && data.schedules.length > 0 && (
        <table>
          <thead>
            <tr><th>Name</th><th>Cron</th><th>Job</th><th>Next tick</th><th>Status</th><th /></tr>
          </thead>
          <tbody>
            {data.schedules.map((s) => (
              <tr key={s.id}>
                <td>{s.name}</td>
                <td><code>{s.cron}</code> <span className="muted">{s.timezone}</span></td>
                <td>{s.job.type} on {s.job.queue}</td>
                <td>{s.paused ? "—" : ago(s.next_tick_at)}</td>
                <td className={s.paused ? "warn" : "ok"}>{s.paused ? "paused" : "active"}</td>
                <td><button onClick={() => toggle(s)}>{s.paused ? "Resume" : "Pause"}</button></td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </>
  );
}
