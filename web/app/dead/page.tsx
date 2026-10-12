"use client";

import { useState } from "react";
import { Job, post, usePoll } from "../lib/api";
import { JobTable } from "../page";

export default function DeadLetters() {
  const { data, error, refresh } = usePoll<{ jobs: Job[] }>("/v1/jobs?state=dead&limit=100");
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);
  const [confirming, setConfirming] = useState(false);
  const act = (p: Promise<unknown>, done: (r: unknown) => string | null) =>
    p.then((r) => (setMsg(done(r) ? { ok: true, text: done(r)! } : null), refresh()))
      .catch((e) => setMsg({ ok: false, text: e.message }));
  const redrive = (id: string) => act(post(`/v1/jobs/${id}/redrive`), () => null);
  const redriveAll = () => {
    if (!confirming) return setConfirming(true); // first click arms, second redrives
    setConfirming(false);
    act(post<{ redriven: number }>("/v1/jobs/redrive"), (r) => `Redrove ${(r as { redriven: number }).redriven} jobs.`);
  };
  return (
    <>
      <h1>Dead letters</h1>
      <p className="muted">Jobs that used up their attempts or failed permanently. Redrive gives one a fresh attempt budget; effects from earlier attempts stay deduplicated.</p>
      {data && data.jobs.length > 0 && (
        <p className="filters">
          <button onClick={redriveAll} onBlur={() => setConfirming(false)}>
            {confirming ? "Click again to redrive every dead job" : "Redrive all"}
          </button>
        </p>
      )}
      {(error || msg) && <p className={msg?.ok ? "ok-msg" : "error"}>{error ?? msg?.text}</p>}
      {data && <JobTable jobs={data.jobs} extra={(j) => <button onClick={() => redrive(j.id)}>Redrive</button>} />}
    </>
  );
}
