// テスト用: GameSession を偽物（コンテナ・ソケット・時計・タイマー・storage・CostGuardClient）で動かす。
// 本番のコードから import しない。本物のタイマーを使わない（setInterval は FakeClock のもの）。

import { expect } from "vitest";

import { GameSession, type GameSessionDeps } from "../game-session.ts";
import type { Env } from "../routes/env.ts";
import { hashSessionToken } from "../security/token.ts";
import { readSession } from "./store.ts";
import {
  FakeClock,
  FakeContainer,
  FakeCostGuard,
  FakeSocket,
  createFakeState,
} from "./test-fakes.ts";

export const SESSION_ID =
  "s_abcdefghijklmnopqrstuvwxyz01.ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789abcdefg";
export const TOKEN = "Tok_en-abcdefghijklmno"; // 22 文字
export const MIN = 60_000;

export interface HarnessOptions {
  readonly env?: Env;
  readonly maxMinutes?: number;
  readonly maxAlarms?: number;
  readonly noContainer?: boolean;
}

export async function harness(options: HarnessOptions = {}) {
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
    setInterval: clock.setInterval,
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
    // GET /input-log（T-714）。token が null ならヘッダーを付けない。
    inputLog: (token: string | null = TOKEN, method = "GET") => {
      const headers: Record<string, string> = {};
      if (token !== null) headers["x-session-token"] = token;
      return session.fetch(
        new Request("https://game-session.internal/input-log", { method, headers }),
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
    events: () => logs.map((line) => JSON.parse(line) as { event: string; [key: string]: unknown }),
  };
  return h;
}

export type Harness = Awaited<ReturnType<typeof harness>>;

// init → connect → drain（コンテナにつながるまで）
export async function started(options: HarnessOptions = {}) {
  const h = await harness(options);
  expect((await h.init()).status).toBe(201);
  expect((await h.connect()).status).toBe(200);
  await h.drain();
  return h;
}

export async function reconnect(h: Harness) {
  expect((await h.connect()).status).toBe(200);
  await h.drain();
  return { browser: h.lastBrowser(), socket: h.lastContainerSocket() };
}

export const msg = (type: string, extra: Record<string, unknown> = {}) =>
  JSON.stringify({ v: 1, type, payload: {}, ...extra });

export function parsed(text: string | undefined): unknown {
  return text === undefined ? undefined : JSON.parse(text);
}
