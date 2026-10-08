"use client";

import { useSearchParams } from "next/navigation";
import { Suspense, useState } from "react";
import { ago, Attempt, Job, post, usePoll } from "../lib/api";
import { stateClass } from "../page";

function JobDetail() {
  const id = useSearchParams().get("id");
  const job = usePoll<Job>(id ? `/v1/jobs/${id}` : null);
  const attempts = usePoll<{ attempts: Attempt[] }>(id ? `/v1/jobs/${id}/attempts` : null);
  const [actionError, setActionError] = useState<string | null>(null);

  const act = (what: "cancel" | "redrive") =>
    post(`/v1/jobs/${id}/${what}`)
      .then(() => (setActionError(null), job.refresh(), attempts.refresh()))
      .catch((e) => setActionError(e.message));

  if (!id) return <p className="error">No job ID.</p>;
  if (job.error) return <p className="error">{job.error}</p>;
  const j = job.data;
  if (!j) return <p className="muted">Loading…</p>;
  return (
    <>
      <h1 className="mono">{j.id}</h1>
      <dl>
        <dt>State</dt>
        <dd className={stateClass(j.state)}>{j.state}{j.cancel_requested && j.state === "running" ? " (cancel requested)" : ""}</dd>
        <dt>Type / queue</dt><dd>{j.type} on {j.queue} (priority {j.priority}{j.affinity ? `, prefers ${j.affinity}` : ""})</dd>
        <dt>Attempt</dt><dd>{j.attempt} of {j.max_attempts}, timeout {j.timeout_seconds}s</dd>
        <dt>Run at</dt><dd>{ago(j.run_at)}</dd>
        <dt>Created</dt><dd>{ago(j.created_at)}</dd>
        <dt>Finished</dt><dd>{ago(j.finished_at)}</dd>
        {j.last_error && (<><dt>Last error</dt><dd className="bad">{j.last_error}</dd></>)}
        <dt>Payload</dt><dd><code>{JSON.stringify(j.payload)}</code></dd>
      </dl>
      <p className="filters">
        {(j.state === "available" || j.state === "running") && <button onClick={() => act("cancel")}>Cancel</button>}
        {j.state === "dead" && <button onClick={() => act("redrive")}>Redrive</button>}
      </p>
      {actionError && <p className="error">{actionError}</p>}

      <h2>Attempts</h2>
      {attempts.data && attempts.data.attempts.length === 0 && <p className="muted">Not claimed yet.</p>}
      {attempts.data && attempts.data.attempts.length > 0 && (
        <table>
          <thead>
            <tr><th>#</th><th>Worker</th><th>Started</th><th>Finished</th><th>Outcome</th><th>Late result</th></tr>
          </thead>
          <tbody>
            {attempts.data.attempts.map((a) => (
              <tr key={a.attempt}>
                <td>{a.attempt}</td>
                <td>{a.node_name ?? <span className="mono">{a.node_id.slice(0, 8)}</span>}</td>
                <td>{ago(a.started_at)}</td>
                <td>{ago(a.finished_at)}</td>
                <td className={a.outcome === "succeeded" ? "ok" : a.outcome ? "bad" : "warn"}>
                  {a.outcome ?? "running"}{a.error ? `: ${a.error}` : ""}
                </td>
                <td>
                  {a.late_result && (
                    <span className="bad" title="The worker reported after losing its lease; the result was discarded (fencing).">
                      {a.late_result} rejected {ago(a.late_at)}
                    </span>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </>
  );
}

export default function JobPage() {
  return (
    <Suspense fallback={<p className="muted">Loading…</p>}>
      <JobDetail />
    </Suspense>
  );
}
