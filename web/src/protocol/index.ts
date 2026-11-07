// プロトコルの型の入口（T-301 / ADR-0014）。ほかのモジュールは ../protocol/index.ts から import する。
// protocol.gen.ts は make gen で生成する（手で編集しない）。events.ts・messages.ts は手書き。
export * from "./protocol.gen.ts";
export type * from "./events.ts";
export type * from "./messages.ts";
