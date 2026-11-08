// cf build の出力（.cloudflare/output/v0/）が、モードごとの決まりに合うことを確かめる（T-601 / #65・DTN-12 / #169）。
// dtn/main では DTN の環境だけを作る（docs/dtn/environment.md §1）。DTN のデプロイのワークフロー（H3）でも、
// cf deploy の前に使う。
//
//   node web/scripts/check-build-output.ts --mode <dtn|development|local-container> <.cloudflare/output/v0>
//
// 成功: {"ok":true,"mode":"<モード>","worker":"<名前>","images":[<localReference>…]} を標準出力に出して 0。
// 失敗: 理由を 1 行ずつ（項目名で始める）標準エラーに出して 1。引数の誤り・出力を読めない → 2。
//
// 期待する値は、このファイルの中に表として持つ（cloudflare.config.ts を import しない。
// 設定の誤りを、設定とは別に書いた表で見つけるため）。表は docs/dtn/environment.md §1 と同じ。
// 出力に prod・demo の Worker の名前・ホスト名・GameSession が 1 つでもあれば失敗にする（失敗しても安全な側）。
import { existsSync, readdirSync, readFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

export const MODES = ["dtn", "development", "local-container"] as const;
export type Mode = (typeof MODES)[number];

interface Expected {
  worker: string;
  host: string | undefined; // domains。undefined（ローカル）なら ALLOWED_ORIGINS も書かない
  // DtnSession・ActorHost の Container（undefined なら Container も DtnSession・ActorHost もない）
  containers: { coordinator: string; actor: string } | undefined;
  rateLimitNamespace: string;
}

const DTN_HOST = "raft-dtn.kizuku-hackathon.work";
export const ALLOWED_ORIGINS = `https://${DTN_HOST}`;

export const EXPECTED: Record<Mode, Expected> = {
  dtn: {
    worker: "raft-game-dtn",
    host: DTN_HOST,
    containers: { coordinator: "raft-dtn-coordinator", actor: "raft-dtn-actor" },
    rateLimitNamespace: "1004",
  },
  development: {
    worker: "raft-game-dtn-dev",
    host: undefined,
    containers: undefined,
    rateLimitNamespace: "1005",
  },
  "local-container": {
    worker: "raft-game-dtn-dev",
    host: undefined,
    containers: { coordinator: "raft-dtn-dev-coordinator", actor: "raft-dtn-dev-actor" },
    rateLimitNamespace: "1005",
  },
};

// prod・demo（main）の名前（出力のどこにも現れてはいけない）。JSON の文字列としてそのまま一致するもの
export const FORBIDDEN_NAMES = [
  "raft-game",
  "raft-game-demo",
  "raft-game-dev",
  "raft-game-server",
  "raft-game-demo-server",
  "raft-game-dev-server",
  "raft-game-replays",
  "GameSession",
  "GAME_SESSION",
] as const;
// prod・demo のホスト名（部分一致でも誤り）
export const FORBIDDEN_HOSTS = [
  "raft.kizuku-hackathon.work",
  "raft-demo.kizuku-hackathon.work",
] as const;
// prod 1001・demo 1002・main の dev 1003（ADR-0024）
const FORBIDDEN_RATE_LIMIT_NAMESPACES = ["1001", "1002", "1003"];

const SECRETS = [
  "SESSION_SIGNING_KEY",
  "TURNSTILE_SECRET",
  "ADMIN_TOKEN",
  "DTN_CONTACT_KEY",
] as const;
const RUN_WORKER_FIRST = ["/api/*", "/ws/*"];
const RATE_LIMIT = { limit: 10, period: 60 };
const BUILD_ID_RE = /^[A-Za-z0-9._:-]{1,64}$/;
const GUARDS_KEYS = [
  "acceptGames",
  "budget",
  "cpuMs",
  "gameTime",
  "rateLimit",
  "turnstile",
  "unitCostUsd",
];

/** モードの env のキーの集合（上の表から作る） */
export function expectedEnvKeys(mode: Mode): string[] {
  const e = EXPECTED[mode];
  const keys = [
    "DEMO_CONTROLS",
    "BUILD_ID",
    "CONTAINER_INSTANCE",
    "COST_GUARD",
    "GUARDS",
    "SESSION_SIGNING_KEY",
    "TURNSTILE_SECRET",
    "ADMIN_TOKEN",
    "DTN_CONTACT_KEY",
    "SESSION_RATE_LIMIT",
  ];
  if (e.host !== undefined) keys.push("ALLOWED_ORIGINS");
  if (e.containers !== undefined) keys.push("DTN_SESSION", "ACTOR_HOST");
  return keys.sort();
}

type Obj = Record<string, unknown>;

function isObj(v: unknown): v is Obj {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

function show(v: unknown): string {
  return v === undefined ? "なし" : JSON.stringify(v);
}

function isMode(v: string): v is Mode {
  return (MODES as readonly string[]).includes(v);
}

export interface BuildOutput {
  worker: unknown;
  containers: { file: string; config: unknown }[];
}

export function readOutput(dir: string): BuildOutput {
  const worker: unknown = JSON.parse(
    readFileSync(join(dir, "workers", "default", "worker.config.json"), "utf8"),
  );
  const containers: BuildOutput["containers"] = [];
  const cdir = join(dir, "containers");
  if (existsSync(cdir)) {
    for (const name of readdirSync(cdir).sort()) {
      const file = join(cdir, name, "container.config.json");
      if (existsSync(file))
        containers.push({ file, config: JSON.parse(readFileSync(file, "utf8")) as unknown });
    }
  }
  return { worker, containers };
}

function checkDomains(w: Obj, e: Expected, errors: string[]): void {
  const domains = w["domains"];
  if (Array.isArray(domains)) {
    for (const d of domains) {
      const host = isObj(d) ? d["hostname"] : d;
      if (typeof host === "string" && host.includes("*")) {
        errors.push(`domains: ${JSON.stringify(host)}（ワイルドカードを使わない）`);
      }
    }
  }
  if (e.host === undefined) {
    if (Object.hasOwn(w, "domains"))
      errors.push(`domains: ${show(domains)}（このモードでは書かない）`);
    return;
  }
  if (!Array.isArray(domains) || domains.length !== 1) {
    errors.push(`domains: ${show(domains)}（["${e.host}"] のちょうど 1 つであること）`);
    return;
  }
  const d: unknown = domains[0];
  const ok =
    d === e.host ||
    (isObj(d) &&
      Object.keys(d).length === 3 &&
      d["hostname"] === e.host &&
      d["production"] === true &&
      d["previews"] === false);
  if (!ok) errors.push(`domains: ${show(domains)}（["${e.host}"] のちょうど 1 つであること）`);
}

function checkGuards(env: Obj, errors: string[]): Obj | undefined {
  const b = env["GUARDS"];
  if (b === undefined) return undefined; // 有無は env のキーの集合で見る
  if (!isObj(b) || b["type"] !== "json" || !isObj(b["value"])) {
    errors.push('env.GUARDS: type "json" でオブジェクトの値を持つこと');
    return undefined;
  }
  const g = b["value"];
  const keys = Object.keys(g).sort();
  if (Object.hasOwn(g, "signature"))
    errors.push("env.GUARDS.signature: 書かない（G3 を設定で外さない）");
  if (JSON.stringify(keys) !== JSON.stringify(GUARDS_KEYS)) {
    errors.push(
      `env.GUARDS: キーが ${JSON.stringify(keys)}（${GUARDS_KEYS.join("・")} のちょうど 7 つであること）`,
    );
  }
  const at = (path: string): unknown => {
    let v: unknown = g;
    for (const k of path.split(".")) v = isObj(v) ? v[k] : undefined;
    return v;
  };
  for (const p of ["budget.enabled", "turnstile.enabled", "rateLimit.enabled", "acceptGames"]) {
    if (at(p) !== true) errors.push(`env.GUARDS.${p}: ${show(at(p))}（true であること）`);
  }
  // DTN の専用の上限（environment.md §1・§2: 1 日 1 USD・1 か月 10 USD・G2 は 15 分）
  const limits: [string, number][] = [
    ["budget.monthlyUsd", 10],
    ["budget.dailyUsd", 1],
    ["gameTime.maxMinutes", 15],
    ["gameTime.maxAlarms", 20],
  ];
  for (const [p, max] of limits) {
    const v = at(p);
    if (typeof v !== "number" || !Number.isFinite(v) || v <= 0 || v > max) {
      errors.push(
        `env.GUARDS.${p}: ${show(v)}（0 より大きく ${max} 以下であること。environment.md）`,
      );
    }
  }
  // 内訳つきの予約の単価（environment.md §3）
  for (const p of ["unitCostUsd.container.lite", "unitCostUsd.do"]) {
    const unit = isObj(g["unitCostUsd"])
      ? g["unitCostUsd"][p.slice("unitCostUsd.".length)]
      : undefined;
    if (typeof unit !== "number" || !Number.isFinite(unit) || unit <= 0) {
      errors.push(`env.GUARDS.${p}: ${show(unit)}（0 より大きい数であること）`);
    }
  }
  return g;
}

function checkText(env: Obj, key: string, expected: string, errors: string[]): void {
  const b = env[key];
  if (b === undefined) return; // 有無は env のキーの集合で見る
  if (!isObj(b) || b["type"] !== "text" || b["value"] !== expected) {
    errors.push(
      `env.${key}: ${show(isObj(b) ? b["value"] : b)}（type "text" で ${JSON.stringify(expected)} であること）`,
    );
  }
}

function checkDurableObject(
  env: Obj,
  key: string,
  worker: string,
  exportName: string,
  errors: string[],
): void {
  const b = env[key];
  if (b === undefined) return;
  if (
    !isObj(b) ||
    b["type"] !== "durable-object" ||
    b["worker"] !== worker ||
    b["exportName"] !== exportName
  ) {
    const got = isObj(b) ? `${show(b["worker"])} の ${show(b["exportName"])}` : show(b);
    errors.push(`env.${key}: ${got}（${worker} の ${exportName} を指すこと）`);
  }
}

// JSON の中の文字列（キーと値）をすべて集める。
function strings(value: unknown, out: string[] = []): string[] {
  if (typeof value === "string") out.push(value);
  else if (Array.isArray(value)) for (const item of value) strings(item, out);
  else if (isObj(value)) {
    for (const [key, item] of Object.entries(value)) {
      out.push(key);
      strings(item, out);
    }
  }
  return out;
}

/** prod・demo の Worker の名前・ホスト名・GameSession が出力にないか（DTN-12 §1.3） */
export function checkForbidden(out: BuildOutput): string[] {
  const errors: string[] = [];
  const all = [
    ...strings(out.worker).map((s) => ["worker.config.json", s] as const),
    ...out.containers.flatMap((c) =>
      strings(c.config).map((s) => ["container.config.json", s] as const),
    ),
  ];
  for (const [file, s] of all) {
    if ((FORBIDDEN_NAMES as readonly string[]).includes(s)) {
      errors.push(`forbidden: ${file} に prod・demo の名前 ${JSON.stringify(s)} がある`);
    }
    for (const host of FORBIDDEN_HOSTS) {
      if (s.includes(host))
        errors.push(`forbidden: ${file} に prod・demo のホスト名 ${host} がある`);
    }
  }
  return errors;
}

/** 違反の一覧（空なら成功）。各行は項目名で始まる */
export function checkOutput(out: BuildOutput, mode: Mode): string[] {
  const e = EXPECTED[mode];
  const errors: string[] = [];
  const w = out.worker;
  if (!isObj(w)) return ["worker.config.json: オブジェクトでない"];

  // 0. prod・demo の名前・ホスト名・GameSession がない
  errors.push(...checkForbidden(out));

  // 1. name
  if (w["name"] !== e.worker) errors.push(`name: ${show(w["name"])}（${e.worker} であること）`);

  // 2. domains
  checkDomains(w, e, errors);

  // 3. triggers・routes・workersDev・previewUrls
  if (Object.hasOwn(w, "triggers"))
    errors.push("triggers: 書かない（ゾーンには domains の 1 つのホスト名だけを使う）");
  if (Object.hasOwn(w, "routes"))
    errors.push("routes: 書かない（ゾーンには domains の 1 つのホスト名だけを使う）");
  if (w["workersDev"] !== false)
    errors.push(`workersDev: ${show(w["workersDev"])}（false であること）`);
  if (w["previewUrls"] !== false)
    errors.push(`previewUrls: ${show(w["previewUrls"])}（false であること）`);

  const env = isObj(w["env"]) ? w["env"] : {};
  const guards = checkGuards(env, errors);

  // 4. limits.cpuMs
  const limits = w["limits"];
  const cpuMs = isObj(limits) ? limits["cpuMs"] : undefined;
  if (typeof cpuMs !== "number" || !Number.isInteger(cpuMs) || cpuMs < 10 || cpuMs > 30000) {
    errors.push(`limits.cpuMs: ${show(cpuMs)}（10〜30000 の整数であること）`);
  } else if (guards !== undefined && guards["cpuMs"] !== cpuMs) {
    errors.push(`limits.cpuMs: ${cpuMs} が env.GUARDS.cpuMs（${show(guards["cpuMs"])}）と違う`);
  }

  // 5. observability
  const obs = isObj(w["observability"]) ? w["observability"] : {};
  const logs = isObj(obs["logs"]) ? obs["logs"] : {};
  if (obs["enabled"] !== true)
    errors.push(`observability.enabled: ${show(obs["enabled"])}（true であること）`);
  if (logs["enabled"] !== true)
    errors.push(`observability.logs.enabled: ${show(logs["enabled"])}（true であること）`);
  if (obs["redactQueryString"] !== true) {
    errors.push(
      `observability.redactQueryString: ${show(obs["redactQueryString"])}（true であること。?token= をログに残さない）`,
    );
  }

  // 6. assets.runWorkerFirst
  const assets = isObj(w["assets"]) ? w["assets"] : {};
  if (JSON.stringify(assets["runWorkerFirst"]) !== JSON.stringify(RUN_WORKER_FIRST)) {
    errors.push(
      `assets.runWorkerFirst: ${show(assets["runWorkerFirst"])}（${JSON.stringify(RUN_WORKER_FIRST)} であること）`,
    );
  }

  // 7. env のキーの集合
  if (!isObj(w["env"])) errors.push("env: オブジェクトでない");
  const want = expectedEnvKeys(mode);
  for (const k of Object.keys(env).sort()) {
    if (!want.includes(k)) errors.push(`env.${k}: このモードでは置かない`);
  }
  for (const k of want) {
    if (!Object.hasOwn(env, k)) errors.push(`env.${k}: ない`);
  }

  // 8. シークレット
  for (const k of SECRETS) {
    const b = env[k];
    if (b === undefined) continue;
    if (!isObj(b) || b["type"] !== "secret") errors.push(`env.${k}: type "secret" であること`);
    else if (Object.hasOwn(b, "value"))
      errors.push(`env.${k}: 値を持たないこと（--secrets-file で渡す）`);
  }

  // 9. 文字列（DEMO_CONTROLS は全モードで 1。Container はすべて lite）
  checkText(env, "DEMO_CONTROLS", "1", errors);
  checkText(env, "CONTAINER_INSTANCE", "lite", errors);
  if (e.host !== undefined) checkText(env, "ALLOWED_ORIGINS", ALLOWED_ORIGINS, errors);
  const buildId = env["BUILD_ID"];
  if (buildId !== undefined) {
    const v = isObj(buildId) ? buildId["value"] : undefined;
    if (
      !isObj(buildId) ||
      buildId["type"] !== "text" ||
      typeof v !== "string" ||
      !BUILD_ID_RE.test(v)
    ) {
      errors.push(`env.BUILD_ID: ${show(v)}（type "text" で ${BUILD_ID_RE.source} に合うこと）`);
    }
  }

  // 10. SESSION_RATE_LIMIT（namespace は prod・demo・main の dev と重ならない。ADR-0024）
  const rl = env["SESSION_RATE_LIMIT"];
  if (rl !== undefined) {
    if (!isObj(rl) || rl["type"] !== "rate-limit") {
      errors.push('env.SESSION_RATE_LIMIT: type "rate-limit" であること');
    } else {
      const ns = rl["namespace"];
      if (
        typeof ns !== "string" ||
        !/^[1-9][0-9]*$/.test(ns) ||
        ns !== e.rateLimitNamespace ||
        FORBIDDEN_RATE_LIMIT_NAMESPACES.includes(ns)
      ) {
        errors.push(
          `env.SESSION_RATE_LIMIT.namespace: ${show(ns)}（正の整数の文字列で ${JSON.stringify(e.rateLimitNamespace)} であること）`,
        );
      }
      const simple = rl["simple"];
      if (!isObj(simple) || JSON.stringify(simple) !== JSON.stringify(RATE_LIMIT)) {
        errors.push(
          `env.SESSION_RATE_LIMIT.simple: ${show(simple)}（${JSON.stringify(RATE_LIMIT)} であること）`,
        );
      }
      const perIp =
        guards !== undefined && isObj(guards["rateLimit"])
          ? guards["rateLimit"]["perIpPerMinute"]
          : undefined;
      if (guards !== undefined && isObj(simple) && simple["limit"] !== perIp) {
        errors.push(
          `env.SESSION_RATE_LIMIT.simple.limit: ${show(simple["limit"])} が env.GUARDS.rateLimit.perIpPerMinute（${show(perIp)}）と違う`,
        );
      }
    }
  }

  // 11. Durable Object のバインディング（すべて自分の Worker。prod の CostGuard を参照しない）
  checkDurableObject(env, "DTN_SESSION", e.worker, "DtnSession", errors);
  checkDurableObject(env, "ACTOR_HOST", e.worker, "ActorHost", errors);
  checkDurableObject(env, "COST_GUARD", e.worker, "CostGuard", errors);

  // 12. exports
  const ex = isObj(w["exports"]) ? w["exports"] : {};
  const wantExports: Record<string, string | undefined> = { CostGuard: undefined };
  if (e.containers !== undefined) {
    wantExports["DtnSession"] = e.containers.coordinator;
    wantExports["ActorHost"] = e.containers.actor;
  }
  for (const k of Object.keys(ex).sort()) {
    if (!Object.hasOwn(wantExports, k)) errors.push(`exports.${k}: このモードでは宣言しない`);
  }
  for (const k of Object.keys(wantExports)) {
    if (!Object.hasOwn(ex, k)) errors.push(`exports.${k}: ない`);
  }
  const containerNames = out.containers.map((c) =>
    isObj(c.config) ? c.config["name"] : undefined,
  );
  for (const k of Object.keys(ex)) {
    const x = ex[k];
    if (!isObj(x) || x["type"] !== "durable-object" || x["storage"] !== "sqlite") {
      errors.push(`exports.${k}: type "durable-object"・storage "sqlite" であること`);
      continue;
    }
    const wantContainer = wantExports[k];
    if (wantContainer !== undefined) {
      if (x["container"] !== wantContainer) {
        errors.push(
          `exports.${k}.container: ${show(x["container"])}（${wantContainer} であること）`,
        );
      } else if (!containerNames.includes(wantContainer)) {
        errors.push(`exports.${k}.container: ${wantContainer} の container.config.json がない`);
      }
    } else if (Object.hasOwn(x, "container")) {
      errors.push(`exports.${k}.container: コンテナを付けないこと`);
    }
  }

  // 13. コンテナ（DO のクラスごとに 1 つ。どちらも image は dtnactor だけ）
  if (e.containers === undefined) {
    if (out.containers.length > 0) {
      errors.push(
        `containers: ${JSON.stringify(containerNames)}（このモードではコンテナを持たない）`,
      );
    }
  } else {
    const wantNames = [e.containers.actor, e.containers.coordinator].sort();
    if (JSON.stringify([...containerNames].sort()) !== JSON.stringify(wantNames)) {
      errors.push(
        `containers: ${JSON.stringify(containerNames)}（${wantNames.join("・")} のちょうど 2 つであること）`,
      );
    }
    for (const c of out.containers) {
      const cfg = isObj(c.config) ? c.config : {};
      if (!wantNames.includes(String(cfg["name"])))
        errors.push(
          `containers.name: ${show(cfg["name"])}（${wantNames.join("・")} のどれかであること）`,
        );
      if (cfg["schedulingPolicy"] !== "durable-object") {
        errors.push(
          `containers.schedulingPolicy: ${show(cfg["schedulingPolicy"])}（durable-object であること）`,
        );
      }
      const images = isObj(cfg["images"]) ? Object.keys(cfg["images"]) : [];
      if (JSON.stringify(images) !== JSON.stringify(["dtnactor"])) {
        errors.push(`containers.images: ${JSON.stringify(images)}（dtnactor だけであること）`);
      }
    }
  }

  return errors;
}

/** コンテナの localReference（cf build がローカルの Docker に作ったイメージ） */
export function localImages(out: BuildOutput): string[] {
  const refs: string[] = [];
  for (const c of out.containers) {
    const images = isObj(c.config) && isObj(c.config["images"]) ? c.config["images"] : {};
    for (const img of Object.values(images)) {
      if (isObj(img) && typeof img["localReference"] === "string") refs.push(img["localReference"]);
    }
  }
  return refs;
}

const USAGE = `使い方: node web/scripts/check-build-output.ts --mode <${MODES.join("|")}> <.cloudflare/output/v0>\n`;

export interface Io {
  stdout: (s: string) => void;
  stderr: (s: string) => void;
}

export function main(argv: string[], io: Io): number {
  const [flag, mode, dir, ...rest] = argv;
  if (flag !== "--mode" || mode === undefined || dir === undefined || rest.length > 0) {
    io.stderr(USAGE);
    return 2;
  }
  if (!isMode(mode)) {
    io.stderr(`check-build-output: 知らないモード: ${mode}\n${USAGE}`);
    return 2;
  }
  let out: BuildOutput;
  try {
    out = readOutput(dir);
  } catch (err) {
    io.stderr(
      `check-build-output: 出力を読めない: ${err instanceof Error ? err.message : String(err)}\n`,
    );
    return 2;
  }
  const errors = checkOutput(out, mode);
  if (errors.length > 0) {
    for (const msg of errors) io.stderr(`${msg}\n`);
    return 1;
  }
  const w = out.worker as Obj;
  io.stdout(`${JSON.stringify({ ok: true, mode, worker: w["name"], images: localImages(out) })}\n`);
  return 0;
}

if (process.argv[1] !== undefined && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  process.exitCode = main(process.argv.slice(2), {
    stdout: (s) => process.stdout.write(s),
    stderr: (s) => process.stderr.write(s),
  });
}
