// リプレイの WASM（R2・server/cmd/replaywasm/doc.go の「JS との約束」）の応答を読む（ADR-0028 R4・T-720d）。
// 形が約束と違うもの・知らない code・記録の誤りは code: "internal" にする。
import { parseRecord, type GameRecord } from "../net/index.ts";
import type { IslandState, RoundResult, Score, StageView } from "../protocol/index.ts";

export type ReplayErrorCode =
  | "too_large"
  | "bad_input"
  | "unknown_stage"
  | "stage_mismatch"
  | "diverged"
  | "not_started"
  | "internal"
  | "load_failed"; // WASM を読めなかった（web の側だけの値）

export type ReplaySuccess = {
  ok: true;
  build: string;
  ignored: number;
  record: GameRecord;
  stage: StageView;
  islandsState: IslandState[];
  score: Score;
  result: RoundResult;
};

export type ReplayFailure = {
  ok: false;
  code: ReplayErrorCode;
  message: string;
  build: string | null;
  stageId: string | null;
  logBuild: string | null;
};

export type ReplayOutcome = ReplaySuccess | ReplayFailure;

// WASM が返す code（load_failed は web の側だけなので含めない）。
const WASM_CODES: ReadonlySet<string> = new Set([
  "too_large",
  "bad_input",
  "unknown_stage",
  "stage_mismatch",
  "diverged",
  "not_started",
  "internal",
]);

type Obj = Record<string, unknown>;

function isObject(value: unknown): value is Obj {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

// replayFailure は web の側で作る失敗（too_large・load_failed・internal）。
export function replayFailure(
  code: ReplayErrorCode,
  message: string,
  fields: Partial<Pick<ReplayFailure, "build" | "stageId" | "logBuild">> = {},
): ReplayFailure {
  return {
    ok: false,
    code,
    message,
    build: fields.build ?? null,
    stageId: fields.stageId ?? null,
    logBuild: fields.logBuild ?? null,
  };
}

function optionalString(value: unknown): string | null {
  return typeof value === "string" ? value : null;
}

function internal(message: string, build: string | null = null): ReplayFailure {
  return replayFailure("internal", message, { build });
}

// parseReplayResponse は raftReplay の戻り値（JSON の文字列）を読む。
export function parseReplayResponse(text: string): ReplayOutcome {
  let value: unknown;
  try {
    value = JSON.parse(text);
  } catch {
    return internal("応答が JSON ではありません");
  }
  if (!isObject(value)) {
    return internal("応答がオブジェクトではありません");
  }
  const build = optionalString(value["build"]);
  if (value["ok"] === false) {
    const code = value["code"];
    if (typeof code !== "string" || !WASM_CODES.has(code)) {
      return internal("応答の code を読めません", build);
    }
    return {
      ok: false,
      code: code as ReplayErrorCode,
      message: optionalString(value["message"]) ?? "",
      build,
      stageId: optionalString(value["stageId"]),
      logBuild: optionalString(value["logBuild"]),
    };
  }
  if (value["ok"] !== true) {
    return internal("応答の ok を読めません", build);
  }
  const ignored = value["ignored"];
  const stage = value["stage"];
  const islandsState = value["islandsState"];
  const score = value["score"];
  const result = value["result"];
  if (
    build === null ||
    !Number.isSafeInteger(ignored) ||
    (ignored as number) < 0 ||
    !isObject(stage) ||
    !Array.isArray(stage["islands"]) ||
    typeof stage["title"] !== "string" ||
    !Array.isArray(islandsState) ||
    !isObject(score) ||
    !isObject(result)
  ) {
    return internal("応答の形が約束と違います", build);
  }
  let record: GameRecord;
  try {
    record = parseRecord(value["record"]);
  } catch {
    return internal("応答の記録を読めません", build);
  }
  return {
    ok: true,
    build,
    ignored: ignored as number,
    record,
    stage: stage as unknown as StageView,
    islandsState: islandsState as IslandState[],
    score: score as unknown as Score,
    result: result as unknown as RoundResult,
  };
}
