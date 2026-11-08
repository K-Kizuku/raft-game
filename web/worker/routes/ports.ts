// Worker が呼ぶ部品の形（T-307）。中身は T-308（session）・T-309（admin）・T-310（security）。
//
// G3（ADR-0018）を構造で守るため、Durable Object に触れる connect・init・reserve は
// VerifiedSessionId しか受け取らない。VerifiedSessionId を作れるのは issueSession と
// verifySessionId の戻り値だけ（ルーターの中で型の主張をしない）。

import type { DtnPorts } from "../dtn/ports.ts";
import type { Env } from "./env.ts";
import type { StageSummary } from "./stages.ts";

export type VerifiedSessionId = string & { readonly __brand: "VerifiedSessionId" };

export interface RequestContext {
  readonly request: Request;
  readonly url: URL;
  readonly env: Env;
  readonly clientIp: string | null; // CF-Connecting-IP。なければ null
}

export interface IssuedSession {
  readonly sessionId: VerifiedSessionId; // 署名付き（T-310）
  readonly token: string; // ブラウザに返す。Worker・DO は保存しない
  readonly tokenHash: string; // DO に渡す（T-308 が検証に使う）
}

export interface SessionInit {
  readonly stageId: string;
  readonly seed: number;
  readonly demo: boolean;
  readonly tokenHash: string;
}

// T-310
export interface SecurityPorts {
  checkRateLimit(ctx: RequestContext): Promise<"ok" | "rate_limited">;
  verifyTurnstile(
    token: string | undefined,
    ctx: RequestContext,
  ): Promise<"ok" | "turnstile_failed">;
  issueSession(ctx: RequestContext): Promise<IssuedSession>;
  verifySessionId(raw: string, ctx: RequestContext): Promise<VerifiedSessionId | null>;
}

// T-309
export interface CostGuardPorts {
  reserve(
    sessionId: VerifiedSessionId,
    ctx: RequestContext,
  ): Promise<"ok" | "budget_exhausted" | "sessions_disabled">;
  cancel(sessionId: VerifiedSessionId, ctx: RequestContext): Promise<void>; // init に失敗したときの取り消し
  handleAdmin(ctx: RequestContext): Promise<Response>; // /api/admin/guards
}

// T-308
export interface SessionPorts {
  init(sessionId: VerifiedSessionId, init: SessionInit, ctx: RequestContext): Promise<void>;
  connect(sessionId: VerifiedSessionId, ctx: RequestContext): Promise<Response>; // DO の fetch の応答をそのまま返す
  // T-714。GET /input-log の応答をそのまま返す。トークンは ctx.request の X-Session-Token から読む。
  // 省略できるのは、inputLog より前に作ったテストの偽物のため。ないときルーターは 404 にする。
  inputLog?(sessionId: VerifiedSessionId, ctx: RequestContext): Promise<Response>;
}

// 部品がない（undefined）ときは、ルーターが必ず拒否する側（503 か 404）に倒す。
// 検証を飛ばす設定はここにも Env にも作らない。
export interface WorkerDeps {
  readonly stages: readonly StageSummary[]; // 全モードのステージ（デモ用を含む）。見せる分は visibleStages で決める
  readonly security?: SecurityPorts;
  readonly costGuard?: CostGuardPorts;
  readonly session?: SessionPorts;
  readonly dtn?: DtnPorts; // DTN-12。DTN の環境の Worker だけ（DTN_SESSION がなければ undefined）
}
