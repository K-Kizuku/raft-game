// SPIKE_TOKEN の検査。トークンが正しく設定されていないときは、/spike/healthz 以外を 404 にする（router.ts）。
import { MIN_TOKEN_LENGTH } from "./limits.ts";

/** SPIKE_TOKEN が使える値か（32 文字以上・空白や改行を含まない） */
export function isTokenConfigured(token: string | undefined): token is string {
  return token !== undefined && token.length >= MIN_TOKEN_LENGTH && !/\s/.test(token);
}

/** `Authorization: Bearer <token>` からトークンを取り出す。書式が違えば null */
export function bearerToken(header: string | null): string | null {
  if (header === null) return null;
  const m = /^Bearer ([^\s]+)$/.exec(header);
  return m?.[1] ?? null;
}

/** 定数時間の比較（長さが違っても、長い方の長さだけ回す） */
export function timingSafeEqual(a: string, b: string): boolean {
  const enc = new TextEncoder();
  const x = enc.encode(a);
  const y = enc.encode(b);
  const n = Math.max(x.length, y.length);
  let diff = x.length ^ y.length;
  for (let i = 0; i < n; i++) {
    diff |= (x[i] ?? 0) ^ (y[i] ?? 0);
  }
  return diff === 0;
}
