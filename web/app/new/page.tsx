"use client";

import { useRouter } from "next/navigation";
import { FormEvent, useRef, useState } from "react";
import { Job, post } from "../lib/api";
import { emptyJob, jobBody, JobFields, JobFieldsEditor } from "../lib/jobform";

export default function NewJob() {
  const router = useRouter();
  const [f, setF] = useState<JobFields>(emptyJob);
  const [runAt, setRunAt] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  // One Idempotency-Key per version of the form: submitting it again (a double click, or a
  // retry after "outcome unknown") returns the same job; any edit starts a new key.
  const key = useRef<string | null>(null);
  const edited = () => {
    key.current = null;
    setError(null);
  };

  const submit = (e: FormEvent) => {
    e.preventDefault();
    const r = jobBody(f);
    if ("error" in r) return setError(r.error);
    if (runAt) r.body.run_at = new Date(runAt).toISOString();
    key.current ??= crypto.randomUUID();
    setBusy(true);
    post<Job>("/v1/jobs", r.body, { "Idempotency-Key": key.current })
      .then((job) => router.push(`/job/?id=${job.id}`))
      .catch((err) => {
        setError(err.message);
        setBusy(false);
      });
  };

  return (
    <>
      <h1>New job</h1>
      <p className="muted">A free worker picks it up within moments. After you submit, the job&apos;s page opens so you can watch it run.</p>
      <form className="job" onSubmit={submit}>
        <JobFieldsEditor
          value={f}
          onChange={(patch) => (setF({ ...f, ...patch }), edited())}
          extra={
            <label>
              <span>Run at (your time; empty = now)</span>
              <input type="datetime-local" value={runAt} onChange={(e) => (setRunAt(e.target.value), edited())} />
            </label>
          }
        />
        <button type="submit" disabled={busy}>{busy ? "Submitting…" : "Submit job"}</button>
        {error && <p className="error">{error}</p>}
      </form>
    </>
  );
}
