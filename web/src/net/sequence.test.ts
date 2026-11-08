import { describe, expect, it } from "vitest";

import { acceptEvents, checkEnvelopeSeq } from "./sequence.ts";

describe("checkEnvelopeSeq", () => {
  it.each([
    { expected: null, seq: 7, verdict: "ok", next: 8 },
    { expected: 8, seq: 8, verdict: "ok", next: 9 },
    { expected: 8, seq: 5, verdict: "duplicate", next: 8 },
    { expected: 8, seq: 10, verdict: "gap", next: 11 },
  ] as const)("SQ-1: ($expected, $seq) → $verdict・$next", ({ expected, seq, verdict, next }) => {
    expect(checkEnvelopeSeq(expected, seq)).toEqual({ verdict, expected: next });
  });
});

describe("acceptEvents", () => {
  const ev = (...seqs: number[]) => Object.freeze(seqs.map((seq) => Object.freeze({ seq })));

  it("SQ-2: 重複を読み飛ばし、抜けの後ろを捨てる。引数を変えない", () => {
    const a = ev(2, 3, 4, 5);
    expect(acceptEvents(3, a)).toEqual({
      accepted: [{ seq: 4 }, { seq: 5 }],
      lastSeq: 5,
      gap: false,
    });
    expect(a.map((e) => e.seq)).toEqual([2, 3, 4, 5]);

    const b = ev(4, 6, 7);
    expect(acceptEvents(3, b)).toEqual({ accepted: [{ seq: 4 }], lastSeq: 4, gap: true });

    const c = ev();
    const r = acceptEvents(3, c);
    expect(r).toEqual({ accepted: [], lastSeq: 3, gap: false });
    expect(r.accepted).not.toBe(c);
  });
});
