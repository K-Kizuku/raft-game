// テスト用の偽物（コンテナ・ソケット・時計・storage・CostGuardClient）。本番のコードから import しない。
// 呼ばれた順序と引数を記録する（スパイクの test-fakes.ts と同じ考え方）。

import type { CostGuardClient } from "../admin/client.ts";
import type { GuardsView } from "../admin/guards.ts";
import { createTestState } from "../admin/test-sqlite.ts";
import type {
  ContainerLike,
  ContainerStartOptionsLike,
  FetcherLike,
  GameSessionStateLike,
  GameSessionStorageLike,
  WebSocketLike,
} from "./cf-types.ts";

export interface Deferred {
  readonly promise: Promise<void>;
  resolve(): void;
  reject(error: Error): void;
}

export function deferred(): Deferred {
  let resolve: () => void = () => undefined;
  let reject: (error: Error) => void = () => undefined;
  const promise = new Promise<void>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  promise.catch(() => undefined); // 見張りが付く前に失敗しても、未処理の拒否にしない
  return { promise, resolve, reject };
}

interface FakeTimer {
  readonly id: number;
  readonly ms: number;
  readonly callback: () => void;
  next: number;
}

// 注入する時計。sleep は待たずに時刻を進める（タイマーは鳴らさない）。
// setInterval は advanceTimers で時刻を進めたときだけ、期限の来たものを時刻の順に呼ぶ。
export class FakeClock {
  t = 1_700_000_000_000;
  readonly sleeps: number[] = [];
  readonly #timers: FakeTimer[] = [];
  #nextTimerId = 0;
  readonly now = (): number => this.t;
  readonly sleep = async (ms: number): Promise<void> => {
    this.sleeps.push(ms);
    this.t += ms;
  };
  readonly setInterval = (callback: () => void, ms: number): (() => void) => {
    this.#nextTimerId += 1;
    const timer: FakeTimer = { id: this.#nextTimerId, ms, callback, next: this.t + ms };
    this.#timers.push(timer);
    return () => {
      const index = this.#timers.indexOf(timer);
      if (index >= 0) this.#timers.splice(index, 1);
    };
  };
  advance(ms: number): void {
    this.t += ms;
  }
  // 時刻を ms 進め、その間に期限の来たタイマーを時刻の順（同じなら作った順）に呼ぶ。
  advanceTimers(ms: number): void {
    const end = this.t + ms;
    for (;;) {
      let due: FakeTimer | undefined;
      for (const timer of this.#timers) {
        if (timer.next > end) continue;
        if (due === undefined || timer.next < due.next) due = timer;
      }
      if (due === undefined) break;
      this.t = due.next;
      due.next += due.ms;
      due.callback();
    }
    this.t = end;
  }
  get activeTimers(): number {
    return this.#timers.length;
  }
}

type Handler = (event: never) => void;

export class FakeSocket implements WebSocketLike {
  readonly name: string;
  readonly events: string[];
  readonly sent: string[] = [];
  readonly closes: { readonly code: number | undefined; readonly reason: string | undefined }[] =
    [];
  accepted = false;
  readonly #listeners = new Map<string, Handler[]>();

  constructor(name: string, events: string[] = []) {
    this.name = name;
    this.events = events;
  }

  accept(): void {
    this.accepted = true;
    this.events.push(`${this.name}.accept`);
  }

  send(message: string): void {
    this.sent.push(message);
    this.events.push(`${this.name}.send`);
  }

  close(code?: number, reason?: string): void {
    this.closes.push({ code, reason });
    this.events.push(`${this.name}.close(${String(code)})`);
  }

  addEventListener(type: string, handler: Handler): void {
    const list = this.#listeners.get(type) ?? [];
    list.push(handler);
    this.#listeners.set(type, list);
  }

  emit(type: "message" | "close" | "error", event: unknown): void {
    for (const handler of this.#listeners.get(type) ?? []) (handler as (e: unknown) => void)(event);
  }

  // 相手から文字列（またはバイナリ）が届いたことにする。
  receive(data: unknown): void {
    this.emit("message", { data });
  }

  get sentTypes(): string[] {
    return this.sent.map((message) => {
      try {
        const parsed: unknown = JSON.parse(message);
        return typeof parsed === "object" && parsed !== null && "type" in parsed
          ? String(parsed.type)
          : "?";
      } catch {
        return "?";
      }
    });
  }
}

export type HealthStep = "throw" | number;

// 偽の ctx.container。getTcpPort(8080).fetch はパスごとに応答を決める。
export class FakeContainer implements ContainerLike {
  running = false;
  images: Record<string, string> = { server: "registry.example/raft-game-server:test" };
  readonly calls: string[];
  readonly starts: ContainerStartOptionsLike[] = [];
  readonly fetches: { readonly url: string; readonly init: RequestInit | undefined }[] = [];
  startError: Error | null = null;
  destroyError: Error | null = null;
  destroyErrors: Error[] = [];
  healthPlan: HealthStep[] = [];
  healthDefault: HealthStep = 200;
  wsFails = false;
  readonly sockets: FakeSocket[] = []; // 開いたコンテナへの WebSocket（DO の側から見た相手）
  exit: Deferred = deferred();
  // あれば /healthz はこれが解決するまで応答しない（待っている間に何かを起こすため）
  healthGate: Promise<void> | null = null;

  constructor(calls: string[] = []) {
    this.calls = calls;
  }

  start(options: ContainerStartOptionsLike): void {
    this.calls.push("start");
    this.starts.push(options);
    if (this.startError !== null) throw this.startError;
    this.running = true;
    this.exit = deferred();
  }

  monitor(): Promise<void> {
    this.calls.push("monitor");
    return this.exit.promise;
  }

  // 失敗するときは、止まらずに動き続ける（destroyErrors を 1 回に 1 つずつ使い、なくなれば destroyError）。
  async destroy(): Promise<void> {
    this.calls.push("destroy");
    const error = this.destroyErrors.shift() ?? this.destroyError;
    if (error !== null) throw error;
    this.running = false;
    this.exit.resolve();
  }

  // コンテナが予期せず止まったことにする。
  crash(): void {
    this.running = false;
    this.exit.reject(new Error("exit code 3"));
  }

  get healthCalls(): number {
    return this.fetches.filter((call) => call.url.endsWith("/healthz")).length;
  }

  get wsCalls(): number {
    return this.fetches.filter((call) => call.url.endsWith("/ws")).length;
  }

  getTcpPort(port: number): FetcherLike {
    this.calls.push(`getTcpPort(${port})`);
    return {
      fetch: async (input, init) => {
        this.fetches.push({ url: input, init });
        const path = new URL(input).pathname;
        if (path === "/healthz") {
          if (this.healthGate !== null) await this.healthGate;
          const step = this.healthPlan.shift() ?? this.healthDefault;
          if (step === "throw") throw new Error("connection refused");
          return new Response(step === 200 ? "ok" : "not ready", { status: step });
        }
        if (path === "/ws") {
          if (this.wsFails) throw new Error("connect failed");
          const socket = new FakeSocket(`container${this.sockets.length + 1}`, this.calls);
          this.sockets.push(socket);
          const response = new Response(null, { status: 200 });
          Object.defineProperty(response, "webSocket", { value: socket });
          return response;
        }
        return new Response("no route", { status: 404 });
      },
    };
  }
}

export interface FakeStorage extends GameSessionStorageLike {
  readonly alarms: (number | "delete")[];
  setAlarmError: Error | null;
}

// node:sqlite の storage に setAlarm・deleteAlarm を足す。events に順序を記録する。
export async function createFakeState(
  calls: string[],
  container: ContainerLike | undefined,
): Promise<{ readonly state: GameSessionStateLike; readonly storage: FakeStorage }> {
  const base = await createTestState();
  const alarms: (number | "delete")[] = [];
  const storage: FakeStorage = {
    sql: base.storage.sql,
    transactionSync: (fn) => base.storage.transactionSync(fn),
    alarms,
    setAlarmError: null,
    async setAlarm(time) {
      calls.push(`setAlarm(${time})`);
      alarms.push(time);
      if (storage.setAlarmError !== null) throw storage.setAlarmError;
    },
    async deleteAlarm() {
      calls.push("deleteAlarm");
      alarms.push("delete");
    },
  };
  return { state: { storage, container }, storage };
}

// 偽の CostGuardClient（settle と view だけを使う）。
export class FakeCostGuard implements CostGuardClient {
  readonly settles: { readonly sessionId: string; readonly runSeconds: number }[] = [];
  view: () => Promise<GuardsView | "guards_invalid">;
  settleError: Error | null = null;
  readonly #calls: string[];

  constructor(calls: string[], maxMinutes = 30, maxAlarms = 20) {
    this.#calls = calls;
    this.view = async () => guardsView(maxMinutes, maxAlarms);
  }

  async reserve(): Promise<never> {
    throw new Error("not used");
  }

  async cancel(): Promise<never> {
    throw new Error("not used");
  }

  async patch(): Promise<never> {
    throw new Error("not used");
  }

  async settle(sessionId: string, runSeconds: number): Promise<void> {
    this.#calls.push(`settle(${runSeconds})`);
    this.settles.push({ sessionId, runSeconds });
    if (this.settleError !== null) throw this.settleError;
  }
}

export function guardsView(maxMinutes: number, maxAlarms: number): GuardsView {
  return {
    guards: {
      budget: { enabled: true, monthlyUsd: 30, dailyUsd: 1 },
      gameTime: { enabled: true, maxMinutes, maxAlarms },
      signature: { enabled: true, locked: true },
      turnstile: { enabled: true },
      rateLimit: { enabled: true, perIpPerMinute: 10 },
      cpuMs: { value: 50, note: "変更には再デプロイが必要" },
      acceptGames: true,
    },
    usage: { todayUsd: 0, monthUsd: 0, activeGames: 0 },
    unitCostUsd: { lite: 0.0065, "standard-1": 0.04 },
    recentChanges: [],
  };
}
