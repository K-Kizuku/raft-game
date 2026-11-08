// web/scripts/check-build-output.ts のテスト（T-601 / #65 の C-1〜C-15 を DTN の環境に合わせたもの・DTN-12 / #169）。
// 写し（fixtures/<モード>/）は、BUILD_ID=git:fixture の cf build --mode <モード> の出力から作った
// （manifest の中身を削り、localReference を cloudflare-build/000000000000/<コンテナ>-dtnactor:fixture に置き換えた。
// dtn・local-container は Docker のイメージを作らない偽の docker（WRANGLER_DOCKER_BIN）で出力だけを作った）。
// テストは写しを読み、一部を書き換えたものを checkOutput に渡す（ファイルに書かない）。
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import {
  ALLOWED_ORIGINS,
  checkForbidden,
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

type Case = [name: string, mode: Mode, mutate: (o: BuildOutput) => void, expected: string];

function expectViolation([, mode, mutate, expected]: Case): void {
  const out = fixture(mode);
  mutate(out);
  const errors = checkOutput(out, mode);
  expect(errors.length).toBeGreaterThan(0);
  expect(
    errors.filter((e) => e.startsWith(expected)),
    errors.join("\n"),
  ).not.toEqual([]);
}

const DTN_HOST = "raft-dtn.kizuku-hackathon.work";

describe("check-build-output（DTN）", () => {
  describe("C-1: 3 つのモードの写し", () => {
    it.each(MODES)("%s の写しは、そのモードで違反がない", (mode) => {
      expect(checkOutput(fixture(mode), mode)).toEqual([]);
    });

    it.each(MODES.flatMap((a) => MODES.filter((b) => b !== a).map((b) => [a, b] as const)))(
      "%s の写しを %s で検査すると違反がある",
      (fixtureMode, checkAs) => {
        expect(checkOutput(fixture(fixtureMode), checkAs).length).toBeGreaterThan(0);
      },
    );

    it("どのモードの Worker の名前も DTN 用（prod・demo の名前を使わない）", () => {
      for (const mode of MODES) expect(EXPECTED[mode].worker).toMatch(/^raft-game-dtn(-dev)?$/);
    });

    it("dtn は 2 つの Container と DtnSession・ActorHost・CostGuard を持つ", () => {
      const out = fixture("dtn");
      expect(Object.keys(sub(worker(out), "exports")).sort()).toEqual([
        "ActorHost",
        "CostGuard",
        "DtnSession",
      ]);
      expect(out.containers.map((c) => (c.config as Obj)["name"]).sort()).toEqual([
        "raft-dtn-actor",
        "raft-dtn-coordinator",
      ]);
    });
  });

  describe("C-0: prod・demo の名前・ホスト名・GameSession がない（DTN-12 §1.3）", () => {
    const cases: Case[] = [
      ["Worker の名前が raft-game", "dtn", (o) => (worker(o)["name"] = "raft-game"), "forbidden:"],
      [
        "Worker の名前が raft-game-demo",
        "dtn",
        (o) => (worker(o)["name"] = "raft-game-demo"),
        "forbidden:",
      ],
      [
        "prod のホスト名の domains",
        "dtn",
        (o) => (worker(o)["domains"] = ["raft.kizuku-hackathon.work"]),
        "forbidden:",
      ],
      [
        "ALLOWED_ORIGINS に demo のオリジン",
        "dtn",
        (o) =>
          (env(o)["ALLOWED_ORIGINS"] = {
            type: "text",
            value: `${ALLOWED_ORIGINS},https://raft-demo.kizuku-hackathon.work`,
          }),
        "forbidden:",
      ],
      [
        "GameSession の export",
        "dtn",
        (o) =>
          (sub(worker(o), "exports")["GameSession"] = {
            type: "durable-object",
            storage: "sqlite",
          }),
        "forbidden:",
      ],
      [
        "GAME_SESSION のバインディング",
        "local-container",
        (o) =>
          (env(o)["GAME_SESSION"] = {
            type: "durable-object",
            worker: "raft-game-dtn-dev",
            exportName: "X",
          }),
        "forbidden:",
      ],
      [
        "prod の CostGuard を参照",
        "dtn",
        (o) => (sub(env(o), "COST_GUARD")["worker"] = "raft-game"),
        "forbidden:",
      ],
      [
        "prod の R2",
        "dtn",
        (o) => (env(o)["REPLAYS"] = { type: "r2", name: "raft-game-replays" }),
        "forbidden:",
      ],
      [
        "prod のコンテナの名前",
        "dtn",
        (o) => (container(o)["name"] = "raft-game-server"),
        "forbidden:",
      ],
    ];
    it.each(cases)("%s → 誤り", (...c) => expectViolation(c));

    it("写しには禁止の名前がない", () => {
      for (const mode of MODES) expect(checkForbidden(fixture(mode))).toEqual([]);
    });
  });

  describe("C-2: domains", () => {
    const cases: Case[] = [
      ["dtn に domains がない", "dtn", (o) => delete worker(o)["domains"], "domains:"],
      [
        "dtn の domains が 2 つ",
        "dtn",
        (o) => (worker(o)["domains"] = [DTN_HOST, "x.example"]),
        "domains:",
      ],
      [
        "ワイルドカード",
        "dtn",
        (o) => (worker(o)["domains"] = ["*.kizuku-hackathon.work"]),
        "domains:",
      ],
      [
        "development に domains",
        "development",
        (o) => (worker(o)["domains"] = [DTN_HOST]),
        "domains:",
      ],
    ];
    it.each(cases)("%s → 誤り", (...c) => expectViolation(c));

    it("{ hostname, production: true, previews: false } の形は通る", () => {
      const out = fixture("dtn");
      worker(out)["domains"] = [{ hostname: DTN_HOST, production: true, previews: false }];
      expect(checkOutput(out, "dtn")).toEqual([]);
    });
  });

  describe("C-3: triggers・routes・workersDev・previewUrls・limits・observability・assets", () => {
    const cases: Case[] = [
      ["triggers", "dtn", (o) => (worker(o)["triggers"] = {}), "triggers:"],
      ["routes", "dtn", (o) => (worker(o)["routes"] = []), "routes:"],
      ["workersDev: true", "dtn", (o) => (worker(o)["workersDev"] = true), "workersDev:"],
      ["previewUrls: true", "dtn", (o) => (worker(o)["previewUrls"] = true), "previewUrls:"],
      [
        "cpuMs が GUARDS と違う",
        "dtn",
        (o) => (sub(worker(o), "limits")["cpuMs"] = 60),
        "limits.cpuMs:",
      ],
      [
        "redactQueryString: false",
        "dtn",
        (o) => (sub(worker(o), "observability")["redactQueryString"] = false),
        "observability.redactQueryString:",
      ],
      [
        "runWorkerFirst が違う",
        "dtn",
        (o) => (sub(worker(o), "assets")["runWorkerFirst"] = ["/api/*"]),
        "assets.runWorkerFirst:",
      ],
    ];
    it.each(cases)("%s → 誤り", (...c) => expectViolation(c));
  });

  describe("C-6: env のキーの集合・シークレット・文字列", () => {
    const cases: Case[] = [
      [
        "DTN_CONTACT_KEY がない",
        "dtn",
        (o) => delete env(o)["DTN_CONTACT_KEY"],
        "env.DTN_CONTACT_KEY:",
      ],
      ["ACTOR_HOST がない", "dtn", (o) => delete env(o)["ACTOR_HOST"], "env.ACTOR_HOST:"],
      ["余分なキー", "dtn", (o) => (env(o)["EXTRA"] = { type: "text", value: "x" }), "env.EXTRA:"],
      [
        "development に DTN_SESSION",
        "development",
        (o) =>
          (env(o)["DTN_SESSION"] = {
            type: "durable-object",
            worker: "raft-game-dtn-dev",
            exportName: "DtnSession",
          }),
        "env.DTN_SESSION:",
      ],
      [
        "シークレットに値",
        "dtn",
        (o) => (env(o)["DTN_CONTACT_KEY"] = { type: "secret", value: "x" }),
        "env.DTN_CONTACT_KEY:",
      ],
      [
        "ADMIN_TOKEN が text",
        "dtn",
        (o) => (env(o)["ADMIN_TOKEN"] = { type: "text", value: "x" }),
        "env.ADMIN_TOKEN:",
      ],
      [
        "DEMO_CONTROLS が 0",
        "dtn",
        (o) => (env(o)["DEMO_CONTROLS"] = { type: "text", value: "0" }),
        "env.DEMO_CONTROLS:",
      ],
      [
        "CONTAINER_INSTANCE が standard-1",
        "dtn",
        (o) => (env(o)["CONTAINER_INSTANCE"] = { type: "text", value: "standard-1" }),
        "env.CONTAINER_INSTANCE:",
      ],
      [
        "BUILD_ID が不正",
        "dtn",
        (o) => (env(o)["BUILD_ID"] = { type: "text", value: "a b" }),
        "env.BUILD_ID:",
      ],
    ];
    it.each(cases)("%s → 誤り", (...c) => expectViolation(c));
  });

  describe("C-9: SESSION_RATE_LIMIT", () => {
    const cases: Case[] = [
      [
        "prod の namespace（1001）",
        "dtn",
        (o) => (sub(env(o), "SESSION_RATE_LIMIT")["namespace"] = "1001"),
        "env.SESSION_RATE_LIMIT.namespace:",
      ],
      [
        "main の dev の namespace（1003）",
        "development",
        (o) => (sub(env(o), "SESSION_RATE_LIMIT")["namespace"] = "1003"),
        "env.SESSION_RATE_LIMIT.namespace:",
      ],
      [
        "simple が違う",
        "dtn",
        (o) => (sub(env(o), "SESSION_RATE_LIMIT")["simple"] = { limit: 20, period: 60 }),
        "env.SESSION_RATE_LIMIT.simple:",
      ],
    ];
    it.each(cases)("%s → 誤り", (...c) => expectViolation(c));
  });

  describe("C-10: GUARDS（DTN の専用の上限）", () => {
    const cases: Case[] = [
      [
        "monthlyUsd が 30",
        "dtn",
        (o) => (sub(guards(o), "budget")["monthlyUsd"] = 30),
        "env.GUARDS.budget.monthlyUsd:",
      ],
      [
        "dailyUsd が 2",
        "dtn",
        (o) => (sub(guards(o), "budget")["dailyUsd"] = 2),
        "env.GUARDS.budget.dailyUsd:",
      ],
      [
        "maxMinutes が 30",
        "dtn",
        (o) => (sub(guards(o), "gameTime")["maxMinutes"] = 30),
        "env.GUARDS.gameTime.maxMinutes:",
      ],
      [
        "turnstile が無効",
        "dtn",
        (o) => (sub(guards(o), "turnstile")["enabled"] = false),
        "env.GUARDS.turnstile.enabled:",
      ],
      [
        "signature を書く",
        "dtn",
        (o) => (guards(o)["signature"] = { enabled: false }),
        "env.GUARDS.signature:",
      ],
      [
        "do の単価がない",
        "dtn",
        (o) => delete sub(guards(o), "unitCostUsd")["do"],
        "env.GUARDS.unitCostUsd.do:",
      ],
      [
        "container.lite の単価がない",
        "development",
        (o) => delete sub(guards(o), "unitCostUsd")["container.lite"],
        "env.GUARDS.unitCostUsd.container.lite:",
      ],
    ];
    it.each(cases)("%s → 誤り", (...c) => expectViolation(c));
  });

  describe("C-11〜13: Durable Object・exports・コンテナ", () => {
    const cases: Case[] = [
      [
        "ACTOR_HOST が DtnSession を指す",
        "dtn",
        (o) => (sub(env(o), "ACTOR_HOST")["exportName"] = "DtnSession"),
        "env.ACTOR_HOST:",
      ],
      [
        "COST_GUARD が別の Worker",
        "dtn",
        (o) => (sub(env(o), "COST_GUARD")["worker"] = "raft-game-dtn-dev"),
        "env.COST_GUARD:",
      ],
      [
        "ActorHost の export がない",
        "dtn",
        (o) => delete sub(worker(o), "exports")["ActorHost"],
        "exports.ActorHost:",
      ],
      [
        "DtnSession のコンテナが actor",
        "dtn",
        (o) => (sub(worker(o), "exports", "DtnSession")["container"] = "raft-dtn-actor"),
        "exports.DtnSession.container:",
      ],
      [
        "CostGuard にコンテナ",
        "dtn",
        (o) => (sub(worker(o), "exports", "CostGuard")["container"] = "raft-dtn-actor"),
        "exports.CostGuard.container:",
      ],
      ["コンテナが 1 つ", "dtn", (o) => o.containers.pop(), "containers:"],
      [
        "schedulingPolicy が default",
        "dtn",
        (o) => (container(o)["schedulingPolicy"] = "default"),
        "containers.schedulingPolicy:",
      ],
      [
        "image が server",
        "local-container",
        (o) => (container(o)["images"] = { server: { localReference: "x" } }),
        "containers.images:",
      ],
      [
        "development にコンテナ",
        "development",
        (o) => o.containers.push(...fixture("dtn").containers),
        "containers:",
      ],
    ];
    it.each(cases)("%s → 誤り", (...c) => expectViolation(c));
  });

  describe("C-15: main", () => {
    it.each([
      [[]],
      [["--mode", "dtn"]],
      [["--mode", "production", fixtureDir("dtn")]],
      [["--mode", "demo", fixtureDir("dtn")]],
      [["--mode", "dtn", "/nonexistent/output"]],
    ])("引数 %j → 2", (argv) => {
      const err: string[] = [];
      expect(main(argv, { stdout: () => undefined, stderr: (s) => err.push(s) })).toBe(2);
      expect(err.join("")).not.toBe("");
    });

    it("違反があれば 1 で、標準エラーに項目名", () => {
      const err: string[] = [];
      const code = main(["--mode", "development", fixtureDir("dtn")], {
        stdout: () => undefined,
        stderr: (s) => err.push(s),
      });
      expect(code).toBe(1);
      expect(err.join("")).toContain("name:");
    });

    it.each(MODES)("%s の写しなら 0 で、標準出力が {ok:true,…} の JSON の 1 行", (mode) => {
      const out: string[] = [];
      const code = main(["--mode", mode, fixtureDir(mode)], {
        stdout: (s) => out.push(s),
        stderr: () => undefined,
      });
      expect(code).toBe(0);
      expect(JSON.parse(out.join(""))).toEqual({
        ok: true,
        mode,
        worker: EXPECTED[mode].worker,
        images: localImages(fixture(mode)),
      });
    });

    it("localImages は写しの localReference", () => {
      expect(localImages(fixture("dtn")).sort()).toEqual([
        "cloudflare-build/000000000000/raft-dtn-actor-dtnactor:fixture",
        "cloudflare-build/000000000000/raft-dtn-coordinator-dtnactor:fixture",
      ]);
      expect(localImages(fixture("development"))).toEqual([]);
    });
  });
});
