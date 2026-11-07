import { describe, expect, it, vi } from "vitest";

import type { Env } from "./env.ts";
import type {
  CostGuardPorts,
  RequestContext,
  SecurityPorts,
  SessionInit,
  SessionPorts,
  VerifiedSessionId,
  WorkerDeps,
} from "./ports.ts";
import { createHandler } from "./router.ts";
import type { StageSummary } from "./stages.ts";

const ORIGIN = "https://raft.example";
const SESSION_ID = "s_abc.def" as VerifiedSessionId;

const STAGES: readonly StageSummary[] = [
  {
    id: "stage1",
    title: "はじめての郵便船",
    region: "瀬戸内（香川県）",
    islands: 3,
    ferries: 1,
    durationMs: 180000,
    unlock: null,
  },
];

interface FakeOptions {
  readonly rate?: "ok" | "rate_limited";
  readonly turnstile?: "ok" | "turnstile_failed";
  readonly reserve?: "ok" | "budget_exhausted" | "sessions_disabled";
  readonly initThrows?: boolean;
  readonly cancelThrows?: boolean;
}

interface Call {
  readonly name: string;
  readonly args: readonly unknown[];
}

function makeFakes(options: FakeOptions = {}) {
  const calls: Call[] = [];
  const record = (name: string, ...args: unknown[]) => calls.push({ name, args });
  const security: SecurityPorts = {
    checkRateLimit: async (ctx) => (record("checkRateLimit", ctx), options.rate ?? "ok"),
    verifyTurnstile: async (token, ctx) => (
      record("verifyTurnstile", token, ctx),
      options.turnstile ?? "ok"
    ),
    issueSession: async (ctx) => (
      record("issueSession", ctx),
      { sessionId: SESSION_ID, token: "tok", tokenHash: "hash" }
    ),
    verifySessionId: async (raw, ctx) => (record("verifySessionId", raw, ctx), SESSION_ID),
  };
  const costGuard: CostGuardPorts = {
    reserve: async (id, ctx) => (record("reserve", id, ctx), options.reserve ?? "ok"),
    cancel: async (id, ctx) => {
      record("cancel", id, ctx);
      if (options.cancelThrows === true) throw new Error("cancel failed");
    },
    handleAdmin: async (ctx) => (record("handleAdmin", ctx), new Response("{}")),
  };
  const session: SessionPorts = {
    init: async (id, init, ctx) => {
      record("init", id, init, ctx);
      if (options.initThrows === true) throw new Error("init failed");
    },
    connect: async (id, ctx) => (record("connect", id, ctx), new Response("{}")),
  };
  return {
    calls,
    names: () => calls.map((call) => call.name),
    deps: { stages: STAGES, security, costGuard, session } satisfies WorkerDeps,
  };
}

function handlerFor(deps: WorkerDeps) {
  return createHandler(() => deps);
}

type StreamingRequestInit = RequestInit & { duplex?: "half" };

function post(
  body: BodyInit | null,
  headers: Record<string, string> = { "content-type": "application/json" },
  extra: StreamingRequestInit = {},
): Request {
  return new Request(`${ORIGIN}/api/sessions`, {
    method: "POST",
    headers: { origin: ORIGIN, ...headers },
    body,
    ...extra,
  } as StreamingRequestInit);
}

function postJson(value: unknown): Request {
  return post(JSON.stringify(value));
}

function streamOf(bytes: number): ReadableStream<Uint8Array> {
  return new ReadableStream({
    start(controller) {
      controller.enqueue(new Uint8Array(bytes).fill(0x20));
      controller.close();
    },
  });
}

describe("POST /api/sessions", () => {
  describe("S-1 部品がない", () => {
    const cases: readonly [string, (f: ReturnType<typeof makeFakes>) => WorkerDeps][] = [
      ["全部なし", () => ({ stages: STAGES })],
      ["security だけ", (f) => ({ stages: STAGES, security: f.deps.security })],
      [
        "security と costGuard だけ",
        (f) => ({ stages: STAGES, security: f.deps.security, costGuard: f.deps.costGuard }),
      ],
    ];
    it.each(cases)("%s → 503 sessions_disabled で部品を呼ばない", async (_name, pick) => {
      const fakes = makeFakes();
      const res = await handlerFor(pick(fakes))(postJson({ stageId: "stage1" }), {});

      expect(res.status).toBe(503);
      expect(await res.json()).toMatchObject({ code: "sessions_disabled" });
      expect(fakes.calls).toEqual([]);
    });
  });

  describe("S-2 正常系", () => {
    it("決まった順序で各 1 回呼び、201 を返す", async () => {
      const fakes = makeFakes();
      const res = await handlerFor(fakes.deps)(
        postJson({ stageId: "stage1", seed: 20261101, turnstileToken: "t" }),
        {},
      );

      expect(res.status).toBe(201);
      expect(res.headers.get("cache-control")).toBe("no-store");
      expect(await res.text()).toBe(
        JSON.stringify({
          sessionId: SESSION_ID,
          token: "tok",
          wsPath: `/ws/sessions/${SESSION_ID}`,
        }),
      );
      expect(fakes.names()).toEqual([
        "checkRateLimit",
        "verifyTurnstile",
        "issueSession",
        "reserve",
        "init",
      ]);
      const byName = (name: string) => fakes.calls.find((call) => call.name === name);
      expect(byName("verifyTurnstile")?.args[0]).toBe("t");
      expect(byName("reserve")?.args[0]).toBe(SESSION_ID);
      expect(byName("init")?.args[0]).toBe(SESSION_ID);
      expect(byName("init")?.args[1]).toEqual({
        stageId: "stage1",
        seed: 20261101,
        demo: false,
        tokenHash: "hash",
      } satisfies SessionInit);
    });

    it("seed と turnstileToken を省略すると、seed は 0 以上 2^32 未満の整数で、トークンは undefined", async () => {
      const fakes = makeFakes();
      const res = await handlerFor(fakes.deps)(postJson({ stageId: "stage1" }), {});

      expect(res.status).toBe(201);
      const turnstile = fakes.calls.find((call) => call.name === "verifyTurnstile");
      expect(turnstile?.args[0]).toBeUndefined();
      const init = fakes.calls.find((call) => call.name === "init")?.args[1] as SessionInit;
      expect(Number.isInteger(init.seed)).toBe(true);
      expect(init.seed).toBeGreaterThanOrEqual(0);
      expect(init.seed).toBeLessThan(2 ** 32);
      expect(init.demo).toBe(false);
    });

    it("部品に渡す ctx は元の Request と CF-Connecting-IP を持つ", async () => {
      const fakes = makeFakes();
      const request = post(JSON.stringify({ stageId: "stage1" }), {
        "content-type": "application/json",
        "cf-connecting-ip": "203.0.113.1",
      });
      await handlerFor(fakes.deps)(request, {});

      const ctx = fakes.calls[0]?.args[0] as RequestContext;
      expect(ctx.request).toBe(request);
      expect(ctx.clientIp).toBe("203.0.113.1");
      expect(ctx.url.pathname).toBe("/api/sessions");
    });
  });

  describe("S-3 本文の検証", () => {
    const json = { "content-type": "application/json" };
    const bad: readonly [string, () => Request][] = [
      [
        "Content-Type が text/plain",
        () => post('{"stageId":"stage1"}', { "content-type": "text/plain" }),
      ],
      ["Content-Type なし", () => post(new TextEncoder().encode('{"stageId":"stage1"}'), {})],
      [
        "Content-Length: 5000",
        () => post('{"stageId":"stage1"}', { ...json, "content-length": "5000" }),
      ],
      [
        "Content-Length なしのストリームで 4097 バイト",
        () => post(streamOf(4097), json, { duplex: "half" }),
      ],
      ["空の本文", () => post("", json)],
      ["本文なし", () => post(null, json)],
      ["不正な UTF-8", () => post(new Uint8Array([0x7b, 0xff, 0x7d]), json)],
      ["壊れた JSON", () => post('{"stageId":', json)],
      ["null", () => postJson(null)],
      ["[]", () => postJson([])],
      ['"x"', () => postJson("x")],
      ["数", () => postJson(1)],
      ["未知のキー extra", () => postJson({ stageId: "stage1", extra: 1 })],
      ["stageId なし", () => postJson({})],
      ["stageId が 1", () => postJson({ stageId: 1 })],
      ["stageId が一覧にない", () => postJson({ stageId: "stage9" })],
      ["seed が -1", () => postJson({ stageId: "stage1", seed: -1 })],
      ["seed が 1.5", () => postJson({ stageId: "stage1", seed: 1.5 })],
      ['seed が "1"', () => postJson({ stageId: "stage1", seed: "1" })],
      ["seed が 2**53", () => postJson({ stageId: "stage1", seed: 2 ** 53 })],
      ["seed が null", () => postJson({ stageId: "stage1", seed: null })],
      ['demo が "true"', () => postJson({ stageId: "stage1", demo: "true" })],
      ["turnstileToken が 123", () => postJson({ stageId: "stage1", turnstileToken: 123 })],
      [
        "turnstileToken が 2049 文字",
        () => postJson({ stageId: "stage1", turnstileToken: "a".repeat(2049) }),
      ],
    ];
    it.each(bad)("%s → 400 bad_request", async (_name, makeRequest) => {
      const fakes = makeFakes();
      const res = await handlerFor(fakes.deps)(makeRequest(), {});

      expect(res.status).toBe(400);
      expect(await res.json()).toMatchObject({ code: "bad_request" });
      expect(fakes.names()).toEqual(["checkRateLimit"]);
    });

    const good: readonly [string, () => Request][] = [
      [
        "Content-Type: application/json; charset=utf-8",
        () => post('{"stageId":"stage1"}', { "content-type": "application/json; charset=utf-8" }),
      ],
      ["末尾が空白の本文", () => post('{"stageId":"stage1"}  \n', json)],
      ["4096 バイトちょうど", () => post(`{"stageId":"stage1"}${" ".repeat(4076)}`, json)],
      ["seed が 0", () => postJson({ stageId: "stage1", seed: 0 })],
      [
        "seed が Number.MAX_SAFE_INTEGER",
        () => postJson({ stageId: "stage1", seed: Number.MAX_SAFE_INTEGER }),
      ],
      ["demo が false", () => postJson({ stageId: "stage1", demo: false })],
      [
        "turnstileToken が 2048 文字",
        () => postJson({ stageId: "stage1", turnstileToken: "a".repeat(2048) }),
      ],
    ];
    it.each(good)("%s → 201", async (_name, makeRequest) => {
      const fakes = makeFakes();
      const res = await handlerFor(fakes.deps)(makeRequest(), {});

      expect(res.status).toBe(201);
    });
  });

  describe("S-4 demo", () => {
    it.each<[string, Env]>([
      ["DEMO_CONTROLS が未設定", {}],
      ['DEMO_CONTROLS が "0"', { DEMO_CONTROLS: "0" }],
    ])("%s では demo: true が 400", async (_name, env) => {
      const fakes = makeFakes();
      const res = await handlerFor(fakes.deps)(postJson({ stageId: "stage1", demo: true }), env);

      expect(res.status).toBe(400);
      expect(await res.json()).toMatchObject({ code: "bad_request" });
      expect(fakes.names()).toEqual(["checkRateLimit"]);
    });

    it('DEMO_CONTROLS が "1" なら 201 で init に demo: true が渡る', async () => {
      const fakes = makeFakes();
      const res = await handlerFor(fakes.deps)(postJson({ stageId: "stage1", demo: true }), {
        DEMO_CONTROLS: "1",
      });

      expect(res.status).toBe(201);
      const init = fakes.calls.find((call) => call.name === "init")?.args[1] as SessionInit;
      expect(init.demo).toBe(true);
    });
  });

  describe("S-5 拒否と後続", () => {
    const cases: readonly [string, FakeOptions, number, string, readonly string[]][] = [
      ["rate_limited", { rate: "rate_limited" }, 429, "rate_limited", ["checkRateLimit"]],
      [
        "turnstile_failed",
        { turnstile: "turnstile_failed" },
        403,
        "turnstile_failed",
        ["checkRateLimit", "verifyTurnstile"],
      ],
      [
        "budget_exhausted",
        { reserve: "budget_exhausted" },
        503,
        "budget_exhausted",
        ["checkRateLimit", "verifyTurnstile", "issueSession", "reserve"],
      ],
      [
        "sessions_disabled",
        { reserve: "sessions_disabled" },
        503,
        "sessions_disabled",
        ["checkRateLimit", "verifyTurnstile", "issueSession", "reserve"],
      ],
      [
        "init が例外",
        { initThrows: true },
        500,
        "internal_error",
        ["checkRateLimit", "verifyTurnstile", "issueSession", "reserve", "init", "cancel"],
      ],
      [
        "init と cancel が例外",
        { initThrows: true, cancelThrows: true },
        500,
        "internal_error",
        ["checkRateLimit", "verifyTurnstile", "issueSession", "reserve", "init", "cancel"],
      ],
    ];
    it.each(cases)("%s", async (_name, options, status, code, expectedCalls) => {
      const fakes = makeFakes(options);
      const consoleError = vi.spyOn(console, "error").mockImplementation(() => undefined);
      try {
        const res = await handlerFor(fakes.deps)(postJson({ stageId: "stage1" }), {});

        expect(res.status).toBe(status);
        expect(await res.json()).toMatchObject({ code });
        expect(fakes.names()).toEqual(expectedCalls);
        const cancel = fakes.calls.find((call) => call.name === "cancel");
        if (cancel !== undefined) expect(cancel.args[0]).toBe(SESSION_ID);
      } finally {
        consoleError.mockRestore();
      }
    });

    it("rate_limited のときは本文を読まない", async () => {
      const fakes = makeFakes({ rate: "rate_limited" });
      const request = postJson({ stageId: "stage1" });
      const res = await handlerFor(fakes.deps)(request, {});

      expect(res.status).toBe(429);
      expect(request.bodyUsed).toBe(false);
    });
  });
});
