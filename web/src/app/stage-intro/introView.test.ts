import { describe, expect, it } from "vitest";

import type { StageTextSource } from "../types.ts";
import {
  LEARNING_HIDDEN_STAGE_IDS,
  LEARNING_LABELS,
  learningView,
  PLAN_FIELD_LABELS,
  sourceText,
} from "./introView.ts";

const SOURCE: StageTextSource = {
  dataset: "mlit-ritou-2026",
  region: "小豆島地域",
  field: "NATURAL_CLIMATE_FEATURES",
  quote: "温暖、小雨の典型的な瀬戸内式気候",
};

describe("sourceText", () => {
  it.each<[string, StageTextSource, string]>([
    ["計画書の項目", SOURCE, "離島振興計画書（小豆島地域）の「自然・気象特性」から"],
    [
      "知らない項目は英字のまま",
      { ...SOURCE, field: "AIR_TRANSPORT" },
      "離島振興計画書（小豆島地域）の「AIR_TRANSPORT」から",
    ],
    [
      "Object.prototype のキーも項目の名前として読まない",
      { ...SOURCE, field: "toString" },
      "離島振興計画書（小豆島地域）の「toString」から",
    ],
    [
      "ほかのデータセット",
      { ...SOURCE, dataset: "other" },
      "離島振興計画書（小豆島地域）の「自然・気象特性」から（データ: other）",
    ],
    [
      "引用は含めない",
      { ...SOURCE, quote: null },
      "離島振興計画書（小豆島地域）の「自然・気象特性」から",
    ],
  ])("IV-1 %s", (_label, source, expected) => {
    expect(sourceText(source)).toBe(expected);
  });
});

describe("learningView", () => {
  it("IV-2 stage1 は伏せる", () => {
    expect(learningView("stage1", ["log_replication"])).toEqual({ status: "hidden" });
    expect(learningView("stage1", [])).toEqual({ status: "hidden" });
  });

  it("IV-2 learning の順に知っているキーだけを並べる", () => {
    expect(learningView("stage2", ["heartbeat", "unknown_key", "check_quorum"])).toEqual({
      status: "shown",
      items: [
        { key: "heartbeat", game: "定期便", raft: "Heartbeat" },
        { key: "check_quorum", game: "孤立した代表が退く", raft: "CheckQuorum" },
      ],
    });
  });

  it("IV-2 重複は 1 回だけ", () => {
    expect(learningView("stage2", ["quorum", "quorum"])).toEqual({
      status: "shown",
      items: [{ key: "quorum", game: "過半数", raft: "Quorum" }],
    });
  });

  it.each<[string, string[]]>([
    ["空", []],
    ["知らないキーだけ", ["unknown_key"]],
    ["Object.prototype のキー", ["constructor", "toString"]],
  ])("IV-2 %s → none", (_label, learning) => {
    expect(learningView("demo-izu", learning)).toEqual({ status: "none" });
  });
});

describe("表", () => {
  it("IV-3 PLAN_FIELD_LABELS に D2 の 7 項目がある", () => {
    expect(Object.keys(PLAN_FIELD_LABELS).sort()).toEqual(
      [
        "GEO_LOCATION_AREA",
        "NATURAL_CLIMATE_FEATURES",
        "POPULATION_HOUSEHOLDS",
        "MARITIME_TRANSPORT",
        "PORTS",
        "LOCAL_INDUSTRY_TOURISM",
        "DISASTER_LAND_CONSERVATION",
      ].sort(),
    );
  });

  it("IV-3 LEARNING_LABELS に 10 個のキーがある", () => {
    expect(Object.keys(LEARNING_LABELS).sort()).toEqual(
      [
        "log_replication",
        "commit",
        "quorum",
        "prevote",
        "heartbeat",
        "election_timeout",
        "leader_election",
        "check_quorum",
        "state_machine_replication",
        "network_partition",
      ].sort(),
    );
    for (const label of Object.values(LEARNING_LABELS)) {
      expect(label.game).not.toBe("");
      expect(label.raft).not.toBe("");
    }
  });

  it("IV-3 伏せるのは stage1 だけ", () => {
    expect([...LEARNING_HIDDEN_STAGE_IDS]).toEqual(["stage1"]);
  });
});
