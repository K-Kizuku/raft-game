import { describe, expect, it } from "vitest";

import miniRecord from "./fixtures/mini-record.json";
import miniStage from "./fixtures/mini-stage.json";
import type { StageView } from "../protocol/index.ts";
import { createGameConnection } from "./client.ts";
import { parseRecord } from "./mock/record.ts";
import { createRecordServer } from "./mock/server.ts";
import { createManualTimers } from "./timers.ts";

type Log = { kind: "snapshot"; lastSeq: number } | { kind: "events"; seqs: number[] };

// setup は本物のクライアントと、記録を再生するモックのサーバーをつなぐ。
function setup() {
  const timers = createManualTimers();
  const server = createRecordServer(parseRecord(miniRecord), {
    timers,
    stage: miniStage as StageView,
  });
  const log: Log[] = [];
  let roundResults = 0;
  const conn = createGameConnection({
    socketFactory: server.socketFactory,
    timers,
    location: { protocol: "https:", host: "raft.example" },
    session: { wsPath: "/ws/sessions/s_test.sig", token: "tok" },
    handlers: {
      onSnapshot: (snapshot) => log.push({ kind: "snapshot", lastSeq: snapshot.lastSeq }),
      onEvents: (_clock, events) => {
        if (events.length > 0) {
          log.push({ kind: "events", seqs: events.map((e) => e.seq) });
        }
      },
      onRoundResult: () => roundResults++,
    },
  });
  const eventSeqs = () => log.flatMap((l) => (l.kind === "events" ? l.seqs : []));
  const snapshots = () => log.filter((l) => l.kind === "snapshot");
  return { timers, server, conn, log, eventSeqs, snapshots, roundResults: () => roundResults };
}

const range = (from: number, to: number) =>
  Array.from({ length: to - from + 1 }, (_, i) => from + i);

describe("記録の再生（本物のクライアント＋モックのサーバー）", () => {
  it("PB-1: 30 件のイベントが seq の順に 1 回ずつ渡り、round.result で ended", async () => {
    const t = setup();
    t.conn.start();
    t.timers.advance(0);
    expect(t.snapshots()).toHaveLength(1);
    expect(t.conn.getStatus().state).toBe("ready");
    const started = t.conn.sendCommand("cmd.game.start", {});
    t.timers.advance(0);
    await expect(started).resolves.toMatchObject({ ok: true });
    t.timers.advance(60100);
    expect(t.eventSeqs()).toEqual(range(1, 30));
    expect(t.roundResults()).toBe(1);
    expect(t.conn.getStatus().state).toBe("ended");
  });

  it("PB-2: 積む・出航のコマンドがそのままモックに届き、ack で解決する", async () => {
    const t = setup();
    t.conn.start();
    t.timers.advance(0);
    void t.conn.sendCommand("cmd.game.start", {});
    t.timers.advance(1500);
    const load = t.conn.sendCommand("cmd.ferry.load", { ferryId: "f1", letterIds: [2] });
    const depart = t.conn.sendCommand("cmd.ferry.depart", { ferryId: "f1", to: 3 });
    t.timers.advance(0);
    await expect(load).resolves.toMatchObject({ ok: true });
    await expect(depart).resolves.toMatchObject({ ok: true });
    const commands = t.server
      .received()
      .filter((m) => m.type === "cmd.ferry.load" || m.type === "cmd.ferry.depart");
    expect(commands.map((m) => ({ type: m.type, cmdId: m.cmdId, payload: m.payload }))).toEqual([
      { type: "cmd.ferry.load", cmdId: "c-2", payload: { ferryId: "f1", letterIds: [2] } },
      { type: "cmd.ferry.depart", cmdId: "c-3", payload: { ferryId: "f1", to: 3 } },
    ]);
  });

  it("PB-3: 途中の切断から、スナップショットとイベントの再適用で最後まで追いつく", () => {
    const t = setup();
    t.conn.start();
    t.timers.advance(0);
    void t.conn.sendCommand("cmd.game.start", {});
    t.timers.advance(3000);
    expect(t.eventSeqs().at(-1)).toBe(11);
    t.server.disconnect();
    t.timers.advance(0);
    expect(t.conn.getStatus().state).toBe("reconnecting");
    t.timers.advance(499);
    expect(t.snapshots()).toHaveLength(1);
    t.timers.advance(1);
    const hellos = t.server.received().filter((m) => m.type === "hello");
    expect(hellos.map((m) => m.payload)).toEqual([
      { client: "web/0.1.0", resume: false },
      { client: "web/0.1.0", resume: true },
    ]);
    expect(t.snapshots()).toEqual([
      { kind: "snapshot", lastSeq: 0 },
      { kind: "snapshot", lastSeq: 0 },
    ]);
    t.timers.advance(60000);
    expect(t.eventSeqs()).toEqual([...range(1, 11), ...range(1, 11), ...range(12, 30)]);
    expect(t.roundResults()).toBe(1);
    expect(t.conn.getStatus().state).toBe("ended");
  });

  it("PB-4: 電文の欠落から cmd.sync で追いつく", () => {
    const t = setup();
    t.conn.start();
    t.timers.advance(0);
    void t.conn.sendCommand("cmd.game.start", {});
    t.timers.advance(500);
    t.server.dropNextServerMessage(); // t 1000 の events（seq 7・8）を落とす
    t.timers.advance(60000);
    expect(t.server.received().filter((m) => m.type === "cmd.sync")).toHaveLength(1);
    expect(t.roundResults()).toBe(1);
    expect(t.conn.getStatus().state).toBe("ended");
    // 最後のスナップショットの後に受け入れたイベントは、その lastSeq から 30 までつながる。
    const lastSnap = t.log.map((l) => l.kind).lastIndexOf("snapshot");
    const snapshot = t.log[lastSnap];
    expect(snapshot?.kind).toBe("snapshot");
    const lastSeq = snapshot?.kind === "snapshot" ? snapshot.lastSeq : -1;
    const after = t.log.slice(lastSnap + 1).flatMap((l) => (l.kind === "events" ? l.seqs : []));
    expect(after).toEqual(range(lastSeq + 1, 30));
    expect(t.snapshots()).toHaveLength(2);
    expect(t.eventSeqs().slice(0, 6)).toEqual(range(1, 6));
  });
});
