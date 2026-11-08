// POST /api/sessions（プロトコル §2）。
//
// 部品は checkRateLimit → verifyTurnstile → issueSession → reserve → init の順に呼び、
// どこかで止まったら、それより後の部品を呼ばない。Durable Object を作る init は
// CostGuard の予約（reserve）が "ok" のときだけ呼ぶ（G1）。

import { errorResponse, json, readJsonBody } from "./http.ts";
import type { RequestContext, WorkerDeps } from "./ports.ts";
import { type StageSummary, visibleStages } from "./stages.ts";

const MAX_BODY_BYTES = 4096;
const MAX_TURNSTILE_TOKEN_LENGTH = 2048;
const ALLOWED_KEYS: ReadonlySet<string> = new Set(["stageId", "seed", "demo", "turnstileToken"]);

interface CreateSessionBody {
  readonly stageId: string;
  readonly seed: number | undefined;
  readonly demo: boolean;
  readonly turnstileToken: string | undefined;
}

function badRequest(): Response {
  return errorResponse(400, "bad_request", "Invalid request body");
}

// 本文を型を決めて読む（DesignDoc §16）。不正なら null。
function parseBody(
  value: unknown,
  stages: readonly StageSummary[],
  demoControls: string | undefined,
): CreateSessionBody | null {
  if (typeof value !== "object" || value === null || Array.isArray(value)) return null;
  const body = value as Readonly<Record<string, unknown>>;
  for (const key of Object.keys(body)) {
    if (!ALLOWED_KEYS.has(key)) return null;
  }

  const stageId = body["stageId"];
  if (typeof stageId !== "string" || !stages.some((stage) => stage.id === stageId)) return null;

  let seed: number | undefined;
  if (Object.hasOwn(body, "seed")) {
    const raw = body["seed"];
    if (typeof raw !== "number" || !Number.isSafeInteger(raw) || raw < 0) return null;
    seed = raw;
  }

  let demo = false;
  if (Object.hasOwn(body, "demo")) {
    const raw = body["demo"];
    if (typeof raw !== "boolean") return null;
    if (raw && demoControls !== "1") return null;
    demo = raw;
  }

  let turnstileToken: string | undefined;
  if (Object.hasOwn(body, "turnstileToken")) {
    const raw = body["turnstileToken"];
    if (typeof raw !== "string" || raw.length > MAX_TURNSTILE_TOKEN_LENGTH) return null;
    turnstileToken = raw;
  }

  return { stageId, seed, demo, turnstileToken };
}

// seed を省略したときの値。0 以上 2^32 未満の整数（JSON の数として安全な範囲）。
function randomSeed(): number {
  const [seed = 0] = crypto.getRandomValues(new Uint32Array(1));
  return seed;
}

export async function handleCreateSession(
  ctx: RequestContext,
  deps: WorkerDeps,
): Promise<Response> {
  const { security, costGuard, session } = deps;
  if (security === undefined || costGuard === undefined || session === undefined) {
    return errorResponse(503, "sessions_disabled", "Sessions are not available");
  }

  // 本文を読む前に数える（不正な本文の連打も数えるため）。
  if ((await security.checkRateLimit(ctx)) !== "ok") {
    return errorResponse(429, "rate_limited", "Too many requests");
  }

  const read = await readJsonBody(ctx.request, MAX_BODY_BYTES);
  if (!read.ok) return badRequest();
  // stageId は、この Worker の GET /api/stages が返すもの（一般公開ならデモ用を除く）だけを受け付ける。
  const body = parseBody(read.value, visibleStages(deps.stages, ctx.env), ctx.env.DEMO_CONTROLS);
  if (body === null) return badRequest();

  if ((await security.verifyTurnstile(body.turnstileToken, ctx)) !== "ok") {
    return errorResponse(403, "turnstile_failed", "Turnstile verification failed");
  }

  const issued = await security.issueSession(ctx);

  const reservation = await costGuard.reserve(issued.sessionId, ctx);
  if (reservation === "budget_exhausted") {
    return errorResponse(503, "budget_exhausted", "Budget exhausted");
  }
  if (reservation !== "ok") {
    return errorResponse(503, "sessions_disabled", "Sessions are not available");
  }

  try {
    await session.init(
      issued.sessionId,
      {
        stageId: body.stageId,
        seed: body.seed ?? randomSeed(),
        demo: body.demo,
        tokenHash: issued.tokenHash,
      },
      ctx,
    );
  } catch (error) {
    // 予約を取り消す。取り消しの失敗は握りつぶす（最終的な後始末は T-309 の精算）。
    try {
      await costGuard.cancel(issued.sessionId, ctx);
    } catch {
      // 何もしない
    }
    throw error; // ルーターが 500 internal_error にする
  }

  return json(
    {
      sessionId: issued.sessionId,
      token: issued.token,
      wsPath: `/ws/sessions/${issued.sessionId}`,
    },
    201,
  );
}
