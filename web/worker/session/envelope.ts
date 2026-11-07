// ブラウザ・コンテナの電文の分類（T-308・DesignDoc §12.3・プロトコル §2・§3。純粋な関数）。
//
// DO が読むのはエンベロープの type・seq と payload のごく一部だけ。web/src/protocol/ を import せず、
// unknown から形を確かめて読む。中継する電文は、受けた文字列をそのまま送る（書き換えない）。

const encoder = new TextEncoder();

// UTF-8 のバイト数が max を超えるか（短い文字列は数えずに済ませる）。
export function exceedsBytes(text: string, max: number): boolean {
  if (text.length > max) return true; // UTF-16 の 1 単位は 1 バイト以上
  if (text.length * 3 <= max) return false; // UTF-16 の 1 単位は 3 バイト以下
  return encoder.encode(text).byteLength > max;
}

type JsonObject = Readonly<Record<string, unknown>>;

function isPlainObject(value: unknown): value is JsonObject {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function parseJson(text: string): { readonly ok: true; readonly value: unknown } | null {
  try {
    const value: unknown = JSON.parse(text);
    return { ok: true, value };
  } catch {
    return null;
  }
}

export function isInternalType(type: string): boolean {
  return type.toLowerCase().startsWith("internal.");
}

// ---------------------------------------------------------------------------
// ブラウザ → コンテナ

// Go の encoding/json は構造体のフィールド名を大文字・小文字を区別せずに読むため、キーをこの 4 つに限る
// （{"type":"cmd.sync","Type":"internal.init"} を JS は cmd.sync、Go は internal.init と読みうる）。
const BROWSER_KEYS: ReadonlySet<string> = new Set(["v", "type", "cmdId", "payload"]);

export type BrowserVerdict = "forward" | "malformed" | "internal";

// 大きさ・バイナリ・レート制限は呼ぶ側（core.ts）が先に確かめる。
export function classifyBrowserText(text: string): BrowserVerdict {
  const parsed = parseJson(text);
  if (parsed === null || !isPlainObject(parsed.value)) return "malformed";
  const envelope = parsed.value;
  if (Object.keys(envelope).some((key) => !BROWSER_KEYS.has(key))) return "malformed";
  const type = envelope["type"];
  if (typeof type !== "string") return "malformed";
  return isInternalType(type) ? "internal" : "forward";
}

// ---------------------------------------------------------------------------
// コンテナ → ブラウザ

// コンテナの fatal の error のうち、DO がゲームを終える code（ほかの code はコンテナがその接続を閉じる）。
const ENDING_ERROR_CODES: ReadonlySet<string> = new Set([
  "stage_not_found",
  "internal_error",
  "restore_failed",
  "session_mismatch",
]);

export type ContainerMessage =
  | { readonly kind: "result"; readonly seq: number | null; readonly payload: unknown }
  | { readonly kind: "ping"; readonly seq: number | null }
  | { readonly kind: "internal"; readonly seq: number | null } // internal.pong・ほか（何もしない）
  | { readonly kind: "forward"; readonly seq: number | null; readonly endsSession: boolean };

function seqOf(envelope: JsonObject): number | null {
  const seq = envelope["seq"];
  return typeof seq === "number" && Number.isSafeInteger(seq) && seq >= 1 ? seq : null;
}

function endsSession(type: unknown, payload: unknown): boolean {
  if (type !== "error" || !isPlainObject(payload) || payload["fatal"] !== true) return false;
  const code = payload["code"];
  return typeof code === "string" && ENDING_ERROR_CODES.has(code);
}

// 読めない電文はそのままブラウザに送る（コンテナを信頼する。形の検査はブラウザ・Go がする）。
export function classifyContainerText(text: string): ContainerMessage {
  const parsed = parseJson(text);
  if (parsed === null || !isPlainObject(parsed.value)) {
    return { kind: "forward", seq: null, endsSession: false };
  }
  const envelope = parsed.value;
  const seq = seqOf(envelope);
  const type = envelope["type"];
  if (typeof type === "string" && isInternalType(type)) {
    if (type === "internal.result") {
      return { kind: "result", seq, payload: envelope["payload"] ?? null };
    }
    if (type === "internal.ping") return { kind: "ping", seq };
    return { kind: "internal", seq };
  }
  return { kind: "forward", seq, endsSession: endsSession(type, envelope["payload"]) };
}
