import { describe, expect, it } from "vitest";

import { GUARDS_EXAMPLE, guardsWith } from "./admin/test-fixtures.ts";
import { createTestState, type TestState } from "./admin/test-sqlite.ts";
import { CostGuard } from "./cost-guard.ts";
import { CostGuard as ExportedCostGuard } from "./index.ts";

const BASE = "https://cost-guard.internal";

function post(path: string, body: unknown): Request {
  return new Request(`${BASE}${path}`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: typeof body === "string" ? body : JSON.stringify(body),
  });
}

function get(path: string): Request {
  return new Request(`${BASE}${path}`);
}

// 偽の storage.setAlarm は、呼ばれたら失敗する（K-3。CostGuard はアラームを使わない）。
async function noAlarmState(): Promise<TestState> {
  const base = await createTestState();
  const storage = {
    ...base.storage,
    setAlarm: (): never => {
      throw new Error("setAlarm must not be called");
    },
  };
  return { storage };
}

async function call(guard: CostGuard, request: Request) {
  const res = await guard.fetch(request);
  return { status: res.status, body: (await res.json()) as unknown };
}

describe("K-1 内部の API", () => {
  it("reserve・settle・cancel・view・patch が動く", async () => {
    const guard = new CostGuard(await noAlarmState(), { GUARDS: GUARDS_EXAMPLE });

    expect(await call(guard, post("/reserve", { sessionId: "s_a.b", instance: "lite" }))).toEqual({
      status: 200,
      body: { result: "ok" },
    });
    expect(await call(guard, post("/reserve", { sessionId: "s_c.d", instance: "lite" }))).toEqual({
      status: 200,
      body: { result: "ok" },
    });
    expect(await call(guard, post("/settle", { sessionId: "s_a.b", runSeconds: 600 }))).toEqual({
      status: 200,
      body: {},
    });
    expect(await call(guard, post("/cancel", { sessionId: "s_c.d" }))).toEqual({
      status: 200,
      body: {},
    });

    const view = await call(guard, get("/view"));
    expect(view.status).toBe(200);
    expect(view.body).toMatchObject({
      guards: { signature: { enabled: true, locked: true }, acceptGames: true },
      usage: { todayUsd: 0.002167, activeGames: 0 },
    });

    const patched = await call(
      guard,
      post("/patch", { change: { acceptGames: false }, reason: "止める", by: "admin" }),
    );
    expect(patched.status).toBe(200);
    expect(patched.body).toMatchObject({
      guards: { acceptGames: false },
      recentChanges: [{ change: { acceptGames: false }, reason: "止める" }],
    });
    expect(await call(guard, post("/reserve", { sessionId: "s_e.f", instance: "lite" }))).toEqual({
      status: 200,
      body: { result: "sessions_disabled" },
    });
  });

  it("知らないパスは 404、メソッドが違えば 405", async () => {
    const guard = new CostGuard(await noAlarmState(), { GUARDS: GUARDS_EXAMPLE });
    expect((await guard.fetch(get("/x"))).status).toBe(404);
    expect((await guard.fetch(get("/reserve"))).status).toBe(405);
    expect((await guard.fetch(post("/view", {}))).status).toBe(405);
  });

  const badRequests: readonly [string, Request][] = [
    ["壊れた JSON", post("/reserve", "{")],
    ["本文が配列", post("/reserve", "[]")],
    ['sessionId が "a/b"', post("/reserve", { sessionId: "a/b", instance: "lite" })],
    ["sessionId が 257 文字", post("/reserve", { sessionId: "a".repeat(257), instance: "lite" })],
    ['instance が "x"', post("/reserve", { sessionId: "a", instance: "x" })],
    ["cancel の sessionId がない", post("/cancel", {})],
    ['runSeconds が "1"', post("/settle", { sessionId: "a", runSeconds: "1" })],
    ["runSeconds が -1", post("/settle", { sessionId: "a", runSeconds: -1 })],
    ["runSeconds が 86401", post("/settle", { sessionId: "a", runSeconds: 86401 })],
    ["patch の change が不正", post("/patch", { change: { x: 1 }, reason: "x", by: "admin" })],
    ["patch の by がない", post("/patch", { change: { acceptGames: false }, reason: "x" })],
    [
      "patch の by が 65 文字",
      post("/patch", { change: { acceptGames: false }, reason: "x", by: "a".repeat(65) }),
    ],
    ["patch の reason がない", post("/patch", { change: { acceptGames: false }, by: "admin" })],
  ];

  it.each(badRequests)("%s は 400 bad_request", async (_name, request) => {
    const guard = new CostGuard(await noAlarmState(), { GUARDS: GUARDS_EXAMPLE });
    expect(await call(guard, request)).toMatchObject({
      status: 400,
      body: { code: "bad_request" },
    });
  });

  it("/patch に signature を含む change は 400 locked", async () => {
    const guard = new CostGuard(await noAlarmState(), { GUARDS: GUARDS_EXAMPLE });
    for (const change of [{ "signature.enabled": false }, { signature: false }]) {
      expect(await call(guard, post("/patch", { change, reason: "x", by: "admin" }))).toMatchObject(
        { status: 400, body: { code: "locked" } },
      );
    }
  });
});

describe("K-2 再起動", () => {
  it("同じ DB で作り直しても、帳簿・上書き・記録が残っている", async () => {
    const state = await noAlarmState();
    const first = new CostGuard(state, { GUARDS: GUARDS_EXAMPLE });
    await first.fetch(post("/reserve", { sessionId: "a", instance: "standard-1" }));
    await first.fetch(
      post("/patch", { change: { "budget.dailyUsd": 2 }, reason: "上げる", by: "admin" }),
    );

    const second = new CostGuard(state, { GUARDS: GUARDS_EXAMPLE });
    const view = await call(second, get("/view"));
    expect(view.body).toMatchObject({
      guards: { budget: { dailyUsd: 2 } },
      usage: { todayUsd: 0.04, activeGames: 1 },
      recentChanges: [{ change: { "budget.dailyUsd": 2 }, reason: "上げる" }],
    });
  });
});

describe("K-3 アラームを使わない", () => {
  it("setAlarm を呼ばず、alarm を持たない", async () => {
    const guard = new CostGuard(await noAlarmState(), { GUARDS: GUARDS_EXAMPLE });
    await guard.fetch(post("/reserve", { sessionId: "a", instance: "lite" }));
    await guard.fetch(post("/settle", { sessionId: "a", runSeconds: 1 }));
    await guard.fetch(post("/cancel", { sessionId: "a" }));
    await guard.fetch(get("/view"));
    await guard.fetch(post("/patch", { change: { acceptGames: false }, reason: "x", by: "a" }));
    expect("alarm" in CostGuard.prototype).toBe(false);
  });
});

describe("K-4 GUARDS がない・不正", () => {
  it.each([
    ["ない", {}],
    ["不正", { GUARDS: guardsWith({ signature: { enabled: false } }) }],
    ["JSON でない", { GUARDS: "{" }],
  ])("%s なら reserve は sessions_disabled、view は 503 guards_invalid", async (_name, env) => {
    const guard = new CostGuard(await noAlarmState(), env);
    expect(await call(guard, post("/reserve", { sessionId: "a", instance: "lite" }))).toEqual({
      status: 200,
      body: { result: "sessions_disabled" },
    });
    expect(await call(guard, get("/view"))).toMatchObject({
      status: 503,
      body: { code: "guards_invalid" },
    });
    expect(
      await call(guard, post("/patch", { change: { acceptGames: true }, reason: "x", by: "a" })),
    ).toMatchObject({ status: 503, body: { code: "guards_invalid" } });
  });

  it("GUARDS が JSON の文字列でも読める", async () => {
    const guard = new CostGuard(await noAlarmState(), { GUARDS: JSON.stringify(GUARDS_EXAMPLE) });
    expect((await guard.fetch(get("/view"))).status).toBe(200);
  });
});

describe("K-5 エントリポイントからの export", () => {
  it("index.ts の CostGuard は cost-guard.ts の CostGuard と同じ", () => {
    expect(ExportedCostGuard).toBe(CostGuard);
  });
});

// DTN-12（docs/dtn/environment.md §3）: 内訳つきの予約。
describe("K-DTN 内訳つきの予約", () => {
  const DTN_GUARDS = {
    ...GUARDS_EXAMPLE,
    budget: { enabled: true, monthlyUsd: 10, dailyUsd: 1 },
    gameTime: { maxMinutes: 15, maxAlarms: 20 },
    unitCostUsd: {
      lite: 0.0065,
      "standard-1": 0.04,
      "container.lite": 0.0036,
      "container.standard-1": 0.037,
      do: 0.0028,
    },
  };
  const units = (n: number) => [
    { kind: "container", instance: "lite", count: n },
    { kind: "do", count: n },
  ];

  async function ledgerRow(state: TestState, sessionId: string) {
    return state.storage.sql
      .exec("SELECT * FROM ledger WHERE session_id = ?", sessionId)
      .toArray()[0];
  }

  it("予約額 = ceil(Σ count × 単価 × maxMinutes / 30)。demo-izu（13・13）は 15 分で 41,600 µUSD", async () => {
    const state = await noAlarmState();
    const guard = new CostGuard(state, { GUARDS: DTN_GUARDS });
    expect(await call(guard, post("/reserve", { sessionId: "s_izu", units: units(13) }))).toEqual({
      status: 200,
      body: { result: "ok" },
    });
    const row = await ledgerRow(state, "s_izu");
    // 30 分の単価: 13 × 3600 + 13 × 2800 = 83,200 µUSD。15 分なら 41,600
    expect(row).toMatchObject({ instance: "units", rate_micro: 83200, reserved_micro: 41600 });
    expect(JSON.parse(String(row?.["units"]))).toEqual(units(13));
  });

  it("予算を超える予約は budget_exhausted（1 日 1 USD で demo-izu は 24 回まで）", async () => {
    const guard = new CostGuard(await noAlarmState(), { GUARDS: DTN_GUARDS });
    for (let i = 0; i < 24; i += 1) {
      const res = await call(guard, post("/reserve", { sessionId: `s_${i}`, units: units(13) }));
      expect(res.body).toEqual({ result: "ok" });
    }
    expect(await call(guard, post("/reserve", { sessionId: "s_x", units: units(13) }))).toEqual({
      status: 200,
      body: { result: "budget_exhausted" },
    });
  });

  it("精算は行の単価の合計 × 動いた秒数。2 回目は何もしない", async () => {
    const state = await noAlarmState();
    const guard = new CostGuard(state, { GUARDS: DTN_GUARDS });
    await call(guard, post("/reserve", { sessionId: "s_a", units: units(5) }));
    await call(guard, post("/settle", { sessionId: "s_a", runSeconds: 360 }));
    // 5 × 3600 + 5 × 2800 = 32,000 µUSD / 30 分。360 秒 → 6,400
    expect(await ledgerRow(state, "s_a")).toMatchObject({
      status: "settled",
      settled_micro: 6400,
    });
    await call(guard, post("/settle", { sessionId: "s_a", runSeconds: 1800 }));
    expect(await ledgerRow(state, "s_a")).toMatchObject({ settled_micro: 6400 });
  });

  it("今の形 {sessionId, instance} も受ける（units 列は NULL）", async () => {
    const state = await noAlarmState();
    const guard = new CostGuard(state, { GUARDS: DTN_GUARDS });
    expect(
      (await call(guard, post("/reserve", { sessionId: "s_old", instance: "lite" }))).body,
    ).toEqual({ result: "ok" });
    expect(await ledgerRow(state, "s_old")).toMatchObject({
      instance: "lite",
      rate_micro: 6500,
      units: null,
    });
  });

  it("内訳の単価を書いていない GUARDS では sessions_disabled（安全側）", async () => {
    const guard = new CostGuard(await noAlarmState(), { GUARDS: GUARDS_EXAMPLE });
    expect(
      (await call(guard, post("/reserve", { sessionId: "s_a", units: units(5) }))).body,
    ).toEqual({ result: "sessions_disabled" });
  });

  it.each([
    ["instance と units の両方", { sessionId: "s_a", instance: "lite", units: units(1) }],
    ["units が空", { sessionId: "s_a", units: [] }],
    ["count が 0", { sessionId: "s_a", units: [{ kind: "do", count: 0 }] }],
    ["count が小数", { sessionId: "s_a", units: [{ kind: "do", count: 1.5 }] }],
    ["知らない kind", { sessionId: "s_a", units: [{ kind: "gpu", count: 1 }] }],
    [
      "container に instance がない",
      { sessionId: "s_a", units: [{ kind: "container", count: 1 }] },
    ],
    ["do に余分なキー", { sessionId: "s_a", units: [{ kind: "do", count: 1, instance: "lite" }] }],
  ])("%s → 400", async (_name, body) => {
    const guard = new CostGuard(await noAlarmState(), { GUARDS: DTN_GUARDS });
    expect((await call(guard, post("/reserve", body))).status).toBe(400);
  });

  it("前の版で作った表（units 列なし）に列を足す", async () => {
    const state = await noAlarmState();
    state.storage.sql.exec(`CREATE TABLE ledger (
      session_id TEXT PRIMARY KEY, instance TEXT NOT NULL, rate_micro INTEGER NOT NULL,
      reserved_micro INTEGER NOT NULL, settled_micro INTEGER, status TEXT NOT NULL,
      started_at INTEGER NOT NULL, ended_at INTEGER)`);
    const guard = new CostGuard(state, { GUARDS: DTN_GUARDS });
    expect(
      (await call(guard, post("/reserve", { sessionId: "s_a", units: units(2) }))).body,
    ).toEqual({ result: "ok" });
  });

  it("GUARDS の内訳の単価が不正なら設定ごと不正（reserve は sessions_disabled）", async () => {
    const guard = new CostGuard(await noAlarmState(), {
      GUARDS: guardsWith({ "unitCostUsd.do": 0 }),
    });
    expect(
      (await call(guard, post("/reserve", { sessionId: "s_a", instance: "lite" }))).body,
    ).toEqual({ result: "sessions_disabled" });
  });
});
