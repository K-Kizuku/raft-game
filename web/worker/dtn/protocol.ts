// Actor の電文（内部 API）の型・検証・上限（DTN-12・docs/dtn/actor-protocol.md §2・§3・§6）。
// Go の server/internal/dtn/protocol（DTN-02）と同じ契約。Worker の中に手書きで持ち、
// 同じ JSON の fixture（host/fixtures/）で合わせる。

import { utf8Bytes } from "./codec.ts";

export const PROTOCOL_VERSION = 1;

// ---- 上限（actor-protocol.md §6・persistence-and-recovery.md §6）----
export const MAX_MESSAGE_BYTES = 1024 * 1024; // 1 つの電文
export const MAX_BUNDLE_BYTES = 16 * 1024; // 1 つの Bundle
export const MAX_LETTERS_PER_CALL = 16; // 1 回の deliver・cargo.add・transfer の手紙
export const MAX_OUTPUTS = 4096; // 1 回の応答の outputs
export const MAX_JOURNAL_ROWS = 20_000; // 1 Actor の journal の行
export const MAX_ACTOR_STORAGE_BYTES = 8 * 1024 * 1024; // 1 Actor の保存の合計
export const MAX_CONTAINER_STARTS = 4; // 1 Actor の Container の起動（最初の 1 回＋復元 3 回）
export const MAX_RESTORES_PER_GAME = 6; // 1 ゲームの Actor の復元の合計
export const MAX_RESTORE_MS = 60_000; // 1 回の復元の実時間
export const MAX_RESTORE_TOTAL_MS = 180_000; // 1 ゲームの Actor の復元の実時間の合計
export const MAX_COORDINATOR_RESTARTS = 3; // 1 ゲームの Coordinator の作り直し
export const MAX_COORD_JOURNAL_ROWS = 50_000; // Coordinator の journal（Session Front）
export const MAX_COORD_JOURNAL_BYTES = 16 * 1024 * 1024;
export const CONTAINER_RESPONSE_TIMEOUT_MS = 10_000; // Container の応答を待つ実時間の上限（§3.1 の 4）
export const CONTAINER_PORT = 8080; // env PORT
export const IMAGE_NAME = "dtnactor"; // ctx.container.images["dtnactor"]
export const CLEANUP_RETRY_MS = 30_000; // 止められなかったものを再試行する間隔（environment.md §4）

// Actor Host の上限（fixture の limits で上書きできる。persistence-and-recovery.md §7.3）。
export interface HostLimits {
  readonly maxMessageBytes: number;
  readonly maxBundleBytes: number;
  readonly maxLetters: number;
  readonly maxOutputs: number;
  readonly maxJournalRows: number;
  readonly maxStoredBytes: number;
}

export const DEFAULT_HOST_LIMITS: HostLimits = {
  maxMessageBytes: MAX_MESSAGE_BYTES,
  maxBundleBytes: MAX_BUNDLE_BYTES,
  maxLetters: MAX_LETTERS_PER_CALL,
  maxOutputs: MAX_OUTPUTS,
  maxJournalRows: MAX_JOURNAL_ROWS,
  maxStoredBytes: MAX_ACTOR_STORAGE_BYTES,
};

// ---- 種類（§3）----
// Actor（Container）が受ける
export const CONTAINER_KINDS: ReadonlySet<string> = new Set([
  "init",
  "advance",
  "deliver",
  "port.remove",
  "unload",
  "relay.remove",
  "arrive",
  "depart",
  "cargo.add",
  "cargo.remove",
  "bundle.export",
  "inspect",
]);
// Actor Host だけが処理する（Container には送らない）。transfer.accept は /cla/accept だけで受ける
export const HOST_KINDS: ReadonlySet<string> = new Set([
  "contact.open",
  "contact.close",
  "transfer.prepare",
  "transfer.activate",
  "transfer.gc",
  "transfer.abort",
  "transfer.status",
  "host.status",
]);
// journal に入れない（状態を変えない。persistence-and-recovery.md §2）
export const NOT_JOURNALED: ReadonlySet<string> = new Set([
  "bundle.export",
  "inspect",
  "transfer.status",
  "host.status",
]);
// epoch の照合をしない（新しい epoch を知るための問い合わせ）
export const EPOCH_FREE: ReadonlySet<string> = new Set(["transfer.status", "host.status"]);
// transfer.activate の then に使える種類（受け取り側）と transfer.gc の then（送り元）
export const ACTIVATE_THEN: ReadonlySet<string> = new Set(["deliver", "cargo.add", "unload"]);
export const GC_THEN: ReadonlySet<string> = new Set([
  "port.remove",
  "relay.remove",
  "cargo.remove",
]);

// ---- エラー（§6）----
export type ErrorCode =
  | "invalid"
  | "unsupported_version"
  | "session_mismatch"
  | "unknown_actor"
  | "stale_epoch"
  | "stale_coord_epoch"
  | "stale_event"
  | "conflict"
  | "too_large"
  | "not_ready"
  | "contact_closed"
  | "contact_mismatch"
  | "restore_failed"
  | "internal";

export class ProtocolError extends Error {
  readonly code: ErrorCode;
  constructor(code: ErrorCode, message: string = code) {
    super(message);
    this.code = code;
    this.name = "ProtocolError";
  }
}

// ---- 型（§2）----
export type JsonObject = Readonly<Record<string, unknown>>;

export interface Envelope {
  readonly v: number;
  readonly sessionId: string;
  readonly actor: string;
  readonly epoch: number;
  readonly coordEpoch: number;
  readonly eventSeq: number;
  readonly kind: string;
  readonly payload: JsonObject;
  readonly replay?: boolean;
}

export interface Output {
  readonly t: number;
  readonly class: number;
  readonly seq: number;
  readonly type: string;
  readonly data: JsonObject;
  readonly bundle?: string; // Actor → Host の応答にだけある（Host が取り除く）
}

export interface ResponseError {
  readonly code: string;
  readonly message: string;
}

export interface ResponseEnvelope {
  readonly v: number;
  readonly actor: string;
  readonly epoch: number;
  readonly eventSeq: number;
  readonly status: "ok" | "error";
  readonly outputs: readonly Output[];
  readonly cursor?: unknown;
  readonly error: ResponseError | null;
  readonly result?: unknown; // Host だけで処理する種類の結果（transfer.*・host.status。DTN-12 で足した）
}

export function isObject(value: unknown): value is JsonObject {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

export function isSafeNonNegative(value: unknown): value is number {
  return typeof value === "number" && Number.isSafeInteger(value) && value >= 0;
}

export const SESSION_ID_PATTERN = /^[A-Za-z0-9._-]{1,256}$/;
export const ACTOR_PATTERN = /^(coordinator|island:[1-9][0-9]{0,2}|ferry:[A-Za-z0-9_-]{1,32})$/;
export const TRANSFER_ID_PATTERN = /^[A-Za-z0-9._:-]{1,64}$/;
export const CONTACT_ID_PATTERN = /^[A-Za-z0-9._:-]{1,64}$/;

export type ActorRole = "coordinator" | "island" | "ferry";

export function roleOf(actor: string): ActorRole {
  if (actor === "coordinator") return "coordinator";
  return actor.startsWith("island:") ? "island" : "ferry";
}

// 要求のエンベロープの形を確かめる。版が違えば unsupported_version、形が違えば invalid。
export function parseEnvelope(value: unknown): Envelope {
  if (!isObject(value)) throw new ProtocolError("invalid", "envelope is not an object");
  if (value["v"] !== PROTOCOL_VERSION) {
    throw new ProtocolError("unsupported_version", "unsupported version");
  }
  const { sessionId, actor, epoch, coordEpoch, eventSeq, kind, payload, replay } = value;
  if (typeof sessionId !== "string" || !SESSION_ID_PATTERN.test(sessionId)) {
    throw new ProtocolError("invalid", "sessionId");
  }
  if (typeof actor !== "string" || !ACTOR_PATTERN.test(actor)) {
    throw new ProtocolError("invalid", "actor");
  }
  if (!isSafeNonNegative(epoch) || !isSafeNonNegative(coordEpoch) || !isSafeNonNegative(eventSeq)) {
    throw new ProtocolError("invalid", "epoch/coordEpoch/eventSeq");
  }
  if (typeof kind !== "string" || kind === "") throw new ProtocolError("invalid", "kind");
  if (!isObject(payload)) throw new ProtocolError("invalid", "payload");
  if (replay !== undefined && typeof replay !== "boolean") {
    throw new ProtocolError("invalid", "replay");
  }
  return {
    v: PROTOCOL_VERSION,
    sessionId,
    actor,
    epoch,
    coordEpoch,
    eventSeq,
    kind,
    payload,
    ...(replay === undefined ? {} : { replay }),
  };
}

// 要求の digest の対象（§5: epoch・coordEpoch・replay を除く）。
export function requestDigestBody(env: Envelope): JsonObject {
  return {
    v: env.v,
    sessionId: env.sessionId,
    actor: env.actor,
    eventSeq: env.eventSeq,
    kind: env.kind,
    payload: env.payload,
  };
}

// 足りない t・class・seq は 0 とみなす（Go の dtnproto.Output のゼロ値と同じ）。
function parseOutput(value: unknown): Output {
  if (!isObject(value)) throw new ProtocolError("invalid", "output");
  const { type, data, bundle } = value;
  const t = value["t"] ?? 0;
  const seq = value["seq"] ?? 0;
  const cls = value["class"] ?? 0;
  if (!isSafeNonNegative(t) || !isSafeNonNegative(cls) || !isSafeNonNegative(seq)) {
    throw new ProtocolError("invalid", "output t/class/seq");
  }
  if (typeof type !== "string" || type === "" || !isObject(data)) {
    throw new ProtocolError("invalid", "output type/data");
  }
  if (bundle !== undefined && typeof bundle !== "string") {
    throw new ProtocolError("invalid", "output bundle");
  }
  return { t, class: cls, seq, type, data, ...(bundle === undefined ? {} : { bundle }) };
}

// 応答のエンベロープの形を確かめる（Container の応答・Host の応答の両方）。
export function parseResponse(value: unknown): ResponseEnvelope {
  if (!isObject(value)) throw new ProtocolError("invalid", "response is not an object");
  if (value["v"] !== PROTOCOL_VERSION) {
    throw new ProtocolError("unsupported_version", "unsupported version");
  }
  const { actor, epoch, eventSeq, status, outputs, error } = value;
  if (typeof actor !== "string" || !ACTOR_PATTERN.test(actor)) {
    throw new ProtocolError("invalid", "response actor");
  }
  if (!isSafeNonNegative(epoch) || !isSafeNonNegative(eventSeq)) {
    throw new ProtocolError("invalid", "response epoch/eventSeq");
  }
  if (status !== "ok" && status !== "error") throw new ProtocolError("invalid", "status");
  const list = outputs === undefined || outputs === null ? [] : outputs;
  if (!Array.isArray(list)) throw new ProtocolError("invalid", "outputs");
  if (list.length > MAX_OUTPUTS) throw new ProtocolError("too_large", "outputs");
  let parsedError: ResponseError | null = null;
  if (error !== undefined && error !== null) {
    if (!isObject(error) || typeof error["code"] !== "string") {
      throw new ProtocolError("invalid", "error");
    }
    parsedError = {
      code: error["code"],
      message: typeof error["message"] === "string" ? error["message"] : error["code"],
    };
  }
  if (status === "error" && parsedError === null) throw new ProtocolError("invalid", "error");
  return {
    v: PROTOCOL_VERSION,
    actor,
    epoch,
    eventSeq,
    status,
    outputs: (list as readonly unknown[]).map(parseOutput),
    ...(Object.hasOwn(value, "cursor") ? { cursor: value["cursor"] } : {}),
    error: parsedError,
    ...(Object.hasOwn(value, "result") ? { result: value["result"] } : {}),
  };
}

export function errorResponse(
  base: { readonly actor: string; readonly epoch: number; readonly eventSeq: number },
  code: ErrorCode,
  message: string = code,
): ResponseEnvelope {
  return {
    v: PROTOCOL_VERSION,
    actor: base.actor,
    epoch: base.epoch,
    eventSeq: base.eventSeq,
    status: "error",
    outputs: [],
    cursor: null,
    error: { code, message },
  };
}

export function okResponse(
  base: { readonly actor: string; readonly epoch: number; readonly eventSeq: number },
  result?: unknown,
): ResponseEnvelope {
  return {
    v: PROTOCOL_VERSION,
    actor: base.actor,
    epoch: base.epoch,
    eventSeq: base.eventSeq,
    status: "ok",
    outputs: [],
    cursor: null,
    error: null,
    ...(result === undefined ? {} : { result }),
  };
}

// 応答から Bundle の bytes（outputs[].bundle）を取り除く（Session Front と Coordinator には digest と長さだけを返す）。
export function stripBundles(response: ResponseEnvelope): ResponseEnvelope {
  return {
    ...response,
    cursor: response.cursor ?? null,
    outputs: response.outputs.map((output) => ({
      t: output.t,
      class: output.class,
      seq: output.seq,
      type: output.type,
      data: output.data,
    })),
  };
}

// 応答の digest の対象（Go の dtnproto.ResponseDigest と同じ: {v, actor, eventSeq, status, outputs, cursor, error}。
// epoch と outputs[].bundle を含めない。actor-protocol.md §7.2）。
export function responseDigestBody(response: ResponseEnvelope): JsonObject {
  const stripped = stripBundles(response);
  return {
    v: stripped.v,
    actor: stripped.actor,
    eventSeq: stripped.eventSeq,
    status: stripped.status,
    outputs: stripped.outputs,
    cursor: stripped.cursor ?? null,
    error: stripped.error,
  };
}

export function exceedsMessageLimit(text: string): boolean {
  return text.length > MAX_MESSAGE_BYTES || utf8Bytes(text) > MAX_MESSAGE_BYTES;
}

// 手紙の ID（islandId × 10^9 + n）。JSON の安全な整数。
export function isLetterId(value: unknown): value is number {
  return typeof value === "number" && Number.isSafeInteger(value) && value >= 1;
}

export function parseLetterIds(value: unknown): number[] {
  if (!Array.isArray(value) || value.length < 1 || value.length > MAX_LETTERS_PER_CALL) {
    throw new ProtocolError("invalid", "letterIds");
  }
  const ids = value as readonly unknown[];
  if (!ids.every(isLetterId) || new Set(ids).size !== ids.length) {
    throw new ProtocolError("invalid", "letterIds");
  }
  return [...(ids as readonly number[])];
}

// 出力の手紙の ID（data.letterId）。なければ null。
export function outputLetterId(output: Output): number | null {
  const id = output.data["letterId"];
  return isLetterId(id) ? id : null;
}

// 出力の Bundle の bytes（outputs[].bundle。actor-protocol.md §2.2）。
export function outputBundle(output: Output): string | null {
  return typeof output.bundle === "string" ? output.bundle : null;
}
