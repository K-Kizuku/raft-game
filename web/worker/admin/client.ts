// Worker・GameSession から CostGuard（Durable Object）を呼ぶクライアント（T-309・ADR-0023）。
//
// CostGuard は全体で 1 つ（idFromName("global")）。スタブは呼ぶたびに作る（同じ名前なら同じ ID）。
// 呼ぶまで idFromName も get も呼ばない（認証の前に Durable Object に触れないため）。
// 想定外の応答（200・400・503 以外、本文の形の違い）では Error を投げる。

import type { Env } from "../routes/env.ts";
import type { VerifiedSessionId } from "../routes/ports.ts";
import { asDurableObjectNamespace, type DurableObjectNamespaceLike } from "./cf-types.ts";
import {
  isPlainObject,
  parseGuardsView,
  type ContainerInstance,
  type FlatGuardsChange,
  type GuardsView,
} from "./guards.ts";
import { isValidRunSeconds, type ReserveResult } from "./ledger.ts";

export type { ContainerInstance } from "./guards.ts";
export type { ReserveResult } from "./ledger.ts";

export type PatchResult =
  | { readonly ok: true; readonly view: GuardsView }
  | {
      readonly ok: false;
      readonly status: 400 | 503;
      readonly code: "bad_request" | "locked" | "guards_invalid";
    };

export interface CostGuardClient {
  reserve(sessionId: VerifiedSessionId, instance: ContainerInstance): Promise<ReserveResult>;
  cancel(sessionId: VerifiedSessionId): Promise<void>;
  settle(sessionId: string, runSeconds: number): Promise<void>; // T-308 がラウンドの終了で呼ぶ
  view(): Promise<GuardsView | "guards_invalid">; // T-308・T-310 は guards.* を読む
  patch(change: FlatGuardsChange, reason: string, by: string): Promise<PatchResult>;
}

export const COST_GUARD_NAME = "global";
const BASE_URL = "https://cost-guard.internal";
export const SESSION_ID_PATTERN = /^[A-Za-z0-9._-]{1,256}$/;

const RESERVE_RESULTS: ReadonlySet<string> = new Set([
  "ok",
  "budget_exhausted",
  "sessions_disabled",
]);

function isReserveResult(value: unknown): value is ReserveResult {
  return typeof value === "string" && RESERVE_RESULTS.has(value);
}

interface CallResult {
  readonly status: number;
  readonly body: unknown;
}

function unexpected(op: string): Error {
  return new Error(`CostGuard: unexpected response from ${op}`);
}

function errorCode(body: unknown): unknown {
  return isPlainObject(body) ? body["code"] : undefined;
}

export function createCostGuardClient(namespace: DurableObjectNamespaceLike): CostGuardClient {
  const call = async (
    method: "GET" | "POST",
    op: string,
    payload?: unknown,
  ): Promise<CallResult> => {
    const stub = namespace.get(namespace.idFromName(COST_GUARD_NAME));
    const init: RequestInit =
      payload === undefined
        ? { method }
        : {
            method,
            headers: { "content-type": "application/json" },
            body: JSON.stringify(payload),
          };
    const response = await stub.fetch(new Request(`${BASE_URL}/${op}`, init));
    let body: unknown;
    try {
      body = await response.json();
    } catch {
      throw unexpected(op);
    }
    return { status: response.status, body };
  };

  const expectEmpty = async (op: string, payload: unknown): Promise<void> => {
    const { status, body } = await call("POST", op, payload);
    if (status !== 200 || !isPlainObject(body)) throw unexpected(op);
  };

  return {
    async reserve(sessionId, instance) {
      const { status, body } = await call("POST", "reserve", { sessionId, instance });
      const result = isPlainObject(body) ? body["result"] : undefined;
      if (status !== 200 || !isReserveResult(result)) throw unexpected("reserve");
      return result;
    },

    async cancel(sessionId) {
      await expectEmpty("cancel", { sessionId });
    },

    async settle(sessionId, runSeconds) {
      if (!SESSION_ID_PATTERN.test(sessionId)) throw new RangeError("sessionId is invalid");
      if (!isValidRunSeconds(runSeconds)) throw new RangeError("runSeconds is out of range");
      await expectEmpty("settle", { sessionId, runSeconds });
    },

    async view() {
      const { status, body } = await call("GET", "view");
      if (status === 503 && errorCode(body) === "guards_invalid") return "guards_invalid";
      const view = status === 200 ? parseGuardsView(body) : null;
      if (view === null) throw unexpected("view");
      return view;
    },

    async patch(change, reason, by) {
      const { status, body } = await call("POST", "patch", { change, reason, by });
      if (status === 200) {
        const view = parseGuardsView(body);
        if (view === null) throw unexpected("patch");
        return { ok: true, view };
      }
      const code = errorCode(body);
      if (status === 400 && (code === "bad_request" || code === "locked")) {
        return { ok: false, status: 400, code };
      }
      if (status === 503 && code === "guards_invalid") {
        return { ok: false, status: 503, code };
      }
      throw unexpected("patch");
    },
  };
}

// COST_GUARD がない・形が違うなら undefined。
export function getCostGuardClient(env: Env): CostGuardClient | undefined {
  const namespace = asDurableObjectNamespace(env["COST_GUARD"]);
  return namespace === undefined ? undefined : createCostGuardClient(namespace);
}
