// 入力ログの記録と internal.restore（T-308b・DesignDoc §12.3「入力ログと復元」・ADR-0017。純粋な関数）。

import { MAX_CMD_ID_LENGTH } from "./limits.ts";
import type { SessionRow } from "./store.ts";

// 記録しない種類（cmdId を持っていても覚えない）。cmd.ferry.auto（T-724）は Engine に触れないので記録しない
// （入力ログに入れると、リプレイ・simcli が知らない種類として断る）。自動の船のボットの操作は internal.input で届く。
const UNRECORDED_TYPES: ReadonlySet<string> = new Set([
  "hello",
  "ping",
  "cmd.sync",
  "sub.engineer",
  "cmd.ferry.auto",
]);

// internal.input（コンテナ → DO。ボットが適用したコマンド。T-724）で記録してよい種類（ボットが出す Engine のコマンド）。
const BOT_COMMAND_TYPES: ReadonlySet<string> = new Set([
  "cmd.ferry.load",
  "cmd.ferry.unload",
  "cmd.ferry.autoload",
  "cmd.ferry.depart",
  "cmd.relay.load",
]);

export function isBotCommandType(type: string): boolean {
  return BOT_COMMAND_TYPES.has(type);
}

export type RecordDecision = "record" | "forward" | "drop";

// record: 覚えて送る。forward: 覚えずに送る。drop: 送らずに捨てる（覚えられない cmdId）。
export function recordDecision(type: string, cmdId: unknown): RecordDecision {
  if (UNRECORDED_TYPES.has(type) || cmdId === undefined) return "forward";
  if (typeof cmdId !== "string" || cmdId.length < 1 || cmdId.length > MAX_CMD_ID_LENGTH) {
    return "drop";
  }
  return "record";
}

// 入力ログの cmd（キーの順は type・payload）。
export function commandJson(type: string, payload: unknown): string {
  return JSON.stringify({ type, payload });
}

// 復元で最後に進める時刻。最後の clock.t と最後の記録の t の大きいほう（なければ 0）。
export function restoreUntil(clockT: number | null, lastT: number | null): number {
  return Math.max(clockT ?? 0, lastT ?? 0);
}

type LogEntries = readonly { readonly t: number; readonly cmd: string }[];

// 入力ログ（DesignDoc §10.1 の形式 1）の JSON。cmd は保存した JSON をそのまま埋め込む（読み直さない）。
// internal.restore の inputLog と GET /input-log（T-714・FR-LOG-03）の本文で共通に使う。
// build・stageHash は snapshot を受ける前なら NULL なので "" にする。
export function buildInputLogJson(row: SessionRow, commands: LogEntries): string {
  const logHead = JSON.stringify({
    format: 1,
    build: row.build ?? "",
    stageId: row.stageId,
    stageHash: row.stageHash ?? "",
    seed: row.seed,
  });
  const entries = commands.map((entry) => `{"t":${String(entry.t)},"cmd":${entry.cmd}}`);
  return `${logHead.slice(0, -1)},"commands":[${entries.join(",")}]}`;
}

// internal.init・internal.restore の共通の部分。autoFerry（T-724）は有効なときだけキーを入れる（Go は省くと無効）。
export function initPayload(row: SessionRow, autoFerry: boolean): Record<string, unknown> {
  return {
    sessionId: row.sessionId,
    stageId: row.stageId,
    seed: row.seed,
    demo: row.demo,
    ...(autoFerry ? { autoFerry: true } : {}),
  };
}

// internal.restore の電文。cmd は保存した JSON をそのまま埋め込む。until が 0 ならキーを省く。
export function buildRestoreMessage(
  row: SessionRow,
  commands: LogEntries,
  until: number,
  autoFerry = false,
): string {
  const head = JSON.stringify(initPayload(row, autoFerry));
  const inputLog = buildInputLogJson(row, commands);
  const untilPart = until > 0 ? `,"until":${String(until)}` : "";
  const payload = `${head.slice(0, -1)},"inputLog":${inputLog}${untilPart}}`;
  return `{"v":1,"type":"internal.restore","payload":${payload}}`;
}
