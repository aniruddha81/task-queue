import type { Metadata } from "next";
import LeaderBadge from "./lib/leader";
import Nav from "./lib/nav";
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
          <Nav />
          <LeaderBadge />
          <SessionMenu />
        </header>
        <main>{children}</main>
      </body>
    </html>
  );
}
