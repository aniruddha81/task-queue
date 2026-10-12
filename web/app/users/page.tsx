"use client";

import { FormEvent, useState } from "react";
import { ago, del, Me, post, usePoll } from "../lib/api";

type User = Me & { created_at: string };

export default function Users() {
  const { data, error, refresh } = usePoll<{ users: User[] }>("/v1/auth/users");
  const { data: me } = usePoll<Me>("/v1/auth/me");
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);
  const [confirming, setConfirming] = useState<string | null>(null);

  const create = (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    const form = e.currentTarget;
    const v = new FormData(form);
    post("/v1/auth/users", { email: v.get("email"), password: v.get("password"), admin: v.get("admin") === "on" })
      .then(() => (form.reset(), setMsg({ ok: true, text: `Created ${v.get("email")}.` }), refresh()))
      .catch((err) => setMsg({ ok: false, text: err.message }));
  };
  const remove = (u: User) => {
    if (confirming !== u.id) return setConfirming(u.id); // first click arms, second deletes
    setConfirming(null);
    del(`/v1/auth/users/${u.id}`)
      .then(() => (setMsg({ ok: true, text: `Deleted ${u.email}. A session already open ends within an hour.` }), refresh()))
      .catch((err) => setMsg({ ok: false, text: err.message }));
  };

  return (
    <>
      <h1>Users</h1>
      <p className="muted">Admins only. Each user sees only their own jobs and schedules.</p>
      {error && <p className="error">{error}</p>}
      {data && (
        <table>
          <thead><tr><th>Email</th><th>Role</th><th>Created</th><th /></tr></thead>
          <tbody>
            {data.users.map((u) => (
              <tr key={u.id}>
                <td>{u.email}</td>
                <td>{u.admin ? "admin" : "user"}</td>
                <td className="muted">{ago(u.created_at)}</td>
                <td>
                  {u.id !== me?.id && (
                    <button onClick={() => remove(u)} onBlur={() => setConfirming(null)}>
                      {confirming === u.id ? "Click again to delete" : "Delete"}
                    </button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {data && (
        <>
          <h2>Add a user</h2>
          <form className="job" onSubmit={create} style={{ maxWidth: 360 }}>
            <label><span>Email</span><input name="email" type="email" autoComplete="off" required /></label>
            <label><span>Password (at least 12 characters)</span>
              <input name="password" type="password" autoComplete="new-password" minLength={12} required /></label>
            <label style={{ display: "flex", gap: 8, alignItems: "center" }}>
              <input name="admin" type="checkbox" /> <span>Admin (can manage users)</span></label>
            <button type="submit">Add user</button>
          </form>
        </>
      )}
      {msg && <p className={msg.ok ? "ok-msg" : "error"}>{msg.text}</p>}
    </>
  );
}
