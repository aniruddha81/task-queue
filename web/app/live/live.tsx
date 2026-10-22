"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
import { Attempt, Job, post, usePoll } from "../lib/api";

// The public face of the system, for people without an account: what it is doing right now,
// and a demo they can run themselves (/v1/public/, see internal/jobsapi/public.go).

type Stats = {
  clouds: { cloud: string; workers: number; running: number }[];
  epoch: number;
  leader: boolean;
  day: { succeeded: number; recovered: number; fenced: number };
};

const CLOUDS: Record<string, [string, string]> = { aws: ["AWS", "Mumbai"], azure: ["Azure", "Central India"] };
const where = (cloud: string | null) => (cloud ? CLOUDS[cloud]?.join(" ") ?? cloud : "an unknown cloud");
const REPO = "https://github.com/aniruddha81/task-queue/blob/main";

type Scenario = { title: string; pitch: string; after: string };

const SCENARIOS: Record<string, Scenario> = {
  pay: {
    title: "Send a payment",
    pitch: "₹100 from you to a shop. A server in one of the two clouds picks it up and the ledger records it, once.",
    after:
      "Your payment was saved before you got an answer, so it would still go through even if every server crashed right after you clicked. Each payment carries a unique key and the ledger checks it, so a repeat can never charge you twice.",
  },
  freeze: {
    title: "Freeze the server mid-payment",
    pitch: "The first server pays, then freezes before it can report back. Watch another one take over, and nothing gets charged twice.",
    after:
      "Attempt 1 made the payment, then froze. When its 3 seconds ran out, a fresh attempt took over and sent the same payment with the same key. The ledger recognised it and didn't charge again. When the frozen server woke up and tried to report, it was refused: its turn was over. You were charged exactly once.",
  },
  flaky: {
    title: "Call a service that keeps failing",
    pitch: "This one fails half the time, on purpose. The system keeps retrying, with a longer pause each time, until it works.",
    after:
      "Every failure was retried automatically, waiting a little longer each time (up to 1 s, 2 s, 4 s…, at random), so a struggling service isn't hammered. Nobody had to write retry code or watch for errors.",
  },
  later: {
    title: "Do it in 20 seconds",
    pitch: "Book work for later, like a reminder or a renewal. It waits, then runs on time.",
    after:
      "The start time was kept in the database and checked on the database's own clock, so no server with a drifting clock can start it early. If servers restart while it waits, it still runs.",
  },
};

export default function Live() {
  const stats = usePoll<Stats>("/v1/public/stats").data;
  const [run, setRun] = useState<{ scenario: string; id: string } | null>(null);
  const [error, setError] = useState<string | null>(null);
  const start = (scenario: string) => {
    setError(null);
    post<Job>("/v1/public/try", { scenario })
      .then((j) => setRun({ scenario, id: j.id }))
      .catch((e) => setError(e.message));
  };

  return (
    <div className="live">
      <section className="hero">
        <p className="eyebrow"><span className="pulse" /> Live right now on AWS Mumbai and Azure Central India</p>
        <h1>Work that has to happen, happens. Exactly once.</h1>
        <p className="lede">
          Payments, order notifications, reminders, nightly reports: work an app hands off to run in the background.
          This system runs it on servers in two clouds, retries what fails, and takes over from servers that crash,
          without ever losing a job or doing it twice. Even if a whole cloud goes down.
        </p>
        <a className="cta" href="#try">Try it yourself: no account needed ↓</a>
      </section>

      <section>
        <h2>Running now</h2>
        <div className="clouds">
          {(stats?.clouds ?? [{ cloud: "aws", workers: 0, running: 0 }, { cloud: "azure", workers: 0, running: 0 }]).map((c) => (
            <div key={c.cloud} className={`cloud ${stats && c.workers === 0 ? "down" : ""}`}>
              <div className="cloud-name">{CLOUDS[c.cloud]?.[0] ?? c.cloud}<span>{CLOUDS[c.cloud]?.[1]}</span></div>
              {!stats ? <p className="muted">Connecting…</p> : c.workers > 0 ? (
                <>
                  <div className="big">{c.workers}</div>
                  <div className="muted">{c.workers === 1 ? "worker" : "workers"} online · {c.running} {c.running === 1 ? "job" : "jobs"} running</div>
                </>
              ) : (
                <>
                  <div className="big bad">0</div>
                  <div className="muted">workers online. The other cloud carries the work.</div>
                </>
              )}
            </div>
          ))}
        </div>
        <div className="tiles">
          <Tile n={stats?.day.succeeded} label="jobs finished in the last 24 hours" />
          <Tile n={stats?.day.recovered} label="of them hit a failure and recovered on their own" />
          <Tile n={stats?.day.fenced} label="late answers from frozen servers refused, so nothing ran twice" />
          <Tile n={stats?.epoch} prefix="#" label={stats && !stats.leader ? "leader term: changing hands right now" : "leader term: one scheduler is in charge, and a new term starts whenever it changes"} />
        </div>
      </section>

      <section id="try">
        <h2>Try it</h2>
        <p className="muted">Pick one. It runs as a real job on the live system, and you watch every step.</p>
        <div className="scenarios">
          {Object.entries(SCENARIOS).map(([key, s]) => (
            <button key={key} className={`scenario ${run?.scenario === key ? "picked" : ""}`} onClick={() => start(key)}>
              <strong>{s.title}</strong>
              <span>{s.pitch}</span>
            </button>
          ))}
        </div>
        {error && <p className="error">{error}</p>}
        {run && <Timeline key={run.id} id={run.id} scenario={run.scenario} />}
      </section>

      <section>
        <h2>What you can build on it</h2>
        <div className="uses">
          <Use title="Payments and wallets" text="A transfer is never lost and never applied twice, even when a server dies halfway through." />
          <Use title="Order notifications" text="Tell a partner's server an order shipped. If it's down, keep trying with growing pauses, then set it aside for a person." />
          <Use title="Daily reports" text="Build the sales report at 09:00 every day: exactly one per day, even with two schedulers running." />
          <Use title="Reminders and follow-ups" text="Send something 24 hours after signup. It never starts early, and it still runs if servers restart in between." />
          <Use title="Slow work in the background" text="Make the PDF invoice or the export after the page has already answered, on whichever server is free." />
          <Use title="Surviving an outage" text="If AWS or Azure goes down, the other cloud keeps accepting and running work. The database fails over on its own." />
        </div>
      </section>

      <section>
        <h2>Broken on purpose, to prove it</h2>
        <div className="proof">
          <Proof n="12 of 12" text="faults survived in production with every guarantee checked: servers killed, the database primary crashed, the clouds cut apart, all of Azure switched off, all of AWS switched off." href={`${REPO}/docs/results/act2-resilience.md`} />
          <Proof n="0 of 59,599" text="requests failed during 16 software releases in a row, watched by a probe outside both clouds that never retries." href={`${REPO}/docs/results/act2-resilience.md`} />
          <Proof n="~17 s" text="without a database when the primary crashed. Then the copy in the other cloud took over, and no accepted job was lost." href={`${REPO}/docs/results/act2-database.md`} />
          <Proof n="6 of 6" text="deliberately broken versions caught. Each removes one safety mechanism, and the tests must fail. A test that can't fail proves nothing." href={`${REPO}/docs/bugs.md`} />
        </div>
      </section>

      <footer className="live-foot">
        <span>For developers:</span>
        <a href="/openapi.yaml">API (OpenAPI)</a>
        <a href={`${REPO}/docs/usage.md`}>Usage guide</a>
        <a href={`${REPO}/docs/design.md`}>How it works</a>
        <a href="https://github.com/aniruddha81/task-queue">Source</a>
        <Link href="/login/">Sign in</Link>
      </footer>
    </div>
  );
}

function Tile({ n, label, prefix = "" }: { n?: number; label: string; prefix?: string }) {
  return (
    <div className="tile">
      <div className="big">{n === undefined ? "…" : prefix + n.toLocaleString()}</div>
      <div className="muted">{label}</div>
    </div>
  );
}

const Use = ({ title, text }: { title: string; text: string }) => (
  <div className="use"><strong>{title}</strong><p>{text}</p></div>
);

const Proof = ({ n, text, href }: { n: string; text: string; href: string }) => (
  <a className="proof-item" href={href}><div className="big">{n}</div><p>{text}</p></a>
);

/** Seconds as people say them. */
const span = (ms: number) => (ms < 1000 ? `${Math.max(ms, 1)} ms` : `${(ms / 1000).toFixed(1)} s`);

/** One demo job, step by step in plain words, until it is over. */
function Timeline({ id, scenario }: { id: string; scenario: string }) {
  const { data } = usePoll<{ job: Job; attempts: Attempt[] }>(`/v1/public/try/${id}`);
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 250);
    return () => clearInterval(t);
  }, []);

  const job = data?.job;
  const attempts = data?.attempts ?? [];
  const finished = job && (job.state === "succeeded" || job.state === "dead" || job.state === "cancelled");
  // A frozen payment's late answer arrives after the job is done (10 s after it started): wait for it.
  const waitingForLate = job?.type === "demo.freeze" && attempts.length > 1 && !attempts[0].late_result &&
    now - new Date(attempts[0].started_at).getTime() < 20000;

  if (!job) return <div className="timeline"><p className="muted">Starting…</p></div>;
  const created = new Date(job.created_at).getTime();
  const runAt = new Date(job.run_at).getTime();

  return (
    <div className="timeline" aria-live="polite">
      <Step state="ok" title="Accepted" when={null}>
        Saved to the database, with a copy in the other cloud, before you got an answer. From here, it can’t be lost.
      </Step>
      {scenario === "later" && (
        <Step state={attempts.length ? "ok" : "wait"} title={`Booked for ${new Date(runAt).toLocaleTimeString()}`} when={null}>
          {attempts.length ? "Waited until its start time." : `Waiting… starts in ${Math.max(0, Math.ceil((runAt - now) / 1000))} s.`}
        </Step>
      )}
      {attempts.map((a) => {
        const started = new Date(a.started_at).getTime();
        const ended = a.finished_at ? new Date(a.finished_at).getTime() : null;
        const who = `${a.node_name ?? "a worker"} in ${where(a.node_cloud)}`;
        return (
          <Step
            key={a.attempt}
            state={a.outcome === "succeeded" ? "ok" : a.outcome ? "bad" : "wait"}
            title={`Attempt ${a.attempt}: picked up by ${who}`}
            when={started - created}
          >
            {a.outcome === null && (job.type === "demo.freeze" && a.attempt === 1 && now - started > 1500
              ? "Paid… and now it's frozen. It isn't answering."
              : "Working…")}
            {a.outcome === "succeeded" && `Done in ${span(ended! - started)}.`}
            {a.outcome === "failed" && `Failed: “${a.error}”.${job.state === "dead" ? "" : " It will be tried again."}`}
            {a.outcome === "lease_expired" && "Stopped answering, and its time ran out. The job was handed to a fresh attempt."}
            {a.outcome === "cancelled" && "Cancelled."}
          </Step>
        );
      })}
      {job.state === "available" && attempts.length > 0 && (
        <Step state="wait" title="Pausing before the next try" when={null}>
          Retrying in {Math.max(0, Math.ceil((runAt - now) / 1000))} s, so a struggling service gets a moment to recover.
        </Step>
      )}
      {job.state === "succeeded" && (
        <Step state="ok" title={`Finished in ${span(new Date(job.finished_at!).getTime() - created)}`} when={null}>
          {attempts.length === 1 ? "One attempt, no problems." : `${attempts.length} attempts, and the effect happened once.`}
          {waitingForLate && " Now watch for the frozen server to wake up…"}
        </Step>
      )}
      {job.state === "dead" && (
        <Step state="bad" title={`Gave up after ${attempts.length} attempts`} when={null}>
          After every try failed, it was set aside for a person to look at (a &ldquo;dead letter&rdquo;). Nothing is lost: one click retries it.
        </Step>
      )}
      {attempts.filter((a) => a.late_result).map((a) => ( // they arrive last, after the job is done
        <Step key={`late-${a.attempt}`} state="fenced" title={`Attempt ${a.attempt}’s frozen server woke up and reported “${a.late_result === "complete" ? "done" : "failed"}”`} when={new Date(a.late_at!).getTime() - created}>
          Refused: its turn was over, and another attempt had already finished the job. That’s why the work counts once.
        </Step>
      ))}
      {finished && !waitingForLate && <p className="explain"><strong>What just happened:</strong> {SCENARIOS[scenario].after}</p>}
    </div>
  );
}

function Step({ state, title, when, children }: { state: "ok" | "bad" | "wait" | "fenced"; title: string; when: number | null; children: React.ReactNode }) {
  return (
    <div className={`step step-${state}`}>
      <span className="dot" />
      <div>
        <div className="step-title">{title}{when !== null && <span className="muted"> · +{span(when)}</span>}</div>
        <div className="step-body">{children}</div>
      </div>
    </div>
  );
}
