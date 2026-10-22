import type { Metadata } from "next";
import { Suspense } from "react";
import Reminder from "./reminder";

export const metadata: Metadata = {
  title: "task-queue · your reminder",
  description: "Confirm a reminder booked on the live page, and watch it get sent.",
};

export default function ReminderPage() {
  return (
    <Suspense fallback={<p className="muted">Loading…</p>}>
      <Reminder />
    </Suspense>
  );
}
