import { afterEach, describe, expect, it, vi } from "vitest";

import type { Env } from "../routes/env.ts";
import type { RequestContext } from "../routes/ports.ts";
import type { CostGuardClient, PatchResult } from "./client.ts";
import type { GuardsView } from "./guards.ts";
import { handleAdmin } from "./handler.ts";

const TOKEN = "0123456789abcdef0123456789abcdef";
const URL_ = "https://raft.example/api/admin/guards";

const VIEW: GuardsView = {
  guards: {
    budget: { enabled: true, monthlyUsd: 30, dailyUsd: 1 },
    gameTime: { enabled: true, maxMinutes: 30, maxAlarms: 20 },
    signature: { enabled: true, locked: true },
    turnstile: { enabled: true },
    rateLimit: { enabled: true, perIpPerMinute: 10 },
    cpuMs: { value: 50, note: "変更には再デプロイが必要" },
    acceptGames: true,
  },
  usage: { todayUsd: 0, monthUsd: 0, activeGames: 0 },
  unitCostUsd: { lite: 0.0065, "standard-1": 0.04 },
  recentChanges: [],
};

interface FakeOptions {
  readonly view?: GuardsView | "guards_invalid" | Error;
  readonly patch?: PatchResult | Error;
}

function fakeClient(options: FakeOptions = {}) {
  const calls: { name: string; args: unknown[] }[] = [];
  const client: CostGuardClient = {
    reserve: async (...args) => (calls.push({ name: "reserve", args }), "ok"),
    cancel: async (...args) => void calls.push({ name: "cancel", args }),
    settle: async (...args) => void calls.push({ name: "settle", args }),
    view: async () => {
      calls.push({ name: "view", args: [] });
      const view = options.view ?? VIEW;
      if (view instanceof Error) throw view;
      return view;
    },
    patch: async (...args) => {
      calls.push({ name: "patch", args });
      const result = options.patch ?? { ok: true, view: VIEW };
      if (result instanceof Error) throw result;
      return result;
    },
  };
  return { client, calls };
}

function ctxFor(request: Request, env: Env = { ADMIN_TOKEN: TOKEN }): RequestContext {
  return { request, url: new URL(request.url), env, clientIp: null };
}

function getRequest(authorization: string | null = `Bearer ${TOKEN}`): Request {
  const headers: Record<string, string> = {};
  if (authorization !== null) headers["authorization"] = authorization;
  return new Request(URL_, { headers });
}

function patchRequest(
  body: string,
  options: { authorization?: string | null; contentType?: string } = {},
): Request {
  const headers: Record<string, string> = {
    "content-type": options.contentType ?? "application/json",
  };
  const authorization =
    options.authorization === undefined ? `Bearer ${TOKEN}` : options.authorization;
  if (authorization !== null) headers["authorization"] = authorization;
  return new Request(URL_, { method: "PATCH", headers, body });
}

async function bodyOf(res: Response): Promise<unknown> {
  return res.json();
}

describe("H-1 ADMIN_TOKEN が無効", () => {
  it.each([
    ["GET", () => getRequest()],
    ["PATCH", () => patchRequest('{"acceptGames":false,"reason":"x"}')],
  ])("%s は 404 not_found で、クライアントを呼ばない", async (_name, make) => {
    for (const env of [{}, { ADMIN_TOKEN: "short" }]) {
      const fake = fakeClient();
      const request = make();
      const res = await handleAdmin(ctxFor(request, env), fake.client);
      expect(res.status).toBe(404);
      expect(await bodyOf(res)).toMatchObject({ code: "not_found" });
      expect(fake.calls).toEqual([]);
      expect(request.bodyUsed).toBe(false);
    }
  });
});

describe("H-2 認証に失敗", () => {
  it.each([null, "Bearer wrong-token-wrong-token-wrong-token", `Basic ${TOKEN}`])(
    "Authorization %s は 401 で、クライアントを呼ばず本文を読まない",
    async (authorization) => {
      for (const request of [
        getRequest(authorization),
        patchRequest('{"acceptGames":false,"reason":"x"}', { authorization }),
      ]) {
        const fake = fakeClient();
        const res = await handleAdmin(ctxFor(request), fake.client);
        expect(res.status).toBe(401);
        expect(res.headers.get("www-authenticate")).toBe("Bearer");
        expect(await bodyOf(res)).toMatchObject({ code: "unauthorized" });
        expect(fake.calls).toEqual([]);
        expect(request.bodyUsed).toBe(false);
      }
    },
  );
});

describe("H-3 GET", () => {
  it("クライアントの GuardsView が 200 でそのまま返る", async () => {
    const fake = fakeClient();
    const res = await handleAdmin(ctxFor(getRequest()), fake.client);
    expect(res.status).toBe(200);
    expect(res.headers.get("content-type")).toMatch(/^application\/json/);
    expect(res.headers.get("cache-control")).toBe("no-store");
    expect(await res.text()).toBe(JSON.stringify(VIEW));
    expect(fake.calls).toEqual([{ name: "view", args: [] }]);
  });
});

describe("H-4 PATCH の拒否", () => {
  const cases: readonly [string, () => Request, "bad_request" | "locked"][] = [
    [
      "Content-Type: text/plain",
      () => patchRequest('{"acceptGames":false,"reason":"x"}', { contentType: "text/plain" }),
      "bad_request",
    ],
    [
      "5000 バイト",
      () => patchRequest(JSON.stringify({ acceptGames: false, reason: "x".repeat(4980) })),
      "bad_request",
    ],
    ["壊れた JSON", () => patchRequest("{"), "bad_request"],
    ["null", () => patchRequest("null"), "bad_request"],
    ["未知のキー", () => patchRequest('{"extra":1,"reason":"x"}'), "bad_request"],
    ["budget: true", () => patchRequest('{"budget":true,"reason":"x"}'), "bad_request"],
    ["範囲の外", () => patchRequest('{"gameTime":{"maxMinutes":61},"reason":"x"}'), "bad_request"],
    ["reason がない", () => patchRequest('{"acceptGames":false}'), "bad_request"],
    ["変える項目がない", () => patchRequest('{"reason":"x"}'), "bad_request"],
    ["signature", () => patchRequest('{"signature":{"enabled":false},"reason":"x"}'), "locked"],
    ["cpuMs", () => patchRequest('{"cpuMs":100,"reason":"x"}'), "locked"],
    [
      "gameTime.enabled",
      () => patchRequest('{"gameTime":{"enabled":false},"reason":"x"}'),
      "locked",
    ],
    [
      "gameTime.maxAlarms",
      () => patchRequest('{"gameTime":{"maxAlarms":50},"reason":"x"}'),
      "locked",
    ],
    ["signature と未知のキー", () => patchRequest('{"signature":null,"extra":1}'), "locked"],
  ];

  it.each(cases)("%s → 400 %s で、patch を呼ばない", async (_name, make, code) => {
    const fake = fakeClient();
    const res = await handleAdmin(ctxFor(make()), fake.client);
    expect(res.status).toBe(400);
    expect(await bodyOf(res)).toMatchObject({ code });
    expect(fake.calls).toEqual([]);
  });
});

describe("H-5 PATCH の成功", () => {
  it("平らな change・空白を除いた reason・admin で 1 回呼び、200 で GuardsView を返す", async () => {
    const fake = fakeClient();
    const res = await handleAdmin(
      ctxFor(patchRequest('{"budget":{"enabled":false},"acceptGames":true,"reason":"  理由  "}')),
      fake.client,
    );
    expect(res.status).toBe(200);
    expect(res.headers.get("cache-control")).toBe("no-store");
    expect(await res.text()).toBe(JSON.stringify(VIEW));
    expect(fake.calls).toEqual([
      { name: "patch", args: [{ "budget.enabled": false, acceptGames: true }, "理由", "admin"] },
    ]);
  });
});

describe("H-6 CostGuard の失敗", () => {
  it("view が guards_invalid → 503 guards_invalid", async () => {
    const fake = fakeClient({ view: "guards_invalid" });
    const res = await handleAdmin(ctxFor(getRequest()), fake.client);
    expect(res.status).toBe(503);
    expect(await bodyOf(res)).toMatchObject({ code: "guards_invalid" });
  });

  it("patch が guards_invalid → 503 guards_invalid", async () => {
    const fake = fakeClient({ patch: { ok: false, status: 503, code: "guards_invalid" } });
    const res = await handleAdmin(
      ctxFor(patchRequest('{"acceptGames":false,"reason":"x"}')),
      fake.client,
    );
    expect(res.status).toBe(503);
    expect(await bodyOf(res)).toMatchObject({ code: "guards_invalid" });
  });

  it("patch が 400 locked → 400 locked", async () => {
    const fake = fakeClient({ patch: { ok: false, status: 400, code: "locked" } });
    const res = await handleAdmin(
      ctxFor(patchRequest('{"acceptGames":false,"reason":"x"}')),
      fake.client,
    );
    expect(res.status).toBe(400);
    expect(await bodyOf(res)).toMatchObject({ code: "locked" });
  });

  it("クライアントの例外はそのまま伝わる", async () => {
    const error = new Error("boom");
    await expect(
      handleAdmin(ctxFor(getRequest()), fakeClient({ view: error }).client),
    ).rejects.toBe(error);
    await expect(
      handleAdmin(
        ctxFor(patchRequest('{"acceptGames":false,"reason":"x"}')),
        fakeClient({ patch: error }).client,
      ),
    ).rejects.toBe(error);
  });
});

describe("H-7 ログを出さない", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("401・400・404 のとき console が呼ばれない", async () => {
    const spies = (["log", "info", "warn", "error", "debug"] as const).map((name) =>
      vi.spyOn(console, name).mockImplementation(() => undefined),
    );
    const fake = fakeClient();
    await handleAdmin(ctxFor(getRequest(), {}), fake.client);
    await handleAdmin(ctxFor(getRequest("Bearer nope")), fake.client);
    await handleAdmin(ctxFor(patchRequest("{")), fake.client);
    await handleAdmin(ctxFor(patchRequest('{"signature":null,"reason":"x"}')), fake.client);
    for (const spy of spies) expect(spy).not.toHaveBeenCalled();
  });
});
