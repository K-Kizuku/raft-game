import { describe, expect, it } from "vitest";

import type { Env } from "./env.ts";
import type { CostGuardPorts, SecurityPorts, SessionPorts, WorkerDeps } from "./ports.ts";
import { createHandler } from "./router.ts";
import { type StageSummary, visibleStages } from "./stages.ts";
import { stageFixture } from "./test-stages.ts";

const ORIGIN = "https://raft.example";

// デモ用の 2 件と通常の 2 件を交互に並べた一覧。
const STAGES: readonly StageSummary[] = [
  stageFixture({ id: "demo-goto", demo: true }),
  stageFixture({ id: "stage1" }),
  stageFixture({ id: "demo-izu", demo: true }),
  stageFixture({ id: "stage2", unlock: "stage1" }),
];

// 呼ばれたら必ず失敗する部品（router.test.ts の untouchable() と同じ）。呼ばれた回数も数える。
function untouchable(): {
  calls: string[];
  deps: Required<Pick<WorkerDeps, "security" | "costGuard" | "session">>;
} {
  const calls: string[] = [];
  const fail = (name: string) => async (): Promise<never> => {
    calls.push(name);
    throw new Error("must not be called");
  };
  const security: SecurityPorts = {
    checkRateLimit: fail("checkRateLimit"),
    verifyTurnstile: fail("verifyTurnstile"),
    issueSession: fail("issueSession"),
    verifySessionId: fail("verifySessionId"),
  };
  const costGuard: CostGuardPorts = {
    reserve: fail("reserve"),
    cancel: fail("cancel"),
    handleAdmin: fail("handleAdmin"),
  };
  const session: SessionPorts = { init: fail("init"), connect: fail("connect") };
  return { calls, deps: { security, costGuard, session } };
}

describe("ST-1 visibleStages", () => {
  it.each<[string, Env]>([
    ["DEMO_CONTROLS が未設定", {}],
    ['DEMO_CONTROLS が "0"', { DEMO_CONTROLS: "0" }],
    ['DEMO_CONTROLS が "true"', { DEMO_CONTROLS: "true" }],
    ['DEMO_CONTROLS が " 1"', { DEMO_CONTROLS: " 1" }],
  ])("%s → 通常の 2 件だけ（順序を保つ）", (_name, env) => {
    expect(visibleStages(STAGES, env).map((s) => s.id)).toEqual(["stage1", "stage2"]);
  });

  it('DEMO_CONTROLS が "1" → 4 件すべて', () => {
    expect(visibleStages(STAGES, { DEMO_CONTROLS: "1" })).toEqual(STAGES);
  });
});

describe("ST-2 GET /api/stages", () => {
  it.each<[string, Record<string, string>]>([
    ["Origin なし", {}],
    ["同じオリジン", { origin: ORIGIN }],
  ])("%s: 部品に触れずに 200 を返す", async (_name, headers) => {
    const { calls, deps } = untouchable();
    const res = await createHandler(() => ({ stages: STAGES, ...deps }))(
      new Request(`${ORIGIN}/api/stages`, { headers }),
      {},
    );

    expect(res.status).toBe(200);
    expect(res.headers.get("cache-control")).toBe("no-store");
    expect(await res.json()).toEqual({
      stages: [STAGES[1], STAGES[3]],
      features: { autoFerry: false },
    });
    expect(calls).toEqual([]);
  });

  // T-724: AUTO_FERRY が "1" のときだけ features.autoFerry が true（"1" 以外・未設定は false）。
  it.each<[string, Record<string, string>, boolean]>([
    ["AUTO_FERRY が 1", { AUTO_FERRY: "1" }, true],
    ["AUTO_FERRY が 0", { AUTO_FERRY: "0" }, false],
    ["AUTO_FERRY が true", { AUTO_FERRY: "true" }, false],
    ["AUTO_FERRY が空", { AUTO_FERRY: "" }, false],
    ["AUTO_FERRY がない", {}, false],
  ])("%s: features.autoFerry は %s", async (_name, env, want) => {
    const res = await createHandler(() => ({ stages: STAGES }))(
      new Request(`${ORIGIN}/api/stages`),
      env,
    );

    expect(res.status).toBe(200);
    expect(await res.json()).toMatchObject({ features: { autoFerry: want } });
  });
});
