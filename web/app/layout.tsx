import type { Metadata } from "next";
import Link from "next/link";
import LeaderBadge from "./lib/leader";
import SessionMenu from "./lib/session";
import "./globals.css";

export const metadata: Metadata = {
  title: "task-queue",
  description: "Jobs, queues, workers and schedules",
};

export default function RootLayout({ children }: LayoutProps<"/">) {
  return (
    <html lang="en">
      <body>
        <header>
          <strong>task-queue</strong>
          <nav>
            <Link href="/">Jobs</Link>
            <Link href="/new/">New job</Link>
            <Link href="/dead/">Dead letters</Link>
            <Link href="/queues/">Queues</Link>
            <Link href="/workers/">Workers</Link>
            <Link href="/schedules/">Schedules</Link>
          </nav>
          <LeaderBadge />
          <SessionMenu />
        </header>
        <main>{children}</main>
      </body>
    </html>
  );
}
