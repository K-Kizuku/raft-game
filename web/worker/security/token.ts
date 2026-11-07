// 接続用のトークン（T-310・プロトコル §2）。
//
// token は base64url(乱数 16 バイト)（22 文字）。Worker と Durable Object はトークンそのものを保存せず、
// hashSessionToken の値（SHA-256 の 16 進の小文字）だけを Durable Object に渡す。
// T-308 は ?token= が SESSION_TOKEN_PATTERN に合うことを確かめてからハッシュし、保存した値と
// 定数時間で比べる。

import { encodeBase64Url } from "./base64url.ts";

export const SESSION_TOKEN_PATTERN = /^[A-Za-z0-9_-]{22}$/;
const SESSION_TOKEN_BYTES = 16;

// 呼ぶのは issueSession（ports.ts）だけ。
export function createSessionToken(randomBytes: (length: number) => Uint8Array): string {
  const random = randomBytes(SESSION_TOKEN_BYTES);
  if (random.length !== SESSION_TOKEN_BYTES) {
    throw new RangeError("randomBytes returned a wrong length");
  }
  return encodeBase64Url(random);
}

export async function hashSessionToken(token: string): Promise<string> {
  const digest = new Uint8Array(
    await crypto.subtle.digest("SHA-256", new TextEncoder().encode(token)),
  );
  let hex = "";
  for (const byte of digest) hex += byte.toString(16).padStart(2, "0");
  return hex;
}
