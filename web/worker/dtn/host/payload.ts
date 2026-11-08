// 要求の payload の検査のうち、Actor Host が確かめるもの（actor-protocol.md §7.1 の手紙の列・Bundle の上限）。
// Go の dtnproto.Validate と同じ誤りの種類: 手紙の列が空・ID の重複・base64 の誤り・digest の不一致は invalid、
// 手紙が多すぎる・Bundle が大きすぎるのは too_large。

import { decodeBase64, DIGEST_PATTERN, sha256Hex } from "../codec.ts";
import {
  isLetterId,
  isObject,
  isSafeNonNegative,
  ProtocolError,
  type HostLimits,
  type JsonObject,
} from "../protocol.ts";

const LETTER_KINDS: ReadonlySet<string> = new Set(["deliver", "cargo.add", "unload"]);
const LETTER_ID_KINDS: ReadonlySet<string> = new Set([
  "port.remove",
  "relay.remove",
  "cargo.remove",
  "bundle.export",
]);

export interface CheckedLetter {
  readonly letterId: number;
  readonly bundle: string; // base64
  readonly digest: string;
  readonly size: number; // bytes の長さ
  readonly place: "port" | "relay" | null; // unload だけ
}

function fail(code: "invalid" | "too_large", message: string): never {
  throw new ProtocolError(code, message);
}

function countCheck(list: unknown, limits: HostLimits, name: string): readonly unknown[] {
  if (!Array.isArray(list) || list.length === 0) fail("invalid", `${name} must not be empty`);
  if (list.length > limits.maxLetters) fail("too_large", `${name}: too many letters`);
  return list as readonly unknown[];
}

// 手紙の列（deliver・cargo.add・unload の letters）。bytes の長さ・digest を確かめて返す。
export async function checkLetters(
  kind: string,
  payload: JsonObject,
  limits: HostLimits,
): Promise<CheckedLetter[]> {
  const list = countCheck(payload["letters"], limits, "letters");
  const out: CheckedLetter[] = [];
  const seen = new Set<number>();
  for (const [i, raw] of list.entries()) {
    if (!isObject(raw)) fail("invalid", `letters[${i}]`);
    const { letterId, createdAt, digest, bundle } = raw;
    if (!isLetterId(letterId) || !isSafeNonNegative(createdAt)) fail("invalid", `letters[${i}]`);
    if (seen.has(letterId)) fail("invalid", `letters[${i}]: duplicate letterId`);
    seen.add(letterId);
    if (typeof digest !== "string" || !DIGEST_PATTERN.test(digest)) {
      fail("invalid", `letters[${i}].digest`);
    }
    if (typeof bundle !== "string") fail("invalid", `letters[${i}].bundle`);
    const bytes = decodeBase64(bundle);
    if (bytes === null) fail("invalid", `letters[${i}].bundle is not base64`);
    if (bytes.byteLength > limits.maxBundleBytes) fail("too_large", `letters[${i}].bundle`);
    if ((await sha256Hex(bytes)) !== digest) fail("invalid", `letters[${i}].digest does not match`);
    let place: CheckedLetter["place"] = null;
    if (kind === "unload") {
      if (raw["place"] !== "port" && raw["place"] !== "relay")
        fail("invalid", `letters[${i}].place`);
      place = raw["place"];
    }
    out.push({ letterId, bundle, digest, size: bytes.byteLength, place });
  }
  return out;
}

// payload の検査（手紙の列と手紙の ID の列）。ほかの種類の形は Actor が確かめる。
export async function checkPayload(
  kind: string,
  payload: JsonObject,
  limits: HostLimits,
): Promise<void> {
  if (LETTER_KINDS.has(kind)) {
    await checkLetters(kind, payload, limits);
    return;
  }
  if (LETTER_ID_KINDS.has(kind)) {
    const ids = countCheck(payload["letterIds"], limits, "letterIds");
    if (!ids.every(isLetterId) || new Set(ids).size !== ids.length) fail("invalid", "letterIds");
  }
}
