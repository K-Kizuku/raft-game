import { describe, expect, it } from "vitest";

import type { Env } from "./env.ts";
import { parseAllowedOrigins } from "./origin.ts";
import type {
  CostGuardPorts,
  SecurityPorts,
  SessionPorts,
  VerifiedSessionId,
  WorkerDeps,
} from "./ports.ts";
import { createHandler } from "./router.ts";
import { stageFixture } from "./test-stages.ts";

const REQUEST_URL = new URL("https://self.example/api/sessions");

describe("O-1 parseAllowedOrigins", () => {
  it.each<[string, string | undefined, readonly string[]]>([
    [
      "カンマ区切りの 2 件",
      "https://a.example, https://b.example",
      ["https://a.example", "https://b.example"],
    ],
    ["末尾の /", "https://a.example/", ["https://a.example"]],
    ["パス付き", "https://a.example/path", ["https://a.example"]],
    ["http も可", "http://localhost:15213", ["http://localhost:15213"]],
    ["不正な要素は捨てる", "not a url, https://a.example", ["https://a.example"]],
    ["ftp は捨てる", "ftp://a.example,https://a.example", ["https://a.example"]],
    ['"null" は捨てる', "null,https://a.example", ["https://a.example"]],
    ["undefined", undefined, ["https://self.example"]],
    ["空文字", "", ["https://self.example"]],
    ["空白とカンマだけ", " , ", ["https://self.example"]],
    ["不正な要素だけ", "not a url, ftp://a.example, null", ["https://self.example"]],
  ])("%s", (_name, raw, expected) => {
    expect([...parseAllowedOrigins(raw, REQUEST_URL)]).toEqual(expected);
  });
});

function makeFakes() {
  const calls: string[] = [];
  const id = "s_abc.def" as VerifiedSessionId;
  const security: SecurityPorts = {
    checkRateLimit: async () => (calls.push("checkRateLimit"), "ok"),
    verifyTurnstile: async () => (calls.push("verifyTurnstile"), "ok"),
    issueSession: async () => (
      calls.push("issueSession"),
      { sessionId: id, token: "tok", tokenHash: "hash" }
    ),
    verifySessionId: async () => (calls.push("verifySessionId"), id),
  };
  const costGuard: CostGuardPorts = {
    reserve: async () => (calls.push("reserve"), "ok"),
    cancel: async () => void calls.push("cancel"),
    handleAdmin: async () => (calls.push("handleAdmin"), new Response("{}")),
  };
  const session: SessionPorts = {
    init: async () => void calls.push("init"),
    connect: async () => (calls.push("connect"), new Response("{}")),
  };
  const deps: WorkerDeps = {
    stages: [stageFixture({ id: "stage1", title: "t", durationMs: 1 })],
    security,
    costGuard,
    session,
  };
  return { calls, deps };
}

const BASE = "https://raft.example";

function sessionsRequest(origin: string | undefined, base = BASE): Request {
  return new Request(`${base}/api/sessions`, {
    method: "POST",
    headers: {
      "content-type": "application/json",
      ...(origin === undefined ? {} : { origin }),
    },
    body: '{"stageId":"stage1"}',
  });
}

function wsRequest(origin: string | undefined, base = BASE): Request {
  return new Request(`${base}/ws/sessions/s_abc.def?token=t`, {
    headers: { upgrade: "websocket", ...(origin === undefined ? {} : { origin }) },
  });
}

describe("O-2 照合の組み合わせ", () => {
  const env: Env = { ALLOWED_ORIGINS: "https://raft.example" };

  it.each<[string, string | undefined, number]>([
    ["Origin なし", undefined, 200],
    ["許可", "https://raft.example", 200],
    ["許可されない", "https://evil.example", 403],
  ])("GET /api/healthz: %s → %i", async (_name, origin, status) => {
    const headers: Record<string, string> = origin === undefined ? {} : { origin };
    const res = await createHandler(() => makeFakes().deps)(
      new Request(`${BASE}/api/healthz`, { headers }),
      env,
    );

    expect(res.status).toBe(status);
  });

  const rejected: readonly [string, string | undefined][] = [
    ["Origin なし", undefined],
    ["https://evil.example", "https://evil.example"],
    ["null", "null"],
    ["大文字の違い", "HTTPS://RAFT.EXAMPLE"],
    ["複数の値", "https://raft.example, https://evil.example"],
  ];

  it.each(rejected)("POST /api/sessions: %s → 403 で部品を呼ばない", async (_name, origin) => {
    const fakes = makeFakes();
    const res = await createHandler(() => fakes.deps)(sessionsRequest(origin), env);

    expect(res.status).toBe(403);
    expect(await res.json()).toMatchObject({ code: "forbidden_origin" });
    expect(fakes.calls).toEqual([]);
  });

  it.each(rejected)("GET /ws/sessions/{id}: %s → 403 で部品を呼ばない", async (_name, origin) => {
    const fakes = makeFakes();
    const res = await createHandler(() => fakes.deps)(wsRequest(origin), env);

    expect(res.status).toBe(403);
    expect(await res.json()).toMatchObject({ code: "forbidden_origin" });
    expect(fakes.calls).toEqual([]);
  });

  it("許可された Origin なら POST /api/sessions は 201", async () => {
    const fakes = makeFakes();
    const res = await createHandler(() => fakes.deps)(sessionsRequest(BASE), env);

    expect(res.status).toBe(201);
  });
});

describe("O-3 フォールバック", () => {
  it("ALLOWED_ORIGINS なしで、同じオリジンは通る", async () => {
    const fakes = makeFakes();
    const handler = createHandler(() => fakes.deps);

    expect((await handler(sessionsRequest(BASE), {})).status).toBe(201);
    expect((await handler(wsRequest(BASE), {})).status).toBe(200);
  });

  it("ALLOWED_ORIGINS なしで、別のオリジンは 403", async () => {
    const fakes = makeFakes();
    const handler = createHandler(() => fakes.deps);

    expect((await handler(sessionsRequest("https://evil.example"), {})).status).toBe(403);
    expect((await handler(wsRequest("https://evil.example"), {})).status).toBe(403);
    expect(fakes.calls).toEqual([]);
  });

  it("ALLOWED_ORIGINS に有効な要素があれば、同じオリジンでも 403", async () => {
    const fakes = makeFakes();
    const handler = createHandler(() => fakes.deps);
    const env: Env = { ALLOWED_ORIGINS: "https://other.example" };

    expect((await handler(sessionsRequest(BASE), env)).status).toBe(403);
    expect((await handler(wsRequest(BASE), env)).status).toBe(403);
    expect(fakes.calls).toEqual([]);
  });
});
