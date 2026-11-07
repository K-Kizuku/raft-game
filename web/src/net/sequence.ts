// seq の検査（T-402 / プロトコル §3・§6・§10.2）。純粋な関数。

export type EnvelopeSeqVerdict = "ok" | "duplicate" | "gap";

// checkEnvelopeSeq はエンベロープの seq を確かめる。
// expected: 次に来るはずの seq。null は「この接続でまだ何も受けていない」。
export function checkEnvelopeSeq(
  expected: number | null,
  seq: number,
): { verdict: EnvelopeSeqVerdict; expected: number } {
  if (expected === null || seq === expected) {
    return { verdict: "ok", expected: seq + 1 };
  }
  if (seq < expected) {
    return { verdict: "duplicate", expected };
  }
  return { verdict: "gap", expected: seq + 1 };
}

// acceptEvents は lastSeq からつながるイベントだけを受け入れる。
// 重複（seq <= lastSeq）は読み飛ばし、抜けが来たらそこから後ろをすべて捨てる。
export function acceptEvents<E extends { seq: number }>(
  lastSeq: number,
  events: readonly E[],
): { accepted: E[]; lastSeq: number; gap: boolean } {
  const accepted: E[] = [];
  let last = lastSeq;
  for (const event of events) {
    if (event.seq <= last) {
      continue;
    }
    if (event.seq !== last + 1) {
      return { accepted, lastSeq: last, gap: true };
    }
    accepted.push(event);
    last = event.seq;
  }
  return { accepted, lastSeq: last, gap: false };
}
