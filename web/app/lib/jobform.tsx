"use client";

import { ReactNode } from "react";

// The built-in job types (internal/handlers), each with a sample payload to start from.
type Template = { help: string; payload: () => unknown; timeout?: number; attempts?: number };

// Workers may only call allowlisted webhook URLs: the test receiver, named differently per deployment.
const webhookURL = () =>
  location.hostname === "localhost" ? "https://sinks:8090/webhook" : "https://ops-a:8090/webhook";

const TYPES: Record<string, Template> = {
  "chaos.sleep": { help: "Sleeps for ms milliseconds, then succeeds. The simplest test.", payload: () => ({ ms: 500 }) },
  "ledger.transfer": {
    help: "Moves amount from one ledger account to another. Exactly once, even if the job runs twice.",
    payload: () => ({ from: "alice", to: "bob", amount: 500 }),
  },
  "webhook.deliver": {
    help: "POSTs body to url with the job's effect key. Only the allowlisted test receiver is accepted.",
    payload: () => ({ url: webhookURL(), body: { hello: "world" } }),
  },
  "report.daily": { help: "Records one daily digest. No payload needed.", payload: () => ({}) },
  "email.send": {
    help: "Sends a plain-text email with this subject and body: to Mailpit for example.com addresses, otherwise for real. At least once: SMTP can't refuse a duplicate.",
    payload: () => ({ to: "you@example.com", subject: "Hello", body: "Hi,\n\nThis email was sent by a task-queue job.\n" }),
  },
  "chaos.fail": { help: "Fails with probability p, so you can watch retries and dead letters.", payload: () => ({ p: 0.5 }) },
  "chaos.overrun": {
    help: "Ignores its deadline, so its late result is refused (fencing). Sets a 2 s timeout and 2 attempts under More options.",
    payload: () => ({ ms: 4000 }),
    timeout: 2,
    attempts: 2,
  },
  "chaos.crash": {
    help: "Kills the worker process with probability p. Its jobs come back when their leases expire.",
    payload: () => ({ p: 0.1 }),
  },
};

const CUSTOM = "custom";
export const NAME = "[a-z0-9._\\-]{1,64}"; // the API's rule for queue, type and schedule names

export type JobFields = {
  type: string;
  custom: string;
  queue: string;
  payload: string;
  priority: string;
  attempts: string;
  timeout: string;
  affinity: string;
};

const pretty = (v: unknown) => JSON.stringify(v, null, 2);

export const emptyJob = (): JobFields => ({
  type: "chaos.sleep", custom: "", queue: "default", payload: pretty({ ms: 500 }),
  priority: "", attempts: "", timeout: "", affinity: "",
});

/** jobBody turns the fields into the API's job object, or says what is wrong. */
export function jobBody(f: JobFields): { body: Record<string, unknown> } | { error: string } {
  let payload: unknown;
  try {
    payload = JSON.parse(f.payload);
  } catch (err) {
    return { error: `Payload is not valid JSON: ${(err as Error).message}` };
  }
  const body: Record<string, unknown> = { queue: f.queue, type: f.type === CUSTOM ? f.custom : f.type, payload };
  if (f.priority) body.priority = Number(f.priority);
  if (f.attempts) body.max_attempts = Number(f.attempts);
  if (f.timeout) body.timeout_seconds = Number(f.timeout);
  if (f.affinity) body.affinity = f.affinity;
  return { body };
}

/** The job's fields: type (with a sample payload per built-in type), queue, payload, options. */
export function JobFieldsEditor({ value: f, onChange, extra }: {
  value: JobFields;
  onChange: (patch: Partial<JobFields>) => void;
  extra?: ReactNode; // more inputs under More options
}) {
  const pickType = (type: string) => {
    const t = TYPES[type];
    onChange({
      type,
      payload: t ? pretty(t.payload()) : "{}",
      timeout: t?.timeout ? String(t.timeout) : "",
      attempts: t?.attempts ? String(t.attempts) : "",
    });
  };
  return (
    <>
      <label>
        <span>Type</span>
        <select value={f.type} onChange={(e) => pickType(e.target.value)}>
          {Object.keys(TYPES).map((t) => <option key={t} value={t}>{t}</option>)}
          <option value={CUSTOM}>custom…</option>
        </select>
      </label>
      {f.type === CUSTOM ? (
        <label>
          <span>Custom type (a worker must handle it, or the job waits)</span>
          <input required pattern={NAME} placeholder="invoice.render" value={f.custom}
            onChange={(e) => onChange({ custom: e.target.value.trim() })} />
        </label>
      ) : (
        <p className="muted">{TYPES[f.type].help}</p>
      )}
      <label>
        <span>Queue</span>
        <input required pattern={NAME} value={f.queue} onChange={(e) => onChange({ queue: e.target.value.trim() })} />
      </label>
      <label>
        <span>Payload (JSON)</span>
        <textarea rows={8} spellCheck={false} value={f.payload} onChange={(e) => onChange({ payload: e.target.value })} />
      </label>
      <details>
        <summary>More options</summary>
        <div className="row">
          <label>
            <span>Priority (higher first)</span>
            <input type="number" min={-1000} max={1000} placeholder="0" value={f.priority}
              onChange={(e) => onChange({ priority: e.target.value })} />
          </label>
          {extra}
          <label>
            <span>Max attempts</span>
            <input type="number" min={1} max={50} placeholder="5" value={f.attempts}
              onChange={(e) => onChange({ attempts: e.target.value })} />
          </label>
          <label>
            <span>Timeout (seconds)</span>
            <input type="number" min={1} max={3600} placeholder="60" value={f.timeout}
              onChange={(e) => onChange({ timeout: e.target.value })} />
          </label>
          <label>
            <span>Cloud</span>
            <select value={f.affinity} onChange={(e) => onChange({ affinity: e.target.value })}>
              <option value="">any</option>
              <option value="aws">prefer AWS</option>
              <option value="azure">prefer Azure</option>
            </select>
          </label>
        </div>
      </details>
    </>
  );
}
