import { describe, expect, it } from "vitest";

import miniRecord from "../fixtures/mini-record.json";
import miniStage from "../fixtures/mini-stage.json";
import type { IslandState, Score, StageView } from "../../protocol/index.ts";
import {
  createRecordServer as createRecordServerFromIndex,
  parseRecord as parseRecordFromIndex,
  readOnlyAckPolicy as readOnlyAckPolicyFromIndex,
} from "../index.ts";
import { createManualTimers, type ManualTimers } from "../timers.ts";
import { parseRecord } from "./record.ts";
import {
  createRecordServer,
  readOnlyAckPolicy,
  type RecordServer,
  type RecordServerOptions,
} from "./server.ts";

type Wire = { v: number; type: string; seq: number; payload: Record<string, unknown> };

const URL_OK = "wss://raft.example/ws/sessions/s_test?token=tok";

function rawClient(server: RecordServer) {
  const got: Wire[] = [];
  let n = 0;
  const sock = server.socketFactory(URL_OK, {
    onOpen: () => {},
    onMessage: (data) => got.push(JSON.parse(data as string) as Wire),
    onClose: () => {},
    onError: () => {},
  });
  const send = (type: string, payload: unknown = {}, cmdId?: string) =>
    sock.send(JSON.stringify({ v: 1, type, ...(cmdId !== undefined ? { cmdId } : {}), payload }));
  return {
    got,
    send,
    cmd: (type: string, payload: unknown = {}) => {
      n++;
      send(type, payload, `c-${n}`);
      return `c-${n}`;
    },
    of: (type: string) => got.filter((m) => m.type === type),
    acks: () => got.filter((m) => m.type === "ack").map((m) => m.payload),
    eventSeqs: () =>
      got
        .filter((m) => m.type === "events")
        .flatMap((m) => (m.payload["events"] as { seq: number }[]).map((e) => e.seq)),
  };
}

function setup(options: Partial<RecordServerOptions> = {}, input: unknown = miniRecord) {
  const timers: ManualTimers = createManualTimers();
  const server = createRecordServer(parseRecord(input), {
    timers,
    stage: miniStage as StageView,
    ...options,
  });
  return { timers, server };
}

describe("readOnlyAckPolicy", () => {
  it("RP-4: 船などの操作は phase_invalid で断り、開始・一時停止・速度・skip は受け付けて再生が進む", () => {
    const t = setup({ ackPolicy: readOnlyAckPolicy });
    const c = rawClient(t.server);
    t.timers.advance(0);
    c.send("hello", { client: "test", resume: false });
    expect(c.cmd("cmd.game.start")).toBe("c-1");
    t.timers.advance(1000);
    expect(t.server.simTime()).toBe(1000);

    const ops: [string, unknown][] = [
      ["cmd.ferry.load", { ferryId: "f1", letterIds: [2] }],
      ["cmd.ferry.unload", { ferryId: "f1", letterIds: [2] }],
      ["cmd.ferry.autoload", { ferryId: "f1", enabled: true }],
      ["cmd.ferry.depart", { ferryId: "f1", to: 3 }],
      ["cmd.relay.load", { islandId: 2 }],
      ["demo.trigger", { kind: "storm" }],
    ];
    for (const [type, payload] of ops) {
      c.cmd(type, payload);
    }
    t.timers.advance(0);
    const refused = c.acks().slice(1);
    expect(refused).toHaveLength(ops.length);
    for (const [i, ack] of refused.entries()) {
      expect(ack).toEqual({
        cmdId: `c-${i + 2}`,
        ok: false,
        t: 1000,
        code: "phase_invalid",
        message: "リプレイでは操作できません",
      });
    }

    c.cmd("cmd.game.speed", { speed: 2 });
    t.timers.advance(100);
    expect(t.server.simTime()).toBe(1200);
    c.cmd("cmd.game.pause", { paused: true });
    t.timers.advance(1000);
    expect(t.server.simTime()).toBe(1200);
    c.cmd("cmd.game.pause", { paused: false });
    t.timers.advance(100);
    expect(t.server.simTime()).toBe(1400);
    c.cmd("cmd.game.skip", { until: "next_notable" });
    t.timers.advance(0);
    expect(t.server.simTime()).toBe(7000);
    expect(
      c
        .acks()
        .slice(-4)
        .map((a) => a["ok"]),
    ).toEqual([true, true, true, true]);
    expect(c.eventSeqs().at(-1)).toBe(14);
  });
});

describe("createRecordServer（replay）", () => {
  it("RP-5: islandsState・score・sessionId が snapshot の電文に入る", () => {
    const islandsState: IslandState[] = [{ id: 1, satisfaction: 70, lastDeliveredAt: null }];
    const score: Score = { total: 10, successes: 1, target: 4, criticalSecured: [] };
    const t = setup({ islandsState, score, sessionId: "replay_mini_1" });
    const c = rawClient(t.server);
    t.timers.advance(0);
    c.send("hello", { client: "test", resume: false });
    t.timers.advance(0);
    const snapshot = c.of("snapshot")[0]?.payload;
    expect((snapshot?.["session"] as { id: string }).id).toBe("replay_mini_1");
    expect(snapshot?.["islandsState"]).toEqual(islandsState);
    expect(snapshot?.["score"]).toEqual(score);
  });

  it("AC-3: bot が空文字列の記録を最後まで再生できる", () => {
    const input = structuredClone(miniRecord) as unknown as Record<string, unknown>;
    input["bot"] = "";
    const t = setup({ ackPolicy: readOnlyAckPolicy }, input);
    const c = rawClient(t.server);
    t.timers.advance(0);
    c.send("hello", { client: "test", resume: false });
    c.cmd("cmd.game.start");
    t.timers.advance(60000);
    expect(c.eventSeqs()).toEqual(Array.from({ length: 30 }, (_, i) => i + 1));
    expect(c.got.at(-1)?.type).toBe("round.result");
  });
});

describe("net/index.ts", () => {
  it("RP-6: parseRecord・createRecordServer・readOnlyAckPolicy を import できる", () => {
    expect(parseRecordFromIndex).toBe(parseRecord);
    expect(createRecordServerFromIndex).toBe(createRecordServer);
    expect(readOnlyAckPolicyFromIndex).toBe(readOnlyAckPolicy);
  });
});
