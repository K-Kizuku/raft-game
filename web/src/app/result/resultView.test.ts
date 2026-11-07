import { describe, expect, it } from "vitest";

import type { FinalLog, RoundResult } from "../../protocol/index.ts";
import {
  TERM_COLORS,
  clampStars,
  deliveredRows,
  formatPoints,
  formatSeconds,
  formatSecondsOrDash,
  hasFireworks,
  logBands,
  logScale,
  normalizeResult,
  termColor,
} from "./resultView.ts";
import { FULL_RESULT, SCORE_ONLY_RESULT, resultWith } from "./testing/results.ts";

function log(overrides: Partial<FinalLog>): FinalLog {
  return {
    id: 1,
    lastIndex: 24,
    commit: 24,
    terms: [
      [1, 1, 9],
      [3, 10, 24],
    ],
    ...overrides,
  };
}

describe("formatPoints", () => {
  it.each([
    [1300, true, "+1,300"],
    [-100, true, "−100"],
    [0, true, "0"],
    [1520, false, "1,520"],
    [-50, false, "−50"],
  ])("RV-1 (%d, %s) →「%s」", (value, signed, want) => {
    expect(formatPoints(value, signed)).toBe(want);
  });
});

describe("formatSeconds", () => {
  it.each([
    [18400, "18.4 秒"],
    [41000, "41.0 秒"],
    [61299, "61.2 秒"],
    [99, "0.0 秒"],
    [-5, "0.0 秒"],
  ])("RV-2 %d →「%s」", (ms, want) => {
    expect(formatSeconds(ms)).toBe(want);
  });

  it.each([
    [0, "—"],
    [-1, "—"],
    [18400, "18.4 秒"],
  ])("RV-2 formatSecondsOrDash %d →「%s」", (ms, want) => {
    expect(formatSecondsOrDash(ms)).toBe(want);
  });
});

describe("clampStars・hasFireworks", () => {
  it.each<[unknown, number]>([
    [2, 2],
    [5, 3],
    [-1, 0],
    [1.9, 1],
    [Number.NaN, 0],
    ["2", 0],
    [null, 0],
  ])("RV-3 clampStars(%s) → %d", (value, want) => {
    expect(clampStars(value)).toBe(want);
  });

  it.each([
    [0, false],
    [1, false],
    [2, true],
    [3, true],
  ] as const)("RV-3 hasFireworks(%d) → %s", (stars, want) => {
    expect(hasFireworks(stars)).toBe(want);
  });
});

describe("normalizeResult", () => {
  it("RV-4 FULL_RESULT", () => {
    const view = normalizeResult(FULL_RESULT);
    expect(view.total).toBe(1520);
    expect(view.stars).toBe(2);
    expect(view.breakdown).toEqual({
      requests: 1300,
      expired: -100,
      ferryCost: -180,
      fairness: 500,
    });
    expect(view.technical).not.toBeNull();
    expect(view.story).toHaveLength(1);
  });

  it("RV-4 SCORE_ONLY_RESULT", () => {
    const view = normalizeResult(SCORE_ONLY_RESULT);
    expect(view.technical).toBeNull();
    expect(view.story).toEqual([]);
    expect(view.succeeded).toBe(1);
    expect(view.total).toBe(396);
  });

  it("RV-4 stats の無い値 → 数はすべて 0", () => {
    const view = normalizeResult({
      score: { total: null, breakdown: { requests: "1", expired: Number.NaN } },
      stars: 2,
    } as unknown as RoundResult);
    expect(view.total).toBe(0);
    expect(view.breakdown).toEqual({ requests: 0, expired: 0, ferryCost: 0, fairness: 0 });
    expect(view.succeeded).toBe(0);
    expect(view.rejected).toBe(0);
    expect(view.expired).toBe(0);
    expect(view.technical).toBeNull();
    expect(view.story).toEqual([]);
  });

  it("RV-4 空の lettersDelivered・finalLogs → technical がある", () => {
    const view = normalizeResult(
      resultWith({ stats: { ...FULL_RESULT.stats, lettersDelivered: {}, finalLogs: [] } }),
    );
    expect(view.technical).not.toBeNull();
    expect(view.technical?.lettersDelivered).toEqual({});
    expect(view.technical?.finalLogs).toEqual([]);
  });

  it("RV-4 finalLogs は id の昇順・lettersDelivered は有限の数だけ", () => {
    const view = normalizeResult(
      resultWith({
        stats: {
          ...FULL_RESULT.stats,
          lettersDelivered: { heartbeat: 3, vote: null, append: "2" } as unknown as Record<
            string,
            number
          >,
          finalLogs: [log({ id: 3 }), null, log({ id: 1 })] as unknown as FinalLog[],
        },
      }),
    );
    expect(view.technical?.finalLogs.map((l) => l.id)).toEqual([1, 3]);
    expect(view.technical?.lettersDelivered).toEqual({ heartbeat: 3 });
  });

  it("RV-4 story の text が数の項目は落ちる", () => {
    const view = normalizeResult(
      resultWith({
        story: [
          { t: 1000, text: 5, concepts: ["Vote"] },
          { t: 2000, text: "残る", concepts: null },
          { t: 3000, text: "残る 2", concepts: ["A", 1] },
        ] as unknown as RoundResult["story"],
      }),
    );
    expect(view.story).toEqual([
      { t: 2000, text: "残る", concepts: [] },
      { t: 3000, text: "残る 2", concepts: ["A"] },
    ]);
  });
});

describe("deliveredRows", () => {
  it("RV-5 FULL_RESULT の値", () => {
    const { rows, total } = deliveredRows(FULL_RESULT.stats.lettersDelivered);
    expect(rows.map((r) => r.label)).toEqual([
      "heartbeat",
      "append",
      "append_resp",
      "vote",
      "request",
    ]);
    expect(total).toBe(93);
    expect(rows[0]).toEqual({
      label: "heartbeat",
      name: "お元気ですか？",
      raft: "MsgHeartbeat",
      count: 40,
    });
    expect(rows[4]?.raft).toBeNull();
  });

  it("RV-5 知らない label は後ろにコードポイントの昇順、0 以下は出さない", () => {
    const { rows, total } = deliveredRows({ zzz: 2, aaa: 1, heartbeat: 0 });
    expect(rows.map((r) => r.label)).toEqual(["aaa", "zzz"]);
    expect(rows[0]?.name).toBe("その他（aaa）");
    expect(rows[0]?.raft).toBeNull();
    expect(total).toBe(3);
  });
});

describe("logScale・logBands", () => {
  it("RV-6 すべて確定", () => {
    expect(logBands(log({}), 24)).toEqual({
      offset: 0,
      segments: [
        { term: 1, first: 1, last: 9, committed: true, size: 9 },
        { term: 3, first: 10, last: 24, committed: true, size: 15 },
      ],
      padding: 0,
    });
  });

  it("RV-6 commit 20 で分ける", () => {
    expect(logBands(log({ commit: 20 }), 24).segments).toEqual([
      { term: 1, first: 1, last: 9, committed: true, size: 9 },
      { term: 3, first: 10, last: 20, committed: true, size: 11 },
      { term: 3, first: 21, last: 24, committed: false, size: 4 },
    ]);
  });

  it("RV-6 scale 30 → padding 6", () => {
    expect(logBands(log({}), 30).padding).toBe(6);
  });

  it("RV-6 壊れた要素は使わない", () => {
    expect(logBands(log({ terms: [[2, 5, 3]] }), 30)).toEqual({
      offset: 0,
      segments: [],
      padding: 30,
    });
    const broken = [[1, 0, 3], [1, 2], "x"] as unknown as FinalLog["terms"];
    expect(logBands(log({ terms: broken }), 10).segments).toEqual([]);
  });

  it("RV-6 offset", () => {
    expect(logBands(log({ lastIndex: 15, commit: 15, terms: [[4, 11, 15]] }), 15).offset).toBe(10);
  });

  it("RV-6 logScale", () => {
    expect(logScale([])).toBe(0);
    expect(
      logScale([log({ id: 1, lastIndex: 24 }), log({ id: 2, lastIndex: 30, terms: [[1, 1, 30]] })]),
    ).toBe(30);
  });
});

describe("termColor", () => {
  it("RV-7 8 色をくり返す", () => {
    expect(TERM_COLORS).toHaveLength(8);
    expect(termColor(1)).toBe(termColor(9));
    expect(termColor(1)).not.toBe(termColor(2));
  });
});
