import { describe, expect, it } from "vitest";

import type { ServerEvent } from "../../protocol/index.ts";
import { createGameStore, type World } from "../../state/index.ts";
import { soundCues } from "./cues.ts";
import {
  CLOCK,
  arrived,
  delivered,
  departed,
  dropped,
  forecast,
  requestUpdated,
  soundSnapshot,
} from "./testing/snapshot.ts";

function worlds(events: readonly ServerEvent[]): [World, World] {
  const store = createGameStore();
  store.getState().applySnapshot(soundSnapshot(), 0);
  const prev = store.getState().world;
  store.getState().applyEvents(CLOCK, events, 1);
  const next = store.getState().world;
  if (prev === null || next === null) throw new Error("no world");
  return [prev, next];
}

describe("soundCues", () => {
  it("SC-1 出航", () => {
    expect(soundCues(...worlds([departed(11)]))).toEqual(["depart"]);
  });

  it("SC-2 到着", () => {
    expect(soundCues(...worlds([arrived(11)]))).toEqual(["arrive"]);
  });

  it("SC-3 配達（処分は鳴らさない）", () => {
    expect(soundCues(...worlds([delivered(11)]))).toEqual(["deliver"]);
    expect(soundCues(...worlds([dropped(11)]))).toEqual([]);
  });

  it("SC-4 依頼の確定（succeeded）だけ", () => {
    expect(soundCues(...worlds([requestUpdated(11, "succeeded")]))).toEqual(["confirm"]);
    expect(soundCues(...worlds([requestUpdated(11, "rejected")]))).toEqual([]);
  });

  it("SC-5 嵐の予報（ラッシュの予報は鳴らさない）", () => {
    expect(soundCues(...worlds([forecast(11, "storm")]))).toEqual(["storm"]);
    expect(soundCues(...worlds([forecast(11, "rush")]))).toEqual([]);
  });

  it("SC-6 1 回に同じ出来事がたくさん来ても、種類ごとに 1 つ（決まった順）", () => {
    const events = [
      forecast(11, "storm"),
      forecast(12, "storm"),
      requestUpdated(13, "succeeded"),
      delivered(14),
      arrived(15),
      departed(16),
    ];
    expect(soundCues(...worlds(events))).toEqual([
      "depart",
      "arrive",
      "deliver",
      "confirm",
      "storm",
    ]);
  });

  it("SC-7 関係のないイベント・古い seq では鳴らさない", () => {
    const notice: ServerEvent = {
      type: "notice",
      seq: 11,
      t: 21000,
      islandId: 1,
      key: "hello",
      params: {},
    };
    expect(soundCues(...worlds([notice]))).toEqual([]);
    // snapshot の lastSeq（10）以下のイベントはストアが捨てる
    expect(soundCues(...worlds([departed(10)]))).toEqual([]);
  });

  it("SC-8 snapshot を当てはめたとき（つなぎ直し・リプレイの頭出し）は鳴らさない", () => {
    const store = createGameStore();
    store.getState().applySnapshot(soundSnapshot(), 0);
    const prev = store.getState().world;
    // 船の状態が変わった snapshot が届いても、過去の出来事なので鳴らさない
    const snap = soundSnapshot({ lastSeq: 20 });
    store
      .getState()
      .applySnapshot(
        { ...snap, ferries: snap.ferries.map((f) => ({ ...f, state: "sailing" as const })) },
        1,
      );
    const next = store.getState().world;
    expect(soundCues(prev, next)).toEqual([]);
    expect(soundCues(null, next)).toEqual([]);
    expect(soundCues(prev, null)).toEqual([]);
    expect(soundCues(prev, prev)).toEqual([]);
  });
});
