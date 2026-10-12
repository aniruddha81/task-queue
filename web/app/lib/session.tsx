"use client";

import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import { Me, post, usePoll } from "./api";

/** Who is signed in, their account links, and Sign out. Hidden on the login page. */
export default function SessionMenu() {
  const router = useRouter();
  const onLogin = usePathname()?.startsWith("/login");
  const { data: me } = usePoll<Me>(onLogin ? null : "/v1/auth/me");
  if (!me) return null;
  const signOut = () => post("/v1/auth/logout").finally(() => router.push("/login/"));
  return (
    <span className="session">
      <span className="muted">{me.email}</span>
      <Link href="/account/">Account</Link>
      {me.admin && <Link href="/users/">Users</Link>}
      <button onClick={signOut}>Sign out</button>
    </span>
  );
}
