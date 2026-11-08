import { afterEach, describe, expect, it, vi } from "vitest";

import type { Clock, ServerEvent, Snapshot } from "../protocol/index.ts";
import {
  CLIENT_NAME,
  CommandError,
  createGameConnection,
  type ConnectionStatus,
  type GameConnectionOptions,
} from "./client.ts";
import type { SocketFactory } from "./socket.ts";
import { createManualTimers } from "./timers.ts";

const TOKEN = "tok-SECRET+/=";

// FakeSocket は手で動かすソケット。サーバー側の操作（open・deliver・serverClose）と、送られた電文を持つ。
type FakeSocket = {
  url: string;
  sent: string[];
  closedBy: { code: number | undefined; reason: string | undefined }[];
  open(): void;
  raw(data: unknown): void;
  deliver(obj: unknown): void;
  msg(type: string, payload: unknown, seq?: number): void;
  serverClose(code: number): void;
  parsed(): { v: number; type: string; cmdId?: string; payload: unknown }[];
  types(): string[];
};

function fakeNetwork() {
  const sockets: FakeSocket[] = [];
  const factory: SocketFactory = (url, handlers) => {
    let nextSeq = 1;
    const socket: FakeSocket = {
      url,
      sent: [],
      closedBy: [],
      open: () => handlers.onOpen(),
      raw: (data) => handlers.onMessage(data),
      deliver: (obj) => handlers.onMessage(JSON.stringify(obj)),
      msg(type, payload, seq) {
        const s = seq ?? nextSeq;
        nextSeq = s + 1;
        socket.deliver({ v: 1, type, seq: s, payload });
      },
      serverClose: (code) => handlers.onClose(code, ""),
      parsed: () => socket.sent.map((d) => JSON.parse(d)),
      types: () => socket.parsed().map((m) => m.type),
    };
    sockets.push(socket);
    return {
      send: (data) => socket.sent.push(data),
      close: (code, reason) => socket.closedBy.push({ code, reason }),
    };
  };
  const last = (): FakeSocket => {
    const s = sockets[sockets.length - 1];
    if (s === undefined) {
      throw new Error("no socket");
    }
    return s;
  };
  return { sockets, factory, last };
}

const clock: Clock = { t: 0, speed: 1, paused: false, endsAt: 60000 };

function snap(lastSeq: number): Snapshot {
  return {
    protocol: 1,
    session: { id: "s_test", stageId: "mini", seed: 1, demo: false, restored: false },
    phase: "sailing",
    clock: { ...clock, allowedSpeeds: [0.5, 1, 2, 4] },
    stage: { title: "t", islands: [], routes: [], resources: [] },
    ferries: [],
    routesState: [],
    letters: [],
    requests: [],
    nodes: [],
    islandsState: [],
    score: { total: 0, successes: 0, target: 0, criticalSecured: [] },
    lastSeq,
  };
}

function evs(...seqs: number[]) {
  return {
    clock,
    events: seqs.map((seq) => ({ seq, t: 0, type: "phase.changed", phase: "sailing" })),
  };
}

function setup(extra: Partial<GameConnectionOptions> = {}) {
  const timers = createManualTimers();
  const net = fakeNetwork();
  const snapshots: { snapshot: Snapshot; receivedAt: number }[] = [];
  const events: { clock: Clock; seqs: number[]; receivedAt: number }[] = [];
  const statuses: (ConnectionStatus & { at: number })[] = [];
  const statusObjects: ConnectionStatus[] = [];
  const pongs: number[] = [];
  const roundResults: unknown[] = [];
  const serverErrors: unknown[] = [];
  const conn = createGameConnection({
    session: { wsPath: "/ws/sessions/s_test.sig", token: TOKEN },
    location: { protocol: "https:", host: "raft.example" },
    socketFactory: net.factory,
    timers,
    handlers: {
      onSnapshot: (snapshot, receivedAt) => snapshots.push({ snapshot, receivedAt }),
      onEvents: (c, e: readonly ServerEvent[], receivedAt) =>
        events.push({ clock: c, seqs: e.map((x) => x.seq), receivedAt }),
      onStatus: (s) => {
        statusObjects.push(s);
        statuses.push({ ...s, at: timers.now() });
      },
      onPong: (rtt) => pongs.push(rtt),
      onRoundResult: (r) => roundResults.push(r),
      onServerError: (e) => serverErrors.push(e),
    },
    ...extra,
  });
  // ready は接続を開いてスナップショット（lastSeq）を受けた状態にする。
  const ready = (lastSeq = 0) => {
    conn.start();
    const s = net.last();
    s.open();
    s.msg("snapshot", snap(lastSeq));
    return s;
  };
  return {
    timers,
    net,
    conn,
    snapshots,
    events,
    statuses,
    statusObjects,
    pongs,
    roundResults,
    serverErrors,
    ready,
  };
}

const sentOf = (s: FakeSocket, type: string) => s.parsed().filter((m) => m.type === type);

afterEach(() => {
  vi.restoreAllMocks();
});

describe("createGameConnection", () => {
  it("CL-1: 接続して hello を送り、スナップショットで ready になる", () => {
    const t = setup();
    expect(t.conn.getStatus()).toEqual({ state: "idle", attempt: 0, reason: null });
    t.conn.start();
    expect(t.net.sockets).toHaveLength(1);
    const s = t.net.last();
    expect(s.url).toBe(
      `wss://raft.example/ws/sessions/s_test.sig?token=${encodeURIComponent(TOKEN)}`,
    );
    expect(t.conn.getStatus()).toEqual({ state: "connecting", attempt: 1, reason: null });
    s.open();
    expect(s.parsed()).toEqual([
      { v: 1, type: "hello", payload: { client: CLIENT_NAME, resume: false } },
    ]);
    expect(CLIENT_NAME).toBe("web/0.1.0");
    t.timers.advance(250);
    s.msg("snapshot", snap(0));
    expect(t.snapshots).toHaveLength(1);
    expect(t.snapshots[0]?.receivedAt).toBe(250);
    expect(t.conn.getStatus().state).toBe("ready");
    t.conn.start();
    expect(t.net.sockets).toHaveLength(1);
  });

  it("CL-2: イベントの重複を読み飛ばし、空の events でも渡す。スナップショットの前は捨てる", () => {
    const t = setup();
    const s = t.ready(0);
    s.msg("events", evs(1, 2, 3));
    s.msg("events", evs(2, 3, 4, 5));
    s.msg("events", evs());
    expect(t.events.map((e) => e.seqs)).toEqual([[1, 2, 3], [4, 5], []]);
    expect(t.events[0]?.clock).toEqual(clock);

    const u = setup();
    u.conn.start();
    const s2 = u.net.last();
    s2.open();
    s2.msg("events", evs(1));
    expect(u.events).toHaveLength(0);
    s2.msg("snapshot", snap(1));
    s2.msg("events", evs(1, 2));
    expect(u.events.map((e) => e.seqs)).toEqual([[2]]);
  });

  it("CL-3: イベントの抜けで cmd.sync を 1 回だけ送り、スナップショットで戻る", () => {
    const t = setup();
    const s = t.ready(0);
    s.msg("events", evs(1));
    s.msg("events", evs(2, 4, 5));
    expect(t.events.map((e) => e.seqs)).toEqual([[1], [2]]);
    expect(sentOf(s, "cmd.sync")).toEqual([{ v: 1, type: "cmd.sync", payload: {} }]);
    expect(t.conn.getStatus().state).toBe("syncing");
    s.msg("events", evs(3, 4, 6));
    s.msg("events", evs(8), 20); // エンベロープの抜けも重ねる
    expect(t.events).toHaveLength(2);
    expect(sentOf(s, "cmd.sync")).toHaveLength(1);
    s.msg("snapshot", snap(5));
    expect(t.conn.getStatus().state).toBe("ready");
    s.msg("events", evs(5, 6));
    expect(t.events.map((e) => e.seqs)).toEqual([[1], [2], [6]]);
  });

  it("CL-4: エンベロープの seq は最初の電文を基準にし、重複を捨て、抜けで cmd.sync", () => {
    const t = setup();
    t.conn.start();
    const s = t.net.last();
    s.open();
    s.msg("snapshot", snap(0), 41);
    s.msg("events", evs(1), 42);
    expect(t.events).toHaveLength(1);
    s.msg("events", evs(1), 42);
    expect(t.events).toHaveLength(1);
    expect(sentOf(s, "cmd.sync")).toHaveLength(0);
    s.msg("pong", { clientTime: 0, simTime: 0 }, 44);
    expect(t.pongs).toEqual([0]);
    expect(sentOf(s, "cmd.sync")).toHaveLength(1);
    expect(t.conn.getStatus().state).toBe("syncing");
  });

  it("CL-5: cmd.sync の後 5000 ms スナップショットが来ないと close(4000) して再接続する", () => {
    const t = setup();
    const s = t.ready(0);
    s.msg("events", evs(2));
    expect(t.conn.getStatus().state).toBe("syncing");
    t.timers.advance(4999);
    expect(s.closedBy).toEqual([]);
    t.timers.advance(1);
    expect(s.closedBy).toEqual([{ code: 4000, reason: "sync timeout" }]);
    expect(t.conn.getStatus().state).toBe("reconnecting");
    t.timers.advance(500);
    expect(t.net.sockets).toHaveLength(2);
  });

  it("CL-6: 15000 ms ごとに ping を送り、pong で rtt を渡す。閉じた後は送らない", () => {
    const t = setup();
    const s = t.ready(0);
    t.timers.advance(15000);
    t.timers.advance(15000);
    expect(sentOf(s, "ping").map((m) => m.payload)).toEqual([
      { clientTime: 15000 },
      { clientTime: 30000 },
    ]);
    t.timers.advance(120);
    s.msg("pong", { clientTime: 30000, simTime: 0 });
    expect(t.pongs).toEqual([120]);
    s.serverClose(1006);
    t.timers.advance(60000);
    expect(sentOf(s, "ping")).toHaveLength(2);
  });

  it("CL-7: 45000 ms 何も来ないと close(4000) して再接続する。電文が来れば延びる", () => {
    const t = setup();
    const s = t.ready(0);
    t.timers.advance(44999);
    s.msg("events", evs());
    t.timers.advance(44999);
    expect(s.closedBy).toEqual([]);
    t.timers.advance(1);
    expect(s.closedBy).toEqual([{ code: 4000, reason: "idle" }]);
    expect(t.conn.getStatus().state).toBe("reconnecting");
  });

  it("CL-8: 切断で再接続し、待ちが 500 ms から 2 倍ずつ 8000 ms まで広がる", () => {
    const t = setup();
    const s1 = t.ready(0);
    s1.serverClose(1006);
    expect(t.conn.getStatus()).toEqual({ state: "reconnecting", attempt: 2, reason: null });
    t.timers.advance(499);
    expect(t.net.sockets).toHaveLength(1);
    t.timers.advance(1);
    expect(t.net.sockets).toHaveLength(2);
    const s2 = t.net.last();
    s2.open();
    expect(s2.parsed()[0]).toEqual({
      v: 1,
      type: "hello",
      payload: { client: CLIENT_NAME, resume: true },
    });
    s2.msg("snapshot", snap(0));
    expect(t.conn.getStatus().state).toBe("ready");
    expect(t.snapshots).toHaveLength(2);

    // スナップショットで数えが戻るので、次の切断はまた 500 ms から。開いてすぐ切れると広がる。
    s2.serverClose(1006);
    for (const delay of [500, 1000, 2000, 4000, 8000, 8000]) {
      const count = t.net.sockets.length;
      t.timers.advance(delay - 1);
      expect(t.net.sockets).toHaveLength(count);
      t.timers.advance(1);
      expect(t.net.sockets).toHaveLength(count + 1);
      const s = t.net.last();
      s.open();
      s.serverClose(1006);
    }
  });

  it("CL-9: 一度もスナップショットを受けずに 3 回切れると unreachable", () => {
    const t = setup();
    t.conn.start();
    t.net.last().serverClose(1006);
    t.timers.advance(500);
    t.net.last().open();
    t.net.last().serverClose(1006);
    t.timers.advance(1000);
    expect(t.net.sockets).toHaveLength(3);
    t.net.last().serverClose(1006);
    expect(t.conn.getStatus()).toEqual({ state: "failed", attempt: 3, reason: "unreachable" });
    t.timers.advance(60000);
    expect(t.net.sockets).toHaveLength(3);
  });

  it("CL-9: ready の後の切断から 300000 ms を過ぎると gave_up", () => {
    const t = setup();
    const s = t.ready(0);
    t.timers.advance(1000);
    const disconnectedAt = t.timers.now();
    s.serverClose(1006);
    for (let i = 0; i < 10000 && t.conn.getStatus().state !== "failed"; i++) {
      const count = t.net.sockets.length;
      t.timers.advance(100);
      if (t.net.sockets.length > count) {
        t.net.last().serverClose(1006);
      }
    }
    const failed = t.statuses[t.statuses.length - 1];
    expect(failed?.state).toBe("failed");
    expect(failed?.reason).toBe("gave_up");
    expect(failed!.at - disconnectedAt).toBeGreaterThan(300000);
    expect(failed!.at - disconnectedAt).toBeLessThanOrEqual(300000 + 8000 + 100);
    const count = t.net.sockets.length;
    t.timers.advance(60000);
    expect(t.net.sockets).toHaveLength(count);
  });

  it("CL-10: 4001 で切られたら replaced で、再接続しない", () => {
    const t = setup();
    t.ready(0).serverClose(4001);
    expect(t.conn.getStatus()).toMatchObject({ state: "failed", reason: "replaced" });
    t.timers.advance(60000);
    expect(t.net.sockets).toHaveLength(1);
  });

  it("CL-10: round.result で ended になり、自分で閉じ、その後の切断で再接続しない", () => {
    const t = setup();
    const s = t.ready(0);
    s.msg("round.result", { score: { total: 1 } });
    expect(t.roundResults).toHaveLength(1);
    expect(t.conn.getStatus().state).toBe("ended");
    expect(s.closedBy).toEqual([{ code: 1000, reason: "round ended" }]);
    s.serverClose(1000);
    t.timers.advance(60000);
    expect(t.net.sockets).toHaveLength(1);
    expect(t.conn.getStatus().state).toBe("ended");
  });

  it("CL-10: error の fatal で fatal_error、fatal でなければ状態を変えない", () => {
    const t = setup();
    const s = t.ready(0);
    s.msg("error", { code: "x", message: "m", fatal: false });
    expect(t.serverErrors).toHaveLength(1);
    expect(t.conn.getStatus().state).toBe("ready");
    s.msg("error", { code: "x", message: "m", fatal: true });
    expect(t.serverErrors).toHaveLength(2);
    expect(t.conn.getStatus()).toMatchObject({ state: "failed", reason: "fatal_error" });
    s.serverClose(1011);
    t.timers.advance(60000);
    expect(t.net.sockets).toHaveLength(1);
  });

  it("CL-10: v が 2 の電文で version_mismatch。close() で closed", () => {
    const t = setup();
    const s = t.ready(0);
    s.deliver({ v: 2, type: "events", seq: 2, payload: evs() });
    expect(t.conn.getStatus()).toMatchObject({ state: "failed", reason: "version_mismatch" });
    expect(s.closedBy[0]?.code).toBe(1000);

    const u = setup();
    const s2 = u.ready(0);
    u.conn.close();
    expect(u.conn.getStatus().state).toBe("closed");
    expect(s2.closedBy).toHaveLength(1);
    s2.serverClose(1000);
    u.timers.advance(60000);
    expect(u.net.sockets).toHaveLength(1);
    u.conn.close();
    expect(u.conn.getStatus().state).toBe("closed");
  });

  it("CL-11: コマンドに cmdId を付けて送り、ack で解決する", async () => {
    const t = setup();
    const s = t.ready(0);
    const p1 = t.conn.sendCommand("cmd.ferry.load", { ferryId: "f1", letterIds: [12, 13] });
    expect(s.sent[s.sent.length - 1]).toBe(
      '{"v":1,"type":"cmd.ferry.load","cmdId":"c-1","payload":{"ferryId":"f1","letterIds":[12,13]}}',
    );
    s.msg("ack", { cmdId: "c-1", ok: true, t: 61200 });
    await expect(p1).resolves.toEqual({ cmdId: "c-1", ok: true, t: 61200 });

    const p2 = t.conn.sendCommand("cmd.ferry.depart", { ferryId: "f1", to: 3 });
    expect(s.parsed()[s.sent.length - 1]?.cmdId).toBe("c-2");
    s.msg("ack", { cmdId: "c-99", ok: true, t: 0 });
    s.msg("ack", { cmdId: "c-2", ok: false, t: 61300, code: "route_blocked", message: "嵐" });
    await expect(p2).resolves.toEqual({
      cmdId: "c-2",
      ok: false,
      t: 61300,
      code: "route_blocked",
      message: "嵐",
    });
  });

  it("CL-12: コマンドの失敗は CommandError で reason が合う", async () => {
    const a = setup();
    a.conn.start();
    a.net.last().open();
    const p = a.conn.sendCommand("cmd.game.start", {});
    await expect(p).rejects.toBeInstanceOf(CommandError);
    await expect(p).rejects.toMatchObject({ reason: "not_connected" });
    expect(a.net.last().types()).toEqual(["hello"]);

    const b = setup();
    const sb = b.ready(0);
    const pb = b.conn.sendCommand("cmd.game.start", {});
    sb.serverClose(1006);
    await expect(pb).rejects.toMatchObject({ reason: "disconnected" });

    const c = setup();
    const sc = c.ready(0);
    const pc = c.conn.sendCommand("cmd.game.start", {});
    c.timers.advance(10000);
    await expect(pc).rejects.toMatchObject({ reason: "timeout" });
    sc.msg("ack", { cmdId: "c-1", ok: true, t: 0 });
    expect(c.conn.getStatus().state).toBe("ready");

    const d = setup();
    d.ready(0);
    const pd = d.conn.sendCommand("cmd.game.start", {});
    d.conn.close();
    await expect(pd).rejects.toBeInstanceOf(CommandError);
    await expect(pd).rejects.toMatchObject({ reason: "closed" });
  });

  it("CL-13: エンジニアの購読を覚え、スナップショットの後に送り直す", () => {
    const t = setup();
    t.conn.start();
    const s1 = t.net.last();
    s1.open();
    t.conn.setEngineerSubscription({ enabled: true, nodeId: 2 });
    expect(s1.types()).toEqual(["hello"]);
    s1.msg("snapshot", snap(0));
    expect(sentOf(s1, "sub.engineer")).toEqual([
      { v: 1, type: "sub.engineer", payload: { enabled: true, nodeId: 2 } },
    ]);
    t.conn.setEngineerSubscription({ enabled: true, nodeId: 3 });
    expect(sentOf(s1, "sub.engineer")).toHaveLength(2);

    s1.serverClose(1006);
    t.timers.advance(500);
    const s2 = t.net.last();
    s2.open();
    expect(sentOf(s2, "sub.engineer")).toHaveLength(0);
    s2.msg("snapshot", snap(0));
    expect(sentOf(s2, "sub.engineer").map((m) => m.payload)).toEqual([
      { enabled: true, nodeId: 3 },
    ]);
    t.conn.setEngineerSubscription({ enabled: false });
    expect(sentOf(s2, "sub.engineer")).toHaveLength(2);

    s2.serverClose(1006);
    t.timers.advance(500);
    const s3 = t.net.last();
    s3.open();
    s3.msg("snapshot", snap(0));
    expect(sentOf(s3, "sub.engineer")).toHaveLength(0);
  });

  it("CL-14: 状態の変わり目でだけ通知し、トークンを含まない", () => {
    const t = setup();
    const s = t.ready(0);
    s.msg("snapshot", snap(0));
    s.msg("events", evs(2));
    s.msg("events", evs(5));
    s.msg("snapshot", snap(5));
    s.serverClose(1006);
    t.timers.advance(500);
    t.conn.close();
    t.conn.close();
    expect(t.statuses.map((x) => x.state)).toEqual([
      "connecting",
      "ready",
      "syncing",
      "ready",
      "reconnecting",
      "connecting",
      "closed",
    ]);
    for (let i = 1; i < t.statusObjects.length; i++) {
      expect(t.statusObjects[i]).not.toEqual(t.statusObjects[i - 1]);
    }
    expect(t.conn.getStatus()).toBe(t.statusObjects[t.statusObjects.length - 1]);
    const text = JSON.stringify(t.statusObjects);
    expect(text).not.toContain("tok-SECRET");
    expect(text).not.toContain(encodeURIComponent(TOKEN));
  });

  it("CL-15: ハンドラーが投げても次の events を渡し、例外はマイクロタスクで投げ直す", () => {
    const queued: (() => void)[] = [];
    vi.spyOn(globalThis, "queueMicrotask").mockImplementation((fn) => {
      queued.push(fn);
    });
    let calls = 0;
    const timers = createManualTimers();
    const net = fakeNetwork();
    const conn = createGameConnection({
      session: { wsPath: "/ws/sessions/s_test.sig", token: TOKEN },
      location: { protocol: "https:", host: "raft.example" },
      socketFactory: net.factory,
      timers,
      handlers: {
        onSnapshot: () => {},
        onEvents: () => {
          calls++;
          if (calls === 1) {
            throw new Error("boom");
          }
        },
      },
    });
    conn.start();
    const s = net.last();
    s.open();
    s.msg("snapshot", snap(0));
    s.msg("events", evs(1));
    s.msg("events", evs(2));
    expect(calls).toBe(2);
    expect(queued).toHaveLength(1);
    expect(() => queued[0]?.()).toThrow("boom");
    expect(conn.getStatus().state).toBe("ready");
  });

  it("CL-16: 壊れた電文で例外が出ず、直後の正しい電文の seq が抜けとして検出される", () => {
    const t = setup();
    const s = t.ready(0); // seq 1
    const before = t.conn.getStatus();
    expect(() => {
      s.raw("{");
      s.raw(new ArrayBuffer(2));
      s.msg("ack", { cmdId: "c-1", ok: "true", t: 0 }); // seq 2（malformed。数えない）
    }).not.toThrow();
    expect(t.conn.getStatus()).toBe(before);
    expect(sentOf(s, "cmd.sync")).toHaveLength(0);
    s.msg("pong", { clientTime: 0, simTime: 0 }); // seq 3
    expect(sentOf(s, "cmd.sync")).toHaveLength(1);
  });
});
