"use client";

import { useRouter } from "next/navigation";
import { FormEvent, useState } from "react";

export default function Login() {
  const router = useRouter();
  const [error, setError] = useState<string | null>(null);
  const submit = async (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    const form = new FormData(e.currentTarget);
    const res = await fetch("/v1/auth/login", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ email: form.get("email"), password: form.get("password") }),
    });
    if (res.ok) {
      router.push("/"); // the session cookie is set; the token never touches page scripts
    } else {
      setError((await res.json().catch(() => null))?.error ?? res.statusText);
    }
  };
  return (
    <>
      <h1>Sign in</h1>
      <form className="login" onSubmit={submit}>
        <input name="email" type="email" placeholder="email" autoComplete="username" required aria-label="Email" />
        <input name="password" type="password" placeholder="password" autoComplete="current-password" required aria-label="Password" />
        <button type="submit">Sign in</button>
        {error && <p className="error">{error}</p>}
      </form>
    </>
  );
}
