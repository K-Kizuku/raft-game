// テスト用の偽物（DTN の Container・ソケット・storage・時計）。本番のコードから import しない。

import { createTestState } from "../admin/test-sqlite.ts";
import type { ContainerStartOptionsLike, FetcherLike } from "../session/cf-types.ts";
import { deferred, FakeSocket, type Deferred } from "../session/test-fakes.ts";
import type { ContainerLike, DtnStateLike, DtnStorageLike } from "./cf-types.ts";

export { FakeSocket } from "../session/test-fakes.ts";

// Container に届いた要求への返事。null なら返事をしない（上限時間まで待たせる）。
export type Responder = (request: Record<string, unknown>, socket: FakeSocket) => unknown;

// 偽の ctx.container（dtnactor）。/healthz は healthy なら 200。/ws は FakeSocket を返し、
// ソケットに送られた要求を responder に渡して、返事をマイクロタスクで返す。
export class FakeDtnContainer implements ContainerLike {
  running = false;
  images: Record<string, string> = { dtnactor: "registry.example/dtnactor:test" };
  readonly calls: string[];
  readonly name: string;
  readonly starts: ContainerStartOptionsLike[] = [];
  readonly sockets: FakeSocket[] = [];
  readonly requests: Record<string, unknown>[] = [];
  startError: Error | null = null;
  destroyErrors: Error[] = [];
  destroyError: Error | null = null;
  healthy = true;
  wsFails = false;
  responder: Responder = () => null;
  exit: Deferred = deferred();
  destroys = 0;

  constructor(name = "container", calls: string[] = []) {
    this.name = name;
    this.calls = calls;
  }

  start(options: ContainerStartOptionsLike): void {
    this.calls.push(`${this.name}.start`);
    this.starts.push(options);
    if (this.startError !== null) throw this.startError;
    this.running = true;
    this.exit = deferred();
  }

  monitor(): Promise<void> {
    return this.exit.promise;
  }

  async destroy(): Promise<void> {
    this.calls.push(`${this.name}.destroy`);
    this.destroys += 1;
    const error = this.destroyErrors.shift() ?? this.destroyError;
    if (error !== null) throw error;
    this.running = false;
    for (const socket of this.sockets) socket.emit("close", { code: 1006, reason: "destroyed" });
    this.exit.resolve();
  }

  // Container が予期せず止まったことにする（ソケットも閉じる）。
  crash(): void {
    this.running = false;
    this.exit.reject(new Error("exit code 3"));
    this.socket?.emit("close", { code: 1006, reason: "crashed" });
  }

  get socket(): FakeSocket | undefined {
    return this.sockets.at(-1);
  }

  get sentKinds(): string[] {
    return this.requests.map((request) => String(request["kind"]));
  }

  getTcpPort(port: number): FetcherLike {
    void port;
    return {
      fetch: async (input) => {
        const path = new URL(input).pathname;
        if (path === "/healthz") {
          return new Response(this.healthy ? "ok" : "no", { status: this.healthy ? 200 : 503 });
        }
        if (path === "/ws") {
          if (this.wsFails) throw new Error("connect failed");
          const socket = new FakeSocket(`${this.name}.ws${this.sockets.length + 1}`, this.calls);
          this.sockets.push(socket);
          const originalSend = socket.send.bind(socket);
          socket.send = (message: string) => {
            originalSend(message);
            let request: Record<string, unknown>;
            try {
              request = JSON.parse(message) as Record<string, unknown>;
            } catch {
              return;
            }
            this.requests.push(request);
            const reply = this.responder(request, socket);
            if (reply === null || reply === undefined) return;
            queueMicrotask(() =>
              socket.receive(typeof reply === "string" ? reply : JSON.stringify(reply)),
            );
          };
          const response = new Response(null, { status: 200 });
          Object.defineProperty(response, "webSocket", { value: socket });
          return response;
        }
        return new Response("no route", { status: 404 });
      },
    };
  }
}

export interface FakeDtnStorage extends DtnStorageLike {
  readonly alarms: (number | "delete")[];
  deleted: number;
  setAlarmError: Error | null;
}

export async function createDtnState(
  container: ContainerLike | undefined,
  calls: string[] = [],
): Promise<{ state: DtnStateLike; storage: FakeDtnStorage }> {
  const base = await createTestState();
  const alarms: (number | "delete")[] = [];
  const storage: FakeDtnStorage = {
    sql: base.storage.sql,
    transactionSync: (fn) => base.storage.transactionSync(fn),
    alarms,
    deleted: 0,
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
    async deleteAll() {
      calls.push("deleteAll");
      storage.deleted += 1;
      const tables = base.storage.sql
        .exec("SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'")
        .toArray();
      for (const row of tables)
        base.storage.sql.exec(`DROP TABLE IF EXISTS "${String(row["name"])}"`);
    },
  };
  return { state: { storage, container }, storage };
}

// 手で鳴らすタイマー（setTimer の偽物）。
export class ManualTimers {
  readonly #timers = new Map<number, { callback: () => void; ms: number }>();
  #next = 0;
  readonly setTimer = (callback: () => void, ms: number): (() => void) => {
    this.#next += 1;
    const id = this.#next;
    this.#timers.set(id, { callback, ms });
    return () => this.#timers.delete(id);
  };
  get size(): number {
    return this.#timers.size;
  }
  fireAll(): void {
    const timers = [...this.#timers.values()];
    this.#timers.clear();
    for (const timer of timers) timer.callback();
  }
}

// 何度かマイクロタスクを回す（偽の返事・非同期の処理を進める）。
export async function flush(times = 20): Promise<void> {
  for (let i = 0; i < times; i += 1) await Promise.resolve();
}
