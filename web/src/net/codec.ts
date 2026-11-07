// 電文の読み書き（T-402 / プロトコル §3〜§5・§8）。
// サーバーが正（ADR-0009）なので、エンベロープと、ハンドラーに渡すのに要る最小限の形だけを確かめる。
import { Version } from "../protocol/index.ts";
import type { ClientMessage, ServerMessage } from "../protocol/index.ts";

export type ClientMessageType = ClientMessage["type"];
export type ClientPayload<T extends ClientMessageType> = Extract<
  ClientMessage,
  { type: T }
>["payload"];

// encodeClientMessage はクライアント → サーバーの電文を JSON にする。
// キーの順は v・type・cmdId（あるときだけ）・payload。末尾に改行を付けない。
export function encodeClientMessage<T extends ClientMessageType>(
  type: T,
  payload: ClientPayload<T>,
  cmdId?: string,
): string {
  const message: Record<string, unknown> = { v: Version, type };
  if (cmdId !== undefined) {
    message["cmdId"] = cmdId;
  }
  message["payload"] = payload;
  return JSON.stringify(message);
}

export type DecodeResult =
  | { kind: "message"; message: ServerMessage }
  | { kind: "unknown"; seq: number; type: string } // 知らない type（プロトコル §8）。seq は数える
  | { kind: "version_mismatch" }
  | { kind: "malformed" };

type Obj = Record<string, unknown>;

const SERVER_TYPES: ReadonlySet<string> = new Set([
  "snapshot",
  "events",
  "ack",
  "node.detail",
  "round.result",
  "error",
  "pong",
]);

function isObject(value: unknown): value is Obj {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function isNonNegInt(value: unknown): value is number {
  return Number.isSafeInteger(value) && (value as number) >= 0;
}

function isPosInt(value: unknown): value is number {
  return Number.isSafeInteger(value) && (value as number) >= 1;
}

function isNumber(value: unknown): value is number {
  return typeof value === "number";
}

function isClock(value: unknown): value is Obj {
  return (
    isObject(value) &&
    isNonNegInt(value["t"]) &&
    isNonNegInt(value["endsAt"]) &&
    typeof value["speed"] === "number" &&
    Number.isFinite(value["speed"]) &&
    value["speed"] > 0 &&
    typeof value["paused"] === "boolean"
  );
}

function isSnapshot(p: Obj): boolean {
  if (p["protocol"] !== Version || !isNonNegInt(p["lastSeq"]) || typeof p["phase"] !== "string") {
    return false;
  }
  const clock = p["clock"];
  if (!isClock(clock)) {
    return false;
  }
  const speeds = clock["allowedSpeeds"];
  if (!Array.isArray(speeds) || !speeds.every(isNumber)) {
    return false;
  }
  const stage = p["stage"];
  if (!isObject(p["session"]) || !isObject(stage) || !isObject(p["score"])) {
    return false;
  }
  const arrays: unknown[] = [
    stage["islands"],
    stage["routes"],
    stage["resources"],
    p["ferries"],
    p["routesState"],
    p["letters"],
    p["requests"],
    p["nodes"],
    p["islandsState"],
  ];
  return arrays.every((a) => Array.isArray(a));
}

function isEventsPayload(p: Obj): boolean {
  if (!isClock(p["clock"])) {
    return false;
  }
  const events = p["events"];
  if (!Array.isArray(events)) {
    return false;
  }
  return events.every(
    (e: unknown) =>
      isObject(e) &&
      isPosInt(e["seq"]) &&
      isNonNegInt(e["t"]) &&
      typeof e["type"] === "string" &&
      e["type"] !== "",
  );
}

function hasPayloadShape(type: string, p: Obj): boolean {
  switch (type) {
    case "snapshot":
      return isSnapshot(p);
    case "events":
      return isEventsPayload(p);
    case "ack":
      return typeof p["cmdId"] === "string" && typeof p["ok"] === "boolean" && isNumber(p["t"]);
    case "node.detail":
      return isNumber(p["id"]);
    case "round.result":
      return isObject(p["score"]);
    case "error":
      return (
        typeof p["code"] === "string" &&
        typeof p["message"] === "string" &&
        typeof p["fatal"] === "boolean"
      );
    case "pong":
      return isNumber(p["clientTime"]) && isNumber(p["simTime"]);
    default:
      return false;
  }
}

// decodeServerMessage はサーバー → クライアントの電文を読む。例外を投げない。
export function decodeServerMessage(data: unknown): DecodeResult {
  if (typeof data !== "string") {
    return { kind: "malformed" };
  }
  let value: unknown;
  try {
    value = JSON.parse(data);
  } catch {
    return { kind: "malformed" };
  }
  if (!isObject(value)) {
    return { kind: "malformed" };
  }
  if (value["v"] !== Version) {
    return { kind: "version_mismatch" };
  }
  const type = value["type"];
  const seq = value["seq"];
  const payload = value["payload"];
  if (typeof type !== "string" || type === "" || !isPosInt(seq) || !isObject(payload)) {
    return { kind: "malformed" };
  }
  if (!SERVER_TYPES.has(type)) {
    return { kind: "unknown", seq, type };
  }
  if (!hasPayloadShape(type, payload)) {
    return { kind: "malformed" };
  }
  return { kind: "message", message: value as unknown as ServerMessage };
}
