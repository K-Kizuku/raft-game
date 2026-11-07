// web/scripts/check-build-output.ts のテスト（T-601 / #65、C-1〜C-15）。
// 写し（fixtures/<モード>/）は、BUILD_ID=git:fixture の cf build --mode <モード> の出力から作った
// （manifest の中身を削り、localReference を cloudflare-build/000000000000/<コンテナ>-server:fixture に置き換えた）。
// テストは写しを読み、一部を書き換えたものを checkOutput に渡す（ファイルに書かない）。
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import {
  checkOutput,
  EXPECTED,
  localImages,
  main,
  MODES,
  readOutput,
  type BuildOutput,
  type Mode,
} from "./check-build-output.ts";

type Obj = Record<string, unknown>;

function fixtureDir(mode: Mode): string {
  return fileURLToPath(new URL(`./fixtures/${mode}`, import.meta.url));
}

function fixture(mode: Mode): BuildOutput {
  return structuredClone(readOutput(fixtureDir(mode)));
}

function worker(out: BuildOutput): Obj {
  return out.worker as Obj;
}

function sub(o: Obj, ...keys: string[]): Obj {
  let v: Obj = o;
  for (const k of keys) v = v[k] as Obj;
  return v;
}

function env(out: BuildOutput): Obj {
  return sub(worker(out), "env");
}

function guards(out: BuildOutput): Obj {
  return sub(env(out), "GUARDS", "value");
}

function container(out: BuildOutput, i = 0): Obj {
  return out.containers[i]?.config as Obj;
}

/** 写しを書き換えて検査し、違反の一覧を返す */
function violations(mode: Mode, mutate: (o: BuildOutput) => void, checkAs: Mode = mode): string[] {
  const out = fixture(mode);
  mutate(out);
  return checkOutput(out, checkAs);
}

type Case = [name: string, mode: Mode, mutate: (o: BuildOutput) => void, expected: string];

function expectViolation([, mode, mutate, expected]: Case): void {
  const errors = violations(mode, mutate);
  expect(errors.length).toBeGreaterThan(0);
  expect(
    errors.filter((e) => e.startsWith(expected)),
    errors.join("\n"),
  ).not.toEqual([]);
}

const PROD_HOST = "raft.kizuku-hackathon.work";
const DEMO_HOST = "raft-demo.kizuku-hackathon.work";

describe("check-build-output", () => {
  describe("C-1: 4 つのモードの写し", () => {
    it.each(MODES)("%s の写しは、そのモードで違反がない", (mode) => {
      expect(checkOutput(fixture(mode), mode)).toEqual([]);
    });

    it.each(MODES.flatMap((a) => MODES.filter((b) => b !== a).map((b) => [a, b] as const)))(
      "%s の写しを %s で検査すると違反がある",
      (fixtureMode, checkAs) => {
        expect(checkOutput(fixture(fixtureMode), checkAs).length).toBeGreaterThan(0);
      },
    );

    it("demo の写しを production で検査すると name・domains・exports.CostGuard で誤り", () => {
      const errors = checkOutput(fixture("demo"), "production");
      for (const item of ["name:", "domains:", "exports.CostGuard:"]) {
        expect(
          errors.some((e) => e.startsWith(item)),
          item,
        ).toBe(true);
      }
    });

    it("一般公開とデモで Rate Limiting の namespace が違う", () => {
      expect(EXPECTED.production.rateLimitNamespace).not.toBe(EXPECTED.demo.rateLimitNamespace);
    });
  });

  describe("C-2: domains", () => {
    const cases: Case[] = [
      [
        "production にデモのホスト名",
        "production",
        (o) => (worker(o)["domains"] = [DEMO_HOST]),
        "domains:",
      ],
      [
        "production に 2 つ目のホスト名",
        "production",
        (o) => (worker(o)["domains"] = [PROD_HOST, DEMO_HOST]),
        "domains:",
      ],
      [
        "production にワイルドカード",
        "production",
        (o) => (worker(o)["domains"] = ["*.kizuku-hackathon.work"]),
        "domains:",
      ],
      [
        "production に domains がない",
        "production",
        (o) => delete worker(o)["domains"],
        "domains:",
      ],
      [
        "development に domains",
        "development",
        (o) => (worker(o)["domains"] = [PROD_HOST]),
        "domains:",
      ],
      [
        "local-container に domains",
        "local-container",
        (o) => (worker(o)["domains"] = []),
        "domains:",
      ],
      [
        "demo の production が false",
        "demo",
        (o) =>
          (worker(o)["domains"] = [{ hostname: DEMO_HOST, production: false, previews: false }]),
        "domains:",
      ],
    ];
    it.each(cases)("%s → 誤り", (...c) => expectViolation(c));

    it("{ hostname, production: true, previews: false } の形は通る", () => {
      const errors = violations(
        "production",
        (o) =>
          (worker(o)["domains"] = [{ hostname: PROD_HOST, production: true, previews: false }]),
      );
      expect(errors).toEqual([]);
    });
  });

  describe("C-3: triggers・routes・workersDev・previewUrls", () => {
    const cases: Case[] = [
      ["triggers がある", "production", (o) => (worker(o)["triggers"] = []), "triggers:"],
      ["routes がある", "production", (o) => (worker(o)["routes"] = [`${PROD_HOST}/*`]), "routes:"],
      ["workersDev: true", "demo", (o) => (worker(o)["workersDev"] = true), "workersDev:"],
      [
        "previewUrls: true",
        "development",
        (o) => (worker(o)["previewUrls"] = true),
        "previewUrls:",
      ],
    ];
    it.each(cases)("%s → 誤り", (...c) => expectViolation(c));
  });

  describe("C-4: limits.cpuMs", () => {
    const cases: Case[] = [
      ["limits がない", "production", (o) => delete worker(o)["limits"], "limits.cpuMs:"],
      ["5", "production", (o) => (sub(worker(o), "limits")["cpuMs"] = 5), "limits.cpuMs:"],
      ["50.5", "demo", (o) => (sub(worker(o), "limits")["cpuMs"] = 50.5), "limits.cpuMs:"],
      ["GUARDS.cpuMs と違う", "production", (o) => (guards(o)["cpuMs"] = 500), "limits.cpuMs:"],
    ];
    it.each(cases)("%s → 誤り", (...c) => expectViolation(c));
  });

  describe("C-5: observability・assets", () => {
    const cases: Case[] = [
      [
        "redactQueryString がない",
        "production",
        (o) => delete sub(worker(o), "observability")["redactQueryString"],
        "observability.redactQueryString:",
      ],
      [
        "redactQueryString: false",
        "demo",
        (o) => (sub(worker(o), "observability")["redactQueryString"] = false),
        "observability.redactQueryString:",
      ],
      [
        "observability.enabled: false",
        "production",
        (o) => (sub(worker(o), "observability")["enabled"] = false),
        "observability.enabled:",
      ],
      [
        "observability.logs.enabled: false",
        "production",
        (o) => (sub(worker(o), "observability", "logs")["enabled"] = false),
        "observability.logs.enabled:",
      ],
      [
        "runWorkerFirst が違う",
        "production",
        (o) => (sub(worker(o), "assets")["runWorkerFirst"] = ["/api/*"]),
        "assets.runWorkerFirst:",
      ],
    ];
    it.each(cases)("%s → 誤り", (...c) => expectViolation(c));
  });

  describe("C-6: env のキーの集合", () => {
    const cases: Case[] = [
      [
        "1 つ多い",
        "production",
        (o) => (env(o)["EXTRA_FLAG"] = { type: "text", value: "1" }),
        "env.EXTRA_FLAG:",
      ],
      ["1 つ少ない", "production", (o) => delete env(o)["REPLAYS"], "env.REPLAYS:"],
      [
        "development に GAME_SESSION",
        "development",
        (o) => (env(o)["GAME_SESSION"] = {}),
        "env.GAME_SESSION:",
      ],
      [
        "development に ALLOWED_ORIGINS",
        "development",
        (o) => (env(o)["ALLOWED_ORIGINS"] = {}),
        "env.ALLOWED_ORIGINS:",
      ],
    ];
    it.each(cases)("%s → 誤り（項目名にそのキー）", (...c) => expectViolation(c));
  });

  describe("C-7: シークレット", () => {
    const cases: Case[] = [
      [
        "value がある",
        "production",
        (o) => (sub(env(o), "SESSION_SIGNING_KEY")["value"] = "x".repeat(32)),
        "env.SESSION_SIGNING_KEY:",
      ],
      [
        'type: "text"',
        "demo",
        (o) =>
          (env(o)["TURNSTILE_SECRET"] = {
            type: "text",
            value: "1x0000000000000000000000000000000AA",
          }),
        "env.TURNSTILE_SECRET:",
      ],
      [
        "demo に ADMIN_TOKEN",
        "demo",
        (o) => (env(o)["ADMIN_TOKEN"] = { type: "secret" }),
        "env.ADMIN_TOKEN:",
      ],
    ];
    it.each(cases)("%s → 誤り", (...c) => expectViolation(c));
  });

  describe("C-8: 文字列", () => {
    const cases: Case[] = [
      [
        "DEMO_CONTROLS が 1",
        "production",
        (o) => (sub(env(o), "DEMO_CONTROLS")["value"] = "1"),
        "env.DEMO_CONTROLS:",
      ],
      [
        "CONTAINER_INSTANCE が x",
        "production",
        (o) => (sub(env(o), "CONTAINER_INSTANCE")["value"] = "x"),
        "env.CONTAINER_INSTANCE:",
      ],
      [
        "ALLOWED_ORIGINS がデモのホスト名",
        "production",
        (o) => (sub(env(o), "ALLOWED_ORIGINS")["value"] = `https://${DEMO_HOST}`),
        "env.ALLOWED_ORIGINS:",
      ],
      [
        "BUILD_ID が a b",
        "production",
        (o) => (sub(env(o), "BUILD_ID")["value"] = "a b"),
        "env.BUILD_ID:",
      ],
      [
        "BUILD_ID が 65 文字",
        "demo",
        (o) => (sub(env(o), "BUILD_ID")["value"] = "a".repeat(65)),
        "env.BUILD_ID:",
      ],
    ];
    it.each(cases)("%s → 誤り", (...c) => expectViolation(c));
  });

  describe("C-9: SESSION_RATE_LIMIT", () => {
    const cases: Case[] = [
      [
        "period が 10",
        "production",
        (o) => (sub(env(o), "SESSION_RATE_LIMIT", "simple")["period"] = 10),
        "env.SESSION_RATE_LIMIT.simple:",
      ],
      [
        "limit が GUARDS.rateLimit.perIpPerMinute と違う",
        "production",
        (o) => (sub(guards(o), "rateLimit")["perIpPerMinute"] = 20),
        "env.SESSION_RATE_LIMIT.simple.limit:",
      ],
      [
        "namespace が abc",
        "production",
        (o) => (sub(env(o), "SESSION_RATE_LIMIT")["namespace"] = "abc"),
        "env.SESSION_RATE_LIMIT.namespace:",
      ],
      [
        "デモの namespace が一般公開と同じ",
        "demo",
        (o) =>
          (sub(env(o), "SESSION_RATE_LIMIT")["namespace"] = EXPECTED.production.rateLimitNamespace),
        "env.SESSION_RATE_LIMIT.namespace:",
      ],
      [
        'type が "text"',
        "development",
        (o) => (env(o)["SESSION_RATE_LIMIT"] = { type: "text", value: "10" }),
        "env.SESSION_RATE_LIMIT:",
      ],
    ];
    it.each(cases)("%s → 誤り", (...c) => expectViolation(c));
  });

  describe("C-10: GUARDS", () => {
    const cases: Case[] = [
      [
        "signature がある",
        "production",
        (o) => (guards(o)["signature"] = { enabled: false }),
        "env.GUARDS.signature:",
      ],
      [
        "budget.dailyUsd が 5",
        "production",
        (o) => (sub(guards(o), "budget")["dailyUsd"] = 5),
        "env.GUARDS.budget.dailyUsd:",
      ],
      [
        "turnstile.enabled が false",
        "production",
        (o) => (sub(guards(o), "turnstile")["enabled"] = false),
        "env.GUARDS.turnstile.enabled:",
      ],
      [
        "gameTime.maxAlarms が 50",
        "local-container",
        (o) => (sub(guards(o), "gameTime")["maxAlarms"] = 50),
        "env.GUARDS.gameTime.maxAlarms:",
      ],
      ["キーが 1 つ欠ける", "development", (o) => delete guards(o)["unitCostUsd"], "env.GUARDS:"],
      [
        "acceptGames が false",
        "production",
        (o) => (guards(o)["acceptGames"] = false),
        "env.GUARDS.acceptGames:",
      ],
      [
        "budget.monthlyUsd が 100",
        "production",
        (o) => (sub(guards(o), "budget")["monthlyUsd"] = 100),
        "env.GUARDS.budget.monthlyUsd:",
      ],
      [
        "gameTime.maxMinutes が 60",
        "production",
        (o) => (sub(guards(o), "gameTime")["maxMinutes"] = 60),
        "env.GUARDS.gameTime.maxMinutes:",
      ],
      [
        "demo に GUARDS",
        "demo",
        (o) =>
          (env(o)["GUARDS"] = structuredClone(
            sub(fixture("production").worker as Obj, "env", "GUARDS"),
          )),
        "env.GUARDS:",
      ],
    ];
    it.each(cases)("%s → 誤り", (...c) => expectViolation(c));
  });

  describe("C-11: Durable Object のバインディング", () => {
    const cases: Case[] = [
      [
        "demo の COST_GUARD.worker が raft-game-demo",
        "demo",
        (o) => (sub(env(o), "COST_GUARD")["worker"] = "raft-game-demo"),
        "env.COST_GUARD:",
      ],
      [
        "production の GAME_SESSION.worker が raft-game-demo",
        "production",
        (o) => (sub(env(o), "GAME_SESSION")["worker"] = "raft-game-demo"),
        "env.GAME_SESSION:",
      ],
      [
        "development の COST_GUARD が raft-game",
        "development",
        (o) => (sub(env(o), "COST_GUARD")["worker"] = "raft-game"),
        "env.COST_GUARD:",
      ],
      [
        "COST_GUARD の exportName が違う",
        "production",
        (o) => (sub(env(o), "COST_GUARD")["exportName"] = "GameSession"),
        "env.COST_GUARD:",
      ],
    ];
    it.each(cases)("%s → 誤り", (...c) => expectViolation(c));
  });

  describe("C-12: exports", () => {
    const cases: Case[] = [
      [
        "GameSession.container が raft-game-server でない",
        "production",
        (o) => (sub(worker(o), "exports", "GameSession")["container"] = "raft-game-demo-server"),
        "exports.GameSession.container:",
      ],
      [
        "そのコンテナの出力がない",
        "production",
        (o) => (o.containers = []),
        "exports.GameSession.container:",
      ],
      [
        'storage が "kv"',
        "production",
        (o) => (sub(worker(o), "exports", "CostGuard")["storage"] = "kv"),
        "exports.CostGuard:",
      ],
      [
        "demo の exports に CostGuard",
        "demo",
        (o) =>
          (sub(worker(o), "exports")["CostGuard"] = { type: "durable-object", storage: "sqlite" }),
        "exports.CostGuard:",
      ],
      [
        "production に CostGuard がない",
        "production",
        (o) => delete sub(worker(o), "exports")["CostGuard"],
        "exports.CostGuard:",
      ],
      [
        "CostGuard に container",
        "production",
        (o) => (sub(worker(o), "exports", "CostGuard")["container"] = "raft-game-server"),
        "exports.CostGuard.container:",
      ],
      [
        "development に GameSession",
        "development",
        (o) =>
          (sub(worker(o), "exports")["GameSession"] = {
            type: "durable-object",
            storage: "sqlite",
          }),
        "exports.GameSession:",
      ],
    ];
    it.each(cases)("%s → 誤り", (...c) => expectViolation(c));
  });

  describe("C-13: コンテナ", () => {
    const cases: Case[] = [
      [
        "development に 1 つある",
        "development",
        (o) => (o.containers = fixture("local-container").containers),
        "containers:",
      ],
      [
        "production に 2 つある",
        "production",
        (o) => o.containers.push(...fixture("demo").containers),
        "containers:",
      ],
      [
        'schedulingPolicy が "default"',
        "production",
        (o) => (container(o)["schedulingPolicy"] = "default"),
        "containers.schedulingPolicy:",
      ],
      [
        "images のキーが app",
        "demo",
        (o) => (container(o)["images"] = { app: sub(container(o), "images")["server"] }),
        "containers.images:",
      ],
      [
        "name が違う",
        "local-container",
        (o) => (container(o)["name"] = "raft-game-server"),
        "containers.name:",
      ],
    ];
    it.each(cases)("%s → 誤り", (...c) => expectViolation(c));
  });

  describe("C-14: REPLAYS", () => {
    const cases: Case[] = [
      ["ない", "production", (o) => delete env(o)["REPLAYS"], "env.REPLAYS:"],
      [
        "name が違う",
        "demo",
        (o) => (sub(env(o), "REPLAYS")["name"] = "raft-game-replays-demo"),
        "env.REPLAYS:",
      ],
    ];
    it.each(cases)("%s → 誤り", (...c) => expectViolation(c));
  });

  describe("C-15: main", () => {
    function run(argv: string[]): { code: number; stdout: string; stderr: string } {
      let stdout = "";
      let stderr = "";
      const code = main(argv, { stdout: (s) => (stdout += s), stderr: (s) => (stderr += s) });
      return { code, stdout, stderr };
    }

    it.each([
      ["引数がない", []],
      ["ディレクトリがない（引数）", ["--mode", "production"]],
      ["--mode staging", ["--mode", "staging", fixtureDir("production")]],
      ["--mode がない", [fixtureDir("production")]],
      ["引数が多い", ["--mode", "production", fixtureDir("production"), "x"]],
      [
        "ディレクトリが存在しない",
        ["--mode", "production", fileURLToPath(new URL("./fixtures/no-such-dir", import.meta.url))],
      ],
    ])("%s → 2", (_name, argv) => {
      const r = run(argv);
      expect(r.code).toBe(2);
      expect(r.stdout).toBe("");
      expect(r.stderr).not.toBe("");
    });

    it("違反があれば 1 で、標準エラーに項目名", () => {
      const r = run(["--mode", "production", fixtureDir("demo")]);
      expect(r.code).toBe(1);
      expect(r.stdout).toBe("");
      const lines = r.stderr.trimEnd().split("\n");
      expect(lines).toContain('name: "raft-game-demo"（raft-game であること）');
      expect(lines.some((l) => l.startsWith("exports.CostGuard:"))).toBe(true);
    });

    it.each(MODES)("%s の写しなら 0 で、標準出力が {ok:true,…} の JSON の 1 行", (mode) => {
      const r = run(["--mode", mode, fixtureDir(mode)]);
      expect(r.code).toBe(0);
      expect(r.stderr).toBe("");
      expect(r.stdout.endsWith("\n")).toBe(true);
      expect(r.stdout.trimEnd().split("\n")).toHaveLength(1);
      expect(JSON.parse(r.stdout)).toEqual({
        ok: true,
        mode,
        worker: EXPECTED[mode].worker,
        images: localImages(fixture(mode)),
      });
    });

    it("localImages は写しの localReference", () => {
      expect(localImages(fixture("production"))).toEqual([
        "cloudflare-build/000000000000/raft-game-server-server:fixture",
      ]);
      expect(localImages(fixture("development"))).toEqual([]);
    });
  });
});
