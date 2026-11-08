// DTN の電文の符号化（DTN-12・docs/dtn/actor-protocol.md §1）。
//
// - 正準の JSON: RFC 8785（JSON Canonicalization Scheme）。キーは UTF-16 の符号単位の順、数は ECMAScript の
//   Number の文字列化（JSON.stringify と同じ）、文字列は JSON.stringify のエスケープ
// - digest: "sha256:" ＋ 16 進の小文字
// - Bundle の bytes: base64（RFC 4648 §4、パディングあり）
// - Contact の署名: base64url（RFC 4648 §5、パディングなし）の HMAC-SHA256

import { decodeBase64Url, encodeBase64Url } from "../security/base64url.ts";

const LONE_SURROGATE = /[\uD800-\uDBFF](?![\uDC00-\uDFFF])|(?<![\uD800-\uDBFF])[\uDC00-\uDFFF]/;

// RFC 8785 の正準の JSON。undefined の値を持つキーは JSON.stringify と同じく除く。
// DTN の電文の数は安全な整数だけ（Go の dtnproto と同じ。actor-protocol.md §7.1）。それ以外の数・
// 孤立したサロゲート・関数などは TypeError。
export function canonicalJson(value: unknown): string {
  if (value === null) return "null";
  switch (typeof value) {
    case "boolean":
      return value ? "true" : "false";
    case "number":
      if (!Number.isSafeInteger(value)) throw new TypeError("canonicalJson: not a safe integer");
      return JSON.stringify(value);
    case "string":
      if (LONE_SURROGATE.test(value)) throw new TypeError("canonicalJson: lone surrogate");
      return JSON.stringify(value);
    case "object": {
      if (Array.isArray(value)) {
        return `[${value.map((item: unknown) => canonicalJson(item ?? null)).join(",")}]`;
      }
      const obj = value as Readonly<Record<string, unknown>>;
      const parts: string[] = [];
      for (const key of Object.keys(obj).sort()) {
        const item = obj[key];
        if (item === undefined) continue;
        parts.push(`${canonicalJson(key)}:${canonicalJson(item)}`);
      }
      return `{${parts.join(",")}}`;
    }
    default:
      throw new TypeError(`canonicalJson: unsupported ${typeof value}`);
  }
}

function hex(bytes: Uint8Array): string {
  let out = "";
  for (const b of bytes) out += b.toString(16).padStart(2, "0");
  return out;
}

export async function sha256Hex(data: Uint8Array<ArrayBuffer>): Promise<string> {
  return `sha256:${hex(new Uint8Array(await crypto.subtle.digest("SHA-256", data)))}`;
}

export function utf8(text: string): Uint8Array<ArrayBuffer> {
  return new TextEncoder().encode(text);
}

export function utf8Bytes(text: string): number {
  return utf8(text).byteLength;
}

// 値の正準の JSON の SHA-256。
export function digestOf(value: unknown): Promise<string> {
  return sha256Hex(utf8(canonicalJson(value)));
}

export const DIGEST_PATTERN = /^sha256:[0-9a-f]{64}$/;

// base64（パディングあり）。
export function encodeBase64(bytes: Uint8Array): string {
  const url = encodeBase64Url(bytes).replaceAll("-", "+").replaceAll("_", "/");
  return url + "=".repeat((4 - (url.length % 4)) % 4);
}

const BASE64_PATTERN = /^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$/;

// 厳密な base64 の復号（符号化し直すと元に戻るものだけ）。不正なら null。
export function decodeBase64(text: string): Uint8Array<ArrayBuffer> | null {
  if (!BASE64_PATTERN.test(text)) return null;
  const bytes = decodeBase64Url(text.replaceAll("+", "-").replaceAll("/", "_").replace(/=+$/, ""));
  if (bytes === null || encodeBase64(bytes) !== text) return null;
  return bytes;
}

type HmacKey = Awaited<ReturnType<typeof crypto.subtle.importKey>>;

export interface Hmac {
  sign(text: string): Promise<string>; // base64url
  verify(text: string, signature: string): Promise<boolean>;
}

export function createHmac(key: string): Hmac {
  let keyPromise: Promise<HmacKey> | undefined;
  const hmacKey = (): Promise<HmacKey> => {
    keyPromise ??= crypto.subtle.importKey(
      "raw",
      utf8(key),
      { name: "HMAC", hash: "SHA-256" },
      false,
      ["sign", "verify"],
    );
    return keyPromise;
  };
  return {
    async sign(text) {
      const sig = await crypto.subtle.sign("HMAC", await hmacKey(), utf8(text));
      return encodeBase64Url(new Uint8Array(sig));
    },
    async verify(text, signature) {
      const sig = decodeBase64Url(signature);
      if (sig?.length !== 32) return false;
      return crypto.subtle.verify("HMAC", await hmacKey(), sig, utf8(text));
    },
  };
}

// 厳密な JSON の読み込み（Go の dtnproto の読み込みと同じ規則）: 重複したキー（エスケープの違いを含む）・
// 小数・指数・安全でない整数・孤立したサロゲートは SyntaxError。ほかは JSON.parse の規則。
export function parseStrictJson(text: string): unknown {
  const value: unknown = JSON.parse(text); // 文法の誤り（末尾のカンマ・制御文字・先頭の 0 など）
  let pos = 0;
  const ws = () => {
    while (pos < text.length && " \t\n\r".includes(text.charAt(pos))) pos += 1;
  };
  const str = (): string => {
    const start = pos;
    pos += 1;
    while (text.charAt(pos) !== '"') pos += text.charAt(pos) === "\\" ? 2 : 1;
    pos += 1;
    const decoded = JSON.parse(text.slice(start, pos)) as string;
    if (LONE_SURROGATE.test(decoded)) throw new SyntaxError("lone surrogate");
    return decoded;
  };
  const walk = (): void => {
    ws();
    const c = text.charAt(pos);
    if (c === "{") {
      pos += 1;
      const keys = new Set<string>();
      ws();
      if (text.charAt(pos) === "}") {
        pos += 1;
        return;
      }
      for (;;) {
        ws();
        const key = str();
        if (keys.has(key)) throw new SyntaxError(`duplicate key ${JSON.stringify(key)}`);
        keys.add(key);
        ws();
        pos += 1; // :
        walk();
        ws();
        const next = text.charAt(pos);
        pos += 1;
        if (next === "}") return;
      }
    }
    if (c === "[") {
      pos += 1;
      ws();
      if (text.charAt(pos) === "]") {
        pos += 1;
        return;
      }
      for (;;) {
        walk();
        ws();
        const next = text.charAt(pos);
        pos += 1;
        if (next === "]") return;
      }
    }
    if (c === '"') {
      str();
      return;
    }
    const m = /^-?[0-9]+(\.[0-9]+)?([eE][+-]?[0-9]+)?/.exec(text.slice(pos));
    if (m !== null) {
      if (m[1] !== undefined || m[2] !== undefined) throw new SyntaxError("not an integer");
      if (!Number.isSafeInteger(Number(m[0]))) throw new SyntaxError("unsafe integer");
      pos += m[0].length;
      return;
    }
    const lit = /^(true|false|null)/.exec(text.slice(pos));
    pos += lit?.[0].length ?? 1;
  };
  walk();
  return value;
}
