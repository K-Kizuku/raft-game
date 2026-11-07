// cf build の出力（.cloudflare/output/v0/）が、モードごとの決まりに合うことを確かめる（T-601 / #65）。
// T-603 のデプロイのワークフローでも、cf deploy の前に使う。
//
//   node web/scripts/check-build-output.ts --mode <production|demo|development|local-container> <.cloudflare/output/v0>
//
// 成功: {"ok":true,"mode":"<モード>","worker":"<名前>","images":[<localReference>…]} を標準出力に出して 0。
// 失敗: 理由を 1 行ずつ（項目名で始める）標準エラーに出して 1。引数の誤り・出力を読めない → 2。
//
// 期待する値は、このファイルの中に表として持つ（cloudflare.config.ts を import しない。
// 設定の誤りを、設定とは別に書いた表で見つけるため）。表は docs/03-design-doc.md §12.6 と同じ。
import { existsSync, readdirSync, readFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

export const MODES = ["production", "demo", "development", "local-container"] as const;
export type Mode = (typeof MODES)[number];

interface Expected {
  worker: string;
  host: string | undefined; // domains。undefined（ローカル）なら ALLOWED_ORIGINS も書かない
  container: string | undefined; // GameSession のコンテナ（undefined ならコンテナも GameSession もない）
  ownCostGuard: boolean; // false なら一般公開の CostGuard を参照し、GUARDS・ADMIN_TOKEN を持たない
  demoControls: string;
  instance: string;
  rateLimitNamespace: string;
}

const PRODUCTION_WORKER = "raft-game";
// host のあるモード（一般公開・デモ）の ALLOWED_ORIGINS。両方とも両方のホスト名のオリジン（#130 の決定・T-601b）
export const ALLOWED_ORIGINS =
  "https://raft.kizuku-hackathon.work,https://raft-demo.kizuku-hackathon.work";

export const EXPECTED: Record<Mode, Expected> = {
  production: {
    worker: PRODUCTION_WORKER,
    host: "raft.kizuku-hackathon.work",
    container: "raft-game-server",
    ownCostGuard: true,
    demoControls: "0",
    instance: "lite",
    rateLimitNamespace: "1001",
  },
  demo: {
    worker: "raft-game-demo",
    host: "raft-demo.kizuku-hackathon.work",
    container: "raft-game-demo-server",
    ownCostGuard: false,
    demoControls: "1",
    instance: "standard-1",
    rateLimitNamespace: "1002",
  },
  development: {
    worker: "raft-game-dev",
    host: undefined,
    container: undefined,
    ownCostGuard: true,
    demoControls: "1",
    instance: "lite",
    rateLimitNamespace: "1003",
  },
  "local-container": {
    worker: "raft-game-dev",
    host: undefined,
    container: "raft-game-dev-server",
    ownCostGuard: true,
    demoControls: "1",
    instance: "lite",
    rateLimitNamespace: "1003",
  },
};

const SECRETS = ["SESSION_SIGNING_KEY", "TURNSTILE_SECRET", "ADMIN_TOKEN"] as const;
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
const R2_NAME = "raft-game-replays";

/** モードの env のキーの集合（上の表から作る） */
export function expectedEnvKeys(mode: Mode): string[] {
  const e = EXPECTED[mode];
  const keys = [
    "DEMO_CONTROLS",
    "BUILD_ID",
    "CONTAINER_INSTANCE",
    "COST_GUARD",
    "SESSION_SIGNING_KEY",
    "TURNSTILE_SECRET",
    "SESSION_RATE_LIMIT",
    "REPLAYS",
  ];
  if (e.host !== undefined) keys.push("ALLOWED_ORIGINS");
  if (e.container !== undefined) keys.push("GAME_SESSION");
  if (e.ownCostGuard) keys.push("GUARDS", "ADMIN_TOKEN");
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

function checkGuards(env: Obj, mode: Mode, errors: string[]): Obj | undefined {
  const b = env["GUARDS"];
  if (!EXPECTED[mode].ownCostGuard || b === undefined) return undefined; // 有無は env のキーの集合で見る
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
  const limits: [string, number][] = [
    ["budget.monthlyUsd", 30],
    ["budget.dailyUsd", 1],
    ["gameTime.maxMinutes", 30],
    ["gameTime.maxAlarms", 20],
  ];
  for (const [p, max] of limits) {
    const v = at(p);
    if (typeof v !== "number" || !Number.isFinite(v) || v <= 0 || v > max) {
      errors.push(`env.GUARDS.${p}: ${show(v)}（0 より大きく ${max} 以下であること。ADR-0018）`);
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

/** 違反の一覧（空なら成功）。各行は項目名で始まる */
export function checkOutput(out: BuildOutput, mode: Mode): string[] {
  const e = EXPECTED[mode];
  const errors: string[] = [];
  const w = out.worker;
  if (!isObj(w)) return ["worker.config.json: オブジェクトでない"];

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
  const guards = checkGuards(env, mode, errors);

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

  // 9. 文字列
  checkText(env, "DEMO_CONTROLS", e.demoControls, errors);
  checkText(env, "CONTAINER_INSTANCE", e.instance, errors);
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

  // 10. SESSION_RATE_LIMIT
  const rl = env["SESSION_RATE_LIMIT"];
  if (rl !== undefined) {
    if (!isObj(rl) || rl["type"] !== "rate-limit") {
      errors.push('env.SESSION_RATE_LIMIT: type "rate-limit" であること');
    } else {
      const ns = rl["namespace"];
      if (typeof ns !== "string" || !/^[1-9][0-9]*$/.test(ns) || ns !== e.rateLimitNamespace) {
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

  // 12. Durable Object のバインディング
  checkDurableObject(env, "GAME_SESSION", e.worker, "GameSession", errors);
  checkDurableObject(
    env,
    "COST_GUARD",
    e.ownCostGuard ? e.worker : PRODUCTION_WORKER,
    "CostGuard",
    errors,
  );

  // 15. REPLAYS
  const r2 = env["REPLAYS"];
  if (r2 !== undefined && (!isObj(r2) || r2["type"] !== "r2" || r2["name"] !== R2_NAME)) {
    errors.push(
      `env.REPLAYS: ${show(isObj(r2) ? r2["name"] : r2)}（type "r2" で ${R2_NAME} であること）`,
    );
  }

  // 13. exports
  const ex = isObj(w["exports"]) ? w["exports"] : {};
  const wantExports = [
    ...(e.container !== undefined ? ["GameSession"] : []),
    ...(e.ownCostGuard ? ["CostGuard"] : []),
  ];
  for (const k of Object.keys(ex).sort()) {
    if (!wantExports.includes(k)) errors.push(`exports.${k}: このモードでは宣言しない`);
  }
  for (const k of wantExports) {
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
    if (k === "GameSession" && e.container !== undefined) {
      if (x["container"] !== e.container) {
        errors.push(
          `exports.GameSession.container: ${show(x["container"])}（${e.container} であること）`,
        );
      } else if (!containerNames.includes(e.container)) {
        errors.push(
          `exports.GameSession.container: ${e.container} の container.config.json がない`,
        );
      }
    } else if (Object.hasOwn(x, "container")) {
      errors.push(`exports.${k}.container: コンテナを付けないこと`);
    }
  }

  // 14. コンテナ
  if (e.container === undefined) {
    if (out.containers.length > 0) {
      errors.push(
        `containers: ${JSON.stringify(containerNames)}（このモードではコンテナを持たない）`,
      );
    }
  } else {
    if (out.containers.length !== 1) {
      errors.push(
        `containers: ${JSON.stringify(containerNames)}（${e.container} のちょうど 1 つであること）`,
      );
    }
    for (const c of out.containers) {
      const cfg = isObj(c.config) ? c.config : {};
      if (cfg["name"] !== e.container)
        errors.push(`containers.name: ${show(cfg["name"])}（${e.container} であること）`);
      if (cfg["schedulingPolicy"] !== "durable-object") {
        errors.push(
          `containers.schedulingPolicy: ${show(cfg["schedulingPolicy"])}（durable-object であること）`,
        );
      }
      const images = isObj(cfg["images"]) ? Object.keys(cfg["images"]) : [];
      if (JSON.stringify(images) !== JSON.stringify(["server"])) {
        errors.push(`containers.images: ${JSON.stringify(images)}（server だけであること）`);
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
