// 全モードのステージの一覧（デモ用を含む。T-311）。
//
// stage-catalog.gen.json は server/cmd/stagecatalog が data/ から作った生成物（手で直さない）。
// data/stages/ か data/islands.json か Go の protocol.StageSummary を変えたら、リポジトリのルートで作り直す:
//
//   go -C server run ./cmd/stagecatalog --data ../data --out ../web/worker/routes/stage-catalog.gen.json
//
// Worker の型（stages.ts）は手書きなので、生成物を厳密に確かめてずれを見つける。
// 壊れていれば、モジュールの読み込み（Worker の起動）で失敗する。黙って空の一覧にしない。

import catalogJson from "./stage-catalog.gen.json";
import type { Provenance, StageSummary, StageText, TextSource } from "./stages.ts";

const CATALOG_KEYS = ["format", "stages"] as const;
const STAGE_KEYS = [
  "id",
  "title",
  "region",
  "islands",
  "ferries",
  "durationMs",
  "unlock",
  "intro",
  "learning",
  "demo",
] as const;
const INTRO_KEYS = ["text", "provenance", "source"] as const;
const SOURCE_KEYS = ["dataset", "region", "field", "quote"] as const;
const PROVENANCES: readonly Provenance[] = ["fact", "derived", "creative"];
const STAGE_ID = /^[a-z0-9][a-z0-9-]{0,31}$/;

type JsonObject = Readonly<Record<string, unknown>>;

function fail(path: string, message: string): never {
  throw new Error(`stage-catalog.gen.json: ${path}: ${message}`);
}

// キーがちょうど keys のオブジェクトか確かめる。
function exactObject(value: unknown, keys: readonly string[], path: string): JsonObject {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    fail(path, "オブジェクトでない");
  }
  const obj = value as JsonObject;
  for (const key of Object.keys(obj)) {
    if (!keys.includes(key)) fail(path, `知らないキー ${JSON.stringify(key)}`);
  }
  for (const key of keys) {
    if (!Object.hasOwn(obj, key)) fail(path, `キー ${JSON.stringify(key)} がない`);
  }
  return obj;
}

function nonEmptyString(value: unknown, path: string): string {
  if (typeof value !== "string" || value === "") fail(path, "空でない文字列でない");
  return value;
}

function positiveInt(value: unknown, path: string): number {
  if (typeof value !== "number" || !Number.isSafeInteger(value) || value < 1) {
    fail(path, "1 以上の安全な整数でない");
  }
  return value;
}

function stageId(value: unknown, path: string): string {
  if (typeof value !== "string" || !STAGE_ID.test(value)) {
    fail(path, `ステージの id の形式（${STAGE_ID.source}）でない`);
  }
  return value;
}

function parseSource(value: unknown, path: string): TextSource | null {
  if (value === null) return null;
  const obj = exactObject(value, SOURCE_KEYS, path);
  const quote = obj["quote"];
  if (quote !== null && typeof quote !== "string") fail(`${path}.quote`, "null か文字列でない");
  return Object.freeze({
    dataset: nonEmptyString(obj["dataset"], `${path}.dataset`),
    region: nonEmptyString(obj["region"], `${path}.region`),
    field: nonEmptyString(obj["field"], `${path}.field`),
    quote,
  });
}

function parseIntro(value: unknown, path: string): StageText {
  const obj = exactObject(value, INTRO_KEYS, path);
  const text = nonEmptyString(obj["text"], `${path}.text`);
  const provenance = obj["provenance"];
  if (!PROVENANCES.some((p) => p === provenance)) {
    fail(`${path}.provenance`, `${PROVENANCES.join("・")} のどれでもない`);
  }
  return Object.freeze({
    text,
    provenance: provenance as Provenance,
    source: parseSource(obj["source"], `${path}.source`),
  });
}

function parseLearning(value: unknown, path: string): readonly string[] {
  if (!Array.isArray(value)) fail(path, "配列でない");
  const items: readonly unknown[] = value;
  return Object.freeze(
    items.map((item, i) => {
      if (typeof item !== "string") fail(`${path}[${i}]`, "文字列でない");
      return item;
    }),
  );
}

function parseStage(value: unknown, path: string): StageSummary {
  const obj = exactObject(value, STAGE_KEYS, path);
  const unlock = obj["unlock"];
  const demo = obj["demo"];
  if (typeof demo !== "boolean") fail(`${path}.demo`, "真偽値でない");
  return Object.freeze({
    id: stageId(obj["id"], `${path}.id`),
    title: nonEmptyString(obj["title"], `${path}.title`),
    region: nonEmptyString(obj["region"], `${path}.region`),
    islands: positiveInt(obj["islands"], `${path}.islands`),
    ferries: positiveInt(obj["ferries"], `${path}.ferries`),
    durationMs: positiveInt(obj["durationMs"], `${path}.durationMs`),
    unlock: unlock === null ? null : stageId(unlock, `${path}.unlock`),
    intro: parseIntro(obj["intro"], `${path}.intro`),
    learning: parseLearning(obj["learning"], `${path}.learning`),
    demo,
  });
}

// 生成物を確かめ、確かめた値から新しく作った一覧を返す（入力と記憶を共有しない）。
// 誤りがあれば、場所（例: stages[2].intro.provenance）を含む Error を投げる。
export function parseStageCatalog(value: unknown): readonly StageSummary[] {
  const top = exactObject(value, CATALOG_KEYS, "(最上位)");
  if (top["format"] !== 1) fail("format", "1 でない");
  const stages = top["stages"];
  if (!Array.isArray(stages)) fail("stages", "配列でない");
  const items: readonly unknown[] = stages;
  const seen = new Set<string>();
  const out = items.map((item, i) => {
    const stage = parseStage(item, `stages[${i}]`);
    if (seen.has(stage.id)) fail(`stages[${i}].id`, `重複している（${stage.id}）`);
    seen.add(stage.id);
    return stage;
  });
  return Object.freeze(out);
}

export const STAGE_CATALOG: readonly StageSummary[] = parseStageCatalog(catalogJson);
