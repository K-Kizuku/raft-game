import { afterEach, describe, expect, it } from "vitest";
import { json } from "./http.ts";
import { SpikeSession, type SessionDeps } from "./session.ts";
import { FakeClock, FakeContainer, FakeSocket } from "./test-fakes.ts";
import { openTestSql, type TestSql } from "./test-sqlite.ts";
import type { DurableObjectStateLike, SqlStorageLike } from "./types.ts";

const opened: TestSql[] = [];
afterEach(() => {
  for (const db of opened.splice(0)) db.close();
});

interface Harness {
  session: SpikeSession;
  container: FakeContainer;
  clock: FakeClock;
  alarms: number[];
  logs: string[];
  sql: SqlStorageLike;
  ctx: { storage: { sql: SqlStorageLike; setAlarm(t: number): Promise<void> }; container?: FakeContainer };
  sockets: FakeSocket[];
  remake(): SpikeSession;
}

async function harness(opts: { container?: boolean } = {}): Promise<Harness> {
  const db = await openTestSql();
  opened.push(db);
  const clock = new FakeClock();
  const container = new FakeContainer();
  const alarms: number[] = [];
  const logs: string[] = [];
  const sockets: FakeSocket[] = [];
  const ctx: Harness["ctx"] = {
    storage: {
      sql: db.sql,
      setAlarm: async (t: number) => {
        alarms.push(t);
      },
    },
  };
  if (opts.container !== false) ctx.container = container;
  const deps: Partial<SessionDeps> = {
    now: clock.now,
    sleep: clock.sleep,
    log: (l) => logs.push(l),
    timeoutSignal: () => undefined,
    createPair: () => {
      const client = new FakeSocket("client");
      const server = new FakeSocket("server");
      sockets.push(client, server);
      return [client, server];
    },
    upgradeResponse: () => json(200, { upgraded: true }),
  };
  const make = () => new SpikeSession(ctx as DurableObjectStateLike, {}, deps);
  return { session: make(), container, clock, alarms, logs, sql: db.sql, ctx, sockets, remake: make };
}

function call(session: SpikeSession, method: string, pathAndQuery: string, headers: Record<string, string> = {}) {
  return session.fetch(new Request(`https://spike.internal${pathAndQuery}`, { method, headers }));
}

async function body(res: Response): Promise<Record<string, unknown>> {
  return (await res.json()) as Record<string, unknown>;
}

async function status(session: SpikeSession) {
  const res = await call(session, "GET", "/status");
  expect(res.status).toBe(200);
  return (await res.json()) as {
    running: boolean;
    starts: number;
    alarms: number;
    inputLogRows: number;
    runs: { kind: string; detail: Record<string, unknown> }[];
    relay: Record<string, unknown> | null;
  };
}

describe("SpikeSession", () => {
  it("S-1: start は container.start を既定の引数で 1 回呼び、setInactivityTimeout を設定する", async () => {
    const h = await harness();
    const res = await call(h.session, "POST", "/start", { "x-spike-colo": "NRT" });
    expect(res.status).toBe(200);
    expect(h.container.starts).toEqual([
      {
        image: "registry.example/raft-game-spike-server:test",
        instance: "lite",
        enableInternet: false,
        env: { MODE: "single", PORT: "8080" },
      },
    ]);
    expect(h.container.inactivity).toEqual([120000]);
    const b = await body(res);
    expect(b["colo"]).toBe("NRT");
    expect(b["instance"]).toBe("lite");

    const h2 = await harness();
    expect((await call(h2.session, "POST", "/start?internet=1&instance=standard-1&inactivityMs=30000")).status).toBe(
      200,
    );
    expect(h2.container.starts[0]).toMatchObject({ instance: "standard-1", enableInternet: true });
    expect(h2.container.inactivity).toEqual([30000]);
  });

  it("S-2: /healthz が 2 回失敗（例外・503）してから 200 → polls 3、startMs は注入した時刻の差", async () => {
    const h = await harness();
    h.container.healthPlan = ["throw", 503];
    const res = await call(h.session, "POST", "/start");
    expect(res.status).toBe(200);
    const b = await body(res);
    expect(b["polls"]).toBe(3);
    expect(b["startMs"]).toBe(200);
    expect(h.clock.sleeps).toEqual([100, 100]);
    expect(b["info"]).toEqual(h.container.infoBody);
    const healthCalls = h.container.fetches.filter((f) => f.url === "http://container/healthz");
    expect(healthCalls).toHaveLength(3);
    const s = await status(h.session);
    expect(s.runs.map((r) => r.kind)).toEqual(["start", "ready"]);
    expect(s.runs[1]?.detail).toMatchObject({ startMs: 200, polls: 3 });
  });

  it("S-3: 期限までに準備ができなければ destroy して 504 start_timeout", async () => {
    const h = await harness();
    h.container.healthDefault = "throw";
    const res = await call(h.session, "POST", "/start?timeoutMs=1000");
    expect(res.status).toBe(504);
    const b = await body(res);
    expect(b["error"]).toBe("start_timeout");
    expect(b["polls"]).toBe(11);
    expect(h.container.calls).toContain("destroy");
    expect(h.container.running).toBe(false);
    const s = await status(h.session);
    expect(s.runs.find((r) => r.kind === "stop")).toMatchObject({ detail: { error: "start_timeout", polls: 11 } });
    expect(s.runs.map((r) => r.kind)).toEqual(["start", "stop", "exit"]);
  });

  it("S-4: start ごとに setAlarm(now + maxRunMs) が 1 回。4 回目は 429、動いている間は 409", async () => {
    const h = await harness();
    for (let i = 1; i <= 3; i++) {
      const before = h.clock.t;
      expect((await call(h.session, "POST", "/start?maxRunMs=60000")).status).toBe(200);
      expect(h.alarms).toHaveLength(i);
      expect(h.alarms[i - 1]).toBe(before + 60000);
      expect((await call(h.session, "POST", "/start")).status).toBe(409);
      expect(h.alarms).toHaveLength(i);
      expect((await call(h.session, "POST", "/stop")).status).toBe(200);
    }
    const res = await call(h.session, "POST", "/start");
    expect(res.status).toBe(429);
    expect((await body(res))["error"]).toBe("start_cap");
    expect(h.container.starts).toHaveLength(3);
    expect(h.alarms).toHaveLength(3);
    const s = await status(h.session);
    expect(s.starts).toBe(3);
    expect(s.alarms).toBe(3);
  });

  it("S-5: alarm() は動いていれば destroy し、どの場合もアラームを設定し直さない", async () => {
    const h = await harness();
    expect((await call(h.session, "POST", "/start")).status).toBe(200);
    h.ctx.storage.setAlarm = () => {
      throw new Error("setAlarm must not be called from alarm()");
    };
    await h.session.alarm();
    expect(h.container.calls.filter((c) => c === "destroy")).toHaveLength(1);
    expect(h.container.running).toBe(false);
    await h.session.alarm(); // もう止まっている → 記録だけ
    expect(h.container.calls.filter((c) => c === "destroy")).toHaveLength(1);
    const s = await status(h.session);
    const alarms = s.runs.filter((r) => r.kind === "alarm");
    expect(alarms.map((r) => r.detail)).toEqual([
      { running: true, destroyed: true },
      { running: false, destroyed: false },
    ]);
    expect(h.logs.filter((l) => l.includes('"event":"alarm"'))).toHaveLength(2);
  });

  it("S-6: stop は動いていれば destroy、動いていなければ呼ばずに {running:false}", async () => {
    const h = await harness();
    let res = await call(h.session, "POST", "/stop");
    expect(res.status).toBe(200);
    expect(await body(res)).toEqual({ running: false });
    expect(h.container.calls).not.toContain("destroy");
    await call(h.session, "POST", "/start");
    res = await call(h.session, "POST", "/stop");
    expect(res.status).toBe(200);
    expect(await body(res)).toMatchObject({ running: false, destroyed: true });
    expect(h.container.calls.filter((c) => c === "destroy")).toHaveLength(1);
  });

  it("S-7: ctx.container がなければ 500 no_container、start() が例外なら 500 start_failed", async () => {
    const h = await harness({ container: false });
    let res = await call(h.session, "POST", "/start");
    expect(res.status).toBe(500);
    expect((await body(res))["error"]).toBe("no_container");
    expect(h.alarms).toHaveLength(0);

    const h2 = await harness();
    h2.container.startError = new Error("no capacity");
    res = await call(h2.session, "POST", "/start");
    expect(res.status).toBe(500);
    expect(await body(res)).toMatchObject({ error: "start_failed", message: "no capacity" });
    const s = await status(h2.session);
    expect(s.runs.at(-1)).toMatchObject({ kind: "start", detail: { error: "start_failed" } });
  });

  it("S-8: sql は行を追記し、作り直しても input_log・meta・runs が残る", async () => {
    const h = await harness();
    let res = await call(h.session, "POST", "/sql?rows=1000");
    expect(res.status).toBe(200);
    expect(await body(res)).toEqual({ rows: 1000, total: 1000 });
    await call(h.session, "POST", "/start");
    await call(h.session, "POST", "/stop");
    const again = h.remake(); // 同じ DB で表の作成をもう一度する
    const s = await status(again);
    expect(s.inputLogRows).toBe(1000);
    expect(s.starts).toBe(1);
    expect(s.runs.map((r) => r.kind)).toEqual(["start", "ready", "stop", "exit"]);
    res = await call(again, "POST", "/sql?rows=5");
    expect(await body(res)).toEqual({ rows: 5, total: 1005 });
    const row = h.sql.exec("SELECT cmd FROM input_log WHERE seq = 1").toArray()[0];
    expect(JSON.parse(String(row?.["cmd"]))).toEqual({ type: "spike.cmd", i: 0 });
  });

  it("S-9: crash は monitor() の終わりまでの detectMs を返す", async () => {
    const h = await harness();
    await call(h.session, "POST", "/start");
    h.container.routes.set("/crash", () => {
      h.clock.t += 150;
      h.container.crashNow();
      return json(202, { crashing: true });
    });
    const res = await call(h.session, "POST", "/crash");
    expect(res.status).toBe(200);
    const b = await body(res);
    expect(b).toMatchObject({ crashStatus: 202, detectMs: 150, timeout: false, restart: null, restartMs: null });
    expect(b["exit"]).toEqual({ ok: false, error: "exit code 3" });
    const s = await status(h.session);
    expect(s.runs.map((r) => r.kind)).toEqual(["start", "ready", "exit", "crash"]);
  });

  it("S-9: 30 秒（注入した時刻）で monitor() が終わらなければ timeout", async () => {
    const h = await harness();
    await call(h.session, "POST", "/start");
    h.container.routes.set("/crash", () => json(202, { crashing: true }));
    const t0 = h.clock.t;
    const res = await call(h.session, "POST", "/crash?restart=1");
    expect(res.status).toBe(200);
    expect(await body(res)).toMatchObject({ detectMs: null, timeout: true, restart: null });
    expect(h.clock.t - t0).toBe(30000);
    expect(h.container.starts).toHaveLength(1); // 終わっていないので起動し直さない
  });

  it("S-9: restart=1 は起動し直し、起動の回数に数える。上限なら 429", async () => {
    const h = await harness();
    await call(h.session, "POST", "/start?instance=standard-1&maxRunMs=60000");
    h.container.routes.set("/crash", () => {
      h.container.crashNow();
      return json(202, { crashing: true });
    });
    let res = await call(h.session, "POST", "/crash?restart=1");
    expect(res.status).toBe(200);
    let b = await body(res);
    expect(typeof b["restartMs"]).toBe("number");
    expect(b["restart"]).toMatchObject({ instance: "standard-1", run: 2 });
    expect(h.container.starts.map((s) => s.instance)).toEqual(["standard-1", "standard-1"]);
    expect(h.alarms).toHaveLength(2);
    expect((await status(h.session)).starts).toBe(2);

    res = await call(h.session, "POST", "/crash?restart=1");
    expect(res.status).toBe(200);
    expect((await status(h.session)).starts).toBe(3);
    res = await call(h.session, "POST", "/crash?restart=1");
    expect(res.status).toBe(429);
    b = await body(res);
    expect(b["error"]).toBe("start_cap");
    expect(h.container.starts).toHaveLength(3);
  });

  it("S-10: 動いていないときの ping・bench・egress・crash・ws は 409 not_running", async () => {
    const h = await harness();
    for (const [method, path, headers] of [
      ["POST", "/ping", {}],
      ["POST", "/bench/raft", {}],
      ["POST", "/bench/loop?speed=8", {}],
      ["POST", "/egress", {}],
      ["POST", "/crash", {}],
      ["GET", "/ws", { upgrade: "websocket" }],
    ] as const) {
      const res = await call(h.session, method, path, headers);
      expect(res.status, path).toBe(409);
      expect((await body(res))["error"]).toBe("not_running");
    }
    expect(h.container.fetches).toHaveLength(0);
  });

  it("S-11: DurableObject を継承せず、alarm を持つ", () => {
    expect(Object.getPrototypeOf(SpikeSession.prototype)).toBe(Object.prototype);
    expect(typeof SpikeSession.prototype.alarm).toBe("function");
    expect(typeof SpikeSession.prototype.fetch).toBe("function");
  });

  it("ping は count 回 /healthz を呼んで p50・p99・最大を返す", async () => {
    const h = await harness();
    await call(h.session, "POST", "/start");
    const before = h.container.fetches.length;
    const res = await call(h.session, "POST", "/ping?count=5");
    expect(await body(res)).toEqual({ count: 5, ok: 5, p50: 0, p99: 0, max: 0 });
    expect(h.container.fetches.length - before).toBe(5);
  });

  it("bench・egress はコンテナの同じパスに転送して結果をそのまま返す", async () => {
    const h = await harness();
    await call(h.session, "POST", "/start");
    const seen: string[] = [];
    h.container.routes.set("/bench/raft", (init) => {
      seen.push(String(init?.method));
      return json(200, { nodes: 3, wallMs: 1 });
    });
    h.container.routes.set("/egress", () => json(200, { dns: "x", tcp: "y", https: "z" }));
    let res = await call(h.session, "POST", "/bench/raft?nodes=3&minutes=1");
    expect(await body(res)).toEqual({ nodes: 3, wallMs: 1 });
    expect(h.container.fetches.at(-1)?.url).toBe("http://container/bench/raft?nodes=3&minutes=1");
    expect(seen).toEqual(["POST"]);
    res = await call(h.session, "POST", "/egress");
    expect(await body(res)).toEqual({ dns: "x", tcp: "y", https: "z" });
    expect(h.container.fetches.at(-1)?.url).toBe("http://container/egress");
  });

  it("ws はコンテナの /ws に Upgrade でつなぎ、中継の状態を status で見られる", async () => {
    const h = await harness();
    await call(h.session, "POST", "/start");
    const upstream = new FakeSocket("upstream");
    h.container.routes.set("/ws", (init) => {
      expect(new Headers(init?.headers).get("upgrade")).toBe("websocket");
      const res = new Response(null, { status: 200 });
      Object.defineProperty(res, "webSocket", { value: upstream });
      return res;
    });
    const res = await call(h.session, "GET", "/ws", { upgrade: "websocket" });
    expect(await body(res)).toEqual({ upgraded: true });
    await new Promise((r) => setTimeout(r, 0));
    const server = h.sockets[1];
    expect(server?.accepted).toBe(true);
    expect(upstream.accepted).toBe(true);
    server?.emit("message", { data: "hello" });
    expect(upstream.sent).toEqual(["hello"]);
    expect((await status(h.session)).relay).toMatchObject({ open: true, toContainer: 1 });
  });

  it("メソッド・引数の検査は Durable Object でも行う", async () => {
    const h = await harness();
    expect((await call(h.session, "GET", "/start")).status).toBe(405);
    expect((await call(h.session, "POST", "/start?instance=standard-2")).status).toBe(400);
    expect((await call(h.session, "POST", "/nope")).status).toBe(404);
    expect(h.container.starts).toHaveLength(0);
    expect(h.alarms).toHaveLength(0);
  });
});
