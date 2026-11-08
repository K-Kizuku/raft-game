// DtnSession・ActorHost の Durable Object を fetch で呼ぶ（DTN-12・ADR-0023）。
// スタブは呼ぶたびに作る（同じ名前なら同じ ID）。呼ぶまで idFromName も get も呼ばない（G3）。

import { asDurableObjectNamespace, type DurableObjectNamespaceLike } from "../admin/cf-types.ts";
import type { Env } from "../routes/env.ts";
import type { CallResult } from "./cf-types.ts";

const SESSION_BASE = "https://dtn-session.internal";
const HOST_BASE = "https://actor-host.internal";

export function hostName(sessionId: string, actor: string): string {
  return `${sessionId}/${actor}`;
}

async function call(
  namespace: DurableObjectNamespaceLike,
  name: string,
  url: string,
  method: "GET" | "POST",
  body: unknown,
): Promise<CallResult> {
  const stub = namespace.get(namespace.idFromName(name));
  const init: RequestInit =
    method === "GET"
      ? { method }
      : { method, headers: { "content-type": "application/json" }, body: JSON.stringify(body) };
  const response = await stub.fetch(new Request(url, init));
  let parsed: unknown;
  try {
    parsed = await response.json();
  } catch {
    parsed = undefined;
  }
  return { status: response.status, body: parsed };
}

export type HostCaller = (
  sessionId: string,
  actor: string,
  path: string,
  body: unknown,
) => Promise<CallResult>;
export type SessionCaller = (
  sessionId: string,
  path: string,
  body: unknown,
  method?: "GET" | "POST",
) => Promise<CallResult>;

export function hostCaller(env: Env): HostCaller | undefined {
  const namespace = asDurableObjectNamespace(env["ACTOR_HOST"]);
  if (namespace === undefined) return undefined;
  return (sessionId, actor, path, body) =>
    call(namespace, hostName(sessionId, actor), `${HOST_BASE}${path}`, "POST", body);
}

export function sessionCaller(env: Env): SessionCaller | undefined {
  const namespace = asDurableObjectNamespace(env["DTN_SESSION"]);
  if (namespace === undefined) return undefined;
  return (sessionId, path, body, method = "POST") =>
    call(namespace, sessionId, `${SESSION_BASE}${path}`, method, body);
}

export function unavailable(): Promise<CallResult> {
  return Promise.reject(new Error("binding is not available"));
}
