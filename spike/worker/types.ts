// 構造の型。cloudflare.config.ts から読まれて Node の側の型検査にも入るので、workerd の型（@cloudflare/workers-types）に
// 頼らず、使う所だけを書く。本物の型と合うことは types-check.ts（tsconfig.workerd.json）で確かめる。

/** ctx.storage.sql（SqlStorage）のうち使う所 */
export interface SqlStorageLike {
  exec(query: string, ...bindings: (string | number | null)[]): SqlCursorLike;
}

export interface SqlCursorLike {
  toArray(): Record<string, unknown>[];
}

export interface DurableObjectStorageLike {
  readonly sql: SqlStorageLike;
  setAlarm(scheduledTime: number): Promise<void>;
}

/** ctx.getTcpPort(port) が返すもの（Fetcher）のうち使う所 */
export interface FetcherLike {
  fetch(input: string, init?: RequestInit): Promise<Response>;
}

/** ctx.container.start() に渡すもの（ContainerStartupOptions の一部） */
export interface ContainerStartOptionsLike {
  image: string;
  instance: "lite" | "standard-1";
  enableInternet: boolean;
  env: Record<string, string>;
}

/** ctx.container（Container）のうち使う所 */
export interface ContainerLike {
  readonly running: boolean;
  readonly images: Readonly<Record<string, string>>;
  start(options: ContainerStartOptionsLike): void;
  monitor(): Promise<void>;
  destroy(error?: unknown): Promise<void>;
  getTcpPort(port: number): FetcherLike;
  setInactivityTimeout(durationMs: number): Promise<void>;
}

/** Durable Object の ctx（DurableObjectState）のうち使う所 */
export interface DurableObjectStateLike {
  readonly storage: DurableObjectStorageLike;
  readonly container?: ContainerLike | undefined;
}

export interface DurableObjectStubLike {
  fetch(request: Request): Promise<Response>;
}

/** Durable Object の名前空間（DurableObjectNamespace）のうち使う所 */
export interface DurableObjectNamespaceLike {
  idFromName(name: string): unknown;
  get(id: unknown): DurableObjectStubLike;
}

export interface WsMessageEventLike {
  readonly data: unknown;
}

export interface WsCloseEventLike {
  readonly code: number;
  readonly reason: string;
}

/** WebSocket（workerd の WebSocketPair の片方・コンテナへの接続）のうち使う所 */
export interface WebSocketLike {
  accept(): void;
  send(message: string): void;
  close(code?: number, reason?: string): void;
  addEventListener(type: "message", handler: (event: WsMessageEventLike) => void): void;
  addEventListener(type: "close", handler: (event: WsCloseEventLike) => void): void;
  addEventListener(type: "error", handler: (event: unknown) => void): void;
}

/** Worker の env（cloudflare.config.ts の worker.env） */
export interface Env {
  readonly SESSIONS: DurableObjectNamespaceLike;
  readonly PLAIN: DurableObjectNamespaceLike;
  readonly CONTROL: DurableObjectNamespaceLike;
  readonly SPIKE_MAX_SESSIONS?: string | undefined;
  readonly SPIKE_TOKEN?: string | undefined;
}
