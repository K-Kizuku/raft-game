import { describe, expect, it } from "vitest";

import miniRecord from "../fixtures/mini-record.json";
import miniStage from "../fixtures/mini-stage.json";
import type { StageView } from "../../protocol/index.ts";
import { createManualTimers, type ManualTimers } from "../timers.ts";
import { parseRecord } from "./record.ts";
import { createRecordServer, type RecordServer, type RecordServerOptions } from "./server.ts";

type Wire = { v: number; type: string; seq: number; payload: Record<string, unknown> };

const URL_OK = "wss://raft.example/ws/sessions/s_test?token=tok";

// rawClient はモックのサーバーを直接たたく、手で書いたクライアント側のハンドラー。
function rawClient(server: RecordServer, url = URL_OK) {
  const got: Wire[] = [];
  const closes: number[] = [];
  let opened = false;
  let n = 0;
  const sock = server.socketFactory(url, {
    onOpen: () => (opened = true),
    onMessage: (data) => got.push(JSON.parse(data as string) as Wire),
    onClose: (code) => closes.push(code),
    onError: () => {},
  });
  const send = (type: string, payload: unknown = {}, cmdId?: string) =>
    sock.send(JSON.stringify({ v: 1, type, ...(cmdId !== undefined ? { cmdId } : {}), payload }));
  return {
    got,
    closes,
    isOpen: () => opened,
    sock,
    send,
    cmd: (type: string, payload: unknown = {}) => {
      n++;
      send(type, payload, `c-${n}`);
      return `c-${n}`;
    },
    of: (type: string) => got.filter((m) => m.type === type),
    eventSeqs: () =>
      got
        .filter((m) => m.type === "events")
        .flatMap((m) => (m.payload["events"] as { seq: number }[]).map((e) => e.seq)),
    acks: () => got.filter((m) => m.type === "ack").map((m) => m.payload),
  };
}

function setup(options: Partial<RecordServerOptions> = {}) {
  const timers: ManualTimers = createManualTimers();
  const server = createRecordServer(parseRecord(miniRecord), {
    timers,
    stage: miniStage as StageView,
    ...options,
  });
  return { timers, server };
}

// started は接続して hello と cmd.game.start を送った状態にする。
function started(t: { timers: ManualTimers; server: RecordServer }) {
  const c = rawClient(t.server);
  t.timers.advance(0);
  c.send("hello", { client: "test", resume: false });
  c.cmd("cmd.game.start");
  return c;
}

describe("createRecordServer", () => {
  it("MS-1: hello でスナップショット。cmd.game.start の前は進まない", () => {
    const t = setup();
    const c = rawClient(t.server);
    expect(c.isOpen()).toBe(false);
    t.timers.advance(0);
    expect(c.isOpen()).toBe(true);
    c.send("hello", { client: "test", resume: false });
    expect(c.got).toHaveLength(0); // 同期で呼び返さない
    t.timers.advance(0);
    expect(c.got.map((m) => [m.type, m.seq, m.v])).toEqual([["snapshot", 1, 1]]);
    expect(c.got[0]?.payload["lastSeq"]).toBe(0);
    t.timers.advance(10000);
    expect(c.of("events")).toHaveLength(0);
    expect(t.server.simTime()).toBe(0);
  });

  it("MS-2: 再生して 30 件を順に送り、round.result を送る", () => {
    const t = setup();
    const c = started(t);
    t.timers.advance(100);
    expect(c.acks()).toEqual([{ cmdId: "c-1", ok: true, t: 0 }]);
    const first = c.of("events");
    expect(first).toHaveLength(1);
    expect(c.eventSeqs()).toEqual([1, 2, 3, 4, 5, 6]);
    t.timers.advance(59900);
    expect(c.eventSeqs()).toEqual(Array.from({ length: 30 }, (_, i) => i + 1));
    const last = c.got[c.got.length - 1];
    expect(last?.type).toBe("round.result");
    expect((last?.payload["score"] as { total: number }).total).toBe(100);
    expect(c.got.map((m) => m.seq)).toEqual(c.got.map((_, i) => i + 1));
    c.cmd("cmd.game.start");
    t.timers.advance(0);
    expect(c.acks()[1]).toMatchObject({ cmdId: "c-2", ok: false, code: "phase_invalid" });
    expect(c.closes).toEqual([]);
  });

  it("MS-3: 速度・一時停止", () => {
    const t = setup();
    const c = started(t);
    c.cmd("cmd.game.speed", { speed: 4 });
    t.timers.advance(100);
    expect(t.server.simTime()).toBe(400);
    c.cmd("cmd.game.speed", { speed: 3 });
    t.timers.advance(0);
    expect(c.acks()[2]).toMatchObject({ cmdId: "c-3", ok: false, code: "bad_request" });
    c.cmd("cmd.game.pause", { paused: true });
    t.timers.advance(1000);
    expect(t.server.simTime()).toBe(400);
    expect(c.acks()[3]).toMatchObject({ cmdId: "c-4", ok: true });
    c.cmd("cmd.game.pause", { paused: false });
    t.timers.advance(100);
    expect(t.server.simTime()).toBe(800);
  });

  it("MS-4: cmd.game.skip で次の船の到着まで進めて一時停止する", () => {
    const t = setup();
    const c = started(t);
    t.timers.advance(3000);
    expect(t.server.simTime()).toBe(3000);
    expect(c.eventSeqs().at(-1)).toBe(11);
    c.cmd("cmd.game.skip", { until: "next_notable" });
    t.timers.advance(0);
    expect(t.server.simTime()).toBe(7000);
    expect(c.eventSeqs().slice(-3)).toEqual([12, 13, 14]);
    const lastEvents = c.of("events").at(-1);
    expect((lastEvents?.payload["clock"] as { paused: boolean }).paused).toBe(true);
    expect(c.acks().at(-1)).toEqual({ cmdId: "c-2", ok: true, t: 7000 });
    t.timers.advance(5000);
    expect(t.server.simTime()).toBe(7000);
    expect(c.eventSeqs().at(-1)).toBe(14);
  });

  it("MS-5: コマンドを received() に残し、ack の t は simTime。ackPolicy で失敗を返せる", () => {
    const t = setup({
      ackPolicy: (msg) =>
        msg.type === "cmd.ferry.depart"
          ? { ok: false, code: "route_blocked", message: "嵐" }
          : { ok: true },
    });
    const c = started(t);
    t.timers.advance(1000);
    c.send("cmd.ferry.load", { ferryId: "f1", letterIds: [2] }, "c-9");
    c.send("cmd.ferry.depart", { ferryId: "f1", to: 3 }, "c-10");
    t.timers.advance(0);
    const received = t.server.received();
    expect(received.map((m) => m.type)).toEqual([
      "hello",
      "cmd.game.start",
      "cmd.ferry.load",
      "cmd.ferry.depart",
    ]);
    expect(received[2]).toEqual({
      type: "cmd.ferry.load",
      cmdId: "c-9",
      payload: { ferryId: "f1", letterIds: [2] },
      simTime: 1000,
    });
    expect(c.acks().slice(-2)).toEqual([
      { cmdId: "c-9", ok: true, t: 1000 },
      { cmdId: "c-10", ok: false, t: 1000, code: "route_blocked", message: "嵐" },
    ]);
  });

  it("MS-6: 切断の間は進まず、再接続でスナップショット＋送り済みのイベント。新しい接続で前の接続に 4001", () => {
    const t = setup();
    const c1 = started(t);
    t.timers.advance(3000);
    t.server.disconnect();
    t.timers.advance(0);
    expect(c1.closes).toEqual([1006]);
    t.timers.advance(5000);
    expect(t.server.simTime()).toBe(3000);

    const c2 = rawClient(t.server);
    t.timers.advance(0);
    c2.send("hello", { client: "test", resume: true });
    t.timers.advance(0);
    expect(c2.got.map((m) => m.type)).toEqual(["snapshot", "events"]);
    expect(c2.eventSeqs()).toEqual(Array.from({ length: 11 }, (_, i) => i + 1));
    expect(c2.got[0]!.seq).toBeGreaterThan(c1.got.at(-1)!.seq); // seq は接続をまたいで続ける
    t.timers.advance(100);
    expect(t.server.simTime()).toBe(3100);

    const c3 = rawClient(t.server);
    t.timers.advance(0);
    expect(c2.closes).toEqual([4001]);
    expect(c3.isOpen()).toBe(true);
  });

  it("MS-7: cmd.sync・v の違い・接続の拒否", () => {
    const t = setup();
    const c = started(t);
    t.timers.advance(1000);
    const before = c.got.length;
    c.send("cmd.sync");
    t.timers.advance(0);
    const after = c.got.slice(before);
    expect(after.map((m) => m.type)).toEqual(["snapshot", "events"]);
    expect((after[1]?.payload["events"] as unknown[]).length).toBe(8);

    c.sock.send(JSON.stringify({ v: 2, type: "hello", payload: {} }));
    t.timers.advance(0);
    expect(c.got.at(-1)).toMatchObject({ type: "error", payload: { fatal: true } });
    expect(c.closes).toEqual([1008]);

    t.server.refuseConnections(2);
    const r1 = rawClient(t.server);
    const r2 = rawClient(t.server);
    const r3 = rawClient(t.server);
    t.timers.advance(0);
    expect([r1.isOpen(), r2.isOpen(), r3.isOpen()]).toEqual([false, false, true]);
    expect([r1.closes, r2.closes, r3.closes]).toEqual([[1006], [1006], []]);

    const bad = rawClient(t.server, "wss://raft.example/api/stages");
    t.timers.advance(0);
    expect(bad.isOpen()).toBe(false);
    expect(bad.closes).toEqual([1006]);
  });
});
