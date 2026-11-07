import { describe, expect, it } from "vitest";

import type { Env } from "../routes/env.ts";
import type { RequestContext } from "../routes/ports.ts";
import type { GuardSettingsSource, SecurityGuards } from "./guard-settings.ts";
import { createSecurityPorts, createSecurityPortsWith, type SecurityDeps } from "./ports.ts";
import { createSessionSigner } from "./session-id.ts";
import {
  EXPIRES,
  flipChar,
  HOST,
  ISSUED_ID,
  ISSUED_TOKEN,
  ISSUED_TOKEN_HASH,
  KEY,
  NOW,
  sequentialRandom,
} from "./test-helpers.ts";

const GUARDS_30: SecurityGuards = {
  turnstileEnabled: true,
  rateLimitEnabled: true,
  maxMinutes: 30,
};

function fixedGuards(value: SecurityGuards = GUARDS_30) {
  const loads: Env[] = [];
  const source: GuardSettingsSource = {
    load: async (env) => {
      loads.push(env);
      return value;
    },
  };
  return { source, loads };
}

function depsAt(now: () => number, guards: GuardSettingsSource = fixedGuards().source) {
  const random = sequentialRandom();
  const fetchCalls: Request[] = [];
  const deps: SecurityDeps = {
    now,
    randomBytes: random.randomBytes,
    fetch: async (request) => {
      fetchCalls.push(request);
      return new Response('{"success":true}');
    },
    guards,
  };
  return { deps, random, fetchCalls };
}

function ctxFor(env: Env, url = `https://${HOST}/ws/sessions/x`): RequestContext {
  const request = new Request(url);
  return { request, url: new URL(url), env, clientIp: "203.0.113.5" };
}

describe("P-1 createSecurityPortsWith", () => {
  it.each([
    ["未設定", {}],
    ['""', { SESSION_SIGNING_KEY: "" }],
    ["31 文字", { SESSION_SIGNING_KEY: "k".repeat(31) }],
    ["257 文字", { SESSION_SIGNING_KEY: "k".repeat(257) }],
    ["空白を含む", { SESSION_SIGNING_KEY: `${"k".repeat(20)} ${"k".repeat(20)}` }],
    ["末尾に改行", { SESSION_SIGNING_KEY: `${KEY}\n` }],
    ["タブを含む", { SESSION_SIGNING_KEY: `${KEY}\t` }],
    ["ASCII 以外を含む", { SESSION_SIGNING_KEY: `${KEY}é` }],
    ["123", { SESSION_SIGNING_KEY: 123 }],
    ["SIGNATURE_DISABLED があっても", { SIGNATURE_DISABLED: "1", SKIP_SIGNATURE: "true" }],
  ])("%s → undefined", (_name, env: Env) => {
    expect(createSecurityPortsWith(env, depsAt(() => NOW).deps)).toBeUndefined();
  });

  it.each([
    ["32 文字", "k".repeat(32)],
    ["256 文字", "k".repeat(256)],
    ["KEY", KEY],
  ])("%s → 部品が返る（ほかのバインディングがなくても）", (_name, key) => {
    const ports = createSecurityPortsWith({ SESSION_SIGNING_KEY: key }, depsAt(() => NOW).deps);
    expect(ports).toBeDefined();
  });
});

describe("P-2 issueSession", () => {
  it("固定値で既知の sessionId・token・tokenHash", async () => {
    const { deps, random } = depsAt(() => NOW);
    const ports = createSecurityPortsWith({ SESSION_SIGNING_KEY: KEY }, deps);
    const issued = await ports?.issueSession(ctxFor({}, `https://${HOST}/api/sessions`));
    expect(issued).toEqual({
      sessionId: ISSUED_ID,
      token: ISSUED_TOKEN,
      tokenHash: ISSUED_TOKEN_HASH,
    });
    expect(random.lengths).toEqual([16, 16]);
  });

  it("ctx.url のホストが署名に入る", async () => {
    const { deps } = depsAt(() => NOW);
    const ports = createSecurityPortsWith({ SESSION_SIGNING_KEY: KEY }, deps);
    const issued = await ports?.issueSession(ctxFor({}, "https://raft-demo.example/api/sessions"));
    expect(issued?.sessionId).not.toBe(ISSUED_ID);
    const ok = await ports?.verifySessionId(
      issued?.sessionId ?? "",
      ctxFor({}, "https://raft-demo.example/ws/sessions/x"),
    );
    expect(ok).toBe(issued?.sessionId);
    expect(await ports?.verifySessionId(issued?.sessionId ?? "", ctxFor({}))).toBeNull();
  });

  it("1 つの要求の中では設定を 1 回だけ読む", async () => {
    const guards = fixedGuards({
      turnstileEnabled: false,
      rateLimitEnabled: false,
      maxMinutes: 30,
    });
    const { deps } = depsAt(() => NOW, guards.source);
    const ports = createSecurityPortsWith({ SESSION_SIGNING_KEY: KEY }, deps);
    const ctx = ctxFor({}, `https://${HOST}/api/sessions`);
    expect(await ports?.checkRateLimit(ctx)).toBe("ok");
    expect(await ports?.verifyTurnstile(undefined, ctx)).toBe("ok");
    await ports?.issueSession(ctx);
    expect(guards.loads).toHaveLength(1);
  });
});

interface Recorded {
  readonly name: string;
  readonly args: readonly unknown[];
}

// 呼ばれたら記録するだけの名前空間（idFromName・get・newUniqueId・idFromString）。
function recordingNamespace(calls: Recorded[], label: string) {
  const record =
    (name: string) =>
    (...args: unknown[]) => {
      calls.push({ name: `${label}.${name}`, args });
      return { fetch: async () => new Response("{}") };
    };
  return {
    idFromName: record("idFromName"),
    get: record("get"),
    newUniqueId: record("newUniqueId"),
    idFromString: record("idFromString"),
  };
}

describe("P-3 verifySessionId が触れないもの", () => {
  it("正しい id・改ざんした id・期限切れの id のどれでも、名前空間・fetch・設定を呼ばない", async () => {
    const calls: Recorded[] = [];
    const env: Env = {
      SESSION_SIGNING_KEY: KEY,
      COST_GUARD: recordingNamespace(calls, "COST_GUARD"),
      GAME_SESSION: recordingNamespace(calls, "GAME_SESSION"),
    };
    let now = NOW;
    const guards = fixedGuards();
    const { deps, fetchCalls } = depsAt(() => now, guards.source);
    const ports = createSecurityPortsWith(env, deps);
    const ctx = ctxFor(env);

    expect(await ports?.verifySessionId(ISSUED_ID, ctx)).toBe(ISSUED_ID);
    expect(await ports?.verifySessionId(flipChar(ISSUED_ID, 40), ctx)).toBeNull();
    expect(await ports?.verifySessionId(flipChar(ISSUED_ID, 5), ctx)).toBeNull();
    now = EXPIRES * 1000;
    expect(await ports?.verifySessionId(ISSUED_ID, ctx)).toBeNull();

    expect(calls).toEqual([]);
    expect(fetchCalls).toEqual([]);
    expect(guards.loads).toEqual([]);
  });

  it("別の鍵で作った id は null", async () => {
    const other = createSessionSigner(`${KEY}x`, {
      now: () => NOW,
      randomBytes: sequentialRandom().randomBytes,
    });
    const id = await other.issue(HOST, 30);
    const ports = createSecurityPortsWith({ SESSION_SIGNING_KEY: KEY }, depsAt(() => NOW).deps);
    expect(await ports?.verifySessionId(id, ctxFor({}))).toBeNull();
  });
});

describe("P-4 createSecurityPorts（本番の配線）", () => {
  it("env = {} → undefined", () => {
    expect(createSecurityPorts({})).toBeUndefined();
  });

  it("SESSION_SIGNING_KEY だけ → 部品が返り、s_abc.def は null", async () => {
    const env: Env = { SESSION_SIGNING_KEY: KEY };
    const ports = createSecurityPorts(env);
    expect(ports).toBeDefined();
    expect(await ports?.verifySessionId("s_abc.def", ctxFor(env))).toBeNull();
  });
});
