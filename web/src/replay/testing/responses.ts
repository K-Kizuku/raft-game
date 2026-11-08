// テスト用の WASM の応答（server/cmd/replaywasm/doc.go の形）。net/fixtures の記録（bot を "" にする）と
// ステージから組み立てる。本番のコードから import しない。
import miniRecord from "../../net/fixtures/mini-record.json";
import miniStage from "../../net/fixtures/mini-stage.json";
import { parseRecord, placeholderRoundResult } from "../../net/index.ts";
import type { RoundResult, Score } from "../../protocol/index.ts";

export const TEST_BUILD = "git:1234567";

export function testRecordJson(): Record<string, unknown> {
  return { ...(structuredClone(miniRecord) as Record<string, unknown>), bot: "" };
}

export function testRoundResult(): RoundResult {
  return { ...placeholderRoundResult(parseRecord(testRecordJson())), stars: 2 };
}

export const TEST_SCORE: Score = { total: 0, successes: 0, target: 3, criticalSecured: [] };

export function successResponse(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    ok: true,
    build: TEST_BUILD,
    ignored: 0,
    record: testRecordJson(),
    stage: structuredClone(miniStage),
    islandsState: [],
    score: TEST_SCORE,
    result: testRoundResult(),
    ...overrides,
  };
}

export function failureResponse(
  code: string,
  fields: { stageId?: string; logBuild?: string; message?: string } = {},
): Record<string, unknown> {
  return {
    ok: false,
    build: TEST_BUILD,
    code,
    message: fields.message ?? "test",
    ...(fields.stageId === undefined ? {} : { stageId: fields.stageId }),
    ...(fields.logBuild === undefined ? {} : { logBuild: fields.logBuild }),
  };
}
