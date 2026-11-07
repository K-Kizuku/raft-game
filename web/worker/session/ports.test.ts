import { describe, expect, it } from "vitest";

import type { DurableObjectNamespaceLike } from "../admin/cf-types.ts";
import type { Env } from "../routes/env.ts";
import type {
  CostGuardPorts,
  RequestContext,
  SecurityPorts,
  VerifiedSessionId,
} from "../routes/ports.ts";
import { createHandler } from "../routes/router.ts";
import { createSessionPorts } from "./ports.ts";

const ID = "s_abc.def" as VerifiedSessionId;
const ORIGIN = "https://raft.example";
const INIT = { stageId: "stage1", seed: 7, demo: false, tokenHash: "b".repeat(64) };

function fakeNamespace(respond: (request: Request) => Response | Promise<Response>) {
  const calls: string[] = [];
  const requests: Request[] = [];
  const namespace: DurableObjectNamespaceLike = {
    idFromName: (name) => {
      calls.push(`idFromName(${name})`);
      return { name };
    },
    get: () => {
      calls.push("get");
      return {
        fetch: async (request) => {
          requests.push(request);
          return respond(request);
        },
      };
    },
  };
  return { namespace, calls, requests };
}

function ctxFor(url: string, headers: Record<string, string> = {}): RequestContext {
  const request = new Request(url, { headers });
  return { request, url: new URL(url), env: {}, clientIp: null };
}

function portsOf(env: Env) {
  const ports = createSessionPorts(env);
  if (ports === undefined) throw new Error("ports should exist");
  return ports;
}

describe("P-1 GAME_SESSION がない・形が違う", () => {
  it.each<[string, unknown]>([
    ["なし", undefined],
    ["文字列", "GAME_SESSION"],
    ["get がない", { idFromName: () => 1 }],
  ])("%s → undefined", (_name, binding) => {
    expect(createSessionPorts({ GAME_SESSION: binding })).toBeUndefined();
  });
});

describe("P-2 init", () => {
  it("POST /init に 5 つのキーの JSON を送る", async () => {
    const fake = fakeNamespace(() => new Response("{}", { status: 201 }));
    await portsOf({ GAME_SESSION: fake.namespace }).init(ID, INIT, ctxFor(`${ORIGIN}/`));
    expect(fake.calls).toEqual([`idFromName(${ID})`, "get"]);
    const [request] = fake.requests;
    expect(request?.method).toBe("POST");
    expect(request?.url).toBe("https://game-session.internal/init");
    expect(request?.headers.get("content-type")).toBe("application/json");
    expect(await request?.json()).toEqual({ sessionId: ID, ...INIT });
  });

  it.each([201, 200])("%d → 解決する", async (status) => {
    const fake = fakeNamespace(() => new Response("{}", { status }));
    await expect(
      portsOf({ GAME_SESSION: fake.namespace }).init(ID, INIT, ctxFor(`${ORIGIN}/`)),
    ).resolves.toBeUndefined();
  });

  it.each<[string, () => Response]>([
    ["400", () => new Response('{"code":"bad_request"}', { status: 400 })],
    ["409", () => new Response('{"code":"conflict"}', { status: 409 })],
    ["500", () => new Response('{"code":"start_failed"}', { status: 500 })],
    ["JSON でない本文", () => new Response("oops", { status: 201 })],
  ])("%s → 例外", async (_name, respond) => {
    const fake = fakeNamespace(respond);
    await expect(
      portsOf({ GAME_SESSION: fake.namespace }).init(ID, INIT, ctxFor(`${ORIGIN}/`)),
    ).rejects.toThrow(Error);
  });
});

describe("P-3 connect", () => {
  it("?token= を X-Session-Token に入れ、Cookie は渡さず、応答をそのまま返す", async () => {
    const response = new Response(null, { status: 200 });
    const fake = fakeNamespace(() => response);
    const ctx = ctxFor(`${ORIGIN}/ws/sessions/${ID}?token=abc`, {
      upgrade: "websocket",
      cookie: "secret=1",
    });
    const result = await portsOf({ GAME_SESSION: fake.namespace }).connect(ID, ctx);
    expect(result).toBe(response);
    const [request] = fake.requests;
    expect(request?.method).toBe("GET");
    expect(request?.url).toBe("https://game-session.internal/connect");
    expect(request?.headers.get("x-session-token")).toBe("abc");
    expect(request?.headers.get("upgrade")).toBe("websocket");
    expect(request?.headers.get("cookie")).toBeNull();
  });

  it("トークンがなければヘッダーを付けない", async () => {
    const fake = fakeNamespace(() => new Response(null, { status: 404 }));
    await portsOf({ GAME_SESSION: fake.namespace }).connect(
      ID,
      ctxFor(`${ORIGIN}/ws/sessions/${ID}`),
    );
    expect(fake.requests[0]?.headers.has("x-session-token")).toBe(false);
  });
});

describe("P-4 部品を作っただけでは DO に触れない", () => {
  it("idFromName・get を呼ばない", () => {
    const fake = fakeNamespace(() => new Response("{}", { status: 201 }));
    portsOf({ GAME_SESSION: fake.namespace });
    expect(fake.calls).toEqual([]);
  });
});

describe("P-5 ルーターとの組み合わせ", () => {
  function routerFakes(verified: VerifiedSessionId | null) {
    const calls: string[] = [];
    const security: SecurityPorts = {
      checkRateLimit: async () => "ok",
      verifyTurnstile: async () => "ok",
      issueSession: async () => {
        calls.push("issueSession");
        return { sessionId: ID, token: "t".repeat(22), tokenHash: "b".repeat(64) };
      },
      verifySessionId: async () => {
        calls.push("verifySessionId");
        return verified;
      },
    };
    const costGuard: CostGuardPorts = {
      reserve: async () => {
        calls.push("reserve");
        return "ok";
      },
      cancel: async () => {
        calls.push("cancel");
      },
      handleAdmin: async () => new Response("{}"),
    };
    return { calls, security, costGuard };
  }

  function handlerWith(fakes: ReturnType<typeof routerFakes>) {
    return createHandler((env) => {
      const session = createSessionPorts(env);
      return {
        stages: [
          {
            id: "stage1",
            title: "t",
            region: "r",
            islands: 3,
            ferries: 1,
            durationMs: 180000,
            unlock: null,
          },
        ],
        security: fakes.security,
        costGuard: fakes.costGuard,
        ...(session === undefined ? {} : { session }),
      };
    });
  }

  function createRequest(): Request {
    return new Request(`${ORIGIN}/api/sessions`, {
      method: "POST",
      headers: { origin: ORIGIN, "content-type": "application/json" },
      body: JSON.stringify({ stageId: "stage1", seed: 1 }),
    });
  }

  it("reserve の後に /init を 1 回呼ぶ", async () => {
    const fakes = routerFakes(ID);
    const fake = fakeNamespace((request) => {
      fakes.calls.push(`do:${new URL(request.url).pathname}`);
      return new Response("{}", { status: 201 });
    });
    const res = await handlerWith(fakes)(createRequest(), {
      GAME_SESSION: fake.namespace,
    });
    expect(res.status).toBe(201);
    expect(fakes.calls).toEqual(["issueSession", "reserve", "do:/init"]);
  });

  it("/init が 500 → cancel が呼ばれて 500 internal_error", async () => {
    const fakes = routerFakes(ID);
    const fake = fakeNamespace(() => new Response('{"code":"start_failed"}', { status: 500 }));
    const res = await handlerWith(fakes)(createRequest(), {
      GAME_SESSION: fake.namespace,
    });
    expect(res.status).toBe(500);
    expect(await res.json()).toMatchObject({ code: "internal_error" });
    expect(fakes.calls).toEqual(["issueSession", "reserve", "cancel"]);
  });

  it("verifySessionId が null の /ws/sessions/{id} は GAME_SESSION に触れない（G3）", async () => {
    const fakes = routerFakes(null);
    const fake = fakeNamespace(() => new Response(null, { status: 200 }));
    const res = await handlerWith(fakes)(
      new Request(`${ORIGIN}/ws/sessions/${ID}?token=abc`, {
        headers: { origin: ORIGIN, upgrade: "websocket" },
      }),
      { GAME_SESSION: fake.namespace },
    );
    expect(res.status).toBe(404);
    expect(fakes.calls).toEqual(["verifySessionId"]);
    expect(fake.calls).toEqual([]);
  });
});
