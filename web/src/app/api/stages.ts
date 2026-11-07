// ステージの一覧の取得（T-401・プロトコル §2 の GET /api/stages）。
// 知らないフィールド（unlock など）は読まない（プロトコル §8）。
// demo（T-311）は真偽値でなければ、その要素を不正とする（T-413）。
// intro・learning（T-311）は形を厳密に確かめ、違えば一覧全体を失敗にする（T-416・DesignDoc §13.6）。
import { STAGE_ID_PATTERN } from "../router/routes.ts";
import type { StageCatalogEntry, StageSummary, StageText, StageTextSource } from "../types.ts";
import type { Provenance } from "../../ui/provenance/ProvenanceLabel.tsx";

export class StagesLoadError extends Error {
  constructor(message: string, options?: { cause?: unknown }) {
    super(message, options);
    this.name = "StagesLoadError";
  }
}

const STAGES_PATH = "/api/stages";

function isAbortError(error: unknown): boolean {
  return error instanceof DOMException && error.name === "AbortError";
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function isNonEmptyString(value: unknown): value is string {
  return typeof value === "string" && value !== "";
}

function isPositiveInteger(value: unknown): value is number {
  return typeof value === "number" && Number.isSafeInteger(value) && value >= 1;
}

const PROVENANCES: ReadonlySet<string> = new Set<Provenance>(["fact", "derived", "creative"]);

function isProvenance(value: unknown): value is Provenance {
  return typeof value === "string" && PROVENANCES.has(value);
}

// 学べることのキーの形（データ仕様 §5.4）。
const LEARNING_KEY_PATTERN = /^[a-z][a-z0-9_]*$/;

function toStageTextSource(value: unknown): StageTextSource | null | undefined {
  if (value === null) {
    return null;
  }
  if (!isRecord(value)) {
    return undefined;
  }
  const { dataset, region, field, quote } = value;
  if (
    !isNonEmptyString(dataset) ||
    !isNonEmptyString(region) ||
    !isNonEmptyString(field) ||
    (quote !== null && typeof quote !== "string")
  ) {
    return undefined;
  }
  return { dataset, region, field, quote };
}

// 形が違えば null。text・quote は受け取った文字のまま返す。
function toStageText(value: unknown): StageText | null {
  if (!isRecord(value)) {
    return null;
  }
  const { text, provenance } = value;
  if (typeof text !== "string" || text.trim() === "" || !isProvenance(provenance)) {
    return null;
  }
  const source = toStageTextSource(value.source);
  if (source === undefined) {
    return null;
  }
  return { text, provenance, source };
}

// 形が違えば null。重複は最初の 1 つだけ残す。
function toLearning(value: unknown): string[] | null {
  if (!Array.isArray(value)) {
    return null;
  }
  const keys: string[] = [];
  for (const key of value as unknown[]) {
    if (typeof key !== "string" || !LEARNING_KEY_PATTERN.test(key)) {
      return null;
    }
    if (!keys.includes(key)) {
      keys.push(key);
    }
  }
  return keys;
}

function toStageSummary(value: unknown): StageSummary | null {
  if (!isRecord(value)) {
    return null;
  }
  const { id, title, region, islands, ferries, durationMs, demo } = value;
  if (
    typeof id !== "string" ||
    !STAGE_ID_PATTERN.test(id) ||
    !isNonEmptyString(title) ||
    !isNonEmptyString(region) ||
    !isPositiveInteger(islands) ||
    !isPositiveInteger(ferries) ||
    !isPositiveInteger(durationMs) ||
    typeof demo !== "boolean"
  ) {
    return null;
  }
  return { id, title, region, islands, ferries, durationMs, demo };
}

function toStageCatalogEntry(value: unknown): StageCatalogEntry | null {
  const summary = toStageSummary(value);
  if (summary === null || !isRecord(value)) {
    return null;
  }
  const intro = toStageText(value.intro);
  const learning = toLearning(value.learning);
  if (intro === null || learning === null) {
    return null;
  }
  return { ...summary, intro, learning };
}

function parseStages(body: unknown): StageCatalogEntry[] {
  if (!isRecord(body) || !Array.isArray(body.stages)) {
    throw new StagesLoadError("ステージの一覧の形が違います");
  }
  const stages: StageCatalogEntry[] = [];
  const seen = new Set<string>();
  for (const item of body.stages as unknown[]) {
    const stage = toStageCatalogEntry(item);
    if (stage === null) {
      throw new StagesLoadError("ステージの形が違います");
    }
    if (seen.has(stage.id)) {
      throw new StagesLoadError(`ステージの ID が重複しています: ${stage.id}`);
    }
    seen.add(stage.id);
    stages.push(stage);
  }
  return stages;
}

export async function fetchStages(options?: {
  fetch?: typeof fetch;
  signal?: AbortSignal;
}): Promise<StageCatalogEntry[]> {
  const doFetch = options?.fetch ?? fetch;
  const signal = options?.signal;
  signal?.throwIfAborted();
  let response: Response;
  try {
    response = await doFetch(STAGES_PATH, {
      method: "GET",
      headers: { Accept: "application/json" },
      ...(signal ? { signal } : {}),
    });
  } catch (error) {
    if (isAbortError(error)) {
      throw error;
    }
    throw new StagesLoadError("ステージの一覧を取得できませんでした", { cause: error });
  }
  if (response.status !== 200) {
    throw new StagesLoadError(`ステージの一覧の取得に失敗しました（${response.status}）`);
  }
  let body: unknown;
  try {
    body = await response.json();
  } catch (error) {
    if (isAbortError(error)) {
      throw error;
    }
    throw new StagesLoadError("ステージの一覧が JSON として読めません", { cause: error });
  }
  // 本文を読み終えた後に中断されたときも、結果を返さない。
  signal?.throwIfAborted();
  return parseStages(body);
}
