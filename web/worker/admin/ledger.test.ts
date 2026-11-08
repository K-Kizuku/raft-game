import { describe, expect, it } from "vitest";

import type { SqlValue } from "./cf-types.ts";
import { parseGuardsConfig, type GuardsView } from "./guards.ts";
import { CostGuardCore, ensureSchema } from "./ledger.ts";
import { GUARDS_EXAMPLE, guardsWith } from "./test-fixtures.ts";
import { createTestState } from "./test-sqlite.ts";

const T0 = Date.parse("2026-11-07T03:00:00.000Z");
const MINUTE = 60_000;
const DAY = 24 * 60 * MINUTE;

async function setup(guards: unknown = GUARDS_EXAMPLE, start = T0) {
  const state = await createTestState();
  const { sql } = state.storage;
  ensureSchema(sql);
  let now = start;
  const core = new CostGuardCore(
    sql,
    (fn) => state.storage.transactionSync(fn),
    parseGuardsConfig(guards),
    () => now,
  );
  return {
    core,
    sql,
    setNow(t: number | string) {
      now = typeof t === "string" ? Date.parse(t) : t;
    },
    rows(): Record<string, SqlValue>[] {
      return sql.exec("SELECT * FROM ledger ORDER BY started_at, session_id").toArray();
    },
    row(id: string): Record<string, SqlValue> | undefined {
      return sql.exec("SELECT * FROM ledger WHERE session_id = ?", id).toArray()[0];
    },
    auditCount(): SqlValue | undefined {
      return sql.exec("SELECT COUNT(*) AS n FROM audit").toArray()[0]?.["n"];
    },
  };
}

function viewOf(core: CostGuardCore): GuardsView {
  const view = core.view();
  if (view === "guards_invalid") throw new Error("guards_invalid");
  return view;
}

function usageOf(core: CostGuardCore) {
  return viewOf(core).usage;
}

describe("L-1 予約額", () => {
  it("lite は 6500 µUSD・standard-1 は 40000（30 分）で、行に単価・状態・時刻が入る", async () => {
    const t = await setup();
    expect(t.core.reserve("a", "lite")).toBe("ok");
    expect(t.core.reserve("b", "standard-1")).toBe("ok");
    expect(t.rows()).toEqual([
      {
        session_id: "a",
        instance: "lite",
        rate_micro: 6500,
        reserved_micro: 6500,
        settled_micro: null,
        status: "reserved",
        started_at: T0,
        ended_at: null,
        units: null,
      },
      {
        session_id: "b",
        instance: "standard-1",
        rate_micro: 40000,
        reserved_micro: 40000,
        settled_micro: null,
        status: "reserved",
        started_at: T0,
        ended_at: null,
        units: null,
      },
    ]);
  });

  it("maxMinutes: 15 なら lite は 3250", async () => {
    const t = await setup(guardsWith({ "gameTime.maxMinutes": 15 }));
    t.core.reserve("a", "lite");
    expect(t.row("a")).toMatchObject({ rate_micro: 6500, reserved_micro: 3250 });
  });
});

describe("L-2 1 日の上限", () => {
  it("dailyUsd: 0.02 で 3 回 ok、4 回目は budget_exhausted で行が増えない", async () => {
    const t = await setup(guardsWith({ "budget.dailyUsd": 0.02, "budget.monthlyUsd": 1000 }));
    expect(["a", "b", "c"].map((id) => t.core.reserve(id, "lite"))).toEqual(["ok", "ok", "ok"]);
    expect(t.core.reserve("d", "lite")).toBe("budget_exhausted");
    expect(t.rows()).toHaveLength(3);
    expect(usageOf(t.core).todayUsd).toBe(0.0195);
  });

  it("dailyUsd: 0.0195 で 3 回目（ちょうど上限）は ok、4 回目は断る", async () => {
    const t = await setup(guardsWith({ "budget.dailyUsd": 0.0195, "budget.monthlyUsd": 1000 }));
    expect(["a", "b", "c"].map((id) => t.core.reserve(id, "lite"))).toEqual(["ok", "ok", "ok"]);
    expect(t.core.reserve("d", "lite")).toBe("budget_exhausted");
    expect(t.rows()).toHaveLength(3);
  });

  it("dailyUsd: 0 で 1 回目から断る", async () => {
    const t = await setup(guardsWith({ "budget.dailyUsd": 0 }));
    expect(t.core.reserve("a", "lite")).toBe("budget_exhausted");
    expect(t.rows()).toHaveLength(0);
  });
});

describe("L-3 1 か月の上限", () => {
  it("monthlyUsd: 0.02 で日を変えながら予約し、4 回目で断る", async () => {
    const t = await setup(guardsWith({ "budget.monthlyUsd": 0.02, "budget.dailyUsd": 1000 }));
    const results = ["a", "b", "c", "d"].map((id, i) => {
      t.setNow(T0 + i * DAY);
      return t.core.reserve(id, "lite");
    });
    expect(results).toEqual(["ok", "ok", "ok", "budget_exhausted"]);
    expect(t.rows()).toHaveLength(3);
    expect(usageOf(t.core)).toMatchObject({ todayUsd: 0, monthUsd: 0.0195 });
  });
});

describe("L-4 UTC の境目", () => {
  it("日の境目で今日の合計が分かれ、今月の合計は続く", async () => {
    const t = await setup(guardsWith({ "budget.dailyUsd": 0.013, "budget.monthlyUsd": 1000 }));
    t.setNow("2026-11-07T23:59:59.999Z");
    expect(t.core.reserve("a", "lite")).toBe("ok");
    expect(t.core.reserve("b", "lite")).toBe("ok");
    expect(t.core.reserve("c", "lite")).toBe("budget_exhausted");
    expect(usageOf(t.core)).toMatchObject({ todayUsd: 0.013, monthUsd: 0.013 });

    t.setNow("2026-11-08T00:00:00.000Z");
    expect(usageOf(t.core)).toMatchObject({ todayUsd: 0, monthUsd: 0.013 });
    expect(t.core.reserve("c", "lite")).toBe("ok");
    expect(usageOf(t.core)).toMatchObject({ todayUsd: 0.0065, monthUsd: 0.0195 });
  });

  it("月の境目で今月の合計も分かれる", async () => {
    const t = await setup(guardsWith({ "budget.monthlyUsd": 0.0065, "budget.dailyUsd": 1000 }));
    t.setNow("2026-11-30T23:59:59.999Z");
    expect(t.core.reserve("a", "lite")).toBe("ok");
    expect(t.core.reserve("b", "lite")).toBe("budget_exhausted");

    t.setNow("2026-12-01T00:00:00.000Z");
    expect(usageOf(t.core)).toMatchObject({ todayUsd: 0, monthUsd: 0 });
    expect(t.core.reserve("b", "lite")).toBe("ok");
    expect(usageOf(t.core)).toMatchObject({ todayUsd: 0.0065, monthUsd: 0.0065 });
  });
});

describe("L-5 一括停止", () => {
  it("acceptGames: false なら予算に余裕があっても sessions_disabled", async () => {
    const t = await setup(guardsWith({ acceptGames: false }));
    expect(t.core.reserve("a", "lite")).toBe("sessions_disabled");
    expect(t.rows()).toHaveLength(0);
  });

  it("予算の超過と重なっても sessions_disabled", async () => {
    const t = await setup(guardsWith({ acceptGames: false, "budget.dailyUsd": 0 }));
    expect(t.core.reserve("a", "lite")).toBe("sessions_disabled");
    expect(t.rows()).toHaveLength(0);
  });
});

describe("L-6 budget.enabled: false", () => {
  it("上限を超えても ok で、usage に額が出る", async () => {
    const t = await setup(guardsWith({ "budget.enabled": false, "budget.dailyUsd": 0 }));
    expect(t.core.reserve("a", "lite")).toBe("ok");
    expect(t.core.reserve("b", "lite")).toBe("ok");
    expect(usageOf(t.core)).toMatchObject({ todayUsd: 0.013, monthUsd: 0.013 });
  });
});

describe("L-7 精算", () => {
  it.each([
    [600, 2167],
    [0, 0],
    [3600, 13000],
  ])("runSeconds %i で lite は %i（予約額を超えても切らない）", async (runSeconds, micro) => {
    const t = await setup();
    t.core.reserve("a", "lite");
    t.setNow(T0 + MINUTE);
    t.core.settle("a", runSeconds);
    expect(t.row("a")).toMatchObject({
      status: "settled",
      settled_micro: micro,
      reserved_micro: 6500,
      ended_at: T0 + MINUTE,
    });
    expect(usageOf(t.core)).toMatchObject({ todayUsd: micro / 1e6, monthUsd: micro / 1e6 });
  });

  it("予約したときの単価で精算する（あとで単価を変えても変わらない）", async () => {
    const state = await createTestState();
    ensureSchema(state.storage.sql);
    const make = (raw: unknown) =>
      new CostGuardCore(
        state.storage.sql,
        (fn) => state.storage.transactionSync(fn),
        parseGuardsConfig(raw),
        () => T0,
      );
    make(GUARDS_EXAMPLE).reserve("a", "lite");
    make(guardsWith({ "unitCostUsd.lite": 1 })).settle("a", 1800);
    const [row] = state.storage.sql
      .exec("SELECT settled_micro FROM ledger WHERE session_id = 'a'")
      .toArray();
    expect(row).toEqual({ settled_micro: 6500 });
  });

  it("2 回目・知らない id・取り消した行は何もしない", async () => {
    const t = await setup();
    t.core.reserve("a", "lite");
    t.core.reserve("b", "lite");
    t.core.settle("a", 600);
    t.setNow(T0 + MINUTE);
    t.core.settle("a", 1800);
    t.core.settle("zzz", 600);
    t.core.cancel("b");
    t.core.settle("b", 600);
    expect(t.rows()).toMatchObject([
      { session_id: "a", status: "settled", settled_micro: 2167, ended_at: T0 },
      { session_id: "b", status: "cancelled", settled_micro: null, ended_at: T0 + MINUTE },
    ]);
    expect(t.rows()).toHaveLength(2);
  });

  it.each([-1, 1.5, 86401])("runSeconds %d は拒否し、行を変えない", async (runSeconds) => {
    const t = await setup();
    t.core.reserve("a", "lite");
    expect(() => t.core.settle("a", runSeconds)).toThrow(RangeError);
    expect(t.row("a")).toMatchObject({ status: "reserved", settled_micro: null });
  });
});

describe("L-8 取り消し", () => {
  it("reserved は額 0 になる", async () => {
    const t = await setup();
    t.core.reserve("a", "lite");
    t.setNow(T0 + MINUTE);
    t.core.cancel("a");
    expect(t.row("a")).toMatchObject({ status: "cancelled", ended_at: T0 + MINUTE });
    expect(usageOf(t.core)).toMatchObject({ todayUsd: 0, monthUsd: 0, activeGames: 0 });
  });

  it("精算済みは変わらない。知らない id・2 回目は何もしない", async () => {
    const t = await setup();
    t.core.reserve("a", "lite");
    t.core.reserve("b", "lite");
    t.core.settle("a", 600);
    t.core.cancel("b");
    const before = t.rows();
    t.setNow(T0 + MINUTE);
    t.core.cancel("a");
    t.core.cancel("b");
    t.core.cancel("zzz");
    expect(t.rows()).toEqual(before);
    expect(usageOf(t.core).todayUsd).toBe(0.002167);
  });
});

describe("L-9 冪等", () => {
  it("同じ sessionId の 2 回目の reserve は ok で、行は 1 つ、合計は変わらない", async () => {
    const t = await setup();
    expect(t.core.reserve("a", "lite")).toBe("ok");
    t.setNow(T0 + MINUTE);
    expect(t.core.reserve("a", "standard-1")).toBe("ok");
    expect(t.rows()).toEqual([expect.objectContaining({ instance: "lite", started_at: T0 })]);
    expect(usageOf(t.core).todayUsd).toBe(0.0065);
  });
});

describe("L-10 設定が不正", () => {
  it("reserve は sessions_disabled で行が増えない。view・patch は guards_invalid。settle・cancel は動く", async () => {
    const t = await setup();
    t.core.reserve("a", "lite");
    t.core.reserve("b", "lite");
    const broken = new CostGuardCore(
      t.sql,
      (fn) => fn(),
      parseGuardsConfig(guardsWith({ signature: { enabled: false } })),
      () => T0,
    );
    expect(broken.reserve("c", "lite")).toBe("sessions_disabled");
    expect(broken.reserve("a", "lite")).toBe("sessions_disabled");
    expect(t.rows()).toHaveLength(2);
    expect(broken.view()).toBe("guards_invalid");
    expect(broken.patch({ "budget.enabled": false }, "x", "admin")).toEqual({
      ok: false,
      code: "guards_invalid",
    });
    broken.settle("a", 600);
    broken.cancel("b");
    expect(t.rows()).toMatchObject([
      { session_id: "a", status: "settled", settled_micro: 2167 },
      { session_id: "b", status: "cancelled" },
    ]);
  });
});

describe("L-11 上書きと記録", () => {
  it("dailyUsd を上げると、断られた予約が通る", async () => {
    const t = await setup(guardsWith({ "budget.dailyUsd": 0.02, "budget.monthlyUsd": 1000 }));
    ["a", "b", "c"].forEach((id) => t.core.reserve(id, "lite"));
    expect(t.core.reserve("d", "lite")).toBe("budget_exhausted");
    const result = t.core.patch({ "budget.dailyUsd": 1 }, "上げる", "admin");
    expect(result.ok && result.view.guards.budget.dailyUsd).toBe(1);
    expect(t.core.reserve("d", "lite")).toBe("ok");
  });

  it("acceptGames: false の直後から sessions_disabled、null で既定値に戻る", async () => {
    const t = await setup();
    expect(t.core.reserve("a", "lite")).toBe("ok");
    t.core.patch({ acceptGames: false }, "止める", "admin");
    expect(t.core.reserve("b", "lite")).toBe("sessions_disabled");
    expect(viewOf(t.core).guards.acceptGames).toBe(false);
    t.core.patch({ acceptGames: null }, "戻す", "admin");
    expect(t.core.reserve("b", "lite")).toBe("ok");
    expect(viewOf(t.core).guards.acceptGames).toBe(true);
  });

  it("金額は µUSD で保存され、表示は USD", async () => {
    const t = await setup();
    t.core.patch({ "budget.dailyUsd": 0.5, "budget.monthlyUsd": 12.345678 }, "x", "admin");
    expect(t.sql.exec("SELECT key, value FROM settings ORDER BY key").toArray()).toEqual([
      { key: "budget.dailyUsd", value: "500000" },
      { key: "budget.monthlyUsd", value: "12345678" },
    ]);
    expect(viewOf(t.core).guards.budget).toEqual({
      enabled: true,
      monthlyUsd: 12.345678,
      dailyUsd: 0.5,
    });
  });

  it("audit に at・by・change・reason が残り、recentChanges は新しい順で最大 10 件", async () => {
    const t = await setup();
    for (let i = 1; i <= 12; i++) {
      t.setNow(T0 + i * MINUTE);
      t.core.patch({ "rateLimit.perIpPerMinute": i }, ` 理由 ${i} `, "admin");
    }
    const [first] = t.sql.exec("SELECT * FROM audit ORDER BY id LIMIT 1").toArray();
    expect(first).toEqual({
      id: 1,
      at: T0 + MINUTE,
      by: "admin",
      change: '{"rateLimit.perIpPerMinute":1}',
      reason: "理由 1",
    });
    const view = viewOf(t.core);
    expect(view.recentChanges).toHaveLength(10);
    expect(view.recentChanges[0]).toEqual({
      at: new Date(T0 + 12 * MINUTE).toISOString(),
      change: { "rateLimit.perIpPerMinute": 12 },
      reason: "理由 12",
    });
    expect(view.recentChanges.map((c) => c.reason)).toEqual(
      [12, 11, 10, 9, 8, 7, 6, 5, 4, 3].map((i) => `理由 ${i}`),
    );
  });

  it("1001 件記録すると 1000 件だけ残る（古いものから消す）", async () => {
    const t = await setup();
    for (let i = 1; i <= 1001; i++) t.core.patch({ acceptGames: i % 2 === 0 }, `r${i}`, "admin");
    expect(t.auditCount()).toBe(1000);
    const [oldest] = t.sql.exec("SELECT reason FROM audit ORDER BY id LIMIT 1").toArray();
    expect(oldest).toEqual({ reason: "r2" });
  });

  it("変更の検証に失敗したら何も書かない", async () => {
    const t = await setup();
    expect(t.core.patch({ "signature.enabled": false }, "x", "admin")).toEqual({
      ok: false,
      code: "locked",
    });
    expect(t.core.patch({ "budget.dailyUsd": -1 }, "x", "admin")).toEqual({
      ok: false,
      code: "bad_request",
    });
    expect(t.core.patch({ acceptGames: false }, " ", "admin")).toEqual({
      ok: false,
      code: "bad_request",
    });
    expect(t.core.patch({ acceptGames: false }, "x", "")).toEqual({
      ok: false,
      code: "bad_request",
    });
    expect(t.auditCount()).toBe(0);
    expect(t.sql.exec("SELECT * FROM settings").toArray()).toEqual([]);
  });

  it("保存された上書きが今の規則で不正なら、その上書きだけを無視する", async () => {
    const t = await setup();
    t.sql.exec("INSERT INTO settings (key, value) VALUES ('budget.dailyUsd', '\"abc\"')");
    t.sql.exec("INSERT INTO settings (key, value) VALUES ('gameTime.maxMinutes', '999')");
    t.sql.exec("INSERT INTO settings (key, value) VALUES ('acceptGames', '{')");
    t.sql.exec("INSERT INTO settings (key, value) VALUES ('rateLimit.perIpPerMinute', '20')");
    const { guards } = viewOf(t.core);
    expect(guards.budget.dailyUsd).toBe(1);
    expect(guards.gameTime.maxMinutes).toBe(30);
    expect(guards.acceptGames).toBe(true);
    expect(guards.rateLimit.perIpPerMinute).toBe(20);
  });
});

describe("L-12 片付け", () => {
  it("2026-12-15 に予約すると、2026-10-31 の行は消え、2026-11-01 の行は残る", async () => {
    const t = await setup();
    t.setNow("2026-10-31T23:59:59.999Z");
    t.core.reserve("oct", "lite");
    t.setNow("2026-11-01T00:00:00.000Z");
    t.core.reserve("nov", "lite");
    t.setNow("2026-12-15T12:00:00.000Z");
    t.core.reserve("dec", "lite");
    expect(t.rows().map((r) => r["session_id"])).toEqual(["nov", "dec"]);
  });
});

describe("L-13 activeGames", () => {
  it("reserved で maxMinutes 以内の行だけを数える", async () => {
    const t = await setup();
    t.setNow(T0 - 31 * MINUTE);
    t.core.reserve("old", "lite");
    t.setNow(T0 - 30 * MINUTE);
    t.core.reserve("edge", "lite");
    t.setNow(T0 - MINUTE);
    t.core.reserve("settled", "lite");
    t.core.reserve("cancelled", "lite");
    t.core.reserve("active", "lite");
    t.core.settle("settled", 60);
    t.core.cancel("cancelled");
    t.setNow(T0);
    expect(usageOf(t.core).activeGames).toBe(2);
  });
});

describe("L-14 表示の形", () => {
  it("プロトコル §2 の例と同じ状態で、JSON.stringify の結果が固定の文字列と一致する", async () => {
    const t = await setup();
    t.core.reserve("a", "lite");
    t.core.reserve("b", "lite");
    t.core.reserve("c", "lite");
    t.sql.exec(
      "INSERT INTO audit (at, by, change, reason) VALUES (?, 'admin', ?, ?)",
      T0,
      '{"budget.enabled":false}',
      "デモ当日のため一時的に外す",
    );
    expect(JSON.stringify(viewOf(t.core))).toBe(
      '{"guards":{' +
        '"budget":{"enabled":true,"monthlyUsd":30,"dailyUsd":1},' +
        '"gameTime":{"enabled":true,"maxMinutes":30,"maxAlarms":20},' +
        '"signature":{"enabled":true,"locked":true},' +
        '"turnstile":{"enabled":true},' +
        '"rateLimit":{"enabled":true,"perIpPerMinute":10},' +
        '"cpuMs":{"value":50,"note":"変更には再デプロイが必要"},' +
        '"acceptGames":true},' +
        '"usage":{"todayUsd":0.0195,"monthUsd":0.0195,"activeGames":3},' +
        '"unitCostUsd":{"lite":0.0065,"standard-1":0.04},' +
        '"recentChanges":[{"at":"2026-11-07T03:00:00.000Z","change":{"budget.enabled":false},' +
        '"reason":"デモ当日のため一時的に外す"}]}',
    );
  });
});
