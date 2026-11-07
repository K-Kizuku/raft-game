// 入力ログの記録と internal.restore（T-308b・DesignDoc §12.3「入力ログと復元」・ADR-0017。純粋な関数）。

import { MAX_CMD_ID_LENGTH } from "./limits.ts";
import type { SessionRow } from "./store.ts";

// 記録しない種類（cmdId を持っていても覚えない）。
const UNRECORDED_TYPES: ReadonlySet<string> = new Set([
  "hello",
  "ping",
  "cmd.sync",
  "sub.engineer",
]);

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

// internal.restore の電文。cmd は保存した JSON をそのまま埋め込む。until が 0 ならキーを省く。
export function buildRestoreMessage(
  row: SessionRow,
  commands: readonly { readonly t: number; readonly cmd: string }[],
  until: number,
): string {
  const head = JSON.stringify({
    sessionId: row.sessionId,
    stageId: row.stageId,
    seed: row.seed,
    demo: row.demo,
  });
  const logHead = JSON.stringify({
    format: 1,
    build: row.build ?? "",
    stageId: row.stageId,
    stageHash: row.stageHash ?? "",
    seed: row.seed,
  });
  const entries = commands.map((entry) => `{"t":${String(entry.t)},"cmd":${entry.cmd}}`);
  const inputLog = `${logHead.slice(0, -1)},"commands":[${entries.join(",")}]}`;
  const untilPart = until > 0 ? `,"until":${String(until)}` : "";
  const payload = `${head.slice(0, -1)},"inputLog":${inputLog}${untilPart}}`;
  return `{"v":1,"type":"internal.restore","payload":${payload}}`;
}
