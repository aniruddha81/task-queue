"use client";

import { FormEvent, useState } from "react";
import { ago, del, Me, post, usePoll } from "../lib/api";

type Key = { id: string; name: string; key?: string; created_at: string; last_used_at: string | null };

export default function Account() {
  const { data: me } = usePoll<Me>("/v1/auth/me");
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);
  const [busy, setBusy] = useState(false);

  const submit = (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    const form = e.currentTarget;
    const v = new FormData(form);
    if (v.get("new") !== v.get("confirm")) return setMsg({ ok: false, text: "The new passwords don't match." });
    setBusy(true);
    post("/v1/auth/password", { current: v.get("current"), new: v.get("new") })
      .then(() => (form.reset(), setMsg({ ok: true, text: "Password changed. Use it next time you sign in." })))
      .catch((err) => setMsg({ ok: false, text: err.message }))
      .finally(() => setBusy(false));
  };

  return (
    <>
      <h1>Account</h1>
      {me && (
        <dl>
          <dt>Email</dt><dd>{me.email}</dd>
          <dt>Role</dt><dd>{me.admin ? "admin" : "user"}</dd>
        </dl>
      )}
      <h2>Change password</h2>
      <form className="job" onSubmit={submit} style={{ maxWidth: 360 }}>
        <label><span>Current password</span>
          <input name="current" type="password" autoComplete="current-password" required /></label>
        <label><span>New password (at least 12 characters)</span>
          <input name="new" type="password" autoComplete="new-password" minLength={12} required /></label>
        <label><span>New password again</span>
          <input name="confirm" type="password" autoComplete="new-password" minLength={12} required /></label>
        <button type="submit" disabled={busy}>{busy ? "Changing…" : "Change password"}</button>
        {msg && <p className={msg.ok ? "ok-msg" : "error"}>{msg.text}</p>}
      </form>
      <ApiKeys />
    </>
  );
}

/** API keys: a program exchanges one for a token (POST /v1/auth/token) instead of signing in. */
function ApiKeys() {
  const { data, error, refresh } = usePoll<{ keys: Key[] }>("/v1/auth/keys");
  const [name, setName] = useState("");
  const [created, setCreated] = useState<Key | null>(null);
  const [msg, setMsg] = useState<string | null>(null);
  const [confirming, setConfirming] = useState<string | null>(null);

  const create = (e: FormEvent) => {
    e.preventDefault();
    post<Key>("/v1/auth/keys", { name })
      .then((k) => (setCreated(k), setName(""), setMsg(null), refresh()))
      .catch((err) => setMsg(err.message));
  };
  const revoke = (k: Key) => {
    if (confirming !== k.id) return setConfirming(k.id); // first click arms, second revokes
    setConfirming(null);
    del(`/v1/auth/keys/${k.id}`).then(refresh).catch((err) => setMsg(err.message));
  };

  return (
    <>
      <h2>API keys</h2>
      <p className="muted">
        For programs: exchange a key for a 60-minute token with{" "}
        <code>POST /v1/auth/token</code> with the header <code>Authorization: Bearer tq_…</code>.
        A key acts as you. Revoking one stops new tokens; a token already issued runs out within an hour.
      </p>
      {created?.key && (
        <div className="ok-msg">
          <p>New key <strong>{created.name}</strong>. Copy it now: it is not shown again.</p>
          <p className="filters">
            <code style={{ userSelect: "all", wordBreak: "break-all" }}>{created.key}</code>
            <button onClick={() => navigator.clipboard.writeText(created.key!)}>Copy</button>
            <button onClick={() => setCreated(null)}>Done</button>
          </p>
        </div>
      )}
      {(error || msg) && <p className="error">{error ?? msg}</p>}
      {data && data.keys.length > 0 && (
        <table>
          <thead><tr><th>Name</th><th>Created</th><th>Last used</th><th /></tr></thead>
          <tbody>
            {data.keys.map((k) => (
              <tr key={k.id}>
                <td>{k.name}</td>
                <td className="muted">{ago(k.created_at)}</td>
                <td className="muted">{k.last_used_at ? ago(k.last_used_at) : "never"}</td>
                <td>
                  <button onClick={() => revoke(k)} onBlur={() => setConfirming(null)}>
                    {confirming === k.id ? "Click again to revoke" : "Revoke"}
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      <form className="filters" onSubmit={create} style={{ marginTop: 12 }}>
        <input required maxLength={64} placeholder="key name, e.g. billing service" value={name}
          onChange={(e) => setName(e.target.value)} aria-label="Key name" />
        <button type="submit">Create key</button>
      </form>
    </>
  );
}
