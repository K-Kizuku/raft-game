// プロトコルの型のテスト（T-301）。型の検査は pnpm typecheck（tsc）でも効く。
import { describe, expect, expectTypeOf, it } from "vitest";
import generated from "./protocol.gen.ts?raw";
import {
  EventTypePhaseChanged,
  MessageTypeCmdFerryLoad,
  Version,
  type Ack,
  type ClientMessage,
  type EventType,
  type InternalMessage,
  type Letter,
  type LetterLabel,
  type ServerEvent,
  type ServerMessage,
  type Snapshot,
} from "./index.ts";

// summarize は ServerEvent を 22 種類に分ける（足し忘れは default の never への代入で型エラーになる）。
function summarize(ev: ServerEvent): string {
  switch (ev.type) {
    case "phase.changed":
      return ev.phase;
    case "letter.created": {
      const label: LetterLabel = ev.letter.label;
      return label;
    }
    case "letter.moved":
      return `${ev.letterId} ${ev.loc.type}`;
    case "letter.delivered":
      return `${ev.letterId} ${ev.islandId} ${ev.accepted}`;
    case "letter.dropped":
      return `${ev.letterId} ${ev.reason}`;
    case "letter.stale":
      return `${ev.letterId}`;
    case "ferry.departed":
      return `${ev.ferryId} ${ev.from} ${ev.to} ${ev.departAt} ${ev.arriveAt}`;
    case "ferry.arrived":
      return `${ev.ferryId} ${ev.islandId} ${ev.readyAt}`;
    case "ferry.broken":
    case "ferry.repaired":
      return `${ev.ferryId} ${ev.until}`;
    case "route.blocked":
    case "route.unblocked":
      return `${ev.routeIds.join(",")} ${ev.until}`;
    case "forecast":
      return `${ev.event} ${ev.at} ${ev.routeIds.join(",")}`;
    case "node.state":
      return `${ev.id} ${ev.role} ${ev.term} ${ev.lead} ${ev.commit} ${ev.applied} ${ev.lastIndex}`;
    case "node.leader_elected":
      return `${ev.id} ${ev.term}`;
    case "node.stepped_down":
      return `${ev.id} ${ev.term} ${ev.reason}`;
    case "request.created":
      return ev.request.status;
    case "request.updated":
      return `${ev.id} ${ev.status} ${ev.expired} ${ev.result ?? "null"}`;
    case "ledger.applied":
      return `${ev.islandId} ${ev.index} ${ev.reqId} ${ev.result} ${ev.reason ?? ""}`;
    case "island.updated":
      return `${ev.id} ${ev.satisfaction} ${ev.lastDeliveredAt ?? "null"}`;
    case "score.updated":
      return `${ev.total} ${ev.successes} ${ev.criticalSecured.join(",")}`;
    case "notice":
      return `${ev.islandId} ${ev.key} ${Object.keys(ev.params).join(",")}`;
    default: {
      const unreachable: never = ev;
      return unreachable;
    }
  }
}

describe("ServerEvent", () => {
  it("W-1: EventType と ServerEvent の type が過不足なく一致する", () => {
    expectTypeOf<Exclude<EventType, ServerEvent["type"]>>().toEqualTypeOf<never>();
    expectTypeOf<Exclude<ServerEvent["type"], EventType>>().toEqualTypeOf<never>();
  });

  it("W-2: switch で 22 種類に分けられ、letter.created で label が LetterLabel として読める", () => {
    const letter = {
      id: 31,
      kind: "raft",
      from: 1,
      to: 2,
      createdAt: 57000,
      loc: { type: "port", islandId: 1 },
      label: "append",
    } satisfies Letter;
    const ev: ServerEvent = { seq: 1, t: 2, type: "letter.created", letter };
    if (ev.type === "letter.created") {
      expectTypeOf(ev.letter.label).toEqualTypeOf<LetterLabel>();
    }
    expect(summarize(ev)).toBe("append");
    expect(summarize({ seq: 2, t: 3, type: "phase.changed", phase: "sailing" })).toBe("sailing");
  });
});

describe("プロトコルの例", () => {
  it("W-3: §5.1 の Snapshot を型に当てはめられる", () => {
    const snapshot = {
      protocol: 1,
      session: { id: "s_x", stageId: "stage2", seed: 20261101, demo: false, restored: false },
      phase: "sailing",
      clock: { t: 61200, speed: 1, paused: false, endsAt: 210000, allowedSpeeds: [0.5, 1, 2, 4] },
      stage: {
        title: "島から返事が来ない！",
        islands: [
          {
            id: 1,
            name: "福江島",
            municipality: "五島市",
            layout: { x: 620, y: 180 },
            population: {
              value: 31945,
              asOf: "令和8年4月1日",
              provenance: "fact",
              source: "mlit-ritou-2026",
            },
            color: "#E07A5F",
          },
        ],
        routes: [{ id: "1-2", a: 1, b: 2, travelMs: 4000, provenance: "creative" }],
        resources: [{ id: "firework:a", kind: "firework", label: "大花火 A", critical: true }],
      },
      ferries: [
        {
          id: "f1",
          name: "ごとう丸",
          capacity: 4,
          state: "sailing",
          from: 1,
          to: 2,
          departAt: 58000,
          arriveAt: 67000,
          readyAt: 68500,
          cargo: [31, 32],
        },
        {
          id: "f2",
          name: "なる丸",
          capacity: 4,
          state: "docked",
          islandId: 3,
          departAt: 0,
          arriveAt: 0,
          readyAt: 61500,
          cargo: [],
        },
      ],
      routesState: [{ id: "1-2", blockedUntil: null }],
      letters: [],
      requests: [],
      nodes: [{ id: 1, role: "leader", term: 2, lead: 1, commit: 14, applied: 14, lastIndex: 15 }],
      islandsState: [{ id: 1, satisfaction: 64, lastDeliveredAt: 60500 }],
      score: { total: 520, successes: 3, target: 5, criticalSecured: ["firework:a"] },
      lastSeq: 41,
    } satisfies Snapshot;
    expect(snapshot.protocol).toBe(Version);
  });

  it("W-3: §5.2 の Letter を型に当てはめられる", () => {
    const letter = {
      id: 31,
      kind: "raft",
      from: 1,
      to: 2,
      createdAt: 57000,
      loc: { type: "ferry", ferryId: "f1" },
      label: "append",
      raft: {
        type: "MsgApp",
        term: 2,
        logTerm: 2,
        index: 13,
        commit: 13,
        entries: [
          { index: 14, term: 2, type: "normal", summary: "stall.reserve stage2-0005 (奈留島)" },
        ],
        reject: false,
        rejectHint: 0,
        stale: false,
      },
    } satisfies Letter;
    expect(letter.raft.entries).toHaveLength(1);
  });

  it("W-3: §5 の ack（成功と失敗）を型に当てはめられる", () => {
    const ok = { cmdId: "c-12", ok: true, t: 61200 } satisfies Ack;
    const ng = {
      cmdId: "c-12",
      ok: false,
      t: 61200,
      code: "route_blocked",
      message: "route 1-2 is blocked",
    } satisfies Ack;
    const msg = { v: 1, type: "ack", seq: 42, payload: ng } satisfies ServerMessage;
    expect(ok.ok).toBe(true);
    expect(msg.payload.code).toBe("route_blocked");
  });

  it("W-3: §4 の cmd.ferry.load と §9 の internal.init を型に当てはめられる", () => {
    const load = {
      v: 1,
      type: "cmd.ferry.load",
      cmdId: "c-12",
      payload: { ferryId: "f1", letterIds: [12, 13] },
    } satisfies ClientMessage;
    const init = {
      v: 1,
      type: "internal.init",
      payload: { sessionId: "s_x", stageId: "stage2", seed: 20261101, demo: false },
    } satisfies InternalMessage;
    expect(load.type).toBe(MessageTypeCmdFerryLoad);
    expect(init.payload.stageId).toBe("stage2");
  });

  it("W-3: 定数の実行時の値", () => {
    expect(Version).toBe(1);
    expect(MessageTypeCmdFerryLoad).toBe("cmd.ferry.load");
    expect(EventTypePhaseChanged).toBe("phase.changed");
  });
});

describe("生成物", () => {
  it("W-4: any・空の定数・埋め込みのフィールドの誤った出力がない", () => {
    expect(generated.startsWith("// Code generated by tygo. DO NOT EDIT.")).toBe(true);
    expect(generated).not.toMatch(/\bany\b/);
    expect(generated).not.toMatch(/=\s*;/);
    expect(generated).not.toMatch(/EventHeader: EventHeader/);
  });
});
