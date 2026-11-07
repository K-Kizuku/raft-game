// Worker の入口の経路（純粋。名前空間は構造の型）。
//
// 費用の上限（Issue #134「費用と本番との分離」）: /spike/healthz 以外は、
//   1. 経路が分かる  2. SPIKE_TOKEN が使える値  3. 番号が範囲内  4. メソッドが合う  5. トークンが合う  6. 引数が範囲内
// をすべて確かめた後でだけ、Durable Object の idFromName・get を呼ぶ。
import { bearerToken, isTokenConfigured, timingSafeEqual } from "./auth.ts";
import { burn } from "./cpu.ts";
import { COLO_HEADER, errorResponse, json, messageOf } from "./http.ts";
import {
  CPU_PARAMS,
  MAX_STORES,
  NO_PARAMS,
  SESSION_ACTIONS,
  isSessionAction,
  parseIndex,
  parseMaxSessions,
  parseParams,
  type ParamSpecs,
  type ParamValues,
} from "./limits.ts";
import type { DurableObjectNamespaceLike, Env } from "./types.ts";

type Route =
  | { kind: "cpu"; method: "POST"; params: ParamSpecs }
  | { kind: "session"; method: "GET" | "POST"; params: ParamSpecs; rawIndex: string; action: string; ws: boolean }
  | { kind: "store"; method: "POST"; params: ParamSpecs; rawIndex: string; store: "plain" | "control" };

function matchRoute(pathname: string): Route | null {
  if (pathname === "/spike/cpu") return { kind: "cpu", method: "POST", params: CPU_PARAMS };
  const s = /^\/spike\/s\/([^/]+)\/(.+)$/.exec(pathname);
  if (s !== null) {
    const action = s[2] ?? "";
    if (!isSessionAction(action)) return null;
    const spec = SESSION_ACTIONS[action];
    return {
      kind: "session",
      method: spec.method,
      params: spec.params,
      rawIndex: s[1] ?? "",
      action,
      ws: action === "ws",
    };
  }
  const p = /^\/spike\/(plain|control)\/([^/]+)$/.exec(pathname);
  if (p !== null) {
    return {
      kind: "store",
      method: "POST",
      params: NO_PARAMS,
      rawIndex: p[2] ?? "",
      store: p[1] === "plain" ? "plain" : "control",
    };
  }
  return null;
}

function notFound(): Response {
  return errorResponse(404, "not_found", "not found");
}

function toQuery(values: ParamValues): string {
  const q = new URLSearchParams();
  for (const [k, v] of Object.entries(values)) q.set(k, String(v));
  const s = q.toString();
  return s === "" ? "" : `?${s}`;
}

async function forward(
  ns: DurableObjectNamespaceLike,
  name: string,
  path: string,
  values: ParamValues,
  request: Request,
  ws: boolean,
): Promise<Response> {
  const headers = new Headers();
  const colo = (request as { cf?: { colo?: unknown } }).cf?.colo;
  if (typeof colo === "string") headers.set(COLO_HEADER, colo);
  if (ws) headers.set("upgrade", "websocket");
  const stub = ns.get(ns.idFromName(name));
  const doRequest = new Request(`https://spike.internal/${path}${toQuery(values)}`, {
    method: request.method,
    headers,
  });
  try {
    return await stub.fetch(doRequest);
  } catch (e) {
    return errorResponse(502, "do_error", messageOf(e));
  }
}

/** Worker の fetch の本体 */
export async function handleRequest(request: Request, env: Env): Promise<Response> {
  const url = new URL(request.url);
  if (url.pathname === "/spike/healthz") {
    if (request.method !== "GET") return errorResponse(405, "method_not_allowed", "use GET");
    return json(200, { ok: true });
  }

  const route = matchRoute(url.pathname);
  if (route === null) return notFound();

  // トークンが設定されていない・短い・空白を含む → 拒否する側に倒す（Durable Object を呼ばない）
  const token = env.SPIKE_TOKEN;
  if (!isTokenConfigured(token)) return notFound();

  let index: number | null = null;
  if (route.kind === "session") {
    const max = parseMaxSessions(env.SPIKE_MAX_SESSIONS);
    if (max === null) return notFound();
    index = parseIndex(route.rawIndex, max);
    if (index === null) return notFound();
  } else if (route.kind === "store") {
    index = parseIndex(route.rawIndex, MAX_STORES);
    if (index === null) return notFound();
  }

  if (request.method !== route.method) {
    return errorResponse(405, "method_not_allowed", `use ${route.method}`);
  }

  const isWs = route.kind === "session" && route.ws;
  const presented = isWs ? url.searchParams.get("token") : bearerToken(request.headers.get("authorization"));
  const queryTokens = url.searchParams.getAll("token");
  // WS の Upgrade 以外では ?token= を認めない。WS でも ?token= は 1 つだけ
  const tokenShapeOk = isWs ? queryTokens.length === 1 : queryTokens.length === 0;
  if (!tokenShapeOk || presented === null || !timingSafeEqual(presented, token)) {
    return errorResponse(401, "unauthorized", "missing or invalid token");
  }

  const parsed = parseParams(url.searchParams, route.params, isWs ? ["token"] : []);
  if (!parsed.ok) return errorResponse(400, "bad_param", parsed.message);

  switch (route.kind) {
    case "cpu":
      return json(200, { where: "worker", ...burn(Number(parsed.values["iters"])) });
    case "session":
      if (route.ws && request.headers.get("upgrade")?.toLowerCase() !== "websocket") {
        return errorResponse(426, "upgrade_required", "use a WebSocket upgrade");
      }
      return forward(env.SESSIONS, `s-${index}`, route.action, parsed.values, request, route.ws);
    case "store":
      return forward(
        route.store === "plain" ? env.PLAIN : env.CONTROL,
        `p-${index}`,
        route.store,
        parsed.values,
        request,
        false,
      );
  }
}
