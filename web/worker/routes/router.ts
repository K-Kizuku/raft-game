// /api/* と /ws/* の入り口（T-307・DesignDoc §12.2・プロトコル §2）。
//
// 処理の順序:
//   1. パスの完全一致で振り分ける（知らないパスは 404。Origin の検査より先）
//   2. メソッド（違えば 405 と Allow）
//   3. Origin の検査（違反は 403 forbidden_origin）
//   4. ルートごとの処理
//   2〜4 の例外は 500 internal_error（ログにクエリ文字列を出さない）

import { handleAdminGuards } from "./admin.ts";
import type { Env } from "./env.ts";
import { handleHealthz } from "./health.ts";
import { errorResponse } from "./http.ts";
import { isOriginAllowed, parseAllowedOrigins } from "./origin.ts";
import type { RequestContext, WorkerDeps } from "./ports.ts";
import { handleCreateSession } from "./sessions.ts";
import { handleStages } from "./stages.ts";
import { handleWebSocket } from "./ws.ts";

interface Route {
  readonly methods: readonly string[];
  readonly originRequired: boolean;
  readonly handle: (ctx: RequestContext, deps: WorkerDeps, param: string) => Promise<Response>;
}

const FIXED_ROUTES: ReadonlyMap<string, Route> = new Map<string, Route>([
  [
    "/api/healthz",
    { methods: ["GET"], originRequired: false, handle: async (ctx) => handleHealthz(ctx.env) },
  ],
  [
    "/api/stages",
    {
      methods: ["GET"],
      originRequired: false,
      handle: async (ctx, deps) => handleStages(ctx, deps),
    },
  ],
  ["/api/sessions", { methods: ["POST"], originRequired: true, handle: handleCreateSession }],
  [
    "/api/admin/guards",
    { methods: ["GET", "PATCH"], originRequired: false, handle: handleAdminGuards },
  ],
]);

const WS_SESSION_ROUTE: Route = { methods: ["GET"], originRequired: true, handle: handleWebSocket };
const WS_SESSION_PATH = /^\/ws\/sessions\/([^/]+)$/;

function matchRoute(pathname: string): { route: Route; param: string } | null {
  const fixed = FIXED_ROUTES.get(pathname);
  if (fixed !== undefined) return { route: fixed, param: "" };
  const id = WS_SESSION_PATH.exec(pathname)?.[1];
  if (id !== undefined) return { route: WS_SESSION_ROUTE, param: id };
  return null;
}

function originAllowed(route: Route, request: Request, url: URL, env: Env): boolean {
  const origin = request.headers.get("origin");
  if (origin === null) return !route.originRequired;
  return isOriginAllowed(origin, parseAllowedOrigins(env.ALLOWED_ORIGINS, url));
}

function logUnhandled(pathname: string, error: unknown): void {
  // クエリ文字列（?token=）・例外のメッセージ・本文は出さない。
  const name = error instanceof Error ? error.name : "unknown";
  console.error(
    JSON.stringify({ level: "error", event: "worker.unhandled", path: pathname, error: name }),
  );
}

export function createHandler(
  makeDeps: (env: Env) => WorkerDeps,
): (request: Request, env: Env) => Promise<Response> {
  return async (request, env) => {
    const url = new URL(request.url);
    const matched = matchRoute(url.pathname);
    if (matched === null) return errorResponse(404, "not_found", "Not found");
    const { route, param } = matched;

    try {
      if (!route.methods.includes(request.method)) {
        return errorResponse(405, "method_not_allowed", "Method not allowed", {
          allow: route.methods.join(", "),
        });
      }
      if (!originAllowed(route, request, url, env)) {
        return errorResponse(403, "forbidden_origin", "Origin not allowed");
      }
      const ctx: RequestContext = {
        request,
        url,
        env,
        clientIp: request.headers.get("cf-connecting-ip"),
      };
      return await route.handle(ctx, makeDeps(env), param);
    } catch (error) {
      logUnhandled(url.pathname, error);
      return errorResponse(500, "internal_error", "Internal error");
    }
  };
}
