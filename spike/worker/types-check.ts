// tsconfig.workerd.json だけが読む（実行しない）。構造の型（types.ts）が、cf workers types が生成した
// 本物の型と合うことを代入で確かめる。合わなくなったら tsc -p tsconfig.workerd.json が失敗する。
import type {
  ContainerLike,
  ContainerStartOptionsLike,
  DurableObjectNamespaceLike,
  DurableObjectStateLike,
  Env as SpikeEnv,
  SqlStorageLike,
  WebSocketLike,
} from "./types.ts";

declare const state: DurableObjectState;
declare const container: Container;
declare const sql: SqlStorage;
declare const socket: WebSocket;
declare const ns: DurableObjectNamespace;
declare const env: Env;
declare const startOptions: ContainerStartOptionsLike;

export const checks = [
  state satisfies DurableObjectStateLike,
  container satisfies ContainerLike,
  sql satisfies SqlStorageLike,
  socket satisfies WebSocketLike,
  ns satisfies DurableObjectNamespaceLike,
  env satisfies SpikeEnv,
  // 構造の型で組み立てた start() の引数を、本物の start() がそのまま受け取れる
  startOptions satisfies ContainerStartupOptions,
  new WebSocketPair()[0] satisfies WebSocketLike,
] as const;
