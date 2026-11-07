// テスト用の偽物（コンテナ・ソケット・時計）。呼ばれた順序と引数を記録する。
import { json } from "./http.ts";
import type { ContainerLike, ContainerStartOptionsLike, FetcherLike, WebSocketLike } from "./types.ts";

export interface Deferred {
  promise: Promise<void>;
  resolve(): void;
  reject(e: Error): void;
}

export function deferred(): Deferred {
  let resolve!: () => void;
  let reject!: (e: Error) => void;
  const promise = new Promise<void>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

/** 注入する時計。sleep は待たずに時刻を進める */
export class FakeClock {
  t = 1_000_000;
  sleeps: number[] = [];
  now = (): number => this.t;
  sleep = async (ms: number): Promise<void> => {
    this.sleeps.push(ms);
    this.t += ms;
  };
}

type Handler = (event: unknown) => void;

export class FakeSocket implements WebSocketLike {
  readonly name: string;
  readonly events: string[];
  sent: string[] = [];
  closes: { code: number | undefined; reason: string | undefined }[] = [];
  accepted = false;
  private readonly listeners = new Map<string, Handler[]>();

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
    this.events.push(`${this.name}.close(${code})`);
  }

  addEventListener(type: string, handler: (event: never) => void): void {
    const list = this.listeners.get(type) ?? [];
    list.push(handler as Handler);
    this.listeners.set(type, list);
  }

  emit(type: "message" | "close" | "error", event: unknown): void {
    for (const h of this.listeners.get(type) ?? []) h(event);
  }
}

export type HealthStep = "throw" | number;

/** 偽の ctx.container。getTcpPort(8080).fetch はパスごとに route で応答を決める */
export class FakeContainer implements ContainerLike {
  running = false;
  images: Record<string, string> = { server: "registry.example/raft-game-spike-server:test" };
  calls: string[] = [];
  starts: ContainerStartOptionsLike[] = [];
  fetches: { url: string; init: RequestInit | undefined }[] = [];
  inactivity: number[] = [];
  startError: Error | null = null;
  /** /healthz の応答の順（尽きたら healthDefault） */
  healthPlan: HealthStep[] = [];
  healthDefault: HealthStep = 200;
  infoBody: unknown = { numCPU: 1, gomaxprocs: 1, env: { MODE: "single", PORT: "8080" } };
  exit: Deferred = deferred();
  /** パスごとの応答の差し替え（/healthz・/info 以外） */
  routes = new Map<string, (init: RequestInit | undefined) => Promise<Response> | Response>();

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

  async destroy(): Promise<void> {
    this.calls.push("destroy");
    this.running = false;
    this.exit.resolve();
  }

  async setInactivityTimeout(durationMs: number): Promise<void> {
    this.calls.push("setInactivityTimeout");
    this.inactivity.push(durationMs);
  }

  /** コンテナが落ちたことにする */
  crashNow(): void {
    this.running = false;
    this.exit.reject(new Error("exit code 3"));
  }

  getTcpPort(port: number): FetcherLike {
    this.calls.push(`getTcpPort(${port})`);
    return {
      fetch: async (input, init) => {
        this.fetches.push({ url: input, init });
        const path = new URL(input).pathname;
        if (path === "/healthz") {
          const step = this.healthPlan.shift() ?? this.healthDefault;
          if (step === "throw") throw new Error("connection refused");
          return new Response(step === 200 ? "ok" : "not ready", { status: step });
        }
        if (path === "/info") return json(200, this.infoBody);
        const route = this.routes.get(path);
        if (route !== undefined) return route(init);
        return json(404, { error: "no route in fake" });
      },
    };
  }
}
