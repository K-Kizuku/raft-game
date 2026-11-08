// セキュリティの部品（T-310・ADR-0018 G2・G3・G4）。T-307 の既定の配線（常に undefined）を置き換える。
//
// SESSION_SIGNING_KEY が規則に合わないなら部品ごと undefined にする（ルーターは POST /api/sessions を
// 503、/ws/sessions/{id} を 404 にする）。sessionId の署名の確認には、それを外す引数・設定・
// 環境変数を作らない。ほかのバインディング（TURNSTILE_SECRET・SESSION_RATE_LIMIT・COST_GUARD）が
// なくても部品は返し、使うときに拒否する側に倒す。
//
// verifySessionId は署名と期限だけで判定し、設定・Durable Object・fetch を使わない。
// このディレクトリはログを出さない。

import type { Env } from "../routes/env.ts";
import type { RequestContext, SecurityPorts } from "../routes/ports.ts";
import {
  createGuardSettingsSource,
  type GuardSettingsSource,
  type SecurityGuards,
} from "./guard-settings.ts";
import { checkSessionRateLimit } from "./rate-limit.ts";
import { createSessionSigner } from "./session-id.ts";
import { createSessionToken, hashSessionToken } from "./token.ts";
import { verifyTurnstileToken } from "./turnstile.ts";

const SIGNING_KEY_PATTERN = /^[\x21-\x7E]{32,256}$/;
const GUARD_SETTINGS_TTL_MS = 5000;

export interface SecurityDeps {
  readonly now: () => number; // UNIX ミリ秒
  readonly randomBytes: (length: number) => Uint8Array;
  readonly fetch: (request: Request) => Promise<Response>; // Turnstile の siteverify だけに使う
  readonly guards: GuardSettingsSource;
}

function signingKey(env: Env): string | undefined {
  const key = env["SESSION_SIGNING_KEY"];
  return typeof key === "string" && SIGNING_KEY_PATTERN.test(key) ? key : undefined;
}

export function createSecurityPortsWith(env: Env, deps: SecurityDeps): SecurityPorts | undefined {
  const key = signingKey(env);
  if (key === undefined) return undefined;
  const signer = createSessionSigner(key, { now: deps.now, randomBytes: deps.randomBytes });

  // 1 つの要求の中では設定を 1 回だけ読む（checkRateLimit・verifyTurnstile・issueSession で共有）。
  const perRequest = new WeakMap<Request, Promise<SecurityGuards>>();
  const loadGuards = (ctx: RequestContext): Promise<SecurityGuards> => {
    let loaded = perRequest.get(ctx.request);
    if (loaded === undefined) {
      loaded = deps.guards.load(ctx.env);
      perRequest.set(ctx.request, loaded);
    }
    return loaded;
  };

  return {
    async checkRateLimit(ctx) {
      const guards = await loadGuards(ctx);
      return checkSessionRateLimit({
        enabled: guards.rateLimitEnabled,
        env: ctx.env,
        clientIp: ctx.clientIp,
      });
    },

    async verifyTurnstile(token, ctx) {
      const guards = await loadGuards(ctx);
      return verifyTurnstileToken({
        enabled: guards.turnstileEnabled,
        env: ctx.env,
        token,
        clientIp: ctx.clientIp,
        fetch: deps.fetch,
      });
    },

    async issueSession(ctx) {
      const guards = await loadGuards(ctx);
      const sessionId = await signer.issue(ctx.url.host, guards.maxMinutes);
      const token = createSessionToken(deps.randomBytes);
      return { sessionId, token, tokenHash: await hashSessionToken(token) };
    },

    verifySessionId(raw, ctx) {
      return signer.verify(raw, ctx.url.host);
    },
  };
}

function randomBytes(length: number): Uint8Array {
  return crypto.getRandomValues(new Uint8Array(length));
}

// 本番の部品。設定のキャッシュは isolate の中で 1 つだけ。
const productionGuards = createGuardSettingsSource({
  ttlMs: GUARD_SETTINGS_TTL_MS,
  now: () => Date.now(),
});

export const createSecurityPorts: (env: Env) => SecurityPorts | undefined = (env) =>
  createSecurityPortsWith(env, {
    now: () => Date.now(),
    randomBytes,
    fetch: (request) => globalThis.fetch(request),
    guards: productionGuards,
  });
