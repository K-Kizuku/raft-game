import { describe, expect, it } from "vitest";

import { CostGuard } from "../cost-guard.ts";
import worker from "../index.ts";
import type { Env } from "../routes/env.ts";
import type { SecurityPorts, SessionPorts, VerifiedSessionId } from "../routes/ports.ts";
import { createHandler } from "../routes/router.ts";
import type { StageSummary } from "../routes/stages.ts";
import type { DurableObjectNamespaceLike } from "./cf-types.ts";
import { createCostGuardPorts } from "./ports.ts";
import { GUARDS_EXAMPLE, guardsWith } from "./test-fixtures.ts";
import { createTestState, type TestState } from "./test-sqlite.ts";

const ORIGIN = "https://raft.example";
const TOKEN = "0123456789abcdef0123456789abcdef";

const STAGES: readonly StageSummary[] = [
  {
    id: "stage1",
    title: "はじめての郵便船",
    region: "瀬戸内（香川県）",
    islands: 3,
    ferries: 1,
    durationMs: 180000,
    unlock: null,
  },
];

interface Call {
  readonly name: string;
  readonly args: readonly unknown[];
}

async function setup(options: { guards?: unknown; initThrows?: boolean } = {}) {
  const state: TestState = await createTestState();
  const guard = new CostGuard(state, { GUARDS: options.guards ?? GUARDS_EXAMPLE });
  const nsCalls: Call[] = [];
  const namespace: DurableObjectNamespaceLike = {
    idFromName(name) {
      nsCalls.push({ name: "idFromName", args: [name] });
      return { name };
    },
    get(id) {
      nsCalls.push({ name: "get", args: [id] });
      return { fetch: (request) => guard.fetch(request) };
    },
  };

  const calls: Call[] = [];
  let issued = 0;
  const security: SecurityPorts = {
    checkRateLimit: async () => "ok",
    verifyTurnstile: async () => "ok",
    issueSession: async () => {
      issued += 1;
      // テストの偽の部品だけが VerifiedSessionId を作る（本物は T-310）。
      const sessionId = `s_${issued}.sig` as VerifiedSessionId;
      calls.push({ name: "issueSession", args: [sessionId] });
      return { sessionId, token: "tok", tokenHash: "hash" };
    },
    verifySessionId: async () => null,
  };
  const session: SessionPorts = {
    init: async (sessionId) => {
      calls.push({ name: "init", args: [sessionId] });
      if (options.initThrows === true) throw new Error("init failed");
    },
    connect: async () => new Response(null, { status: 500 }),
  };

  const handler = createHandler((env) => ({
    stages: STAGES,
    security,
    costGuard: createCostGuardPorts(env),
    session,
  }));

  const baseEnv: Env = { COST_GUARD: namespace, ADMIN_TOKEN: TOKEN, CONTAINER_INSTANCE: "lite" };
  return {
    state,
    calls,
    nsCalls,
    names: () => calls.map((call) => call.name),
    createSession(env: Env = baseEnv) {
      return handler(
        new Request(`${ORIGIN}/api/sessions`, {
          method: "POST",
          headers: { origin: ORIGIN, "content-type": "application/json" },
          body: '{"stageId":"stage1"}',
        }),
        env,
      );
    },
    admin(
      init: { method?: string; body?: unknown; authorization?: string | null } = {},
      env: Env = baseEnv,
    ) {
      const headers: Record<string, string> = {};
      const authorization =
        init.authorization === undefined ? `Bearer ${TOKEN}` : init.authorization;
      if (authorization !== null) headers["authorization"] = authorization;
      if (init.body !== undefined) headers["content-type"] = "application/json";
      return handler(
        new Request(`${ORIGIN}/api/admin/guards`, {
          method: init.method ?? "GET",
          headers,
          body: init.body === undefined ? null : JSON.stringify(init.body),
        }),
        env,
      );
    },
    ledger() {
      return state.storage.sql
        .exec("SELECT session_id, reserved_micro, status FROM ledger ORDER BY session_id")
        .toArray();
    },
    baseEnv,
  };
}

describe("P-1 createCostGuardPorts", () => {
  it.each([
    ["COST_GUARD がない", {}],
    ["{}", { COST_GUARD: {} }],
    ['"x"', { COST_GUARD: "x" }],
    ["get だけ", { COST_GUARD: { get: () => ({}) } }],
    ["idFromName が関数でない", { COST_GUARD: { idFromName: 1, get: () => ({}) } }],
  ])("%s → undefined", (_name, env) => {
    expect(createCostGuardPorts(env)).toBeUndefined();
  });

  it("偽の名前空間 → 部品が返り、作るだけでは名前空間を呼ばない", async () => {
    const t = await setup();
    const ports = createCostGuardPorts(t.baseEnv);
    expect(ports).toBeDefined();
    expect(t.nsCalls).toEqual([]);
  });
});

describe("P-2 POST /api/sessions の全体", () => {
  it("201 になり、帳簿に行が 1 つ増える（idFromName は global だけ）", async () => {
    const t = await setup();
    const res = await t.createSession();
    expect(res.status).toBe(201);
    expect(t.ledger()).toEqual([
      { session_id: "s_1.sig", reserved_micro: 6500, status: "reserved" },
    ]);
    expect(t.names()).toEqual(["issueSession", "init"]);
    const idCalls = t.nsCalls.filter((call) => call.name === "idFromName");
    expect(idCalls.length).toBeGreaterThan(0);
    expect(idCalls.every((call) => call.args[0] === "global")).toBe(true);
  });

  it("PATCH で acceptGames: false にした後は 503 sessions_disabled で init が呼ばれない", async () => {
    const t = await setup();
    const patched = await t.admin({
      method: "PATCH",
      body: { acceptGames: false, reason: "緊急停止" },
    });
    expect(patched.status).toBe(200);
    const res = await t.createSession();
    expect(res.status).toBe(503);
    expect(await res.json()).toMatchObject({ code: "sessions_disabled" });
    expect(t.names()).not.toContain("init");
    expect(t.ledger()).toEqual([]);
  });

  it("dailyUsd を小さくすると 503 budget_exhausted で init が呼ばれず行も増えない", async () => {
    const t = await setup();
    expect((await t.createSession()).status).toBe(201);
    await t.admin({ method: "PATCH", body: { budget: { dailyUsd: 0.01 }, reason: "下げる" } });
    const res = await t.createSession();
    expect(res.status).toBe(503);
    expect(await res.json()).toMatchObject({ code: "budget_exhausted" });
    expect(t.names()).toEqual(["issueSession", "init", "issueSession"]);
    expect(t.ledger()).toHaveLength(1);
  });

  it("init が例外 → 500 で、帳簿の行が cancelled になり今日の合計が 0 に戻る", async () => {
    const t = await setup({ initThrows: true });
    const res = await t.createSession();
    expect(res.status).toBe(500);
    expect(t.ledger()).toEqual([
      { session_id: "s_1.sig", reserved_micro: 6500, status: "cancelled" },
    ]);
    const view = await (await t.admin()).json();
    expect(view).toMatchObject({ usage: { todayUsd: 0, monthUsd: 0, activeGames: 0 } });
  });
});

describe("P-3 CONTAINER_INSTANCE", () => {
  it.each([
    ["lite", "lite", 6500],
    ["standard-1", "standard-1", 40000],
    ["未設定", undefined, 40000],
    ['"x"', "x", 40000],
  ])("%s → %s の単価で予約する", async (_name, instance, micro) => {
    const t = await setup();
    const env: Env = { COST_GUARD: t.baseEnv["COST_GUARD"], CONTAINER_INSTANCE: instance };
    expect((await t.createSession(env)).status).toBe(201);
    expect(t.ledger()).toEqual([
      { session_id: "s_1.sig", reserved_micro: micro, status: "reserved" },
    ]);
  });
});

describe("P-4 管理用 API と Durable Object", () => {
  it("ADMIN_TOKEN なし → 404、違うトークン → 401 で、名前空間を 1 回も呼ばない", async () => {
    const t = await setup();
    const noToken = await t.admin({}, { COST_GUARD: t.baseEnv["COST_GUARD"] });
    expect(noToken.status).toBe(404);
    const wrong = await t.admin({ authorization: `Bearer ${"z".repeat(32)}` });
    expect(wrong.status).toBe(401);
    expect(wrong.headers.get("www-authenticate")).toBe("Bearer");
    const wrongPatch = await t.admin({
      method: "PATCH",
      body: { acceptGames: false, reason: "x" },
      authorization: null,
    });
    expect(wrongPatch.status).toBe(401);
    expect(t.nsCalls).toEqual([]);
  });

  it("正しいトークンの GET → 200 で usage に予約が出る", async () => {
    const t = await setup();
    await t.createSession();
    const res = await t.admin();
    expect(res.status).toBe(200);
    expect(await res.json()).toMatchObject({
      usage: { todayUsd: 0.0065, monthUsd: 0.0065, activeGames: 1 },
    });
  });

  it("PATCH で signature → 400 locked で、その後の GET でも signature は固定", async () => {
    const t = await setup();
    const res = await t.admin({
      method: "PATCH",
      body: { signature: { enabled: false }, reason: "外したい" },
    });
    expect(res.status).toBe(400);
    expect(await res.json()).toMatchObject({ code: "locked" });
    const view = await (await t.admin()).json();
    expect(view).toMatchObject({
      guards: { signature: { enabled: true, locked: true } },
      recentChanges: [],
    });
  });

  it("GUARDS が不正なら GET は 503 guards_invalid", async () => {
    const t = await setup({ guards: guardsWith({ signature: { enabled: false } }) });
    const res = await t.admin();
    expect(res.status).toBe(503);
    expect(await res.json()).toMatchObject({ code: "guards_invalid" });
  });
});

describe("P-5 既定の配線", () => {
  it("env = {} の Worker は /api/admin/guards が 404", async () => {
    const res = await worker.fetch(
      new Request(`${ORIGIN}/api/admin/guards`, {
        headers: { authorization: `Bearer ${TOKEN}` },
      }),
      { ADMIN_TOKEN: TOKEN },
    );
    expect(res.status).toBe(404);
    expect(await res.json()).toMatchObject({ code: "not_found" });
  });
});
