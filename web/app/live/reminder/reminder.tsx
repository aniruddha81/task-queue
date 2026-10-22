"use client";

import Link from "next/link";
import { useSearchParams } from "next/navigation";
import { useState } from "react";
import { Job, post } from "../../lib/api";
import { Timeline } from "../live";

/**
 * Where a reminder's emails lead: ?t= from the confirmation email, ?job= from the reminder itself.
 * Confirming takes a press, not just opening the link: mail scanners open links on their own.
 */
export default function Reminder() {
  const q = useSearchParams();
  const token = q.get("t");
  const [id, setId] = useState<string | null>(q.get("job"));
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const confirm = () => {
    setBusy(true);
    post<Job>(`/v1/public/remind/${encodeURIComponent(token!)}/confirm`)
      .then((j) => setId(j.id))
      .catch((e) => setError(e.message))
      .finally(() => setBusy(false));
  };

  return (
    <div className="live">
      {id ? (
        <section>
          <h1>Your reminder</h1>
          <p className="muted">It runs as a job on the live system. Keep this page open to watch it, or come back later.</p>
          <Timeline id={id} scenario="remind" />
        </section>
      ) : token ? (
        <section>
          <h1>Confirm your reminder</h1>
          <p className="muted">Press Confirm, and the email you asked for is booked.</p>
          <button className="cta" onClick={confirm} disabled={busy}>{busy ? "Confirming…" : "Confirm"}</button>
          {error && <p className="error">{error[0].toUpperCase() + error.slice(1)}.</p>}
        </section>
      ) : (
        <p className="error">This link is incomplete. Copy the whole link from the email.</p>
      )}
      <p><Link href="/live/">← See the system working live</Link></p>
    </div>
  );
}
