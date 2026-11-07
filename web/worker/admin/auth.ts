// 管理用 API の認証（T-309・DesignDoc §16・プロトコル §2）。
//
// ADMIN_TOKEN は 32〜256 文字の表示できる ASCII。合わない（未設定・空・短い・空白や改行を含む）
// なら管理用 API ごと無効（disabled → 404）。Authorization は "Bearer <トークン>"（Bearer の
// 大文字・小文字は区別しない。空白はちょうど 1 つ）。比較は両方の SHA-256 を定数時間で比べる。
// ここでは本文を読まない。

import type { Env } from "../routes/env.ts";

export type AdminAuthResult = "disabled" | "unauthorized" | "ok";

const ADMIN_TOKEN_PATTERN = /^[\x21-\x7E]{32,256}$/;
const AUTHORIZATION_PATTERN = /^Bearer ([\x21-\x7E]{1,256})$/i;

async function sha256(text: string): Promise<Uint8Array> {
  return new Uint8Array(await crypto.subtle.digest("SHA-256", new TextEncoder().encode(text)));
}

function constantTimeEqual(a: Uint8Array, b: Uint8Array): boolean {
  if (a.length !== b.length) return false;
  let diff = 0;
  for (let i = 0; i < a.length; i++) diff |= (a[i] ?? 0) ^ (b[i] ?? 0);
  return diff === 0;
}

export async function checkAdminAuth(request: Request, env: Env): Promise<AdminAuthResult> {
  const expected = env["ADMIN_TOKEN"];
  if (typeof expected !== "string" || !ADMIN_TOKEN_PATTERN.test(expected)) return "disabled";

  const presented = AUTHORIZATION_PATTERN.exec(request.headers.get("authorization") ?? "")?.[1];
  if (presented === undefined) return "unauthorized";

  const [expectedHash, presentedHash] = await Promise.all([sha256(expected), sha256(presented)]);
  return constantTimeEqual(expectedHash, presentedHash) ? "ok" : "unauthorized";
}
