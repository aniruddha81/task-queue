"use client";

import { useState } from "react";
import { Job, post, usePoll } from "../lib/api";
import { JobTable } from "../page";

export default function DeadLetters() {
  const { data, error, refresh } = usePoll<{ jobs: Job[] }>("/v1/jobs?state=dead&limit=100");
  const [actionError, setActionError] = useState<string | null>(null);
  const redrive = (id: string) =>
    post(`/v1/jobs/${id}/redrive`)
      .then(() => (setActionError(null), refresh()))
      .catch((e) => setActionError(e.message));
  return (
    <>
      <h1>Dead letters</h1>
      <p className="muted">Jobs that used up their attempts or failed permanently. Redrive gives one a fresh attempt budget; effects from earlier attempts stay deduplicated.</p>
      {(error || actionError) && <p className="error">{error ?? actionError}</p>}
      {data && <JobTable jobs={data.jobs} extra={(j) => <button onClick={() => redrive(j.id)}>Redrive</button>} />}
    </>
  );
}
