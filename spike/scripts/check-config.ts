// cf build の出力（.cloudflare/output/v0/）が本番と分かれていることを確かめる（T-007a / #134、AC-5）。
//
//   node spike/scripts/check-config.ts spike/.cloudflare/output/v0
//
// 成功なら {"ok":true,"images":[…]} を標準出力に出して終了コード 0（images はコンテナの localReference）。
// 失敗なら理由（項目名を含む）を 1 行ずつ標準エラーに出して終了コード 1。
import { existsSync, readdirSync, readFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

export const WORKER_NAME = "raft-game-spike";
export const CONTAINER_PREFIX = "raft-game-spike-";

type Obj = Record<string, unknown>;

function isObj(v: unknown): v is Obj {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

export interface BuildOutput {
  worker: unknown;
  containers: { file: string; config: unknown }[];
}

export function readOutput(dir: string): BuildOutput {
  const worker: unknown = JSON.parse(readFileSync(join(dir, "workers", "default", "worker.config.json"), "utf8"));
  const containers: BuildOutput["containers"] = [];
  const cdir = join(dir, "containers");
  if (existsSync(cdir)) {
    for (const name of readdirSync(cdir).sort()) {
      const file = join(cdir, name, "container.config.json");
      if (existsSync(file)) containers.push({ file, config: JSON.parse(readFileSync(file, "utf8")) as unknown });
    }
  }
  return { worker, containers };
}

/** 違反の一覧（空なら成功）。各行は項目名で始まる */
export function checkOutput(out: BuildOutput): string[] {
  const errors: string[] = [];
  const w = out.worker;
  if (!isObj(w)) return ["worker.config.json: オブジェクトでない"];

  if (w["name"] !== WORKER_NAME) errors.push(`name: ${JSON.stringify(w["name"])}（${WORKER_NAME} であること）`);
  if (Object.hasOwn(w, "domains")) errors.push("domains: 書かない（ゾーンに触れない）");
  if (Object.hasOwn(w, "routes")) errors.push("routes: 書かない（ゾーンに触れない）");
  if (w["workersDev"] !== true) errors.push(`workersDev: ${JSON.stringify(w["workersDev"])}（true であること）`);
  if (w["previewUrls"] !== false) errors.push(`previewUrls: ${JSON.stringify(w["previewUrls"])}（false であること）`);
  const limits = w["limits"];
  if (!isObj(limits) || !Number.isInteger(limits["cpuMs"])) {
    errors.push(`limits.cpuMs: ${JSON.stringify(isObj(limits) ? limits["cpuMs"] : limits)}（整数であること）`);
  }

  const env = isObj(w["env"]) ? w["env"] : {};
  for (const [name, b] of Object.entries(env)) {
    if (isObj(b) && b["type"] === "durable-object" && b["worker"] !== WORKER_NAME) {
      errors.push(`env.${name}.worker: ${JSON.stringify(b["worker"])}（${WORKER_NAME} だけを指すこと）`);
    }
  }
  const token = env["SPIKE_TOKEN"];
  if (!isObj(token) || token["type"] !== "secret") {
    errors.push("env.SPIKE_TOKEN: secret であること");
  } else if (Object.hasOwn(token, "value")) {
    errors.push("env.SPIKE_TOKEN: 値を持たないこと（--secrets-file で渡す）");
  }

  const exportsObj = isObj(w["exports"]) ? w["exports"] : {};
  const containerNames = out.containers.map((c) => (isObj(c.config) ? c.config["name"] : undefined));
  const session = exportsObj["SpikeSession"];
  if (!isObj(session) || session["type"] !== "durable-object" || session["storage"] !== "sqlite") {
    errors.push('exports.SpikeSession: durable-object・storage "sqlite" であること');
  } else if (typeof session["container"] !== "string") {
    errors.push("exports.SpikeSession.container: コンテナを付けること");
  } else if (!containerNames.includes(session["container"])) {
    errors.push(`exports.SpikeSession.container: ${session["container"]} のコンテナの出力がない`);
  }
  for (const name of ["PlainStore", "ControlStore"]) {
    const e = exportsObj[name];
    if (!isObj(e) || e["type"] !== "durable-object" || e["storage"] !== "sqlite") {
      errors.push(`exports.${name}: durable-object・storage "sqlite" であること`);
    } else if (Object.hasOwn(e, "container")) {
      errors.push(`exports.${name}.container: コンテナを付けないこと`);
    }
  }

  if (out.containers.length === 0) errors.push("containers: コンテナの出力がない");
  for (const c of out.containers) {
    const cfg = isObj(c.config) ? c.config : {};
    const name = cfg["name"];
    if (typeof name !== "string" || !name.startsWith(CONTAINER_PREFIX)) {
      errors.push(`containers.name: ${JSON.stringify(name)}（${CONTAINER_PREFIX} で始まること）`);
    }
    if (cfg["schedulingPolicy"] !== "durable-object") {
      errors.push(`containers.schedulingPolicy: ${JSON.stringify(cfg["schedulingPolicy"])}（durable-object であること）`);
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

function main(argv: string[]): number {
  const dir = argv[0];
  if (dir === undefined || argv.length !== 1) {
    process.stderr.write("使い方: node spike/scripts/check-config.ts <.cloudflare/output/v0>\n");
    return 2;
  }
  let out: BuildOutput;
  try {
    out = readOutput(dir);
  } catch (e) {
    process.stderr.write(`check-config: 出力を読めない: ${e instanceof Error ? e.message : String(e)}\n`);
    return 1;
  }
  const errors = checkOutput(out);
  if (errors.length > 0) {
    for (const e of errors) process.stderr.write(`check-config: ${e}\n`);
    return 1;
  }
  process.stdout.write(`${JSON.stringify({ ok: true, images: localImages(out) })}\n`);
  return 0;
}

if (process.argv[1] !== undefined && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  process.exitCode = main(process.argv.slice(2));
}
