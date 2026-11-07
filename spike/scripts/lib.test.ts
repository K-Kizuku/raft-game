import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import {
  ITEMS,
  START_CAP,
  SessionPool,
  checkBase,
  fmt,
  paddedMessage,
  parseArgs,
  percentile,
  planSessions,
  renderTable,
  sessionsNeeded,
  summarize,
  type MeasureResult,
} from "./lib.ts";

const base = ["--base", "https://raft-game-spike.example.workers.dev"];

describe("parseArgs", () => {
  it("L-1: 既定値", () => {
    const r = parseArgs(base);
    expect(r).toEqual({
      ok: true,
      options: {
        base: "https://raft-game-spike.example.workers.dev",
        tokenFile: null,
        instance: "lite",
        only: [...ITEMS],
        runs: 10,
        concurrency: 3,
        first: 1,
        internetControl: false,
        idle: 120,
        out: null,
      },
    });
  });

  it("L-1: すべての引数", () => {
    const r = parseArgs([
      ...base,
      "--token-file",
      "/tmp/t",
      "--instance",
      "standard-1",
      "--only",
      "ws,start",
      "--runs",
      "3",
      "--concurrency",
      "2",
      "--first",
      "11",
      "--internet-control",
      "--idle",
      "0",
      "--out",
      "/tmp/out.json",
    ]);
    expect(r.ok && r.options).toEqual({
      base: "https://raft-game-spike.example.workers.dev",
      tokenFile: "/tmp/t",
      instance: "standard-1",
      only: ["start", "ws"], // 決まった順に並べ直す
      runs: 3,
      concurrency: 2,
      first: 11,
      internetControl: true,
      idle: 0,
      out: "/tmp/out.json",
    });
  });

  it.each([
    [[...base, "--only", "start,nope"], "--only"],
    [[...base, "--runs", "0"], "--runs"],
    [[...base, "--runs", "51"], "--runs"],
    [[...base, "--runs", "1.5"], "--runs"],
    [[...base, "--instance", "standard-2"], "--instance"],
    [["--base", "http://example.com"], "--base"],
    [["--base", "http://example.com:8080"], "--base"],
    [["--base", "ftp://localhost"], "--base"],
    [["--base", "https://user:pw@example.com"], "--base"],
    [["--base", "https://example.com/spike"], "--base"],
    [["--base", "not a url"], "--base"],
    [[], "--base"],
    [[...base, "--runs"], "--runs"],
    [[...base, "--runs", "3", "--runs", "4"], "--runs"],
    [[...base, "--unknown", "1"], "--unknown"],
    [[...base, "--idle", "601"], "--idle"],
    [[...base, "--concurrency", "6"], "--concurrency"],
    [[...base, "--first", "0"], "--first"],
  ])("L-1: %j はエラー", (argv, mention) => {
    const r = parseArgs(argv);
    expect(r.ok).toBe(false);
    if (!r.ok) expect(r.message).toContain(mention);
  });

  it.each(["http://localhost:15174", "http://127.0.0.1:1", "http://localhost", "https://x.workers.dev/"])(
    "L-1: --base %s は通る",
    (b) => {
      expect(checkBase(b)).toBeNull();
      const r = parseArgs(["--base", b]);
      expect(r.ok).toBe(true);
      if (r.ok) expect(r.options.base).toBe(new URL(b).origin);
    },
  );

  it("使う番号の数（全部で 15 以下なので、2 回目を --first 16 で始めても SPIKE_MAX_SESSIONS=30 に収まる）", () => {
    expect(sessionsNeeded({ only: [...ITEMS], runs: 10, concurrency: 3, internetControl: false })).toBe(9);
    expect(sessionsNeeded({ only: [...ITEMS], runs: 10, concurrency: 3, internetControl: true })).toBe(9);
    expect(sessionsNeeded({ only: [...ITEMS], runs: 50, concurrency: 5, internetControl: true })).toBeLessThanOrEqual(30);
    expect(sessionsNeeded({ only: ["plain"], runs: 10, concurrency: 3, internetControl: true })).toBe(0);
    expect(sessionsNeeded({ only: ["egress"], runs: 10, concurrency: 3, internetControl: true })).toBe(1);
  });
});

describe("SessionPool", () => {
  it("1 つの番号を START_CAP 回まで使い、--first から順に増やす", () => {
    const pool = new SessionPool(16);
    const got = Array.from({ length: 7 }, () => pool.acquire(1));
    expect(got).toEqual([16, 16, 16, 17, 17, 17, 18]);
    expect(pool.acquire(2)).toBe(18); // 18 は残り 2
    expect(pool.acquire(1)).toBe(19);
    expect(pool.used).toEqual([16, 17, 18, 19]);
  });

  it("残りのある一番小さい番号を選び、同時に使う番号は別々にする", () => {
    const pool = new SessionPool(1);
    expect(pool.acquire(2)).toBe(1); // 1 の残り 1
    expect(pool.acquireDistinct(3, 1)).toEqual([1, 2, 3]);
    expect(pool.acquire(0)).toBe(1); // 起動しない用途は残り 0 でも使える
    expect(pool.acquire(3)).toBe(4);
    expect(pool.used).toEqual([1, 2, 3, 4]);
  });

  it("measure.ts の既定の計画: 番号ごとの起動の回数が START_CAP を超えない", () => {
    const counts = new Map<number, number>();
    const pool = new SessionPool(1);
    const orig = pool.acquire.bind(pool);
    pool.acquire = (starts, exclude) => {
      const n = orig(starts, exclude);
      counts.set(n, (counts.get(n) ?? 0) + starts);
      return n;
    };
    planSessions(pool, { only: [...ITEMS], runs: 10, concurrency: 3, internetControl: true });
    for (const [n, c] of counts) expect(c, `s-${n}`).toBeLessThanOrEqual(START_CAP);
    expect([...counts.values()].reduce((a, b) => a + b, 0)).toBe(26);
  });
});

describe("percentile", () => {
  it("L-2: 空は null、1 件はその値、10 件は最近傍の順位", () => {
    expect(percentile([], 50)).toBeNull();
    expect(summarize([])).toEqual({ n: 0, p50: null, p95: null, p99: null, max: null });
    expect(summarize([7])).toEqual({ n: 1, p50: 7, p95: 7, p99: 7, max: 7 });
    const ten = [10, 1, 9, 2, 8, 3, 7, 4, 6, 5];
    expect(summarize(ten)).toEqual({ n: 10, p50: 5, p95: 10, p99: 10, max: 10 });
    expect(percentile(ten, 90)).toBe(9);
    expect(percentile(ten, 10)).toBe(1);
    expect(percentile(ten, 0)).toBe(1);
    const hundred = Array.from({ length: 100 }, (_, i) => i + 1);
    expect(summarize(hundred)).toEqual({ n: 100, p50: 50, p95: 95, p99: 99, max: 100 });
    expect(ten).toEqual([10, 1, 9, 2, 8, 3, 7, 4, 6, 5]); // 元の配列を並べ替えない
  });

  it("fmt は数を短く書く", () => {
    expect([fmt(null), fmt(3), fmt(1534.2), fmt(12.345), fmt(0.0134), fmt(Number.NaN)]).toEqual([
      "?",
      "3",
      "1534",
      "12.3",
      "0.013",
      "?",
    ]);
  });

  it("paddedMessage は決まったバイト数の JSON を作る", () => {
    for (const n of [64, 1024, 16384, 16385]) {
      const m = paddedMessage(7, "rtt", n);
      expect(new TextEncoder().encode(m).byteLength).toBe(n);
      expect(JSON.parse(m)).toMatchObject({ seq: 7, kind: "rtt" });
    }
    expect(() => paddedMessage(1, "rtt", 10)).toThrow(RangeError);
  });
});

describe("renderTable", () => {
  const fixture = JSON.parse(
    readFileSync(new URL("./fixtures/measure-result.json", import.meta.url), "utf8"),
  ) as MeasureResult;

  it("L-3: 固定の結果から、雛形と同じ列の表を組み立てる", () => {
    expect(renderTable(fixture)).toBe(
      [
        "## T-007 の測定結果（2026-11-08、cf@1.0.0-beta.28）",
        "",
        "| ID | 項目 | lite | standard-1 | ローカル（cf dev） | 判断 |",
        "|---|---|---|---|---|---|",
        "| M-1 | コンテナの起動時間（p50 / p95 / 最大、初回、同時 3 つ） | p50 1500 / p95 2400 / 最大 2400 ms（DO の中、3/3 回）・クライアント p50 1620 ms・初回 2400 ms・同時 2: 1 成功・最大 1800 ms | | | |",
        "| M-2 | 復元の速さ（7 島・30 分の再計算。手元の PC との比） | 1534 ms（19.6 分/秒、CPU 1400 ms） | | | |",
        "| M-3 | 8 倍速（1 ステップの p99・遅れの p99・CPU の使用率） | ×1: step p99 0.5 ms・遅れ p99 1.2 ms・CPU 0.02；×8: step p99 4.8 ms・遅れ p99 9 ms・CPU 0.31 | | | |",
        "| M-4 | limits.cpuMs（止まる反復回数・DO に効くか・ふつうの要求の CPU 時間） | Worker: 成功の最大 1e8・最初の失敗 1e9（503 exceededCpu）／DO: 成功の最大 1e9・失敗なし | | | |",
        "| M-5 | WS の中継（往復の p50 / p99、16 KiB、500 件、無通信 120 秒、切断） | 1 KiB p50 12.3 / p99 31.5 ms（200 回）・16 KiB 20/20・超過 破棄・500 件 500/500（順序 ok）・無通信 120 秒 ok・切断 ブラウザ→コンテナ ok／コンテナ→ブラウザ 1011 | | | |",
        "| M-6 | DO とコンテナの HTTP の往復（p50 / p99） | p50 0.8 / p99 3.3 ms（200/200 回） | | | |",
        "| M-7 | DO の SQLite（100 行・1,000 行、再起動の後） | 100 行 45.6 ms・1,000 行 210 ms（クライアント）・再起動の後 残る（1100 行） | | | |",
        "| M-8 | DurableObject を継承しないクラス（sql・container・alarm） | PlainStore（継承しない）ok・ControlStore（継承する）ok・SpikeSession の container ok・alarm ok | | | |",
        '| M-9 | enableInternet: false（DNS・TCP・HTTPS。対照） | false: DNS 失敗（lookup example.com: no such host） / TCP 失敗（dial tcp 1.1.1.1:443: connect: network i…） / HTTPS 失敗（Get "https://example.com/": dial \\| refus…）；true（対照）: DNS ok / TCP ok / HTTPS ok | | | |',
        "| M-10 | 停止（destroy・アラーム・setInactivityTimeout） | destroy 120 ms で running=false・アラーム（60000 ms）で止まった・inactivity（30000 ms）で止まらない | | | |",
        "| M-11 | 落ちたときの検知と再起動 | 検知 350 ms・再起動 1900 ms | | | |",
        "| M-12 | cf dev での動作 | — | — | | |",
        "| M-13 | デプロイ（時間・イメージの大きさ・ログ） | | | — | |",
        "| M-14 | コンテナから見た資源（CPU・メモリ・env・colo） | CPU 4・GOMAXPROCS 4・cpu.max 6250 100000・memory.max 268435456・env single/8080・colo NRT | | | |",
        "",
      ].join("\n"),
    );
  });

  it("L-3: ローカルの結果はローカルの列と M-12 を埋め、失敗の理由を書く", () => {
    const local: MeasureResult = {
      ...fixture,
      target: "local",
      items: {
        start: fixture.items.start!,
        ws: { ok: false, error: "ws open timeout", data: {} },
        stop: fixture.items.stop!,
      },
    };
    const lines = renderTable(local).split("\n");
    expect(lines).toContain("| M-5 | WS の中継（往復の p50 / p99、16 KiB、500 件、無通信 120 秒、切断） | | | 失敗: ws open timeout | |");
    expect(lines).toContain("| M-12 | cf dev での動作 | — | — | start ok・ws ng・stop ok | |");
    expect(lines).toContain("| M-2 | 復元の速さ（7 島・30 分の再計算。手元の PC との比） | | | | |");
  });
});
