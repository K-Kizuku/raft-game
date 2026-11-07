// ステージの一覧の取得（T-401・プロトコル §2 の GET /api/stages）。
// 知らないフィールド（unlock・intro・learning など）は読まない（プロトコル §8）。
// demo（T-311）は真偽値でなければ、その要素を不正とする（T-413）。
import { STAGE_ID_PATTERN } from "../router/routes.ts";
import type { StageSummary } from "../types.ts";

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

function parseStages(body: unknown): StageSummary[] {
  if (!isRecord(body) || !Array.isArray(body.stages)) {
    throw new StagesLoadError("ステージの一覧の形が違います");
  }
  const stages: StageSummary[] = [];
  const seen = new Set<string>();
  for (const item of body.stages as unknown[]) {
    const stage = toStageSummary(item);
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
}): Promise<StageSummary[]> {
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
