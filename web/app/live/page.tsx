import type { Metadata } from "next";
import Live from "./live";

export const metadata: Metadata = {
  title: "task-queue · live",
  description: "Work that has to happen, happens exactly once, across two clouds. Try it live, no account needed.",
};

export default function LivePage() {
  return <Live />;
}
