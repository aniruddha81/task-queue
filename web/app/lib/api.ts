"use client";

import { useEffect, useState } from "react";

// The browser holds the session in an HttpOnly cookie that this code can't read; the
// gateway turns it into a bearer token. Same-origin fetches send it automatically.

export class Unauthorized extends Error {}

/** Pages anyone can see: they never call the signed-in API. */
export const isPublic = (path: string | null): path is string => !!path && (path.startsWith("/login") || path.startsWith("/live"));

async function call<T>(method: string, path: string, body?: unknown, headers: Record<string, string> = {}): Promise<T> {
  const res = await fetch(path, {
    method,
    headers: body === undefined ? headers : { ...headers, "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (res.status === 401) {
    // Outside any component there is no router; a full load is intended. A visitor without an
    // account lands on the live page, which links to sign in.
    // eslint-disable-next-line @next/next/no-location-assign-relative-destination
    window.location.href = "/live/";
    throw new Unauthorized();
  }
  const data = res.status === 204 ? null : await res.json();
  if (!res.ok) throw new Error(data?.error ?? res.statusText);
  return data as T;
}

export const get = <T,>(path: string) => call<T>("GET", path);
export const post = <T,>(path: string, body?: unknown, headers?: Record<string, string>) =>
  call<T>("POST", path, body ?? {}, headers);
export const del = (path: string) => call<null>("DELETE", path);

/** The signed-in user (GET /v1/auth/me). */
export type Me = { id: string; email: string; admin: boolean };

/** usePoll fetches path now and every 2 s, keeping the last good value. */
export function usePoll<T>(path: string | null) {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [tick, setTick] = useState(0);
  useEffect(() => {
    if (!path) return;
    let live = true;
    const load = () =>
      get<T>(path)
        .then((d) => live && (setData(d), setError(null)))
        .catch((e) => live && !(e instanceof Unauthorized) && setError(String(e.message ?? e)));
    load();
    const t = setInterval(load, 2000);
    return () => {
      live = false;
      clearInterval(t);
    };
  }, [path, tick]);
  return { data, error, refresh: () => setTick((n) => n + 1) };
}

export type Job = {
  id: string;
  queue: string;
  type: string;
  payload: unknown;
  priority: number;
  run_at: string;
  state: "available" | "running" | "succeeded" | "dead" | "cancelled";
  cancel_requested: boolean;
  attempt: number;
  max_attempts: number;
  timeout_seconds: number;
  affinity: string | null;
  last_error: string | null;
  created_at: string;
  updated_at: string;
  finished_at: string | null;
};

export type Attempt = {
  attempt: number;
  node_id: string;
  node_name: string | null;
  node_cloud: string | null;
  started_at: string;
  finished_at: string | null;
  outcome: string | null;
  error: string | null;
  late_result: string | null;
  late_at: string | null;
};

export const ago = (iso: string | null) => {
  if (!iso) return "—";
  const s = Math.round((Date.now() - new Date(iso).getTime()) / 1000);
  if (s < -1) return `in ${-s}s`;
  if (s < 60) return `${Math.max(s, 0)}s ago`;
  if (s < 3600) return `${Math.round(s / 60)}m ago`;
  return new Date(iso).toLocaleString();
};
