// GameSession が使う分だけの構造の型（T-308・ADR-0023）。
// @cloudflare/workers-types と Workers のランタイムのモジュールを使わないため、ここで最小の形を決める。
// 名前は本物の型（ctx.container・WebSocket・DurableObjectStorage）に合わせる。

import type { SqlStorageLike } from "../admin/cf-types.ts";
import type { ContainerInstance } from "../admin/guards.ts";

export interface ContainerStartOptionsLike {
  readonly image: string;
  readonly instance: ContainerInstance;
  readonly enableInternet: false; // 外部への通信は禁止（リテラル型で true を渡せなくする）
  readonly env: Readonly<Record<string, string>>;
}

export interface FetcherLike {
  fetch(input: string, init?: RequestInit): Promise<Response>;
}

export interface ContainerLike {
  readonly running: boolean;
  readonly images: Readonly<Record<string, string>>;
  start(options: ContainerStartOptionsLike): void;
  monitor(): Promise<void>;
  destroy(): Promise<void>;
  getTcpPort(port: number): FetcherLike;
}

export interface WebSocketLike {
  accept(): void;
  send(message: string): void;
  close(code?: number, reason?: string): void;
  addEventListener(type: "message", handler: (event: { readonly data: unknown }) => void): void;
  addEventListener(
    type: "close",
    handler: (event: { readonly code: number; readonly reason: string }) => void,
  ): void;
  addEventListener(type: "error", handler: (event: unknown) => void): void;
}

export interface GameSessionStorageLike {
  readonly sql: SqlStorageLike;
  transactionSync<T>(fn: () => T): T;
  setAlarm(scheduledTime: number): Promise<void>;
  deleteAlarm(): Promise<void>;
}

export interface GameSessionStateLike {
  readonly storage: GameSessionStorageLike;
  readonly container?: ContainerLike | undefined;
}

function isWebSocketLike(value: unknown): value is WebSocketLike {
  if (typeof value !== "object" || value === null) return false;
  const methods = ["accept", "send", "close", "addEventListener"] as const;
  return methods.every(
    (name) => name in value && typeof (value as Record<string, unknown>)[name] === "function",
  );
}

// コンテナの /ws の応答（workerd の Response は webSocket を持つ）から WebSocket を取り出す。なければ null。
export function webSocketOf(response: Response): WebSocketLike | null {
  const socket: unknown = (response as unknown as { readonly webSocket?: unknown }).webSocket;
  return isWebSocketLike(socket) ? socket : null;
}
