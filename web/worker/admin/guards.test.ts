import { describe, expect, it } from "vitest";

import { overlayGuards, parseGuardsConfig, parseGuardsPatch, type GuardsConfig } from "./guards.ts";
import { DELETE, GUARDS_EXAMPLE, guardsWith, withValue } from "./test-fixtures.ts";

function config(raw: unknown = GUARDS_EXAMPLE): GuardsConfig {
  const parsed = parseGuardsConfig(raw);
  if (parsed === null) throw new Error("fixture is invalid");
  return parsed;
}

describe("C-1 parseGuardsConfig", () => {
  it("ADR-0018 の値が読める（JSON の文字列でも同じ）", () => {
    expect(parseGuardsConfig(GUARDS_EXAMPLE)).toEqual(GUARDS_EXAMPLE);
    expect(parseGuardsConfig(JSON.stringify(GUARDS_EXAMPLE))).toEqual(GUARDS_EXAMPLE);
  });

  const leafPaths = [
    "budget.enabled",
    "budget.monthlyUsd",
    "budget.dailyUsd",
    "gameTime.maxMinutes",
    "gameTime.maxAlarms",
    "turnstile.enabled",
    "rateLimit.enabled",
    "rateLimit.perIpPerMinute",
    "unitCostUsd.lite",
    "unitCostUsd.standard-1",
  ];
  const topKeys = Object.keys(GUARDS_EXAMPLE);

  const cases: readonly [string, unknown][] = [
    ["undefined", undefined],
    ["null", null],
    ['""', ""],
    ['"{"', "{"],
    ["[]", []],
    ...topKeys.map((key): [string, unknown] => [
      `${key} がない`,
      withValue(GUARDS_EXAMPLE, [key], DELETE),
    ]),
    ...leafPaths.map((path): [string, unknown] => [
      `${path} がない`,
      withValue(GUARDS_EXAMPLE, path.split("."), DELETE),
    ]),
    ["一番上に未知のキー", guardsWith({ extra: 1 })],
    ["budget の中に未知のキー", guardsWith({ "budget.extra": 1 })],
    ["signature のキー（enabled: false）", guardsWith({ signature: { enabled: false } })],
    ["signature のキー（enabled: true）", guardsWith({ signature: { enabled: true } })],
    ["monthlyUsd が -1", guardsWith({ "budget.monthlyUsd": -1 })],
    ["monthlyUsd が 1001", guardsWith({ "budget.monthlyUsd": 1001 })],
    ['monthlyUsd が "30"', guardsWith({ "budget.monthlyUsd": "30" })],
    ["monthlyUsd が NaN", guardsWith({ "budget.monthlyUsd": Number.NaN })],
    ["maxMinutes が 0", guardsWith({ "gameTime.maxMinutes": 0 })],
    ["maxMinutes が 61", guardsWith({ "gameTime.maxMinutes": 61 })],
    ["maxMinutes が 1.5", guardsWith({ "gameTime.maxMinutes": 1.5 })],
    ["maxAlarms が 0", guardsWith({ "gameTime.maxAlarms": 0 })],
    ["maxAlarms が 101", guardsWith({ "gameTime.maxAlarms": 101 })],
    ["perIpPerMinute が 0", guardsWith({ "rateLimit.perIpPerMinute": 0 })],
    ["perIpPerMinute が 1001", guardsWith({ "rateLimit.perIpPerMinute": 1001 })],
    ["cpuMs が 0", guardsWith({ cpuMs: 0 })],
    ["unitCostUsd.lite が 0", guardsWith({ "unitCostUsd.lite": 0 })],
    ["unitCostUsd.lite が -1", guardsWith({ "unitCostUsd.lite": -1 })],
    ["unitCostUsd.lite が 11", guardsWith({ "unitCostUsd.lite": 11 })],
    ["budget がオブジェクトでない", guardsWith({ budget: true })],
  ];

  it.each(cases)("%s は null", (_name, raw) => {
    expect(parseGuardsConfig(raw)).toBeNull();
  });

  it("上限ちょうどの値（0・1000・60・100・1000・300000・10）は読める", () => {
    const raw = guardsWith({
      "budget.monthlyUsd": 1000,
      "budget.dailyUsd": 0,
      "gameTime.maxMinutes": 60,
      "gameTime.maxAlarms": 100,
      "rateLimit.perIpPerMinute": 1000,
      cpuMs: 300000,
      "unitCostUsd.lite": 10,
    });
    expect(parseGuardsConfig(raw)).toEqual(raw);
  });
});

describe("C-2 parseGuardsPatch", () => {
  const okCases: readonly [string, unknown, Record<string, unknown>, string][] = [
    [
      "budget.enabled と前後の空白の reason",
      { budget: { enabled: false }, reason: " x " },
      { "budget.enabled": false },
      "x",
    ],
    [
      "null と acceptGames",
      { budget: { dailyUsd: null }, acceptGames: false, reason: "x" },
      { "budget.dailyUsd": null, acceptGames: false },
      "x",
    ],
    [
      "gameTime.maxMinutes",
      { gameTime: { maxMinutes: 15 }, reason: "x" },
      { "gameTime.maxMinutes": 15 },
      "x",
    ],
    [
      "rateLimit.perIpPerMinute",
      { rateLimit: { perIpPerMinute: 20 }, reason: "x" },
      { "rateLimit.perIpPerMinute": 20 },
      "x",
    ],
    [
      "すべての項目",
      {
        budget: { enabled: true, monthlyUsd: 10, dailyUsd: 0.5 },
        gameTime: { maxMinutes: 20 },
        turnstile: { enabled: false },
        rateLimit: { enabled: false, perIpPerMinute: 5 },
        acceptGames: true,
        reason: "あ".repeat(200),
      },
      {
        "budget.enabled": true,
        "budget.monthlyUsd": 10,
        "budget.dailyUsd": 0.5,
        "gameTime.maxMinutes": 20,
        "turnstile.enabled": false,
        "rateLimit.enabled": false,
        "rateLimit.perIpPerMinute": 5,
        acceptGames: true,
      },
      "あ".repeat(200),
    ],
  ];

  it.each(okCases)("成功: %s", (_name, body, change, reason) => {
    expect(parseGuardsPatch(body)).toEqual({ ok: true, change, reason });
  });

  it("平らな形のキーの順序は決まっている", () => {
    const result = parseGuardsPatch({
      acceptGames: false,
      budget: { dailyUsd: null },
      reason: "x",
    });
    expect(result.ok && JSON.stringify(result.change)).toBe(
      '{"budget.dailyUsd":null,"acceptGames":false}',
    );
  });

  const lockedCases: readonly [string, unknown][] = [
    ["signature: {enabled:false}", { signature: { enabled: false }, reason: "x" }],
    ["signature: {enabled:true}", { signature: { enabled: true }, reason: "x" }],
    ["signature: null", { signature: null, reason: "x" }],
    ["cpuMs: 100", { cpuMs: 100, reason: "x" }],
    ["gameTime.enabled: false", { gameTime: { enabled: false }, reason: "x" }],
    ["gameTime.maxAlarms: 50", { gameTime: { maxAlarms: 50 }, reason: "x" }],
    ["signature と未知のキー", { signature: { enabled: false }, extra: 1 }],
    ["signature と reason なし", { signature: { enabled: false } }],
  ];

  it.each(lockedCases)("locked: %s", (_name, body) => {
    expect(parseGuardsPatch(body)).toEqual({ ok: false, code: "locked" });
  });

  const badCases: readonly [string, unknown][] = [
    ["null", null],
    ["[]", []],
    ['"x"', "x"],
    ["一番上に未知のキー", { extra: 1, reason: "x" }],
    ["budget の中に未知のキー", { budget: { extra: 1 }, reason: "x" }],
    ["budget: true", { budget: true, reason: "x" }],
    ['budget.enabled: "false"', { budget: { enabled: "false" }, reason: "x" }],
    ["monthlyUsd が -1", { budget: { monthlyUsd: -1 }, reason: "x" }],
    ["monthlyUsd が 1001", { budget: { monthlyUsd: 1001 }, reason: "x" }],
    ['monthlyUsd が "30"', { budget: { monthlyUsd: "30" }, reason: "x" }],
    ["maxMinutes が 0", { gameTime: { maxMinutes: 0 }, reason: "x" }],
    ["maxMinutes が 61", { gameTime: { maxMinutes: 61 }, reason: "x" }],
    ["maxMinutes が 1.5", { gameTime: { maxMinutes: 1.5 }, reason: "x" }],
    ["perIpPerMinute が 0", { rateLimit: { perIpPerMinute: 0 }, reason: "x" }],
    ["perIpPerMinute が 1001", { rateLimit: { perIpPerMinute: 1001 }, reason: "x" }],
    ["acceptGames が文字列", { acceptGames: "false", reason: "x" }],
    ["reason がない", { budget: { enabled: false } }],
    ['reason が ""', { budget: { enabled: false }, reason: "" }],
    ['reason が "   "', { budget: { enabled: false }, reason: "   " }],
    ["reason が 201 文字", { budget: { enabled: false }, reason: "あ".repeat(201) }],
    ["reason が 123", { budget: { enabled: false }, reason: 123 }],
    ["変える項目がない（reason だけ）", { reason: "x" }],
    ["変える項目がない（budget が空）", { budget: {}, reason: "x" }],
  ];

  it.each(badCases)("bad_request: %s", (_name, body) => {
    expect(parseGuardsPatch(body)).toEqual({ ok: false, code: "bad_request" });
  });
});

describe("C-3 上書きを重ねる", () => {
  it("上書きなし → GUARDS のまま", () => {
    const guards = overlayGuards(config(), new Map());
    expect(guards).toEqual({
      budget: { enabled: true, monthlyMicro: 30_000_000, dailyMicro: 1_000_000 },
      gameTime: { maxMinutes: 30, maxAlarms: 20 },
      turnstile: { enabled: true },
      rateLimit: { enabled: true, perIpPerMinute: 10 },
      cpuMs: 50,
      acceptGames: true,
      unitMicro: { lite: 6500, "standard-1": 40000 },
    });
  });

  it("budget.dailyUsd を上書き → その値（µUSD）", () => {
    const guards = overlayGuards(config(), new Map([["budget.dailyUsd", 20000]]));
    expect(guards.budget.dailyMicro).toBe(20000);
  });

  it("上書きを消す（null で行を消した後）→ 既定値に戻る", () => {
    const stored = new Map<string, unknown>([["budget.dailyUsd", 20000]]);
    stored.delete("budget.dailyUsd");
    expect(overlayGuards(config(), stored).budget.dailyMicro).toBe(1_000_000);
  });

  const invalidStored: readonly [string, unknown][] = [
    ["budget.dailyUsd", "abc"],
    ["budget.dailyUsd", -1],
    ["budget.dailyUsd", 1_000_000_001],
    ["budget.dailyUsd", 0.5],
    ["budget.enabled", "false"],
    ["gameTime.maxMinutes", 61],
    ["rateLimit.perIpPerMinute", 0],
    ["acceptGames", 0],
  ];

  it.each(invalidStored)("保存された %s = %j は不正なので既定値", (key, value) => {
    expect(overlayGuards(config(), new Map([[key, value]]))).toEqual(
      overlayGuards(config(), new Map()),
    );
  });

  it("maxAlarms・cpuMs・単価は上書きできない", () => {
    const stored = new Map<string, unknown>([
      ["gameTime.maxAlarms", 50],
      ["cpuMs", 100],
      ["unitCostUsd.lite", 1],
      ["signature.enabled", false],
    ]);
    expect(overlayGuards(config(), stored)).toEqual(overlayGuards(config(), new Map()));
  });
});
