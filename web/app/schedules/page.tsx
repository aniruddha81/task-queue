"use client";

import { FormEvent, useState } from "react";
import { ago, del, post, usePoll } from "../lib/api";
import { emptyJob, jobBody, JobFields, JobFieldsEditor, NAME } from "../lib/jobform";

type Schedule = {
  id: string;
  name: string;
  cron: string;
  timezone: string;
  job: { type: string; queue: string };
  paused: boolean;
  next_tick_at: string;
};

const EXAMPLES = [
  ["*/5 * * * *", "every 5 minutes"],
  ["0 * * * *", "every hour"],
  ["0 9 * * *", "09:00 every day"],
  ["30 18 * * 1-5", "18:30 on weekdays"],
  ["0 0 1 * *", "midnight on the 1st"],
];

export default function Schedules() {
  const { data, error, refresh } = usePoll<{ schedules: Schedule[] }>("/v1/schedules");
  const [actionError, setActionError] = useState<string | null>(null);
  const [confirming, setConfirming] = useState<string | null>(null);
  const [name, setName] = useState("");
  const [cron, setCron] = useState("0 9 * * *");
  const [tz, setTz] = useState("Asia/Kolkata");
  const [job, setJob] = useState<JobFields>(emptyJob);
  const [formMsg, setFormMsg] = useState<{ ok: boolean; text: string } | null>(null);

  const act = (p: Promise<unknown>) =>
    p.then(() => (setActionError(null), refresh())).catch((e) => setActionError(e.message));
  const toggle = (s: Schedule) => act(post(`/v1/schedules/${s.id}/${s.paused ? "resume" : "pause"}`));
  const remove = (s: Schedule) => {
    if (confirming !== s.id) return setConfirming(s.id); // first click arms, second deletes
    setConfirming(null);
    act(del(`/v1/schedules/${s.id}`));
  };

  const create = (e: FormEvent) => {
    e.preventDefault();
    const r = jobBody(job);
    if ("error" in r) return setFormMsg({ ok: false, text: r.error });
    post<Schedule>("/v1/schedules", { name, cron: cron.trim(), timezone: tz.trim(), job: r.body })
      .then((s) => (setFormMsg({ ok: true, text: `Created ${s.name}; first job ${ago(s.next_tick_at)}.` }), setName(""), refresh()))
      .catch((err) => setFormMsg({ ok: false, text: err.message }));
  };

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
                <td className="filters" style={{ margin: 0 }}>
                  <button onClick={() => toggle(s)}>{s.paused ? "Resume" : "Pause"}</button>
                  <button onClick={() => remove(s)} onBlur={() => setConfirming(null)}>
                    {confirming === s.id ? "Click again to delete" : "Delete"}
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      <h2>New schedule</h2>
      <p className="muted">Creates one job on every tick. Jobs it already created stay when you delete it.</p>
      <form className="job" onSubmit={create}>
        <label>
          <span>Name</span>
          <input required pattern={NAME} placeholder="daily-report" value={name} onChange={(e) => setName(e.target.value.trim())} />
        </label>
        <div className="row" style={{ marginTop: 0 }}>
          <label>
            <span>Cron (minute hour day month weekday)</span>
            <input required value={cron} onChange={(e) => setCron(e.target.value)} className="mono" />
          </label>
          <label>
            <span>Time zone</span>
            <input required value={tz} onChange={(e) => setTz(e.target.value)} placeholder="Asia/Kolkata" />
          </label>
        </div>
        <p className="muted">
          Examples:{" "}
          {EXAMPLES.map(([c, what], i) => (
            <span key={c}>
              {i > 0 && ", "}
              <a href="#" onClick={(e) => (e.preventDefault(), setCron(c))}><code>{c}</code></a> {what}
            </span>
          ))}
        </p>
        <JobFieldsEditor value={job} onChange={(patch) => setJob({ ...job, ...patch })} />
        <button type="submit">Create schedule</button>
        {formMsg && <p className={formMsg.ok ? "ok-msg" : "error"}>{formMsg.text}</p>}
      </form>
    </>
  );
}
