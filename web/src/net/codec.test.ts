import { describe, expect, it } from "vitest";

import { decodeServerMessage, encodeClientMessage } from "./codec.ts";

const clock = { t: 0, speed: 1, paused: false, endsAt: 60000 };

const snapshotPayload = {
  protocol: 1,
  session: { id: "s", stageId: "mini", seed: 1, demo: false, restored: false },
  phase: "intro",
  clock: { ...clock, allowedSpeeds: [0.5, 1, 2, 4] },
  stage: { title: "t", islands: [], routes: [], resources: [] },
  ferries: [],
  routesState: [],
  letters: [],
  requests: [],
  nodes: [],
  islandsState: [],
  score: { total: 0, successes: 0, target: 0, criticalSecured: [] },
  lastSeq: 0,
};

const minimal: Record<string, Record<string, unknown>> = {
  snapshot: snapshotPayload,
  events: { clock, events: [{ seq: 1, t: 0, type: "phase.changed", phase: "opening" }] },
  ack: { cmdId: "c-1", ok: true, t: 0 },
  "node.detail": { id: 1 },
  "round.result": { score: { total: 0 } },
  error: { code: "x", message: "m", fatal: false },
  pong: { clientTime: 1, simTime: 2 },
};

const wire = (obj: unknown) => JSON.stringify(obj);
const env = (type: string, payload: unknown, extra: Record<string, unknown> = {}) =>
  wire({ v: 1, type, seq: 1, payload, ...extra });

describe("encodeClientMessage", () => {
  it("CD-1: キーの順は v・type・cmdId・payload", () => {
    expect(encodeClientMessage("hello", { client: "web/0.1.0", resume: false })).toBe(
      '{"v":1,"type":"hello","payload":{"client":"web/0.1.0","resume":false}}',
    );
    expect(encodeClientMessage("cmd.ferry.depart", { ferryId: "f1", to: 2 }, "c-3")).toBe(
      '{"v":1,"type":"cmd.ferry.depart","cmdId":"c-3","payload":{"ferryId":"f1","to":2}}',
    );
    // @ts-expect-error internal.* はクライアントから送れない
    encodeClientMessage("internal.init", { sessionId: "s", stageId: "x", seed: 1, demo: false });
  });
});

describe("decodeServerMessage", () => {
  it.each(Object.entries(minimal))("CD-2: %s の最小限の形は message", (type, payload) => {
    const result = decodeServerMessage(env(type, payload));
    expect(result.kind).toBe("message");
    if (result.kind === "message") {
      expect(result.message.type).toBe(type);
    }
  });

  const omit = (obj: Record<string, unknown>, key: string) => {
    const copy = { ...obj };
    delete copy[key];
    return copy;
  };
  const cases: { name: string; data: unknown; kind: string }[] = [
    { name: "ArrayBuffer", data: new ArrayBuffer(4), kind: "malformed" },
    { name: "壊れた JSON", data: "{", kind: "malformed" },
    { name: "[]", data: "[]", kind: "malformed" },
    { name: "null", data: "null", kind: "malformed" },
    {
      name: "v がない",
      data: wire({ type: "pong", seq: 1, payload: minimal["pong"] }),
      kind: "version_mismatch",
    },
    {
      name: "v が 2",
      data: wire({ v: 2, type: "pong", seq: 1, payload: minimal["pong"] }),
      kind: "version_mismatch",
    },
    {
      name: 'v が "1"',
      data: wire({ v: "1", type: "pong", seq: 1, payload: minimal["pong"] }),
      kind: "version_mismatch",
    },
    { name: "seq が 0", data: env("pong", minimal["pong"], { seq: 0 }), kind: "malformed" },
    { name: "seq が -1", data: env("pong", minimal["pong"], { seq: -1 }), kind: "malformed" },
    { name: "seq が 1.5", data: env("pong", minimal["pong"], { seq: 1.5 }), kind: "malformed" },
    { name: 'seq が "1"', data: env("pong", minimal["pong"], { seq: "1" }), kind: "malformed" },
    {
      name: "seq がない",
      data: wire({ v: 1, type: "pong", payload: minimal["pong"] }),
      kind: "malformed",
    },
    { name: "payload が []", data: env("pong", []), kind: "malformed" },
    { name: "payload がない", data: wire({ v: 1, type: "pong", seq: 1 }), kind: "malformed" },
    { name: "type が foo.bar", data: env("foo.bar", {}), kind: "unknown" },
    { name: "type が internal.init", data: env("internal.init", {}), kind: "unknown" },
    {
      name: "snapshot の letters が無い",
      data: env("snapshot", omit(snapshotPayload, "letters")),
      kind: "malformed",
    },
    {
      name: "events の要素の seq が 0",
      data: env("events", { clock, events: [{ seq: 0, t: 0, type: "phase.changed" }] }),
      kind: "malformed",
    },
    {
      name: 'ack の ok が "true"',
      data: env("ack", { cmdId: "c-1", ok: "true", t: 0 }),
      kind: "malformed",
    },
    {
      name: "error の fatal が無い",
      data: env("error", { code: "x", message: "m" }),
      kind: "malformed",
    },
    {
      name: "pong の simTime が文字列",
      data: env("pong", { clientTime: 1, simTime: "2" }),
      kind: "malformed",
    },
  ];
  it.each(cases)("CD-3: $name → $kind", ({ data, kind }) => {
    const result = decodeServerMessage(data);
    expect(result.kind).toBe(kind);
    if (result.kind === "unknown") {
      expect(result.seq).toBe(1);
    }
  });

  it("CD-4: 知らないフィールドがあっても message", () => {
    const result = decodeServerMessage(
      env("ack", { ...minimal["ack"], future: { x: 1 } }, { extra: "field" }),
    );
    expect(result.kind).toBe("message");
    if (result.kind === "message" && result.message.type === "ack") {
      expect(result.message.payload.cmdId).toBe("c-1");
      expect((result.message.payload as unknown as Record<string, unknown>)["future"]).toEqual({
        x: 1,
      });
    }
  });
});
