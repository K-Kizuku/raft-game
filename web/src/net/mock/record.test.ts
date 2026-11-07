import { describe, expect, it } from "vitest";

import miniRecord from "../fixtures/mini-record.json";
import miniStage from "../fixtures/mini-stage.json";
import type { StageView } from "../../protocol/index.ts";
import { parseRecord, RecordFormatError, snapshotFromRecord } from "./record.ts";

type Mutable = Record<string, unknown> & {
  snapshot: Record<string, unknown>;
  events: Record<string, unknown>[];
};

const clone = (): Mutable => structuredClone(miniRecord) as unknown as Mutable;
const stage = miniStage as StageView;

describe("parseRecord", () => {
  it("RC-1: 手で書いた記録を読み、プロトコル §6 の 22 種類のイベントをすべて含む", () => {
    const record = parseRecord(miniRecord);
    expect(record.header).toEqual({
      format: 1,
      kind: "simcli.record",
      build: "test",
      stageId: "mini",
      stageHash: "sha256:test",
      seed: 1,
      bot: "greedy",
      durationMs: 60000,
      eventCount: 30,
      traceHash: "sha256:test",
    });
    expect(record.snapshot.lastSeq).toBe(0);
    expect(record.snapshot.phase).toBe("intro");
    expect(record.events).toHaveLength(30);
    expect(record.events.map((e) => e.seq)).toEqual(Array.from({ length: 30 }, (_, i) => i + 1));
    const all = [
      "phase.changed",
      "letter.created",
      "letter.moved",
      "letter.delivered",
      "letter.dropped",
      "letter.stale",
      "ferry.departed",
      "ferry.arrived",
      "ferry.broken",
      "ferry.repaired",
      "route.blocked",
      "route.unblocked",
      "forecast",
      "node.state",
      "node.leader_elected",
      "node.stepped_down",
      "request.created",
      "request.updated",
      "ledger.applied",
      "island.updated",
      "score.updated",
      "notice",
    ];
    expect(all).toHaveLength(22);
    expect([...new Set(record.events.map((e) => e.type))].sort()).toEqual([...all].sort());
  });

  const cases: { name: string; mutate: (r: Mutable) => void }[] = [
    { name: "format が 2", mutate: (r) => (r["format"] = 2) },
    { name: "kind が違う", mutate: (r) => (r["kind"] = "simcli.trace") },
    { name: "eventCount が 29", mutate: (r) => (r["eventCount"] = 29) },
    { name: "seq の飛び（1, 2, 4）", mutate: (r) => (r.events[2]!["seq"] = 4) },
    {
      name: "seq が 0 から",
      mutate: (r) => r.events.forEach((e, i) => (e["seq"] = i)),
    },
    { name: "t の逆転", mutate: (r) => (r.events[7]!["t"] = 500) },
    { name: "snapshot.letters が無い", mutate: (r) => delete r.snapshot["letters"] },
    { name: "endsAt が durationMs と違う", mutate: (r) => (r.snapshot["endsAt"] = 50000) },
    { name: "events が {}", mutate: (r) => (r["events"] = {} as unknown as []) },
  ];
  it.each(cases)("RC-2: $name → RecordFormatError", ({ mutate }) => {
    const r = clone();
    mutate(r);
    expect(() => parseRecord(r)).toThrow(RecordFormatError);
  });

  it("RC-2: 知らないイベントの type は通す", () => {
    const r = clone();
    r.events[29]!["type"] = "future.event";
    expect(parseRecord(r).events[29]?.type).toBe("future.event");
  });
});

describe("snapshotFromRecord", () => {
  it("RC-3: 記録に無い値をモックの値で埋める", () => {
    const record = parseRecord(clone());
    const snapshot = snapshotFromRecord(record, { stage });
    expect(snapshot.protocol).toBe(1);
    expect(snapshot.session).toEqual({
      id: "mock_mini_1",
      stageId: "mini",
      seed: 1,
      demo: false,
      restored: false,
    });
    expect(snapshot.phase).toBe("intro");
    expect(snapshot.clock).toEqual({
      t: 0,
      speed: 1,
      paused: false,
      endsAt: 60000,
      allowedSpeeds: [0.5, 1, 2, 4],
    });
    expect(snapshot.stage).toEqual(miniStage);
    expect(snapshot.requests).toEqual([]);
    expect(snapshot.islandsState).toEqual([]);
    expect(snapshot.score).toEqual({ total: 0, successes: 0, target: 0, criticalSecured: [] });
    expect(snapshot.ferries).toEqual(record.snapshot.ferries);
    expect(snapshot.nodes).toHaveLength(3);
    expect(snapshot.lastSeq).toBe(0);

    const bare = snapshotFromRecord(record);
    expect(bare.stage).toEqual({ title: "mini", islands: [], routes: [], resources: [] });

    bare.ferries.pop();
    bare.nodes.push(bare.nodes[0]!);
    bare.clock.allowedSpeeds.push(8);
    expect(record.snapshot.ferries).toHaveLength(1);
    expect(record.snapshot.nodes).toHaveLength(3);
    expect(snapshotFromRecord(record).clock.allowedSpeeds).toEqual([0.5, 1, 2, 4]);
  });

  it("RC-3: parseRecord の結果は入力と配列を共有しない", () => {
    const input = clone();
    const record = parseRecord(input);
    input.events.pop();
    (input.snapshot["ferries"] as unknown[]).pop();
    expect(record.events).toHaveLength(30);
    expect(record.snapshot.ferries).toHaveLength(1);
  });
});
