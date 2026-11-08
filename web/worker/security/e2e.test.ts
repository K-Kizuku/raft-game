// 全体のテスト（T-310）。ルーター（T-307）に、本物の createSecurityPortsWith・本物の
// createCostGuardPorts（":memory:" の DB の CostGuard）・呼び出しを記録する偽の session を渡す。
// G3（署名を確かめる前に Durable Object を呼ばない）を中心に確かめる。

import { afterEach, beforeEach, describe, expect, it, vi, type MockInstance } from "vitest";

import type { DurableObjectNamespaceLike } from "../admin/cf-types.ts";
import { createCostGuardPorts } from "../admin/ports.ts";
import { GUARDS_EXAMPLE, guardsWith } from "../admin/test-fixtures.ts";
import { createTestState, type TestState } from "../admin/test-sqlite.ts";
import { CostGuard } from "../cost-guard.ts";
import worker from "../index.ts";
import type { Env } from "../routes/env.ts";
import type { SessionPorts } from "../routes/ports.ts";
import { createHandler } from "../routes/router.ts";
import type { StageSummary } from "../routes/stages.ts";
import { stageFixture } from "../routes/test-stages.ts";
import { createGuardSettingsSource } from "./guard-settings.ts";
import { createSecurityPortsWith } from "./ports.ts";
import { createSessionSigner } from "./session-id.ts";
import {
  flipChar,
  KEY,
  NOW,
  sequentialRandom,
  TURNSTILE_DUMMY_TOKEN,
  TURNSTILE_PASS_SECRET,
} from "./test-helpers.ts";
import { TURNSTILE_SITEVERIFY_URL } from "./turnstile.ts";

const ORIGIN = "https://raft.example";
const ADMIN_TOKEN = "admin-token-0123456789abcdef-012345"; // 作り物の値
const IP = "203.0.113.5";

const STAGES: readonly StageSummary[] = [
  stageFixture({
    id: "stage1",
    title: "はじめての郵便船",
    region: "瀬戸内（香川県）",
    islands: 3,
    ferries: 1,
    durationMs: 180000,
    unlock: null,
  }),
];

interface Recorded {
  readonly name: string;
  readonly args: readonly unknown[];
}

const CONSOLE_METHODS = ["log", "info", "warn", "error", "debug", "trace"] as const;
let consoleSpies: MockInstance[] = [];

beforeEach(() => {
  consoleSpies = CONSOLE_METHODS.map((method) => vi.spyOn(console, method));
});

afterEach(() => {
  for (const spy of consoleSpies) expect(spy).not.toHaveBeenCalled();
  vi.restoreAllMocks();
});

async function setup(options: { readonly guards?: unknown } = {}) {
  const state: TestState = await createTestState();
  const guard = new CostGuard(state, { GUARDS: options.guards ?? GUARDS_EXAMPLE });

  const costGuardCalls: string[] = [];
  const costGuardNamespace: DurableObjectNamespaceLike = {
    idFromName: (name) => ({ name }),
    get: () => ({
      fetch: (request) => {
        costGuardCalls.push(new URL(request.url).pathname);
        return guard.fetch(request);
      },
    }),
  };

  const gameSessionCalls: Recorded[] = [];
  const recordGameSession =
    (name: string) =>
    (...args: unknown[]) => {
      gameSessionCalls.push({ name, args });
      return { fetch: async () => new Response("{}") };
    };
  const gameSessionNamespace = {
    idFromName: recordGameSession("idFromName"),
    get: recordGameSession("get"),
    newUniqueId: recordGameSession("newUniqueId"),
    idFromString: recordGameSession("idFromString"),
  };

  const limiterCounts = new Map<string, number>();
  const limiterCalls: unknown[] = [];
  const rateLimiter = {
    async limit(options: { key: string }) {
      limiterCalls.push(options);
      const count = (limiterCounts.get(options.key) ?? 0) + 1;
      limiterCounts.set(options.key, count);
      return { success: count <= 10 };
    },
  };

  let siteverifyResponse = '{"success":true,"hostname":"example.com","error-codes":[]}';
  const siteverifyCalls: Request[] = [];
  const fetch = async (request: Request): Promise<Response> => {
    if (request.url !== TURNSTILE_SITEVERIFY_URL) throw new Error("unexpected fetch");
    siteverifyCalls.push(request);
    return new Response(siteverifyResponse);
  };

  const sessionCalls: Recorded[] = [];
  const connected = new Response("connected", { status: 200 });
  const session: SessionPorts = {
    init: async (sessionId, init) => {
      sessionCalls.push({ name: "init", args: [sessionId, init] });
    },
    connect: async (sessionId) => {
      sessionCalls.push({ name: "connect", args: [sessionId] });
      return connected;
    },
  };

  const clock = { now: NOW };
  const random = sequentialRandom();
  let guardsSource = createGuardSettingsSource({ ttlMs: 5000, now: () => clock.now });

  const handler = createHandler((env) => ({
    stages: STAGES,
    security: createSecurityPortsWith(env, {
      now: () => clock.now,
      randomBytes: random.randomBytes,
      fetch,
      guards: guardsSource,
    }),
    costGuard: createCostGuardPorts(env),
    session,
  }));

  const env: Env = {
    SESSION_SIGNING_KEY: KEY,
    TURNSTILE_SECRET: TURNSTILE_PASS_SECRET,
    SESSION_RATE_LIMIT: rateLimiter,
    COST_GUARD: costGuardNamespace,
    ADMIN_TOKEN,
    ALLOWED_ORIGINS: ORIGIN,
    GAME_SESSION: gameSessionNamespace,
  };

  const sessionRequest = (
    body: unknown = { stageId: "stage1", turnstileToken: TURNSTILE_DUMMY_TOKEN },
  ) =>
    new Request(`${ORIGIN}/api/sessions`, {
      method: "POST",
      headers: { origin: ORIGIN, "content-type": "application/json", "cf-connecting-ip": IP },
      body: JSON.stringify(body),
    });

  return {
    clock,
    env,
    costGuardCalls,
    gameSessionCalls,
    limiterCalls,
    siteverifyCalls,
    sessionCalls,
    connected,
    names: () => sessionCalls.map((call) => call.name),
    setSiteverify(body: string) {
      siteverifyResponse = body;
    },
    renewGuardsSource() {
      guardsSource = createGuardSettingsSource({ ttlMs: 5000, now: () => clock.now });
    },
    sessionRequest,
    createSession(body?: unknown, e: Env = env) {
      return handler(sessionRequest(body), e);
    },
    send(request: Request, e: Env = env) {
      return handler(request, e);
    },
    connect(id: string, e: Env = env) {
      return handler(
        new Request(`${ORIGIN}/ws/sessions/${id}`, {
          headers: { origin: ORIGIN, upgrade: "websocket" },
        }),
        e,
      );
    },
    patch(body: unknown) {
      return handler(
        new Request(`${ORIGIN}/api/admin/guards`, {
          method: "PATCH",
          headers: { authorization: `Bearer ${ADMIN_TOKEN}`, "content-type": "application/json" },
          body: JSON.stringify(body),
        }),
        env,
      );
    },
    ledger() {
      return state.storage.sql.exec("SELECT session_id, status FROM ledger").toArray();
    },
  };
}

type Setup = Awaited<ReturnType<typeof setup>>;

async function createOk(t: Setup, body?: unknown): Promise<string> {
  const res = await t.createSession(body);
  expect(res.status).toBe(201);
  const json: unknown = await res.json();
  if (typeof json !== "object" || json === null || !("sessionId" in json)) throw new Error("no id");
  const { sessionId } = json;
  if (typeof sessionId !== "string") throw new Error("no id");
  return sessionId;
}

function expiresOf(id: string): number {
  const text = id.slice(2, 30).replace(/-/g, "+").replace(/_/g, "/");
  const bytes = Uint8Array.from(atob(text), (c) => c.charCodeAt(0));
  return new DataView(bytes.buffer).getUint32(17, false);
}

async function expectNotFound(res: Response): Promise<void> {
  expect(res.status).toBe(404);
  expect(await res.json()).toMatchObject({ code: "not_found" });
}

describe("E-1 正常", () => {
  it("201 の sessionId で接続すると connect が同じ値で 1 回呼ばれる", async () => {
    const t = await setup();
    const res = await t.createSession();
    expect(res.status).toBe(201);
    const body: unknown = await res.json();
    expect(body).toMatchObject({
      sessionId: expect.stringMatching(/^s_[A-Za-z0-9_-]{28}\.[A-Za-z0-9_-]{43}$/),
    });
    const id = (body as { sessionId: string }).sessionId;
    expect(id).toHaveLength(74);
    expect(t.costGuardCalls.filter((path) => path === "/view")).toHaveLength(1);
    expect(t.siteverifyCalls).toHaveLength(1);
    expect(t.limiterCalls).toEqual([{ key: `ip4:${IP}` }]);
    expect(t.names()).toEqual(["init"]);

    const ws = await t.connect(id);
    expect(ws).toBe(t.connected);
    expect(t.sessionCalls.filter((call) => call.name === "connect")).toEqual([
      { name: "connect", args: [id] },
    ]);
  });
});

async function forgedIds(t: Setup): Promise<readonly (readonly [string, string])[]> {
  const id = await createOk(t);
  const otherKey = createSessionSigner(`${KEY}x`, {
    now: () => NOW,
    randomBytes: sequentialRandom().randomBytes,
  });
  const demo = createSessionSigner(KEY, {
    now: () => NOW,
    randomBytes: sequentialRandom().randomBytes,
  });
  return [
    ["適当な 74 文字", `s_${"A".repeat(28)}.${"A".repeat(43)}`],
    ["署名を 1 文字変えたもの", flipChar(id, 40)],
    ["payload を 1 文字変えたもの", flipChar(id, 10)],
    ["別の鍵で作った id", await otherKey.issue("raft.example", 30)],
    ["raft-demo.example で作った id", await demo.issue("raft-demo.example", 30)],
    ["s_abc.def", "s_abc.def"],
  ];
}

// WS の要求の間に、connect・GAME_SESSION・COST_GUARD・siteverify のどれも呼ばれないこと。
async function expectRejectedWithoutDurableObject(t: Setup, id: string, env?: Env): Promise<void> {
  const before = {
    costGuard: t.costGuardCalls.length,
    siteverify: t.siteverifyCalls.length,
  };
  await expectNotFound(await t.connect(id, env));
  expect(t.sessionCalls.filter((call) => call.name === "connect")).toEqual([]);
  expect(t.gameSessionCalls).toEqual([]);
  expect(t.costGuardCalls.length).toBe(before.costGuard);
  expect(t.siteverifyCalls.length).toBe(before.siteverify);
}

describe("E-2 G3", () => {
  it("でたらめ・改ざん・別の鍵・別のホストの id はどれも 404 で Durable Object を呼ばない", async () => {
    const t = await setup();
    for (const [name, id] of await forgedIds(t)) {
      await expectRejectedWithoutDurableObject(t, id).catch((error: unknown) => {
        throw new Error(`${name}: ${String(error)}`);
      });
    }
  });

  it("期限を過ぎた id は 404 で Durable Object を呼ばない", async () => {
    const t = await setup();
    const id = await createOk(t);
    t.clock.now = expiresOf(id) * 1000;
    await expectRejectedWithoutDurableObject(t, id);
  });
});

describe("E-3 G3 は外せない", () => {
  it("(a) 管理用 API で signature を変えようとしても 400 locked で、偽の id は 404", async () => {
    const t = await setup();
    const forged = await forgedIds(t);
    const res = await t.patch({ signature: { enabled: false }, reason: "x" });
    expect(res.status).toBe(400);
    expect(await res.json()).toMatchObject({ code: "locked" });
    t.clock.now += 5000;
    for (const [, id] of forged) await expectRejectedWithoutDurableObject(t, id);
  });

  it("(b) env に SIGNATURE_DISABLED・SKIP_SIGNATURE があっても 404", async () => {
    const t = await setup();
    const forged = await forgedIds(t);
    const env: Env = { ...t.env, SIGNATURE_DISABLED: "1", SKIP_SIGNATURE: "true" };
    for (const [, id] of forged) await expectRejectedWithoutDurableObject(t, id, env);
  });

  it("(c) GUARDS に signature: { enabled: false } があっても 404", async () => {
    const t = await setup({ guards: guardsWith({ signature: { enabled: false } }) });
    const res = await t.createSession();
    expect(res.status).toBe(503);
    expect(await res.json()).toEqual({ code: "sessions_disabled", message: expect.any(String) });
    const otherKey = createSessionSigner(`${KEY}x`, {
      now: () => NOW,
      randomBytes: sequentialRandom().randomBytes,
    });
    for (const id of [
      `s_${"A".repeat(28)}.${"A".repeat(43)}`,
      await otherKey.issue("raft.example", 30),
      "s_abc.def",
    ]) {
      await expectRejectedWithoutDurableObject(t, id);
    }
  });

  it("SESSION_SIGNING_KEY を消すと、正しい id も 404 で POST は 503 sessions_disabled", async () => {
    const t = await setup();
    const id = await createOk(t);
    const { SESSION_SIGNING_KEY: _removed, ...withoutKey } = t.env;
    expect(_removed).toBe(KEY);
    const before = t.costGuardCalls.length;
    await expectNotFound(await t.connect(id, withoutKey));
    expect(t.sessionCalls.filter((call) => call.name === "connect")).toEqual([]);
    const res = await t.createSession(undefined, withoutKey);
    expect(res.status).toBe(503);
    expect(await res.json()).toMatchObject({ code: "sessions_disabled" });
    expect(t.costGuardCalls.length).toBe(before);
    expect(t.gameSessionCalls).toEqual([]);
  });
});

describe("E-4 Turnstile", () => {
  it("トークンなし → 403 で、帳簿の行も init も増えない", async () => {
    const t = await setup();
    const res = await t.createSession({ stageId: "stage1" });
    expect(res.status).toBe(403);
    expect(await res.json()).toMatchObject({ code: "turnstile_failed" });
    expect(t.siteverifyCalls).toHaveLength(0);
    expect(t.ledger()).toEqual([]);
    expect(t.names()).toEqual([]);
    expect(t.costGuardCalls).not.toContain("/reserve");
  });

  it("siteverify が success: false → 403 で同じ", async () => {
    const t = await setup();
    t.setSiteverify('{"success":false,"error-codes":["invalid-input-response"]}');
    const res = await t.createSession();
    expect(res.status).toBe(403);
    expect(await res.json()).toMatchObject({ code: "turnstile_failed" });
    expect(t.siteverifyCalls).toHaveLength(1);
    expect(t.ledger()).toEqual([]);
    expect(t.names()).toEqual([]);
  });

  it.each([
    ["新しい GuardSettingsSource", (t: Setup) => t.renewGuardsSource()],
    [
      "now を 5000 ms 進める",
      (t: Setup) => {
        t.clock.now += 5000;
      },
    ],
  ])("turnstile.enabled: false にした後（%s）、トークンなしで 201", async (_name, refresh) => {
    const t = await setup();
    expect((await t.createSession({ stageId: "stage1" })).status).toBe(403);
    const patched = await t.patch({ turnstile: { enabled: false }, reason: "検証を止める" });
    expect(patched.status).toBe(200);
    refresh(t);
    await createOk(t, { stageId: "stage1" });
    expect(t.siteverifyCalls).toHaveLength(0);
    expect(t.names()).toEqual(["init"]);
  });
});

describe("E-5 レート制限", () => {
  it.each([
    ["新しい GuardSettingsSource", (t: Setup) => t.renewGuardsSource()],
    [
      "now を 5000 ms 進める",
      (t: Setup) => {
        t.clock.now += 5000;
      },
    ],
  ])("11 回目は本文を読まずに 429、無効にした後（%s）の 12 回目は 201", async (_name, refresh) => {
    const t = await setup();
    for (let i = 0; i < 10; i++) await createOk(t);
    const siteverify = t.siteverifyCalls.length;
    const inits = t.names().length;

    const request = t.sessionRequest();
    const res = await t.send(request);
    expect(res.status).toBe(429);
    expect(await res.json()).toMatchObject({ code: "rate_limited" });
    expect(request.bodyUsed).toBe(false);
    expect(t.siteverifyCalls.length).toBe(siteverify);
    expect(t.names().length).toBe(inits);
    expect(t.limiterCalls).toHaveLength(11);

    const patched = await t.patch({ rateLimit: { enabled: false }, reason: "制限を止める" });
    expect(patched.status).toBe(200);
    refresh(t);
    await createOk(t);
    expect(t.limiterCalls).toHaveLength(11);
  });
});

describe("E-6 期限と maxMinutes", () => {
  it("maxMinutes: 15 にした後の id は、発行から 20 分ちょうどで 404、1 ms 前は connect", async () => {
    const t = await setup();
    const patched = await t.patch({ gameTime: { maxMinutes: 15 }, reason: "短くする" });
    expect(patched.status).toBe(200);
    t.clock.now += 5000;
    const issuedAt = t.clock.now;
    const id = await createOk(t);
    expect(expiresOf(id) * 1000).toBe(issuedAt + 20 * 60_000);

    t.clock.now = issuedAt + 20 * 60_000 - 1;
    expect(await t.connect(id)).toBe(t.connected);
    expect(t.sessionCalls.filter((call) => call.name === "connect")).toHaveLength(1);

    t.clock.now = issuedAt + 20 * 60_000;
    await expectNotFound(await t.connect(id));
    expect(t.sessionCalls.filter((call) => call.name === "connect")).toHaveLength(1);
    expect(t.gameSessionCalls).toEqual([]);
  });

  it("発行の後に maxMinutes を変えても、発行済みの id の期限は変わらない", async () => {
    const t = await setup();
    const id = await createOk(t);
    const expires = expiresOf(id);
    await t.patch({ gameTime: { maxMinutes: 5 }, reason: "短くする" });
    t.clock.now = expires * 1000 - 1;
    expect(await t.connect(id)).toBe(t.connected);
  });
});

describe("E-7 拒否のときに sessionId を返さない", () => {
  it("budget_exhausted の 503 の本文は {code, message} だけ", async () => {
    const t = await setup();
    const patched = await t.patch({ budget: { dailyUsd: 0.001 }, reason: "下げる" });
    expect(patched.status).toBe(200);
    t.clock.now += 5000;
    const res = await t.createSession();
    expect(res.status).toBe(503);
    const body: unknown = await res.json();
    expect(body).toEqual({ code: "budget_exhausted", message: expect.any(String) });
    expect(JSON.stringify(body)).not.toMatch(/s_[A-Za-z0-9_-]{28}\./);
    expect(t.names()).toEqual([]);
  });
});

describe("E-8 ログ", () => {
  it("E-2〜E-5 の流れで console のどの関数も呼ばれない", async () => {
    const t = await setup();
    for (const [, id] of await forgedIds(t)) await t.connect(id);
    await t.createSession({ stageId: "stage1" });
    t.setSiteverify('{"success":false}');
    await t.createSession();
    for (let i = 0; i < 12; i++) await t.createSession();
    for (const spy of consoleSpies) expect(spy).not.toHaveBeenCalled();
  });
});

describe("E-9 既定の配線", () => {
  it("env = {} の Worker は POST が 503、WS が 404", async () => {
    const post = await worker.fetch(
      new Request(`${ORIGIN}/api/sessions`, {
        method: "POST",
        headers: { origin: ORIGIN, "content-type": "application/json" },
        body: '{"stageId":"stage1"}',
      }),
      {},
    );
    expect(post.status).toBe(503);
    expect(await post.json()).toMatchObject({ code: "sessions_disabled" });
    await expectNotFound(
      await worker.fetch(
        new Request(`${ORIGIN}/ws/sessions/s_abc.def`, {
          headers: { origin: ORIGIN, upgrade: "websocket" },
        }),
        {},
      ),
    );
  });
});
