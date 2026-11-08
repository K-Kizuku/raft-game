// DTN のセッションを作る共通の処理（DTN-12・docs/dtn/environment.md §4 の 1）。
// DTN-13 の POST /api/sessions と、管理者の試運転の口（POST /api/admin/dtn/probe）が使う。
//
// 順序: ステージの確認（対象外のイベント → dtn_unsupported_stage。予約もしない）→ sessionId の発行（署名）→
// 予約（Container = 島＋船＋1、DO = 島＋船＋1）→ DtnSession の /init → 失敗したら予約を取り消す。
// 予約が通らなければ、Container も Durable Object も 1 つも作らない（G1）。

import type { ReserveResult, ReserveUnit } from "../admin/client.ts";
import type { ContainerInstance } from "../admin/guards.ts";
import type { IssuedSession, RequestContext, VerifiedSessionId } from "../routes/ports.ts";
import type { GameTimeLimits } from "../session/config.ts";
import { actorUnits, type DtnStageInfo } from "./catalog.ts";

export interface DtnProbeOptions {
  readonly bot: "greedy";
  readonly speed: number;
}

export interface CreateDtnInput {
  readonly stageId: string;
  readonly seed: number;
  readonly demo: boolean;
  readonly probe?: DtnProbeOptions;
}

export interface DtnInitBody {
  readonly sessionId: VerifiedSessionId;
  readonly stageId: string;
  readonly seed: number;
  readonly demo: boolean;
  readonly maxMinutes: number;
  readonly maxAlarms: number;
  readonly probe?: DtnProbeOptions;
}

export interface CreateDtnDeps {
  readonly catalog: ReadonlyMap<string, DtnStageInfo>;
  readonly instance: ContainerInstance;
  issueSession(ctx: RequestContext): Promise<IssuedSession>;
  reserveUnits(sessionId: VerifiedSessionId, units: readonly ReserveUnit[]): Promise<ReserveResult>;
  cancel(sessionId: VerifiedSessionId): Promise<void>;
  limits(): Promise<GameTimeLimits>;
  // DtnSession の POST /init。201・200 なら解決し、それ以外は Error を投げる
  init(body: DtnInitBody): Promise<void>;
}

export type CreateDtnResult =
  | {
      readonly ok: true;
      readonly session: IssuedSession;
      readonly stageId: string;
      readonly seed: number;
    }
  | {
      readonly ok: false;
      readonly code:
        "bad_request" | "dtn_unsupported_stage" | "budget_exhausted" | "sessions_disabled";
    };

export function reserveUnitsFor(info: DtnStageInfo, instance: ContainerInstance): ReserveUnit[] {
  const count = actorUnits(info);
  return [
    { kind: "container", instance, count },
    { kind: "do", count },
  ];
}

export async function createDtnSession(
  input: CreateDtnInput,
  ctx: RequestContext,
  deps: CreateDtnDeps,
): Promise<CreateDtnResult> {
  const info = deps.catalog.get(input.stageId);
  if (info === undefined) return { ok: false, code: "bad_request" };
  if (info.unsupportedEvents.length > 0) return { ok: false, code: "dtn_unsupported_stage" };

  const session = await deps.issueSession(ctx);
  const reservation = await deps.reserveUnits(
    session.sessionId,
    reserveUnitsFor(info, deps.instance),
  );
  if (reservation !== "ok") return { ok: false, code: reservation };

  try {
    const limits = await deps.limits();
    await deps.init({
      sessionId: session.sessionId,
      stageId: input.stageId,
      seed: input.seed,
      demo: input.demo,
      maxMinutes: limits.maxMinutes,
      maxAlarms: limits.maxAlarms,
      ...(input.probe === undefined ? {} : { probe: input.probe }),
    });
  } catch (error) {
    // 予約を取り消す。取り消しの失敗は握りつぶす（帳簿は予約のまま＝最悪の費用）
    try {
      await deps.cancel(session.sessionId);
    } catch {
      // 何もしない
    }
    throw error;
  }
  return { ok: true, session, stageId: input.stageId, seed: input.seed };
}
