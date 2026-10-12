"use client";

import { FormEvent, useState } from "react";
import { Me, post, usePoll } from "../lib/api";

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
    </>
  );
}
