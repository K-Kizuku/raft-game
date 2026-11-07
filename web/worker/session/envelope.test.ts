import { describe, expect, it } from "vitest";

import { classifyBrowserText, classifyContainerText, exceedsBytes } from "./envelope.ts";
import { MAX_WS_MESSAGE_BYTES } from "./limits.ts";

describe("E-1 ブラウザの電文", () => {
  it.each([
    '{"v":1,"type":"hello","payload":{}}',
    '{"v":1,"type":"cmd.ferry.load","cmdId":"c-1","payload":{"ferryId":"f1"}}',
  ])("送る: %s", (text) => {
    expect(classifyBrowserText(text)).toBe("forward");
  });

  it.each([
    '{"v":1,"type":"internal.init","payload":{}}',
    '{"v":1,"type":"internal.restore","payload":{}}',
    '{"v":1,"type":"Internal.Init","payload":{}}',
    '{"v":1,"type":"INTERNAL.result","payload":{}}',
  ])("internal: %s", (text) => {
    expect(classifyBrowserText(text)).toBe("internal");
  });

  it.each([
    '{"type":"cmd.sync","Type":"internal.init"}',
    '{"v":1,"type":"cmd.sync","payload":{},"seq":1}',
    '{"v":1,"TYPE":"hello","payload":{}}',
    "[]",
    '"x"',
    "null",
    '{"type":1}',
    '{"v":1,"payload":{}}',
    '{"v":1,"type":"hello"',
  ])("malformed: %s", (text) => {
    expect(classifyBrowserText(text)).toBe("malformed");
  });
});

describe("E-2 大きさ", () => {
  it("ASCII の 16384 バイトは通り、16385 バイトは超える", () => {
    expect(exceedsBytes("a".repeat(16384), MAX_WS_MESSAGE_BYTES)).toBe(false);
    expect(exceedsBytes("a".repeat(16385), MAX_WS_MESSAGE_BYTES)).toBe(true);
  });

  it("「あ」（3 バイト）を 5462 個含む電文は超える", () => {
    const text = JSON.stringify({ v: 1, type: "hello", payload: { s: "あ".repeat(5462) } });
    expect(new TextEncoder().encode(text).byteLength).toBeGreaterThanOrEqual(16386);
    expect(exceedsBytes(text, MAX_WS_MESSAGE_BYTES)).toBe(true);
  });
});

describe("E-3 コンテナの電文", () => {
  it("internal.result・internal.ping・internal.pong は DO が受ける", () => {
    expect(classifyContainerText('{"v":1,"type":"internal.result","payload":{"a":1}}')).toEqual({
      kind: "result",
      seq: null,
      payload: { a: 1 },
    });
    expect(classifyContainerText('{"v":1,"type":"internal.ping","payload":{}}')).toEqual({
      kind: "ping",
      seq: null,
    });
    expect(classifyContainerText('{"v":1,"type":"internal.pong","payload":{}}')).toEqual({
      kind: "internal",
      seq: null,
    });
  });

  it.each([
    ['{"v":1,"type":"snapshot","seq":1,"payload":{}}', 1],
    ['{"v":1,"type":"events","seq":42,"payload":{"events":[]}}', 42],
    ['{"v":1,"type":"ack","seq":7,"payload":{"cmdId":"c","ok":true,"t":10}}', 7],
  ])("送る（seq を読む）: %s", (text, seq) => {
    expect(classifyContainerText(text)).toEqual({ kind: "forward", seq, endsSession: false });
  });

  it("JSON でない → そのまま送る（seq なし）", () => {
    expect(classifyContainerText("not json")).toEqual({
      kind: "forward",
      seq: null,
      endsSession: false,
    });
  });

  it("fatal の stage_not_found は終わりにし、replaced・fatal でない internal_error はしない", () => {
    const error = (code: string, fatal: boolean) =>
      JSON.stringify({ v: 1, type: "error", seq: 3, payload: { code, message: "m", fatal } });
    expect(classifyContainerText(error("stage_not_found", true))).toEqual({
      kind: "forward",
      seq: 3,
      endsSession: true,
    });
    expect(classifyContainerText(error("replaced", true))).toMatchObject({ endsSession: false });
    expect(classifyContainerText(error("internal_error", false))).toMatchObject({
      endsSession: false,
    });
  });
});
