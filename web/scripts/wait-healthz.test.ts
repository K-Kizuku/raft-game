// web/scripts/wait-healthz.ts のテスト（T-603 / #67、H-1〜H-6）。
// ネットワークも本物の時計も使わない（fetch・sleep・now を差し替える）。
import { describe, expect, it } from "vitest";
import {
  checkHealthz,
  main,
  waitHealthz,
  type FetchFn,
  type FetchInit,
  type MainDeps,
  type WaitOptions,
} from "./wait-healthz.ts";

const URL_OK = "https://raft.kizuku-hackathon.work/api/healthz";

function ok(build: string): { status: number; body: string } {
  return { status: 200, body: JSON.stringify({ status: "ok", build }) };
}

type Step = { status: number; body: string } | Error;

/** 偽の時計と、決めた応答を順に返す fetch（最後の応答を繰り返す） */
function fake(steps: Step[]): {
  opts: Pick<WaitOptions, "fetch" | "sleep" | "now" | "log">;
  calls: { url: string; init: FetchInit }[];
  sleeps: number[];
} {
  let t = 1_000_000;
  const calls: { url: string; init: FetchInit }[] = [];
  const sleeps: number[] = [];
  const fetchFn: FetchFn = (url, init) => {
    calls.push({ url, init });
    const s = steps[Math.min(calls.length - 1, steps.length - 1)] as Step;
    if (s instanceof Error) return Promise.reject(s);
    return Promise.resolve({ status: s.status, text: () => Promise.resolve(s.body) });
  };
  return {
    opts: {
      fetch: fetchFn,
      sleep: (ms) => {
        sleeps.push(ms);
        t += ms;
        return Promise.resolve();
      },
      now: () => t,
      log: () => {},
    },
    calls,
    sleeps,
  };
}

describe("checkHealthz", () => {
  it("H-1: 200 で build が一致 → 合う", () => {
    expect(
      checkHealthz(200, '{"status":"ok","build":"git:abc1234"}', "git:abc1234"),
    ).toBeUndefined();
  });

  it("H-1: --build なし → build を見ない", () => {
    expect(checkHealthz(200, '{"status":"ok","build":"anything"}', undefined)).toBeUndefined();
    expect(checkHealthz(200, '{"status":"ok"}', undefined)).toBeUndefined();
  });

  it.each([
    ["build が違う", 200, '{"status":"ok","build":"git:0000000"}', "build"],
    ["status が違う", 200, '{"status":"down","build":"git:abc1234"}', "status"],
    ["503", 503, '{"status":"ok","build":"git:abc1234"}', "503"],
    ["JSON でない", 200, "<html>ok</html>", "JSON"],
    ["配列", 200, '[{"status":"ok","build":"git:abc1234"}]', "オブジェクト"],
    ["null", 200, "null", "オブジェクト"],
  ])("H-1: %s → 理由を返す", (_name, status, body, want) => {
    const reason = checkHealthz(status, body, "git:abc1234");
    expect(reason).toBeDefined();
    expect(reason).toContain(want);
  });

  it("本文をそのまま出さない", () => {
    const reason = checkHealthz(200, '{"status":"down","detail":"SECRET-BODY"}', undefined);
    expect(reason).not.toContain("SECRET-BODY");
    expect(checkHealthz(200, "SECRET-BODY", undefined)).not.toContain("SECRET-BODY");
  });
});

describe("waitHealthz", () => {
  it("H-2: 2 回失敗（503・例外）してから成功 → attempts 3、sleep が 2 回", async () => {
    const f = fake([{ status: 503, body: "" }, new TypeError("fetch failed"), ok("git:abc1234")]);
    const r = await waitHealthz({
      url: URL_OK,
      build: "git:abc1234",
      timeoutMs: 600_000,
      intervalMs: 15_000,
      ...f.opts,
    });
    expect(r).toEqual({ ok: true, attempts: 3 });
    expect(f.sleeps).toEqual([15_000, 15_000]);
  });

  it("H-3: ずっと build が違う → 時間切れ。理由に期待と実際の build、回数は上限まで", async () => {
    const f = fake([ok("git:old0000")]);
    const timeoutMs = 60_000;
    const intervalMs = 15_000;
    const r = await waitHealthz({
      url: URL_OK,
      build: "git:new1111",
      timeoutMs,
      intervalMs,
      ...f.opts,
    });
    expect(r.ok).toBe(false);
    if (r.ok) return;
    expect(r.reason).toContain("git:new1111");
    expect(r.reason).toContain("git:old0000");
    expect(r.attempts).toBe(f.calls.length);
    expect(f.calls.length).toBeLessThanOrEqual(timeoutMs / intervalMs + 1);
    expect(f.calls.length).toBeGreaterThan(1);
  });

  it("H-4: timeoutMs 0 → 要求はちょうど 1 回", async () => {
    const f = fake([{ status: 503, body: "" }]);
    const r = await waitHealthz({ url: URL_OK, timeoutMs: 0, intervalMs: 10_000, ...f.opts });
    expect(r.ok).toBe(false);
    expect(f.calls).toHaveLength(1);
    expect(f.sleeps).toEqual([]);
  });

  it("H-5: redirect manual と signal を渡し、302 は失敗として扱う", async () => {
    const f = fake([{ status: 302, body: "" }]);
    const r = await waitHealthz({ url: URL_OK, timeoutMs: 0, intervalMs: 1000, ...f.opts });
    expect(r.ok).toBe(false);
    if (r.ok) return;
    expect(r.reason).toContain("302");
    expect(f.calls[0]?.url).toBe(URL_OK);
    expect(f.calls[0]?.init.redirect).toBe("manual");
    expect(f.calls[0]?.init.signal).toBeInstanceOf(AbortSignal);
  });

  it("試すたびに 1 行を出す", async () => {
    const f = fake([{ status: 503, body: "" }, ok("git:abc1234")]);
    const lines: string[] = [];
    await waitHealthz({
      url: URL_OK,
      timeoutMs: 60_000,
      intervalMs: 1000,
      ...f.opts,
      log: (l) => lines.push(l),
    });
    expect(lines).toHaveLength(2);
    expect(lines[0]).toMatch(/^wait-healthz: 1 回目: HTTP 503/);
    expect(lines[1]).toMatch(/^wait-healthz: 2 回目: /);
  });
});

describe("main", () => {
  function deps(steps: Step[]): {
    d: MainDeps;
    out: () => string;
    err: () => string;
    calls: number;
  } {
    const f = fake(steps);
    let out = "";
    let err = "";
    const d: MainDeps = {
      fetch: f.opts.fetch,
      sleep: f.opts.sleep,
      now: f.opts.now,
      stdout: (s) => {
        out += s;
      },
      stderr: (s) => {
        err += s;
      },
    };
    return {
      d,
      out: () => out,
      err: () => err,
      get calls() {
        return f.calls.length;
      },
    };
  }

  it.each([
    ["http:// の URL", ["--url", "http://raft.kizuku-hackathon.work/api/healthz"]],
    ["URL なし", []],
    ["--build 'a b'", ["--url", URL_OK, "--build", "a b"]],
    ["--timeout -1", ["--url", URL_OK, "--timeout", "-1"]],
    ["--timeout 1.5", ["--url", URL_OK, "--timeout", "1.5"]],
    ["--interval 0", ["--url", URL_OK, "--interval", "0"]],
    ["知らない引数", ["--url", URL_OK, "--retries", "3"]],
  ])("H-6: %s → 2", async (_name, argv) => {
    const x = deps([ok("git:abc1234")]);
    expect(await main(argv, x.d)).toBe(2);
    expect(x.calls).toBe(0);
  });

  it("成功 → 0 と JSON", async () => {
    const x = deps([ok("git:abc1234")]);
    expect(await main(["--url", URL_OK, "--build", "git:abc1234"], x.d)).toBe(0);
    expect(JSON.parse(x.out())).toEqual({
      ok: true,
      url: URL_OK,
      build: "git:abc1234",
      attempts: 1,
    });
  });

  it("時間切れ → 1 と最後の理由", async () => {
    const x = deps([ok("git:old0000")]);
    expect(
      await main(
        ["--url", URL_OK, "--build", "git:new1111", "--timeout", "30", "--interval", "10"],
        x.d,
      ),
    ).toBe(1);
    expect(x.calls).toBe(4);
    expect(x.err()).toContain("git:new1111");
    expect(x.out()).toBe("");
  });
});
