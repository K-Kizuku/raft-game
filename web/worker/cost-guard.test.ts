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
