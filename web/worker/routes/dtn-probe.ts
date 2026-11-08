// 管理者だけの試運転の口（DTN-12・docs/dtn/environment.md §7）。実環境で Q-COLD・Q-IMAGE・Q-RPC・Q-SQLITE・Q-PERF を測る。
//
//   POST /api/admin/dtn/probe {stageId, seed?, bot: "greedy", speed?}  → 201 {sessionId, probePath}
//   GET  /api/admin/dtn/probe/{sessionId}                             → 200 計測の結果（DtnSession の GET /probe）
//
// 順序: ADMIN_TOKEN の形（なければ 404）→ 認証（401）→ 部品（なければ 404）→ 本文・sessionId の署名 → DO。
// sessionId は署名を確かめてから DtnSession を呼ぶ（G3）。作るのは createDtnSession（予約を通る）。

import { checkAdminAuth } from "../admin/auth.ts";
import { errorResponse, json, readJsonBody } from "./http.ts";
import type { RequestContext, WorkerDeps } from "./ports.ts";

const MAX_BODY_BYTES = 1024;
const ALLOWED_KEYS: ReadonlySet<string> = new Set(["stageId", "seed", "bot", "speed"]);
const MAX_SPEED = 8;

async function authorize(ctx: RequestContext, deps: WorkerDeps): Promise<Response | null> {
  const auth = await checkAdminAuth(ctx.request, ctx.env);
  if (auth === "disabled") return errorResponse(404, "not_found", "Not found");
  if (auth !== "ok") {
    return errorResponse(401, "unauthorized", "Unauthorized", { "www-authenticate": "Bearer" });
  }
  if (deps.dtn === undefined) return errorResponse(404, "not_found", "Not found");
  return null;
}

function randomSeed(): number {
  const [seed = 0] = crypto.getRandomValues(new Uint32Array(1));
  return seed;
}

export async function handleDtnProbeCreate(
  ctx: RequestContext,
  deps: WorkerDeps,
): Promise<Response> {
  const denied = await authorize(ctx, deps);
  if (denied !== null) return denied;
  const read = await readJsonBody(ctx.request, MAX_BODY_BYTES);
  const body = read.ok ? read.value : null;
  if (typeof body !== "object" || body === null || Array.isArray(body)) {
    return errorResponse(400, "bad_request", "Invalid request body");
  }
  const obj = body as Readonly<Record<string, unknown>>;
  const { stageId, seed, bot, speed } = obj;
  if (
    Object.keys(obj).some((key) => !ALLOWED_KEYS.has(key)) ||
    typeof stageId !== "string" ||
    (seed !== undefined && (typeof seed !== "number" || !Number.isSafeInteger(seed) || seed < 0)) ||
    bot !== "greedy" ||
    (speed !== undefined &&
      (typeof speed !== "number" || !Number.isInteger(speed) || speed < 1 || speed > MAX_SPEED))
  ) {
    return errorResponse(400, "bad_request", "Invalid request body");
  }
  const dtn = deps.dtn;
  if (dtn === undefined) return errorResponse(404, "not_found", "Not found");
  const result = await dtn.create(
    {
      stageId,
      seed: typeof seed === "number" ? seed : randomSeed(),
      demo: ctx.env.DEMO_CONTROLS === "1",
      probe: { bot: "greedy", speed: typeof speed === "number" ? speed : 1 },
    },
    ctx,
  );
  if (!result.ok) {
    switch (result.code) {
      case "bad_request":
        return errorResponse(400, "bad_request", "Invalid request body");
      case "dtn_unsupported_stage":
        return errorResponse(400, "dtn_unsupported_stage", "Stage is not supported in DTN mode");
      case "budget_exhausted":
        return errorResponse(503, "budget_exhausted", "Budget exhausted");
      default:
        return errorResponse(503, "sessions_disabled", "Sessions are not available");
    }
  }
  const sessionId = result.session.sessionId;
  return json(
    {
      sessionId,
      stageId: result.stageId,
      seed: result.seed,
      probePath: `/api/admin/dtn/probe/${sessionId}`,
    },
    201,
  );
}

export async function handleDtnProbeGet(
  ctx: RequestContext,
  deps: WorkerDeps,
  rawSessionId: string,
): Promise<Response> {
  const denied = await authorize(ctx, deps);
  if (denied !== null) return denied;
  const security = deps.security;
  const dtn = deps.dtn;
  if (security === undefined || dtn === undefined)
    return errorResponse(404, "not_found", "Not found");
  const sessionId = await security.verifySessionId(rawSessionId, ctx);
  if (sessionId === null) return errorResponse(404, "not_found", "Not found");
  const result = await dtn.probe(sessionId);
  if (result.status !== 200) return errorResponse(404, "not_found", "Not found");
  return json(result.body, 200);
}
