// Turnstile のトークンの検証（T-310・ADR-0018 G4・プロトコル §2）。
//
// siteverify の応答は success === true（真偽値）だけを確かめる。hostname・action・cdata は
// 確かめない（テスト用のキーでは hostname が常に example.com で、action を返さないため。
// ホスト名の制限はウィジェットの設定で行う）。失敗の理由で区別せず、例外を投げない。
// シークレット・トークンを結果にも例外にも入れない。

import type { Env } from "../routes/env.ts";

export const TURNSTILE_SITEVERIFY_URL = "https://challenges.cloudflare.com/turnstile/v0/siteverify";
const TURNSTILE_TIMEOUT_MS = 5000;

const TURNSTILE_SECRET_PATTERN = /^[\x21-\x7E]{1,256}$/;

export type TurnstileResult = "ok" | "turnstile_failed";

export interface TurnstileInput {
  readonly enabled: boolean; // turnstile.enabled（CostGuard の設定）
  readonly env: Env;
  readonly token: string | undefined;
  readonly clientIp: string | null;
  readonly fetch: (request: Request) => Promise<Response>;
}

function turnstileSecret(env: Env): string | undefined {
  const secret = env["TURNSTILE_SECRET"];
  return typeof secret === "string" && TURNSTILE_SECRET_PATTERN.test(secret) ? secret : undefined;
}

export async function verifyTurnstileToken(input: TurnstileInput): Promise<TurnstileResult> {
  if (!input.enabled) return "ok";
  const secret = turnstileSecret(input.env);
  if (secret === undefined) return "turnstile_failed";
  if (input.token === undefined || input.token === "") return "turnstile_failed";

  const body: Record<string, string> = { secret, response: input.token };
  if (input.clientIp !== null) body["remoteip"] = input.clientIp;

  try {
    const response = await input.fetch(
      new Request(TURNSTILE_SITEVERIFY_URL, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify(body),
        signal: AbortSignal.timeout(TURNSTILE_TIMEOUT_MS),
      }),
    );
    if (response.status !== 200) return "turnstile_failed";
    const result: unknown = await response.json();
    if (typeof result !== "object" || result === null || Array.isArray(result)) {
      return "turnstile_failed";
    }
    return "success" in result && result.success === true ? "ok" : "turnstile_failed";
  } catch {
    return "turnstile_failed";
  }
}
