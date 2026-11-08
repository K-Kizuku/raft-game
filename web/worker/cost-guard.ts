// Durable Object CostGuard（T-309・ADR-0018・ADR-0023・DesignDoc §12.3.1）。
//
// 全体で 1 つ（idFromName("global")）。fetch のハンドラーで内部の API を受ける
// （Workers のランタイムのモジュールの import・RPC を使わない。ADR-0023）。中身は admin/ledger.ts の
// CostGuardCore。アラームを使わない（alarm() を持たず、アラームを設定しない）。ログを出さない。
//
//   POST /reserve {sessionId, instance}    → 200 {result}
//   POST /reserve {sessionId, units}       → 200 {result}（内訳つき。DTN-12・docs/dtn/environment.md §3）
//   POST /cancel  {sessionId}              → 200 {}
//   POST /settle  {sessionId, runSeconds}  → 200 {}
//   GET  /view                             → 200 GuardsView | 503 guards_invalid
//   POST /patch   {change, reason, by}     → 200 GuardsView | 400 bad_request・locked | 503 guards_invalid

import type { DurableObjectStateLike } from "./admin/cf-types.ts";
import { SESSION_ID_PATTERN } from "./admin/client.ts";
import { isPlainObject, parseGuardsConfig, type ContainerInstance } from "./admin/guards.ts";
import {
  CostGuardCore,
  ensureSchema,
  isValidRunSeconds,
  parseReserveUnits,
} from "./admin/ledger.ts";
import type { Env } from "./routes/env.ts";
import { errorResponse, json, readJsonBody } from "./routes/http.ts";

const MAX_BODY_BYTES = 16 * 1024;

const ROUTES: ReadonlyMap<string, "GET" | "POST"> = new Map([
  ["/reserve", "POST"],
  ["/cancel", "POST"],
  ["/settle", "POST"],
  ["/view", "GET"],
  ["/patch", "POST"],
]);

type Body = Readonly<Record<string, unknown>>;

function badRequest(): Response {
  return errorResponse(400, "bad_request", "Invalid request body");
}

function isSessionId(value: unknown): value is string {
  return typeof value === "string" && SESSION_ID_PATTERN.test(value);
}

function isContainerInstance(value: unknown): value is ContainerInstance {
  return value === "lite" || value === "standard-1";
}

export class CostGuard {
  readonly #core: CostGuardCore;

  constructor(ctx: DurableObjectStateLike, env: Env) {
    const { storage } = ctx;
    ensureSchema(storage.sql);
    this.#core = new CostGuardCore(
      storage.sql,
      (fn) => storage.transactionSync(fn),
      parseGuardsConfig(env["GUARDS"]),
      () => Date.now(),
    );
  }

  async fetch(request: Request): Promise<Response> {
    const { pathname } = new URL(request.url);
    const method = ROUTES.get(pathname);
    if (method === undefined) return errorResponse(404, "not_found", "Not found");
    if (request.method !== method) {
      return errorResponse(405, "method_not_allowed", "Method not allowed", { allow: method });
    }
    if (pathname === "/view") return this.view();

    const read = await readJsonBody(request, MAX_BODY_BYTES);
    if (!read.ok || !isPlainObject(read.value)) return badRequest();
    const body = read.value;
    switch (pathname) {
      case "/reserve":
        return this.reserve(body);
      case "/cancel":
        return this.cancel(body);
      case "/settle":
        return this.settle(body);
      default:
        return this.patch(body);
    }
  }

  // instance と units のどちらか一方だけを受ける。
  private reserve(body: Body): Response {
    const sessionId = body["sessionId"];
    if (!isSessionId(sessionId)) return badRequest();
    const hasUnits = Object.hasOwn(body, "units");
    if (hasUnits === Object.hasOwn(body, "instance")) return badRequest();
    if (hasUnits) {
      const units = parseReserveUnits(body["units"]);
      if (units === null) return badRequest();
      return json({ result: this.#core.reserveUnits(sessionId, units) }, 200);
    }
    const instance = body["instance"];
    if (!isContainerInstance(instance)) return badRequest();
    return json({ result: this.#core.reserve(sessionId, instance) }, 200);
  }

  private cancel(body: Body): Response {
    const sessionId = body["sessionId"];
    if (!isSessionId(sessionId)) return badRequest();
    this.#core.cancel(sessionId);
    return json({}, 200);
  }

  private settle(body: Body): Response {
    const sessionId = body["sessionId"];
    const runSeconds = body["runSeconds"];
    if (!isSessionId(sessionId) || !isValidRunSeconds(runSeconds)) return badRequest();
    this.#core.settle(sessionId, runSeconds);
    return json({}, 200);
  }

  private view(): Response {
    const view = this.#core.view();
    if (view === "guards_invalid") {
      return errorResponse(503, "guards_invalid", "Guards configuration is invalid");
    }
    return json(view, 200);
  }

  private patch(body: Body): Response {
    const result = this.#core.patch(body["change"], body["reason"], body["by"]);
    if (result.ok) return json(result.view, 200);
    if (result.code === "guards_invalid") {
      return errorResponse(503, "guards_invalid", "Guards configuration is invalid");
    }
    return result.code === "locked"
      ? errorResponse(400, "locked", "Guard is locked")
      : badRequest();
  }
}
