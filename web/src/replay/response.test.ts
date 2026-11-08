import { describe, expect, it } from "vitest";

import { parseReplayResponse } from "./response.ts";
import {
  failureResponse,
  successResponse,
  TEST_BUILD,
  TEST_SCORE,
  testRoundResult,
} from "./testing/responses.ts";

function parse(value: unknown): ReturnType<typeof parseReplayResponse> {
  return parseReplayResponse(JSON.stringify(value));
}

describe("parseReplayResponse", () => {
  it("RS-2 成功の応答を読める", () => {
    const outcome = parse(successResponse({ ignored: 2 }));
    expect(outcome.ok).toBe(true);
    if (!outcome.ok) {
      return;
    }
    expect(outcome.build).toBe(TEST_BUILD);
    expect(outcome.ignored).toBe(2);
    expect(outcome.record.header.stageId).toBe("mini");
    expect(outcome.record.header.bot).toBe("");
    expect(outcome.record.events).toHaveLength(30);
    expect(outcome.stage.title).toBe("テストの海");
    expect(outcome.islandsState).toEqual([]);
    expect(outcome.score).toEqual(TEST_SCORE);
    expect(outcome.result).toEqual(testRoundResult());
  });

  it.each([
    "too_large",
    "bad_input",
    "unknown_stage",
    "stage_mismatch",
    "diverged",
    "not_started",
    "internal",
  ])("RS-2 失敗の応答 %s を読める", (code) => {
    expect(parse(failureResponse(code, { stageId: "stage2", logBuild: "git:abcdef0" }))).toEqual({
      ok: false,
      code,
      message: "test",
      build: TEST_BUILD,
      stageId: "stage2",
      logBuild: "git:abcdef0",
    });
  });

  it("RS-2 stageId・logBuild がなければ null", () => {
    expect(parse(failureResponse("bad_input"))).toEqual({
      ok: false,
      code: "bad_input",
      message: "test",
      build: TEST_BUILD,
      stageId: null,
      logBuild: null,
    });
  });

  it.each<{ name: string; text: string }>([
    { name: "知らない code", text: JSON.stringify(failureResponse("exploded")) },
    {
      name: "load_failed は WASM の値ではない",
      text: JSON.stringify(failureResponse("load_failed")),
    },
    { name: "code がない", text: JSON.stringify({ ok: false, build: TEST_BUILD }) },
    { name: "JSON でない", text: "{" },
    { name: "配列", text: "[]" },
    { name: "ok がない", text: JSON.stringify({ build: TEST_BUILD }) },
    { name: "build がない", text: JSON.stringify(successResponse({ build: undefined })) },
    { name: "ignored が負", text: JSON.stringify(successResponse({ ignored: -1 })) },
    { name: "stage がない", text: JSON.stringify(successResponse({ stage: null })) },
    {
      name: "islandsState が配列でない",
      text: JSON.stringify(successResponse({ islandsState: {} })),
    },
    { name: "score がない", text: JSON.stringify(successResponse({ score: undefined })) },
    { name: "result がない", text: JSON.stringify(successResponse({ result: [] })) },
    { name: "記録の誤り", text: JSON.stringify(successResponse({ record: { format: 2 } })) },
  ])("RS-2 $name は internal", ({ text }) => {
    const outcome = parseReplayResponse(text);
    expect(outcome.ok).toBe(false);
    if (!outcome.ok) {
      expect(outcome.code).toBe("internal");
    }
  });
});
