"use client";

import Link from "next/link";
import { useState } from "react";
import { ago, Job, usePoll } from "./lib/api";

const STATES = ["", "available", "running", "succeeded", "dead", "cancelled"];

export function stateClass(s: string) {
  return s === "succeeded" ? "ok" : s === "dead" ? "bad" : s === "running" ? "warn" : "muted";
}

export function JobTable({ jobs, extra }: { jobs: Job[]; extra?: (j: Job) => React.ReactNode }) {
  if (jobs.length === 0) return <p className="muted">No jobs.</p>;
  return (
    <table>
      <thead>
        <tr>
          <th>Job</th><th>Type</th><th>Queue</th><th>State</th><th>Attempt</th><th>Updated</th>
          {extra && <th />}
        </tr>
      </thead>
      <tbody>
        {jobs.map((j) => (
          <tr key={j.id}>
            <td><Link className="mono" href={`/job/?id=${j.id}`}>{j.id.slice(0, 13)}…</Link></td>
            <td>{j.type}</td>
            <td>{j.queue}</td>
            <td className={stateClass(j.state)}>{j.state}{j.cancel_requested && j.state === "running" ? " (cancelling)" : ""}</td>
            <td>{j.attempt}/{j.max_attempts}</td>
            <td className="muted">{ago(j.updated_at)}</td>
            {extra && <td>{extra(j)}</td>}
          </tr>
        ))}
      </tbody>
    </table>
  );
}

export default function Jobs() {
  const [state, setState] = useState("");
  const [queue, setQueue] = useState("");
  const q = new URLSearchParams({ limit: "100" });
  if (state) q.set("state", state);
  if (queue) q.set("queue", queue);
  const { data, error } = usePoll<{ jobs: Job[] }>(`/v1/jobs?${q}`);
  return (
    <>
      <h1>Jobs</h1>
      <div className="filters">
        <select value={state} onChange={(e) => setState(e.target.value)} aria-label="State">
          {STATES.map((s) => <option key={s} value={s}>{s || "all states"}</option>)}
        </select>
        <input placeholder="queue" value={queue} onChange={(e) => setQueue(e.target.value.trim())} aria-label="Queue" />
      </div>
      {error && <p className="error">{error}</p>}
      {data && <JobTable jobs={data.jobs} />}
    </>
  );
}
