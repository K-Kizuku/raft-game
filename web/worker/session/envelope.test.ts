import { describe, expect, it } from "vitest";

import { classifyBrowserText, classifyContainerText, exceedsBytes } from "./envelope.ts";
import { MAX_WS_MESSAGE_BYTES } from "./limits.ts";

describe("E-1 ブラウザの電文", () => {
  it.each([
    '{"v":1,"type":"hello","payload":{}}',
    '{"v":1,"type":"cmd.ferry.load","cmdId":"c-1","payload":{"ferryId":"f1"}}',
  ])("送る: %s", (text) => {
    expect(classifyBrowserText(text)).toMatchObject({ kind: "forward" });
  });

  it.each([
    '{"v":1,"type":"internal.init","payload":{}}',
    '{"v":1,"type":"internal.restore","payload":{}}',
    '{"v":1,"type":"Internal.Init","payload":{}}',
    '{"v":1,"type":"INTERNAL.result","payload":{}}',
  ])("internal: %s", (text) => {
    expect(classifyBrowserText(text)).toEqual({ kind: "internal" });
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
    expect(classifyBrowserText(text)).toEqual({ kind: "malformed" });
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
      kind: "pong",
      seq: null,
    });
    expect(classifyContainerText('{"v":1,"type":"internal.other","payload":{}}')).toEqual({
      kind: "internal",
      seq: null,
    });
  });

  it.each([
    ['{"v":1,"type":"snapshot","seq":1,"payload":{}}', 1],
    ['{"v":1,"type":"events","seq":42,"payload":{"events":[]}}', 42],
    ['{"v":1,"type":"ack","seq":7,"payload":{"cmdId":"c","ok":true,"t":10}}', 7],
  ])("送る（seq を読む）: %s", (text, seq) => {
    expect(classifyContainerText(text)).toMatchObject({ kind: "forward", seq, endsSession: false });
  });

  it("JSON でない → そのまま送る（seq なし）", () => {
    expect(classifyContainerText("not json")).toEqual({
      kind: "forward",
      seq: null,
      type: null,
      endsSession: false,
      fatalCode: null,
      ack: null,
      clockT: null,
      stageHash: null,
      build: null,
    });
  });

  it("fatal の stage_not_found は終わりにし、replaced・fatal でない internal_error はしない", () => {
    const error = (code: string, fatal: boolean) =>
      JSON.stringify({ v: 1, type: "error", seq: 3, payload: { code, message: "m", fatal } });
    expect(classifyContainerText(error("stage_not_found", true))).toMatchObject({
      kind: "forward",
      seq: 3,
      type: "error",
      endsSession: true,
      fatalCode: "stage_not_found",
    });
    expect(classifyContainerText(error("replaced", true))).toMatchObject({ endsSession: false });
    expect(classifyContainerText(error("bad_request", true))).toMatchObject({
      endsSession: false,
      fatalCode: "bad_request",
    });
    expect(classifyContainerText(error("internal_error", false))).toMatchObject({
      endsSession: false,
      fatalCode: null,
    });
  });
});

describe("E-4 コンテナの電文から読む値", () => {
  const env = (type: string, payload: unknown) => JSON.stringify({ v: 1, type, seq: 1, payload });

  it("ack の cmdId・ok・t を読む", () => {
    expect(classifyContainerText(env("ack", { cmdId: "c1", ok: true, t: 1200 }))).toMatchObject({
      kind: "forward",
      type: "ack",
      ack: { cmdId: "c1", ok: true, t: 1200 },
    });
    expect(classifyContainerText(env("ack", { cmdId: "c1", ok: false, t: 0 }))).toMatchObject({
      ack: { cmdId: "c1", ok: false, t: 0 },
    });
  });

  it.each<[string, unknown]>([
    ["t が負", { cmdId: "c", ok: true, t: -1 }],
    ["t が小数", { cmdId: "c", ok: true, t: 1.5 }],
    ["t が文字列", { cmdId: "c", ok: true, t: "5" }],
    ["t がない", { cmdId: "c", ok: true }],
    ["ok が文字列", { cmdId: "c", ok: "true", t: 1 }],
    ["cmdId が数", { cmdId: 1, ok: true, t: 1 }],
    ["payload が配列", []],
  ])("ack なし（%s）。電文は送る", (_name, payload) => {
    expect(classifyContainerText(env("ack", payload))).toMatchObject({
      kind: "forward",
      seq: 1,
      ack: null,
    });
  });

  it("ack 以外の type の cmdId・ok・t は読まない", () => {
    expect(classifyContainerText(env("error", { cmdId: "c", ok: true, t: 1 }))).toMatchObject({
      ack: null,
    });
  });

  it("snapshot の clock.t・session.stageHash・session.build を読む", () => {
    const text = env("snapshot", {
      clock: { t: 0, speed: 1 },
      session: { stageHash: "sha256:abc", build: "b1", restored: false },
    });
    expect(classifyContainerText(text)).toMatchObject({
      type: "snapshot",
      clockT: 0,
      stageHash: "sha256:abc",
      build: "b1",
    });
    const max = "h".repeat(128);
    expect(
      classifyContainerText(env("snapshot", { session: { stageHash: max, build: max } })),
    ).toMatchObject({ stageHash: max, build: max, clockT: null });
  });

  it.each<[string, unknown]>([
    ["129 文字", "x".repeat(129)],
    ["空", ""],
    ["数", 1],
    ["null", null],
  ])("stageHash・build が %s なら読まない", (_name, value) => {
    const text = env("snapshot", { clock: { t: 5 }, session: { stageHash: value, build: value } });
    expect(classifyContainerText(text)).toMatchObject({ clockT: 5, stageHash: null, build: null });
  });

  it("events の clock.t を読み、session は読まない。clock.t が負・小数・文字列なら読まない", () => {
    expect(
      classifyContainerText(env("events", { clock: { t: 61200 }, session: { stageHash: "s" } })),
    ).toMatchObject({ type: "events", clockT: 61200, stageHash: null });
    for (const t of [-1, 1.5, "1"]) {
      expect(classifyContainerText(env("events", { clock: { t } }))).toMatchObject({
        clockT: null,
      });
    }
    expect(classifyContainerText(env("ack", { clock: { t: 3 } }))).toMatchObject({ clockT: null });
  });
});

describe("E-5 ブラウザの電文から読む値", () => {
  it("forward で type・cmdId・payload を返す", () => {
    expect(
      classifyBrowserText('{"v":1,"type":"cmd.ferry.load","cmdId":"c1","payload":{"a":[1]}}'),
    ).toEqual({ kind: "forward", type: "cmd.ferry.load", cmdId: "c1", payload: { a: [1] } });
  });

  it("payload がない・null なら {}。cmdId のキーがなければ undefined、あれば値そのもの", () => {
    expect(classifyBrowserText('{"v":1,"type":"hello"}')).toEqual({
      kind: "forward",
      type: "hello",
      cmdId: undefined,
      payload: {},
    });
    expect(classifyBrowserText('{"v":1,"type":"cmd.x","cmdId":null,"payload":null}')).toEqual({
      kind: "forward",
      type: "cmd.x",
      cmdId: null,
      payload: {},
    });
    expect(classifyBrowserText('{"v":1,"type":"cmd.x","cmdId":7,"payload":[]}')).toEqual({
      kind: "forward",
      type: "cmd.x",
      cmdId: 7,
      payload: [],
    });
  });
});
