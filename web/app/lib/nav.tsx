"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { isPublic } from "./api";

/** The dashboard's pages, or on a public page, the way to the other public page. */
export default function Nav() {
  const path = usePathname();
  if (isPublic(path)) {
    return (
      <nav className="public">
        {path.startsWith("/live") ? <Link href="/login/">Sign in</Link> : <Link href="/live/">Live demo</Link>}
      </nav>
    );
  }
  return (
    <nav>
      <Link href="/">Jobs</Link>
      <Link href="/new/">New job</Link>
      <Link href="/dead/">Dead letters</Link>
      <Link href="/queues/">Queues</Link>
      <Link href="/workers/">Workers</Link>
      <Link href="/schedules/">Schedules</Link>
      <Link href="/live/">Live</Link>
    </nav>
  );
}
