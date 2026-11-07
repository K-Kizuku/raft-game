// ブラウザ・コンテナの電文の分類（T-308・T-308b・DesignDoc §12.3・プロトコル §2・§3。純粋な関数）。
//
// DO が読むのはエンベロープの type・cmdId・seq と payload のごく一部だけ。web/src/protocol/ を import せず、
// unknown から形を確かめて読む。中継する電文は、受けた文字列をそのまま送る（書き換えない）。
// JSON を読むのは電文 1 つにつき 1 回。

import { MAX_SESSION_TEXT_LENGTH } from "./limits.ts";

const encoder = new TextEncoder();

// UTF-8 のバイト数が max を超えるか（短い文字列は数えずに済ませる）。
export function exceedsBytes(text: string, max: number): boolean {
  if (text.length > max) return true; // UTF-16 の 1 単位は 1 バイト以上
  if (text.length * 3 <= max) return false; // UTF-16 の 1 単位は 3 バイト以下
  return encoder.encode(text).byteLength > max;
}

export function utf8Bytes(text: string): number {
  return encoder.encode(text).byteLength;
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

function isNonNegativeSafeInteger(value: unknown): value is number {
  return typeof value === "number" && Number.isSafeInteger(value) && value >= 0;
}

// ---------------------------------------------------------------------------
// ブラウザ → コンテナ

// Go の encoding/json は構造体のフィールド名を大文字・小文字を区別せずに読むため、キーをこの 4 つに限る
// （{"type":"cmd.sync","Type":"internal.init"} を JS は cmd.sync、Go は internal.init と読みうる）。
const BROWSER_KEYS: ReadonlySet<string> = new Set(["v", "type", "cmdId", "payload"]);

export type BrowserVerdict =
  | {
      readonly kind: "forward";
      readonly type: string;
      readonly cmdId: unknown; // キーがなければ undefined、あれば値そのもの
      readonly payload: unknown; // キーがない・null なら {}（Go の DecodeEnvelope と同じ）
    }
  | { readonly kind: "malformed" }
  | { readonly kind: "internal" };

// 大きさ・バイナリ・レート制限は呼ぶ側（core.ts）が先に確かめる。
export function classifyBrowserText(text: string): BrowserVerdict {
  const parsed = parseJson(text);
  if (parsed === null || !isPlainObject(parsed.value)) return { kind: "malformed" };
  const envelope = parsed.value;
  if (Object.keys(envelope).some((key) => !BROWSER_KEYS.has(key))) return { kind: "malformed" };
  const type = envelope["type"];
  if (typeof type !== "string") return { kind: "malformed" };
  if (isInternalType(type)) return { kind: "internal" };
  const cmdId = Object.hasOwn(envelope, "cmdId") ? envelope["cmdId"] : undefined;
  const payload = envelope["payload"] ?? {};
  return { kind: "forward", type, cmdId, payload };
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

export interface AckInfo {
  readonly cmdId: string;
  readonly ok: boolean;
  readonly t: number;
}

export interface ForwardMessage {
  readonly kind: "forward";
  readonly seq: number | null;
  readonly type: string | null;
  readonly endsSession: boolean;
  readonly fatalCode: string | null; // fatal の error の code
  readonly ack: AckInfo | null;
  readonly clockT: number | null; // snapshot・events の payload.clock.t
  readonly stageHash: string | null; // snapshot の payload.session.stageHash
  readonly build: string | null; // snapshot の payload.session.build
}

export type ContainerMessage =
  | { readonly kind: "result"; readonly seq: number | null; readonly payload: unknown }
  | { readonly kind: "ping"; readonly seq: number | null }
  | { readonly kind: "pong"; readonly seq: number | null }
  | { readonly kind: "internal"; readonly seq: number | null } // ほかの internal.*（何もしない）
  | ForwardMessage;

function seqOf(envelope: JsonObject): number | null {
  const seq = envelope["seq"];
  return typeof seq === "number" && Number.isSafeInteger(seq) && seq >= 1 ? seq : null;
}

function fatalCodeOf(type: unknown, payload: unknown): string | null {
  if (type !== "error" || !isPlainObject(payload) || payload["fatal"] !== true) return null;
  const code = payload["code"];
  return typeof code === "string" ? code : null;
}

function ackOf(type: unknown, payload: unknown): AckInfo | null {
  if (type !== "ack" || !isPlainObject(payload)) return null;
  const { cmdId, ok, t } = payload;
  if (typeof cmdId !== "string" || typeof ok !== "boolean" || !isNonNegativeSafeInteger(t)) {
    return null;
  }
  return { cmdId, ok, t };
}

function clockTOf(type: unknown, payload: unknown): number | null {
  if ((type !== "snapshot" && type !== "events") || !isPlainObject(payload)) return null;
  const clock = payload["clock"];
  if (!isPlainObject(clock)) return null;
  const t = clock["t"];
  return isNonNegativeSafeInteger(t) ? t : null;
}

function sessionTextOf(value: unknown): string | null {
  return typeof value === "string" && value.length >= 1 && value.length <= MAX_SESSION_TEXT_LENGTH
    ? value
    : null;
}

function sessionOf(
  type: unknown,
  payload: unknown,
): { readonly stageHash: string | null; readonly build: string | null } {
  if (type !== "snapshot" || !isPlainObject(payload)) return { stageHash: null, build: null };
  const session = payload["session"];
  if (!isPlainObject(session)) return { stageHash: null, build: null };
  return { stageHash: sessionTextOf(session["stageHash"]), build: sessionTextOf(session["build"]) };
}

const UNREADABLE: ForwardMessage = {
  kind: "forward",
  seq: null,
  type: null,
  endsSession: false,
  fatalCode: null,
  ack: null,
  clockT: null,
  stageHash: null,
  build: null,
};

// 読めない電文・形が合わない値は「ない」として扱い、電文はそのままブラウザに送る
// （コンテナを信頼する。形の検査はブラウザ・Go がする）。
export function classifyContainerText(text: string): ContainerMessage {
  const parsed = parseJson(text);
  if (parsed === null || !isPlainObject(parsed.value)) return UNREADABLE;
  const envelope = parsed.value;
  const seq = seqOf(envelope);
  const type = envelope["type"];
  const payload = envelope["payload"];
  if (typeof type === "string" && isInternalType(type)) {
    if (type === "internal.result") return { kind: "result", seq, payload: payload ?? null };
    if (type === "internal.ping") return { kind: "ping", seq };
    if (type === "internal.pong") return { kind: "pong", seq };
    return { kind: "internal", seq };
  }
  const fatalCode = fatalCodeOf(type, payload);
  return {
    kind: "forward",
    seq,
    type: typeof type === "string" ? type : null,
    endsSession: fatalCode !== null && ENDING_ERROR_CODES.has(fatalCode),
    fatalCode,
    ack: ackOf(type, payload),
    clockT: clockTOf(type, payload),
    ...sessionOf(type, payload),
  };
}
