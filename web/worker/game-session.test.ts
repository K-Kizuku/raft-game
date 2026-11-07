import { describe, expect, it } from "vitest";

import type { DurableObjectNamespaceLike } from "./admin/cf-types.ts";
import { GameSession, type GameSessionDeps } from "./game-session.ts";
import type { Env } from "./routes/env.ts";
import type { VerifiedSessionId } from "./routes/ports.ts";
import { createHandler } from "./routes/router.ts";
import { hashSessionToken } from "./security/token.ts";
import { createSessionPorts } from "./session/ports.ts";
import { readSession } from "./session/store.ts";
import {
  FakeClock,
  FakeContainer,
  FakeCostGuard,
  FakeSocket,
  createFakeState,
  deferred,
} from "./session/test-fakes.ts";

const SESSION_ID = "s_abcdefghijklmnopqrstuvwxyz01.ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789abcdefg";
const TOKEN = "Tok_en-abcdefghijklmno"; // 22 文字
const MIN = 60_000;

interface HarnessOptions {
  readonly env?: Env;
  readonly maxMinutes?: number;
  readonly maxAlarms?: number;
  readonly noContainer?: boolean;
}

async function harness(options: HarnessOptions = {}) {
  const calls: string[] = [];
  const clock = new FakeClock();
  const t0 = clock.t;
  const container = new FakeContainer(calls);
  const { state, storage } = await createFakeState(
    calls,
    options.noContainer === true ? undefined : container,
  );
  const costGuard = new FakeCostGuard(calls, options.maxMinutes ?? 30, options.maxAlarms ?? 20);
  const logs: string[] = [];
  const browsers: FakeSocket[] = [];
  const tokenHash = await hashSessionToken(TOKEN);
  const env: Env = options.env ?? {};

  const deps: Partial<GameSessionDeps> = {
    now: clock.now,
    sleep: clock.sleep,
    log: (line) => logs.push(line),
    createPair: () => {
      calls.push("createPair");
      const server = new FakeSocket(`browser${browsers.length + 1}`, calls);
      browsers.push(server);
      return [new FakeSocket("client"), server];
    },
    upgradeResponse: () => new Response(null, { status: 200, headers: { "x-upgrade": "1" } }),
    timeoutSignal: () => undefined,
    costGuard,
  };
  let session = new GameSession(state, env, deps);

  const initBody = {
    sessionId: SESSION_ID,
    stageId: "stage1",
    seed: 20261101,
    demo: false,
    tokenHash,
  };
  const h = {
    calls,
    clock,
    t0,
    container,
    storage,
    costGuard,
    logs,
    browsers,
    tokenHash,
    initBody,
    get session() {
      return session;
    },
    row: () => readSession(storage.sql),
    init: (body: unknown = initBody) =>
      session.fetch(
        new Request("https://game-session.internal/init", {
          method: "POST",
          headers: { "content-type": "application/json" },
          body: typeof body === "string" ? body : JSON.stringify(body),
        }),
      ),
    connect: (token: string | null = TOKEN, upgrade = true) => {
      const headers: Record<string, string> = {};
      if (upgrade) headers["upgrade"] = "websocket";
      if (token !== null) headers["x-session-token"] = token;
      return session.fetch(
        new Request("https://game-session.internal/connect", { method: "GET", headers }),
      );
    },
    drain: () => session.drain(),
    alarm: async () => {
      await session.alarm();
      await session.drain();
    },
    // DO が作り直された（メモリは消え、storage とコンテナは残る）
    recreate: () => {
      session = new GameSession(state, env, deps);
    },
    count: (name: string) => calls.filter((call) => call === name).length,
    lastBrowser: () => {
      const browser = browsers.at(-1);
      if (browser === undefined) throw new Error("no browser");
      return browser;
    },
    lastContainerSocket: () => {
      const socket = container.sockets.at(-1);
      if (socket === undefined) throw new Error("no container socket");
      return socket;
    },
  };
  return h;
}

type Harness = Awaited<ReturnType<typeof harness>>;

// init → connect → drain（コンテナにつながるまで）
async function started(options: HarnessOptions = {}) {
  const h = await harness(options);
  expect((await h.init()).status).toBe(201);
  expect((await h.connect()).status).toBe(200);
  await h.drain();
  return h;
}

async function reconnect(h: Harness) {
  expect((await h.connect()).status).toBe(200);
  await h.drain();
  return { browser: h.lastBrowser(), socket: h.lastContainerSocket() };
}

const msg = (type: string, extra: Record<string, unknown> = {}) =>
  JSON.stringify({ v: 1, type, payload: {}, ...extra });

function parsed(text: string | undefined): unknown {
  return text === undefined ? undefined : JSON.parse(text);
}

describe("G-1 init", () => {
  it("201。行を作り、アラームを設定してからコンテナを起動する", async () => {
    const h = await harness({ maxMinutes: 10 });
    const res = await h.init();
    expect(res.status).toBe(201);
    expect(await res.json()).toEqual({});
    expect(h.row()).toMatchObject({
      sessionId: SESSION_ID,
      stageId: "stage1",
      seed: 20261101,
      demo: false,
      state: "starting",
      createdAt: h.t0,
      deadlineAt: h.t0 + 10 * MIN,
      idleSince: h.t0,
      containerStarts: 1,
      containerStartedAt: h.t0,
      alarmCount: 1,
      alarmAt: h.t0 + 300_000,
    });
    const setAlarm = h.calls.indexOf(`setAlarm(${h.t0 + 300_000})`);
    expect(setAlarm).toBeGreaterThanOrEqual(0);
    expect(setAlarm).toBeLessThan(h.calls.indexOf("start"));
    expect(h.container.starts).toEqual([
      {
        image: "registry.example/raft-game-server:test",
        instance: "standard-1",
        enableInternet: false,
        env: { MODE: "single", PORT: "8080" },
      },
    ]);
    expect(h.count("monitor")).toBe(1);
    expect(h.container.healthCalls).toBe(0);
  });

  it("CONTAINER_INSTANCE が lite なら lite で起動する", async () => {
    const h = await harness({ env: { CONTAINER_INSTANCE: "lite" } });
    await h.init();
    expect(h.container.starts[0]?.instance).toBe("lite");
  });
});

describe("G-2 init の失敗", () => {
  it.each<[string, unknown]>([
    ["配列", []],
    ["余分なキー", { extra: 1 }],
    ["sessionId", { sessionId: "a b" }],
    ["stageId", { stageId: "stage.1" }],
    ["seed が負", { seed: -1 }],
    ["seed が小数", { seed: 1.5 }],
    ["seed が文字列", { seed: "1" }],
    ["demo", { demo: "false" }],
    ["tokenHash が大文字", { tokenHash: "A".repeat(64) }],
    ["tokenHash が短い", { tokenHash: "a".repeat(63) }],
    ["キーがない", { tokenHash: undefined }],
  ])("不正な本文（%s）→ 400 で行・アラーム・start なし", async (_name, change) => {
    const h = await harness();
    const body = Array.isArray(change) ? change : { ...h.initBody, ...(change as object) };
    const res = await h.init(body);
    expect(res.status).toBe(400);
    expect(h.row()).toBeNull();
    expect(h.storage.alarms).toEqual([]);
    expect(h.container.starts).toEqual([]);
  });

  it("大きすぎる本文 → 400", async () => {
    const h = await harness();
    const res = await h.init(JSON.stringify({ ...h.initBody, pad: "x".repeat(5000) }));
    expect(res.status).toBe(400);
    expect(h.row()).toBeNull();
  });

  it.each<[string, (h: Harness) => void, HarnessOptions]>([
    ["ctx.container がない", () => undefined, { noContainer: true }],
    ["イメージがない", (h) => (h.container.images = {}), {}],
    ["start が例外", (h) => (h.container.startError = new Error("boom")), {}],
  ])("%s → 500 start_failed、行は ended、deleteAlarm、settle なし", async (_n, setup, opts) => {
    const h = await harness(opts);
    setup(h);
    const res = await h.init();
    expect(res.status).toBe(500);
    expect(await res.json()).toMatchObject({ code: "start_failed" });
    expect(h.row()).toMatchObject({ state: "ended", endReason: "start_failed", alarmAt: null });
    expect(h.storage.alarms).toEqual([h.t0 + 300_000, "delete"]);
    expect(h.costGuard.settles).toEqual([]);
  });

  it("setAlarm が失敗 → コンテナを起動せず 500 start_failed、settle なし（fail closed）", async () => {
    const h = await harness();
    h.storage.setAlarmError = new Error("storage down");
    const res = await h.init();
    expect(res.status).toBe(500);
    expect(await res.json()).toMatchObject({ code: "start_failed" });
    expect(h.container.starts).toEqual([]);
    expect(h.count("monitor")).toBe(0);
    expect(h.row()).toMatchObject({ state: "ended", endReason: "start_failed", alarmAt: null });
    expect(h.costGuard.settles).toEqual([]);
    expect((await h.connect()).status).toBe(404);
  });

  it("setAlarm が失敗 → ルーターが予約を cancel して 500 internal_error（コンテナは起動しない）", async () => {
    const h = await harness();
    h.storage.setAlarmError = new Error("storage down");
    const routerCalls: string[] = [];
    const namespace: DurableObjectNamespaceLike = {
      idFromName: (name) => ({ name }),
      get: () => ({ fetch: (request) => h.session.fetch(request) }),
    };
    const handler = createHandler((env) => {
      const session = createSessionPorts(env);
      return {
        stages: [
          {
            id: "stage1",
            title: "t",
            region: "r",
            islands: 3,
            ferries: 1,
            durationMs: 180000,
            unlock: null,
          },
        ],
        security: {
          checkRateLimit: async () => "ok",
          verifyTurnstile: async () => "ok",
          issueSession: async () => ({
            sessionId: SESSION_ID as VerifiedSessionId,
            token: TOKEN,
            tokenHash: h.tokenHash,
          }),
          verifySessionId: async () => null,
        },
        costGuard: {
          reserve: async () => (routerCalls.push("reserve"), "ok"),
          cancel: async () => void routerCalls.push("cancel"),
          handleAdmin: async () => new Response("{}"),
        },
        ...(session === undefined ? {} : { session }),
      };
    });
    const origin = "https://raft.example";
    const res = await handler(
      new Request(`${origin}/api/sessions`, {
        method: "POST",
        headers: { origin, "content-type": "application/json" },
        body: JSON.stringify({ stageId: "stage1", seed: 1 }),
      }),
      { GAME_SESSION: namespace },
    );
    expect(res.status).toBe(500);
    expect(await res.json()).toMatchObject({ code: "internal_error" });
    expect(routerCalls).toEqual(["reserve", "cancel"]);
    expect(h.container.starts).toEqual([]);
    expect(h.costGuard.settles).toEqual([]);
  });

  it("同じ内容の 2 回目 → 200 で start は 1 回のまま。違う内容 → 409", async () => {
    const h = await harness();
    expect((await h.init()).status).toBe(201);
    const again = await h.init();
    expect(again.status).toBe(200);
    expect(await again.json()).toEqual({});
    expect(h.container.starts).toHaveLength(1);
    const conflict = await h.init({ ...h.initBody, seed: 1 });
    expect(conflict.status).toBe(409);
    expect(await conflict.json()).toMatchObject({ code: "conflict" });
    expect(h.container.starts).toHaveLength(1);
  });
});

describe("G-3 connect の拒否", () => {
  it("Upgrade がない → 426", async () => {
    const h = await harness();
    await h.init();
    expect((await h.connect(TOKEN, false)).status).toBe(426);
  });

  it.each<[string, (h: Harness) => Promise<string | null>]>([
    ["行がない", async () => TOKEN],
    ["トークンがない", async (h) => (await h.init(), null)],
    ["22 文字でない", async (h) => (await h.init(), TOKEN.slice(0, 21))],
    ["違うトークン", async (h) => (await h.init(), "X".repeat(22))],
  ])("%s → 404 で createPair・getTcpPort を呼ばない", async (_name, setup) => {
    const h = await harness();
    const token = await setup(h);
    const res = await h.connect(token);
    expect(res.status).toBe(404);
    expect(await res.json()).toMatchObject({ code: "not_found" });
    expect(h.count("createPair")).toBe(0);
    expect(h.calls.some((call) => call.startsWith("getTcpPort"))).toBe(false);
  });

  it("終わったセッション → 404", async () => {
    const h = await harness({ noContainer: true });
    expect((await h.init()).status).toBe(500);
    expect((await h.connect()).status).toBe(404);
    expect(h.count("createPair")).toBe(0);
  });
});

describe("G-4 connect の成功", () => {
  it("/healthz を 200 まで試し、internal.init を最初に送り、溜めた電文を続けて送る", async () => {
    const h = await harness();
    await h.init();
    h.container.healthPlan = ["throw", 503, 200];
    const res = await h.connect();
    expect(res.headers.get("x-upgrade")).toBe("1");
    const browser = h.lastBrowser();
    expect(browser.accepted).toBe(true);
    const hello = msg("hello");
    browser.receive(hello);
    await h.drain();

    expect(h.container.healthCalls).toBe(3);
    expect(h.clock.sleeps).toEqual([100, 100]);
    const socket = h.lastContainerSocket();
    expect(socket.accepted).toBe(true);
    expect(parsed(socket.sent[0])).toEqual({
      v: 1,
      type: "internal.init",
      payload: { sessionId: SESSION_ID, stageId: "stage1", seed: 20261101, demo: false },
    });
    expect(socket.sent[1]).toBe(hello);
    expect(h.row()).toMatchObject({ state: "running", readyAt: h.t0 + 200, idleSince: null });
    const ready = h.logs.map((line) => JSON.parse(line)).find((l) => l.event === "container.ready");
    expect(ready).toMatchObject({ startMs: 200 });

    // つなぎ直し: /healthz を待たずに /ws を開き、また internal.init を送る
    const healthBefore = h.container.healthCalls;
    const again = await reconnect(h);
    expect(h.container.healthCalls).toBe(healthBefore);
    expect(again.socket).not.toBe(socket);
    expect(again.socket.sentTypes[0]).toBe("internal.init");
  });
});

describe("G-5 中継", () => {
  it("同じ文字列で両方向に中継し、internal.* を通さない", async () => {
    const h = await started();
    const browser = h.lastBrowser();
    const socket = h.lastContainerSocket();

    const forwarded = [
      msg("hello"),
      JSON.stringify({ v: 1, type: "cmd.ferry.load", cmdId: "c-1", payload: { ferryId: "f1" } }),
    ];
    const dropped = [
      msg("internal.init"),
      msg("Internal.Init"),
      JSON.stringify({ type: "cmd.sync", Type: "internal.init" }),
      "{broken",
    ];
    for (const text of [...forwarded, ...dropped]) browser.receive(text);
    expect(socket.sent.slice(1)).toEqual(forwarded);

    const down = [
      msg("snapshot", { seq: 1 }),
      msg("events", { seq: 2 }),
      JSON.stringify({ v: 1, type: "ack", seq: 3, payload: { cmdId: "c-1", ok: true, t: 5 } }),
    ];
    for (const text of down) socket.receive(text);
    socket.receive(msg("internal.pong"));
    socket.receive(msg("internal.ping"));
    expect(browser.sent).toEqual(down);
    expect(parsed(socket.sent.at(-1))).toEqual({ v: 1, type: "internal.pong", payload: {} });

    socket.receive(msg("internal.result", { payload: { score: 1 } }));
    await h.drain();
    expect(browser.sentTypes).not.toContain("internal.result");
  });
});

describe("G-6 上限", () => {
  it("バイナリ → ブラウザ 1003・コンテナへのソケット 1000", async () => {
    const h = await started();
    h.lastBrowser().receive(new Uint8Array([1, 2]));
    expect(h.lastBrowser().closes).toEqual([{ code: 1003, reason: "binary not supported" }]);
    expect(h.lastContainerSocket().closes[0]?.code).toBe(1000);
    expect(h.lastContainerSocket().sent).toHaveLength(1);
  });

  it("16385 バイト → 1009", async () => {
    const h = await started();
    h.lastBrowser().receive(JSON.stringify({ v: 1, type: "hello", payload: "x".repeat(16400) }));
    expect(h.lastBrowser().closes).toEqual([{ code: 1009, reason: "message too big" }]);
    expect(h.lastContainerSocket().closes[0]?.code).toBe(1000);
    expect(h.lastContainerSocket().sent).toHaveLength(1);
  });

  it("同じ 1 秒に 31 通 → 30 通。1 秒進めると次が届く", async () => {
    const h = await started();
    const socket = h.lastContainerSocket();
    h.clock.t = Math.floor(h.clock.t / 1000) * 1000 + 1000;
    for (let i = 0; i < 31; i++) h.lastBrowser().receive(msg("cmd.sync", { cmdId: `c${i}` }));
    expect(socket.sent).toHaveLength(1 + 30);
    h.lastBrowser().receive(msg("cmd.sync", { cmdId: "late" }));
    expect(socket.sent).toHaveLength(1 + 30);
    h.clock.advance(1000);
    h.lastBrowser().receive(msg("cmd.sync", { cmdId: "next" }));
    expect(socket.sent).toHaveLength(1 + 31);
  });

  it("準備の間に 33 通 → 32 通だけ届く", async () => {
    const h = await harness();
    await h.init();
    await h.connect();
    const browser = h.lastBrowser();
    for (let i = 0; i < 20; i++) browser.receive(msg("hello", { cmdId: `a${i}` }));
    h.clock.advance(1000);
    for (let i = 0; i < 13; i++) browser.receive(msg("hello", { cmdId: `b${i}` }));
    await h.drain();
    const socket = h.lastContainerSocket();
    expect(socket.sent).toHaveLength(1 + 32);
    expect(socket.sent.at(-1)).toBe(msg("hello", { cmdId: "b11" }));
  });
});

describe("G-7 置き換え", () => {
  it("古いブラウザを 4001、古いコンテナへのソケットを 1000 で閉じ、古いソケットの電文を捨てる", async () => {
    const h = await started();
    const browser1 = h.lastBrowser();
    const socket1 = h.lastContainerSocket();
    const { browser: browser2, socket: socket2 } = await reconnect(h);
    expect(browser1.closes).toEqual([{ code: 4001, reason: "replaced" }]);
    expect(socket1.closes).toEqual([{ code: 1000, reason: "replaced" }]);
    expect(socket2).not.toBe(socket1);
    expect(socket2.sentTypes).toEqual(["internal.init"]);

    socket1.receive(msg("snapshot", { seq: 9 }));
    socket1.receive(
      JSON.stringify({
        v: 1,
        type: "error",
        seq: 10,
        payload: { code: "replaced", message: "m", fatal: true },
      }),
    );
    expect(browser1.sent).toEqual([]);
    expect(browser2.sent).toEqual([]);
    expect(h.row()?.state).toBe("running");
    browser1.emit("close", { code: 4001, reason: "replaced" });
    expect(h.row()?.idleSince).toBeNull();
  });

  it("準備の途中で置き換えたら、古い接続はコンテナへのソケットを開かない", async () => {
    const h = await harness();
    await h.init();
    const gate = deferred();
    h.container.healthGate = gate.promise;
    await h.connect();
    const browser1 = h.lastBrowser();
    await h.connect();
    const browser2 = h.lastBrowser();
    expect(browser1.closes).toEqual([{ code: 4001, reason: "replaced" }]);
    gate.resolve();
    await h.drain();
    expect(h.container.wsCalls).toBe(1);
    expect(h.container.sockets).toHaveLength(1);
    browser2.receive(msg("hello"));
    expect(h.lastContainerSocket().sent.at(-1)).toBe(msg("hello"));
  });
});

describe("G-8 ラウンドの終わり", () => {
  it("結果を保存し、destroy・settle を 1 回だけ行い、1000 で閉じてアラームを消す", async () => {
    const h = await started();
    const browser = h.lastBrowser();
    const socket = h.lastContainerSocket();
    h.clock.t = h.t0 + 90_500;
    const roundResult = msg("round.result", { seq: 5, payload: { score: 120 } });
    socket.receive(roundResult);
    socket.receive(msg("internal.result", { payload: { score: 120 } }));
    await h.drain();

    expect(browser.sent).toEqual([roundResult]);
    expect(browser.closes).toEqual([{ code: 1000, reason: "round ended" }]);
    expect(h.count("destroy")).toBe(1);
    expect(h.costGuard.settles).toEqual([{ sessionId: SESSION_ID, runSeconds: 91 }]);
    expect(h.storage.alarms.at(-1)).toBe("delete");
    expect(h.row()).toMatchObject({
      state: "ended",
      endReason: "round_ended",
      settled: true,
      resultJson: JSON.stringify({ score: 120 }),
      alarmAt: null,
      runMs: 90_500,
      containerStartedAt: null,
    });

    socket.receive(msg("internal.result", { payload: { score: 121 } }));
    await h.drain();
    expect(h.row()?.resultJson).toBe(JSON.stringify({ score: 121 }));
    expect(h.count("destroy")).toBe(1);
    expect(h.costGuard.settles).toHaveLength(1);

    expect((await h.connect()).status).toBe(404);
    const alarms = h.storage.alarms.length;
    await h.alarm();
    expect(h.storage.alarms).toHaveLength(alarms);
  });

  it("大きすぎる結果は保存しない", async () => {
    const h = await started();
    h.lastContainerSocket().receive(
      msg("internal.result", { payload: { pad: "x".repeat(262_200) } }),
    );
    await h.drain();
    expect(h.row()).toMatchObject({ state: "ended", resultJson: null });
  });

  it("settle の失敗はログを出して続ける（やり直さない）", async () => {
    const h = await started();
    h.costGuard.settleError = new Error("down");
    h.lastContainerSocket().receive(msg("internal.result"));
    await h.drain();
    expect(h.row()).toMatchObject({ state: "ended", settled: false, alarmAt: null });
    expect(h.costGuard.settles).toHaveLength(1);
    expect(h.storage.alarms.at(-1)).toBe("delete");
    const alarms = h.storage.alarms.length;
    await h.alarm();
    expect(h.costGuard.settles).toHaveLength(1);
    expect(h.storage.alarms).toHaveLength(alarms);
  });

  it("ブラウザが round.result で閉じた後に届いた internal.result も保存し、終える", async () => {
    const h = await started();
    const browser = h.lastBrowser();
    const socket = h.lastContainerSocket();
    h.clock.t = h.t0 + 90_500;
    const roundResult = msg("round.result", { seq: 5, payload: { score: 120 } });
    socket.receive(roundResult);
    browser.emit("close", { code: 1000, reason: "" });
    await h.drain();
    expect(socket.closes).toEqual([{ code: 1000, reason: "browser closed" }]);
    expect(h.row()?.state).toBe("running");

    socket.receive(msg("internal.result", { payload: { score: 120 } }));
    await h.drain();
    expect(browser.sent).toEqual([roundResult]);
    expect(h.row()).toMatchObject({
      state: "ended",
      endReason: "round_ended",
      resultJson: JSON.stringify({ score: 120 }),
      settled: true,
      containerStartedAt: null,
      alarmAt: null,
    });
    expect(h.count("destroy")).toBe(1);
    expect(h.costGuard.settles).toEqual([{ sessionId: SESSION_ID, runSeconds: 91 }]);
  });

  it("閉じた接続のソケットからの internal.result 以外の電文は捨てる", async () => {
    const h = await started();
    const browser = h.lastBrowser();
    const socket = h.lastContainerSocket();
    browser.emit("close", { code: 1000, reason: "" });
    await h.drain();
    socket.receive(msg("internal.ping"));
    socket.receive(msg("snapshot", { seq: 1 }));
    expect(socket.sent).toHaveLength(1); // internal.init だけ（pong を返さない）
    expect(browser.sent).toEqual([]);
    expect(h.row()?.state).toBe("running");
  });
});

describe("G-8b コンテナの停止に失敗したときの後始末", () => {
  it("destroy が 1 回目に失敗 → 精算せずアラームでやり直し、止まってから精算する", async () => {
    const h = await started();
    h.container.destroyErrors = [new Error("busy")];
    h.clock.t = h.t0 + 90_500;
    h.lastContainerSocket().receive(msg("internal.result", { payload: { score: 1 } }));
    await h.drain();

    expect(h.count("destroy")).toBe(1);
    expect(h.container.running).toBe(true);
    expect(h.costGuard.settles).toEqual([]);
    expect(h.row()).toMatchObject({
      state: "ended",
      endReason: "round_ended",
      settled: false,
      containerStartedAt: h.t0,
      alarmCount: 2,
      alarmAt: h.t0 + 120_500,
    });
    expect(h.storage.alarms.at(-1)).toBe(h.t0 + 120_500);

    h.clock.t = h.t0 + 120_500;
    await h.alarm();
    expect(h.count("destroy")).toBe(2);
    expect(h.container.running).toBe(false);
    expect(h.costGuard.settles).toEqual([{ sessionId: SESSION_ID, runSeconds: 121 }]);
    expect(h.row()).toMatchObject({
      settled: true,
      containerStartedAt: null,
      runMs: 120_500,
      alarmAt: null,
    });
    expect(h.storage.alarms.at(-1)).toBe("delete");
    const ends = h.logs.filter((line) => JSON.parse(line).event === "session.end");
    expect(ends.map((line) => JSON.parse(line))).toEqual([
      expect.objectContaining({ reason: "round_ended", runSeconds: 121, settled: true }),
    ]);

    // 止まった後のアラームは何もしない
    const alarms = h.storage.alarms.length;
    await h.alarm();
    expect(h.count("destroy")).toBe(2);
    expect(h.costGuard.settles).toHaveLength(1);
    expect(h.storage.alarms).toHaveLength(alarms);
  });

  it("上限まで失敗し続けたら、精算しないまま（予約のまま）ログを 1 行出してあきらめる", async () => {
    const h = await started({ maxAlarms: 3 });
    h.container.destroyError = new Error("busy");
    h.lastContainerSocket().receive(msg("internal.result"));
    await h.drain();
    for (let i = 0; i < 2; i++) {
      const at = h.row()?.alarmAt;
      expect(at).not.toBeNull();
      h.clock.t = at ?? 0;
      await h.alarm();
    }
    expect(h.count("destroy")).toBe(3);
    expect(h.row()).toMatchObject({
      state: "ended",
      settled: false,
      alarmCount: 3,
      alarmAt: null,
      containerStartedAt: h.t0,
    });
    expect(h.storage.alarms.filter((a) => a !== "delete")).toHaveLength(3);
    expect(h.costGuard.settles).toEqual([]);
    const exhausted = h.logs.filter((line) => line.includes("retry_exhausted"));
    expect(exhausted.map((line) => JSON.parse(line))).toEqual([
      {
        level: "error",
        event: "session.error",
        sessionId: SESSION_ID,
        op: "destroy",
        error: "retry_exhausted",
      },
    ]);
  });

  it("後始末の途中で鳴ったアラームは、二重に destroy・settle しない", async () => {
    const h = await started();
    h.lastContainerSocket().receive(msg("internal.result"));
    void h.session.alarm();
    await h.drain();
    expect(h.count("destroy")).toBe(1);
    expect(h.costGuard.settles).toHaveLength(1);
  });
});

describe("G-9 起動の失敗", () => {
  it("/healthz が期限までに 200 にならない → start_timeout", async () => {
    const h = await harness({ env: { CONTAINER_START_TIMEOUT_MS: "1000" } });
    await h.init();
    h.container.healthDefault = 503;
    await h.connect();
    await h.drain();
    const browser = h.lastBrowser();
    expect(browser.sent.map(parsed)).toEqual([
      {
        v: 1,
        type: "error",
        seq: 1,
        payload: { code: "container_unavailable", message: "Container unavailable", fatal: true },
      },
    ]);
    expect(browser.closes).toEqual([{ code: 1011, reason: "container unavailable" }]);
    expect(h.count("destroy")).toBe(1);
    expect(h.costGuard.settles).toEqual([{ sessionId: SESSION_ID, runSeconds: 1 }]);
    expect(h.container.wsCalls).toBe(0);
    expect(h.row()).toMatchObject({ state: "ended", endReason: "start_timeout" });
  });
});

describe("G-10 アラーム", () => {
  it("(1) 鳴る前は設定し直さず、接続中に鳴ると deadline_at に設定する", async () => {
    const h = await started();
    expect(h.storage.alarms).toEqual([h.t0 + 5 * MIN]);
    h.clock.t = h.t0 + 5 * MIN;
    await h.alarm();
    expect(h.storage.alarms).toEqual([h.t0 + 5 * MIN, h.t0 + 30 * MIN]);
    expect(h.row()).toMatchObject({ alarmCount: 2, alarmAt: h.t0 + 30 * MIN, state: "running" });
  });

  async function afterFirstAlarm(options: HarnessOptions = {}) {
    const h = await started(options);
    h.clock.t = h.t0 + 5 * MIN;
    await h.alarm();
    return h;
  }

  it("(2) 切断から 5 分で idle で終わる", async () => {
    const h = await afterFirstAlarm();
    h.clock.t = h.t0 + 6 * MIN;
    h.lastBrowser().emit("close", { code: 1001, reason: "" });
    await h.drain();
    expect(h.lastContainerSocket().closes).toEqual([{ code: 1000, reason: "browser closed" }]);
    expect(h.row()).toMatchObject({ idleSince: h.t0 + 6 * MIN, alarmAt: h.t0 + 11 * MIN });
    h.clock.t = h.t0 + 11 * MIN;
    await h.alarm();
    expect(h.row()).toMatchObject({ state: "ended", endReason: "idle" });
    expect(h.count("destroy")).toBe(1);
    expect(h.costGuard.settles).toEqual([{ sessionId: SESSION_ID, runSeconds: 11 * 60 }]);
  });

  it("(3) 切断して 4 分後につなぎ直すと、鳴っても終わらず deadline_at に設定し直す", async () => {
    const h = await afterFirstAlarm();
    h.clock.t = h.t0 + 6 * MIN;
    h.lastBrowser().emit("close", { code: 1001, reason: "" });
    await h.drain();
    h.clock.t = h.t0 + 10 * MIN;
    await reconnect(h);
    h.clock.t = h.t0 + 11 * MIN;
    await h.alarm();
    expect(h.row()).toMatchObject({ state: "running", alarmAt: h.t0 + 30 * MIN });
    expect(h.storage.alarms.at(-1)).toBe(h.t0 + 30 * MIN);
  });

  it("(4) 接続中に deadline_at で鳴る → session_ended の error（seq は直前 + 1）と 1000", async () => {
    const h = await afterFirstAlarm();
    const browser = h.lastBrowser();
    h.lastContainerSocket().receive(msg("events", { seq: 7 }));
    h.clock.t = h.t0 + 30 * MIN;
    await h.alarm();
    expect(parsed(browser.sent.at(-1))).toEqual({
      v: 1,
      type: "error",
      seq: 8,
      payload: { code: "session_ended", message: "Session ended", fatal: true },
    });
    expect(browser.closes).toEqual([{ code: 1000, reason: "session ended" }]);
    expect(h.row()).toMatchObject({ state: "ended", endReason: "time_limit" });
    expect(h.count("destroy")).toBe(1);
    expect(h.costGuard.settles).toEqual([{ sessionId: SESSION_ID, runSeconds: 30 * 60 }]);
  });

  it("(5) maxAlarms: 2 なら 2 回を超えず、3 回目が要るときに alarm_cap で終わる", async () => {
    const h = await afterFirstAlarm({ maxAlarms: 2 });
    expect(h.row()?.alarmCount).toBe(2);
    h.clock.t = h.t0 + 6 * MIN;
    h.lastBrowser().emit("close", { code: 1001, reason: "" });
    await h.drain();
    expect(h.row()).toMatchObject({ state: "ended", endReason: "alarm_cap", alarmCount: 2 });
    expect(h.storage.alarms.filter((a) => a !== "delete")).toHaveLength(2);
    expect(h.count("destroy")).toBe(1);
    expect(h.costGuard.settles).toHaveLength(1);
  });

  it("(6) 鳴る前に切断・つなぎ直しを 10 回しても、早いほうへ動いた回数だけ増える", async () => {
    const h = await afterFirstAlarm();
    for (let i = 0; i < 10; i++) {
      h.clock.t = h.t0 + 6 * MIN + i * 10_000;
      h.lastBrowser().emit("close", { code: 1001, reason: "" });
      await h.drain();
      await reconnect(h);
    }
    expect(h.row()).toMatchObject({ alarmCount: 3, alarmAt: h.t0 + 11 * MIN, state: "running" });
  });

  it("切断したときの setAlarm の失敗 → 設定できていないアラームを記録せず、前のアラームを残す", async () => {
    const h = await afterFirstAlarm();
    h.storage.setAlarmError = new Error("storage down");
    h.clock.t = h.t0 + 6 * MIN;
    h.lastBrowser().emit("close", { code: 1001, reason: "" });
    await h.drain();
    expect(h.storage.alarms.at(-1)).toBe(h.t0 + 11 * MIN); // 試した
    expect(h.row()).toMatchObject({ alarmAt: h.t0 + 30 * MIN, alarmCount: 3, state: "running" });

    // 次に切れたときに、また早いほうへ設定しようとする
    h.storage.setAlarmError = null;
    await reconnect(h);
    h.clock.t = h.t0 + 7 * MIN;
    h.lastBrowser().emit("close", { code: 1001, reason: "" });
    await h.drain();
    expect(h.row()).toMatchObject({ alarmAt: h.t0 + 12 * MIN, alarmCount: 4 });
  });

  it("alarm() の中の setAlarm の失敗 → 記録せず例外にする（ランタイムが alarm() をやり直す）", async () => {
    const h = await started();
    h.storage.setAlarmError = new Error("storage down");
    h.clock.t = h.t0 + 5 * MIN;
    await expect(h.session.alarm()).rejects.toThrow();
    expect(h.row()).toMatchObject({ alarmAt: null, alarmCount: 2, state: "running" });

    h.storage.setAlarmError = null;
    await h.alarm();
    expect(h.row()).toMatchObject({ alarmAt: h.t0 + 30 * MIN, alarmCount: 3 });
  });

  it("init の後に誰もつながらない → 5 分で idle", async () => {
    const h = await harness();
    await h.init();
    h.clock.t = h.t0 + 5 * MIN;
    await h.alarm();
    expect(h.row()).toMatchObject({ state: "ended", endReason: "idle" });
    expect(h.count("destroy")).toBe(1);
  });
});

describe("G-11 コンテナが止まる", () => {
  it("接続中に monitor() が失敗で終わる → container_lost", async () => {
    const h = await started();
    const browser = h.lastBrowser();
    h.lastContainerSocket().receive(msg("snapshot", { seq: 1 }));
    h.container.crash();
    await h.drain();
    expect(parsed(browser.sent.at(-1))).toMatchObject({
      type: "error",
      seq: 2,
      payload: { code: "container_unavailable", fatal: true },
    });
    expect(browser.closes).toEqual([{ code: 1011, reason: "container unavailable" }]);
    expect(h.row()).toMatchObject({ state: "ended", endReason: "container_lost" });
    expect(h.count("destroy")).toBe(1);
    expect(h.costGuard.settles).toHaveLength(1);
  });

  it("コンテナへのソケットだけが閉じる → ブラウザ 1011 で終わらず、つなぎ直すと internal.init", async () => {
    const h = await started();
    const browser = h.lastBrowser();
    h.lastContainerSocket().emit("close", { code: 1006, reason: "" });
    await h.drain();
    expect(browser.closes).toEqual([{ code: 1011, reason: "container closed" }]);
    expect(h.row()?.state).toBe("running");
    expect(h.count("destroy")).toBe(0);
    const again = await reconnect(h);
    expect(again.socket.sentTypes).toEqual(["internal.init"]);
  });

  it("コンテナの /ws が開けない → ブラウザを 1011 で閉じるだけ", async () => {
    const h = await harness();
    await h.init();
    h.container.wsFails = true;
    await h.connect();
    await h.drain();
    expect(h.lastBrowser().closes).toEqual([{ code: 1011, reason: "container connect failed" }]);
    expect(h.row()?.state).toBe("starting");
    expect(h.row()?.idleSince).not.toBeNull();
  });
});

describe("G-12 コンテナの fatal の error", () => {
  const fatal = (code: string, seq: number) =>
    JSON.stringify({ v: 1, type: "error", seq, payload: { code, message: "m", fatal: true } });

  it("stage_not_found → 中継した後で 1011、container_error で終わる", async () => {
    const h = await started();
    const browser = h.lastBrowser();
    h.lastContainerSocket().receive(fatal("stage_not_found", 1));
    await h.drain();
    expect(browser.sent).toEqual([fatal("stage_not_found", 1)]);
    expect(browser.closes).toEqual([{ code: 1011, reason: "container error" }]);
    expect(h.row()).toMatchObject({ state: "ended", endReason: "container_error" });
    expect(h.count("destroy")).toBe(1);
  });

  it("古い接続の replaced → 何も起きない", async () => {
    const h = await started();
    const socket1 = h.lastContainerSocket();
    const { browser: browser2 } = await reconnect(h);
    socket1.receive(fatal("replaced", 3));
    await h.drain();
    expect(browser2.sent).toEqual([]);
    expect(browser2.closes).toEqual([]);
    expect(h.row()?.state).toBe("running");
  });
});

describe("G-13 DO の作り直し", () => {
  it("コンテナが動いていれば monitor を呼び直して中継する", async () => {
    const h = await started();
    h.recreate();
    const { browser, socket } = await reconnect(h);
    expect(h.count("monitor")).toBe(2);
    expect(socket.sentTypes).toEqual(["internal.init"]);
    browser.receive(msg("hello"));
    expect(socket.sent.at(-1)).toBe(msg("hello"));
  });

  it("コンテナが動いていなければ container_lost", async () => {
    const h = await started();
    h.recreate();
    h.container.running = false;
    await h.connect();
    await h.drain();
    expect(h.row()).toMatchObject({ state: "ended", endReason: "container_lost" });
    expect(h.lastBrowser().closes).toEqual([{ code: 1011, reason: "container unavailable" }]);
  });

  it("作り直した後の alarm() で idle_since が NULL なら now を入れる", async () => {
    const h = await started();
    expect(h.row()?.idleSince).toBeNull();
    h.recreate();
    h.clock.t = h.t0 + 5 * MIN;
    await h.alarm();
    expect(h.row()).toMatchObject({
      state: "running",
      idleSince: h.t0 + 5 * MIN,
      alarmAt: h.t0 + 10 * MIN,
    });
    expect(h.storage.alarms.at(-1)).toBe(h.t0 + 10 * MIN);
  });
});

describe("G-14 ログ", () => {
  it("JSON で sessionId を持ち、トークン・tokenHash・電文の中身を含まない。1 ゲームで 15 行以下", async () => {
    const h = await started();
    const socket = h.lastContainerSocket();
    h.lastBrowser().receive(msg("hello"));
    h.lastBrowser().receive(msg("internal.init"));
    socket.receive(msg("round.result", { seq: 1 }));
    h.clock.t = h.t0 + 90_500;
    socket.receive(msg("internal.result", { payload: { secret: "payload-text" } }));
    await h.drain();

    expect(h.logs.length).toBeGreaterThan(0);
    expect(h.logs.length).toBeLessThanOrEqual(15);
    for (const line of h.logs) {
      const entry: unknown = JSON.parse(line);
      expect(entry).toMatchObject({ sessionId: SESSION_ID });
      for (const forbidden of [
        TOKEN,
        h.tokenHash,
        "internal.init",
        "internal.result",
        "round.result",
        "payload-text",
      ]) {
        expect(line).not.toContain(forbidden);
      }
    }
    const events = h.logs.map((line) => (JSON.parse(line) as { event: string }).event);
    expect(events).toContain("session.init");
    expect(events).toContain("container.ready");
    expect(events.at(-1)).toBe("session.end");
  });
});

describe("G-15 クラスの形と知らない要求", () => {
  it("DurableObject を継承しない", () => {
    expect(Object.getPrototypeOf(GameSession.prototype)).toBe(Object.prototype);
  });

  it("知らないパス → 404、メソッドの違い → 405（Allow）", async () => {
    const h = await harness();
    const unknown = await h.session.fetch(new Request("https://game-session.internal/nope"));
    expect(unknown.status).toBe(404);
    const getInit = await h.session.fetch(new Request("https://game-session.internal/init"));
    expect(getInit.status).toBe(405);
    expect(getInit.headers.get("allow")).toBe("POST");
    const postConnect = await h.session.fetch(
      new Request("https://game-session.internal/connect", { method: "POST" }),
    );
    expect(postConnect.status).toBe(405);
    expect(postConnect.headers.get("allow")).toBe("GET");
  });
});
