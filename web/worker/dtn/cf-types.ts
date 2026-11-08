// DtnSession・ActorHost が使う分だけの構造の型（DTN-12・ADR-0023）。
// 名前は本物の型（ctx.storage・ctx.container）に合わせる。Container とソケットの型は session/cf-types.ts を使う。

import type { SqlStorageLike } from "../admin/cf-types.ts";
import type { ContainerLike } from "../session/cf-types.ts";

export type { ContainerLike, WebSocketLike } from "../session/cf-types.ts";

export interface DtnStorageLike {
  readonly sql: SqlStorageLike;
  transactionSync<T>(fn: () => T): T;
  setAlarm(scheduledTime: number): Promise<void>;
  deleteAlarm(): Promise<void>;
  deleteAll(): Promise<void>;
}

export interface DtnStateLike {
  readonly storage: DtnStorageLike;
  readonly container?: ContainerLike | undefined;
}

// Durable Object を fetch で呼んだ結果（ADR-0023）。本文が JSON でなければ body は undefined。
export interface CallResult {
  readonly status: number;
  readonly body: unknown;
}
