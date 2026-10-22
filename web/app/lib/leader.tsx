"use client";

import { usePathname } from "next/navigation";
import { isPublic, usePoll } from "./api";

type Leader = { holder: string | null; epoch: number; expires_at: string | null; held: boolean };

/** Which scheduler leads, and its epoch: it changes on every failover. */
export default function LeaderBadge() {
  const { data } = usePoll<Leader>(isPublic(usePathname()) ? null : "/v1/leader");
  if (!data) return null;
  return (
    <span className={`badge ${data.held ? "ok" : "warn"}`} title={data.holder ?? "no holder"}>
      {data.held ? `leader epoch ${data.epoch}` : "no leader"}
    </span>
  );
}
