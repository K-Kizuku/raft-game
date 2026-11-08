import { describe, expect, it } from "vitest";
import { burn } from "./cpu.ts";
import { json } from "./http.ts";
import { handleRequest } from "./router.ts";
import type { DurableObjectNamespaceLike, Env } from "./types.ts";

const TOKEN = "0123456789abcdef0123456789abcdef"; // 32 文字

interface FakeNs {
  ns: DurableObjectNamespaceLike;
  calls: string[];
  requests: Request[];
}

function fakeNs(label: string): FakeNs {
  const calls: string[] = [];
  const requests: Request[] = [];
  return {
    calls,
    requests,
    ns: {
      idFromName(name) {
        calls.push(`${label}.idFromName(${name})`);
        return { name };
      },
      get(id) {
        calls.push(`${label}.get(${(id as { name: string }).name})`);
        return {
          async fetch(req) {
            requests.push(req);
            const u = new URL(req.url);
            return json(200, { from: label, path: u.pathname, query: u.search });
          },
        };
      },
    },
  };
}

function setup(over: Partial<Env> = {}) {
  const sessions = fakeNs("SESSIONS");
  const plain = fakeNs("PLAIN");
  const control = fakeNs("CONTROL");
  const env: Env = {
    SESSIONS: sessions.ns,
    PLAIN: plain.ns,
    CONTROL: control.ns,
    SPIKE_MAX_SESSIONS: "30",
    SPIKE_TOKEN: TOKEN,
    ...over,
  };
  const allCalls = () => [...sessions.calls, ...plain.calls, ...control.calls];
  return { env, sessions, plain, control, allCalls };
}

function req(method: string, path: string, headers: Record<string, string> = {}): Request {
  return new Request(`https://raft-game-spike.example.workers.dev${path}`, { method, headers });
}

const bearer = (t = TOKEN) => ({ authorization: `Bearer ${t}` });
const wsHeaders = { upgrade: "websocket" };

describe("router", () => {
  it("R-1: GET /spike/healthz は名前空間を呼ばずに 200", async () => {
    for (const token of [TOKEN, undefined]) {
      const { env, allCalls } = setup({ SPIKE_TOKEN: token });
      const res = await handleRequest(req("GET", "/spike/healthz"), env);
      expect(res.status).toBe(200);
      expect(await res.json()).toEqual({ ok: true });
      expect(allCalls()).toEqual([]);
    }
  });

  it.each([
    ["未設定", undefined],
    ["空", ""],
    ["31 文字", TOKEN.slice(1)],
    ["空白を含む", `${TOKEN.slice(0, 16)} ${TOKEN.slice(16)}`],
    ["末尾に改行", `${TOKEN}\n`],
  ])("R-2: SPIKE_TOKEN が %s なら 404 で名前空間を呼ばない", async (_name, token) => {
    const { env, allCalls } = setup({ SPIKE_TOKEN: token });
    const presented = token ?? TOKEN;
    const cases = [
      req("POST", "/spike/cpu?iters=10", bearer(presented)),
      req("POST", "/spike/s/1/start", bearer(presented)),
      req("POST", "/spike/plain/1", bearer(presented)),
      req("GET", `/spike/s/1/ws?token=${encodeURIComponent(presented)}`, wsHeaders),
    ];
    for (const r of cases) {
      const res = await handleRequest(r, env);
      expect(res.status, r.url).toBe(404);
    }
    expect(allCalls()).toEqual([]);
  });

  it.each([
    ["Authorization がない", {}],
    ["Basic", { authorization: `Basic ${TOKEN}` }],
    ["Bearer だけ", { authorization: "Bearer" }],
    ["空白 2 つ", { authorization: `Bearer  ${TOKEN}` }],
    ["違う", { authorization: `Bearer ${"f".repeat(32)}` }],
    ["前方一致", { authorization: `Bearer ${TOKEN.slice(0, 31)}` }],
    ["後ろに 1 文字", { authorization: `Bearer ${TOKEN}x` }],
    ["小文字の bearer", { authorization: `bearer ${TOKEN}` }],
  ])("R-3: %s なら 401 で名前空間を呼ばない", async (_name, headers) => {
    const { env, allCalls } = setup();
    for (const path of ["/spike/s/1/start", "/spike/cpu?iters=1", "/spike/control/1"]) {
      const res = await handleRequest(req("POST", path, headers), env);
      expect(res.status, path).toBe(401);
      expect(res.headers.get("content-type")).toBe("application/json");
    }
    expect(allCalls()).toEqual([]);
  });

  it("R-3: HTTP の経路に ?token= を付けても 401", async () => {
    const { env, allCalls } = setup();
    for (const r of [
      req("POST", `/spike/s/1/start?token=${TOKEN}`),
      req("POST", `/spike/s/1/start?token=${TOKEN}`, bearer()),
      req("GET", `/spike/s/1/status?token=${TOKEN}`),
    ]) {
      expect((await handleRequest(r, env)).status, r.url).toBe(401);
    }
    // WS は Authorization ではなく ?token= だけを見る
    expect((await handleRequest(req("GET", "/spike/s/1/ws", { ...wsHeaders, ...bearer() }), env)).status).toBe(401);
    expect(
      (await handleRequest(req("GET", `/spike/s/1/ws?token=${TOKEN}&token=${TOKEN}`, wsHeaders), env)).status,
    ).toBe(401);
    expect(allCalls()).toEqual([]);
  });

  it("R-4: 正しいトークンなら s-3 の Durable Object に /start が渡り、応答がそのまま返る", async () => {
    const { env, sessions, allCalls } = setup();
    const res = await handleRequest(req("POST", "/spike/s/3/start?instance=standard-1&internet=1", bearer()), env);
    expect(res.status).toBe(200);
    expect(allCalls()).toEqual(["SESSIONS.idFromName(s-3)", "SESSIONS.get(s-3)"]);
    const body = (await res.json()) as { from: string; path: string; query: string };
    expect(body.from).toBe("SESSIONS");
    expect(body.path).toBe("/start");
    const q = new URLSearchParams(body.query);
    expect(Object.fromEntries(q)).toEqual({
      instance: "standard-1",
      internet: "1",
      maxRunMs: "300000",
      inactivityMs: "120000",
      timeoutMs: "30000",
    });
    expect(sessions.requests[0]?.method).toBe("POST");
    expect(sessions.requests[0]?.headers.get("authorization")).toBeNull(); // トークンを Durable Object に渡さない
  });

  it("R-4: WS は ?token= で通り、Upgrade を付けて渡す（token は渡さない）", async () => {
    const { env, sessions } = setup();
    const res = await handleRequest(req("GET", `/spike/s/2/ws?token=${TOKEN}`, wsHeaders), env);
    expect(res.status).toBe(200);
    const forwarded = sessions.requests[0];
    expect(forwarded?.headers.get("upgrade")).toBe("websocket");
    expect(new URL(forwarded?.url ?? "").search).toBe("");
    expect(new URL(forwarded?.url ?? "").pathname).toBe("/ws");
    // Upgrade がなければ 426（Durable Object を呼ばない）
    const { env: env2, allCalls } = setup();
    expect((await handleRequest(req("GET", `/spike/s/2/ws?token=${TOKEN}`), env2)).status).toBe(426);
    expect(allCalls()).toEqual([]);
  });

  it("R-4: plain と control は p-{n} のそれぞれの名前空間に渡る", async () => {
    const { env, allCalls } = setup();
    expect((await handleRequest(req("POST", "/spike/plain/2", bearer()), env)).status).toBe(200);
    expect((await handleRequest(req("POST", "/spike/control/5", bearer()), env)).status).toBe(200);
    expect(allCalls()).toEqual([
      "PLAIN.idFromName(p-2)",
      "PLAIN.get(p-2)",
      "CONTROL.idFromName(p-5)",
      "CONTROL.get(p-5)",
    ]);
  });

  it.each(["0", "01", "1.5", "abc", "31", "-1", "1e1", "99999999999999999999"])(
    "R-5: 番号 %s は 404",
    async (n) => {
      const { env, allCalls } = setup();
      const res = await handleRequest(req("POST", `/spike/s/${n}/start`, bearer()), env);
      expect(res.status).toBe(404);
      expect(allCalls()).toEqual([]);
    },
  );

  it("R-5: 番号 30 は通り、plain・control の 6 は 404", async () => {
    const { env, allCalls } = setup();
    expect((await handleRequest(req("POST", "/spike/s/30/stop", bearer()), env)).status).toBe(200);
    expect((await handleRequest(req("POST", "/spike/plain/6", bearer()), env)).status).toBe(404);
    expect((await handleRequest(req("POST", "/spike/control/6", bearer()), env)).status).toBe(404);
    expect((await handleRequest(req("POST", "/spike/control/0", bearer()), env)).status).toBe(404);
    expect(allCalls()).toEqual(["SESSIONS.idFromName(s-30)", "SESSIONS.get(s-30)"]);
  });

  it.each(["0", "51", "x", "", "05"])("R-5: SPIKE_MAX_SESSIONS が %j なら s/1 も 404", async (max) => {
    const { env, allCalls } = setup({ SPIKE_MAX_SESSIONS: max });
    expect((await handleRequest(req("POST", "/spike/s/1/start", bearer()), env)).status).toBe(404);
    expect(allCalls()).toEqual([]);
  });

  it("R-5: SPIKE_MAX_SESSIONS が未設定なら既定の 30", async () => {
    const { env } = setup({ SPIKE_MAX_SESSIONS: undefined });
    expect((await handleRequest(req("POST", "/spike/s/30/stop", bearer()), env)).status).toBe(200);
    expect((await handleRequest(req("POST", "/spike/s/31/stop", bearer()), env)).status).toBe(404);
  });

  it.each([
    "/spike/s/1/start?instance=standard-2",
    "/spike/s/1/start?instance=basic",
    "/spike/s/1/start?internet=2",
    "/spike/s/1/start?maxRunMs=300001",
    "/spike/s/1/start?maxRunMs=9999",
    "/spike/s/1/start?inactivityMs=300001",
    "/spike/s/1/start?timeoutMs=999",
    "/spike/s/1/start?timeoutMs=60001",
    "/spike/s/1/start?timeoutMs=1.5",
    "/spike/s/1/start?timeoutMs=",
    "/spike/s/1/start?internet=0&internet=0",
    "/spike/s/1/start?unknown=1",
    "/spike/s/1/cpu?iters=0",
    "/spike/s/1/cpu?iters=1000000001",
    "/spike/s/1/cpu",
    "/spike/s/1/sql?rows=10001",
    "/spike/s/1/sql?rows=0",
    "/spike/s/1/ping?count=201",
    "/spike/s/1/crash?restart=2",
    "/spike/s/1/bench/raft?nodes=10",
    "/spike/s/1/bench/loop?speed=3",
    "/spike/s/1/egress?host=example.org",
    "/spike/cpu?iters=0",
    "/spike/cpu",
    "/spike/plain/1?x=1",
  ])("R-6: %s は 400 で名前空間を呼ばない", async (path) => {
    const { env, allCalls } = setup();
    const res = await handleRequest(req("POST", path, bearer()), env);
    expect(res.status).toBe(400);
    expect(((await res.json()) as { error: string }).error).toBe("bad_param");
    expect(allCalls()).toEqual([]);
  });

  it("R-7: 知らないパスは 404、メソッドが違えば 405。応答は JSON で no-store", async () => {
    const { env, allCalls } = setup();
    for (const [method, path, status] of [
      ["GET", "/", 404],
      ["GET", "/spike", 404],
      ["POST", "/spike/s/1/nope", 404],
      ["POST", "/spike/s/1", 404],
      ["POST", "/spike/plain/1/x", 404],
      ["GET", "/spike/s/1/start", 405],
      ["POST", "/spike/s/1/status", 405],
      ["GET", "/spike/cpu?iters=1", 405],
      ["POST", "/spike/healthz", 405],
    ] as const) {
      const res = await handleRequest(req(method, path, bearer()), env);
      expect(res.status, `${method} ${path}`).toBe(status);
      expect(res.headers.get("content-type")).toBe("application/json");
      expect(res.headers.get("cache-control")).toBe("no-store");
      const body = (await res.json()) as { error: string; message: string };
      expect(typeof body.error).toBe("string");
      expect(typeof body.message).toBe("string");
    }
    expect(allCalls()).toEqual([]);
  });

  it("R-8: /spike/cpu?iters=1000 は burn(1000) と同じ checksum", async () => {
    const { env, allCalls } = setup();
    const res = await handleRequest(req("POST", "/spike/cpu?iters=1000", bearer()), env);
    expect(res.status).toBe(200);
    expect(res.headers.get("cache-control")).toBe("no-store");
    expect(await res.json()).toEqual({ where: "worker", iters: 1000, checksum: burn(1000).checksum });
    expect(allCalls()).toEqual([]);
    expect(burn(1000)).toEqual(burn(1000));
    expect(burn(1000).checksum).not.toBe(burn(1001).checksum);
  });

  it("Durable Object の例外は 502", async () => {
    const { env } = setup();
    const broken: Env = {
      ...env,
      SESSIONS: {
        idFromName: (n) => n,
        get: () => ({
          fetch: () => Promise.reject(new Error("boom")),
        }),
      },
    };
    const res = await handleRequest(req("POST", "/spike/s/1/stop", bearer()), broken);
    expect(res.status).toBe(502);
  });
});
