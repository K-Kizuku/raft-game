// リザルトの表示のための純粋な関数（T-408・FR-SCORE-01〜03・FR-UI-06・プロトコル §5.5）。
// スコア・★・統計・文章はサーバーの round.result をそのまま使い、ここでは計算し直さない（ADR-0009）。
// Go の nil の map・slice は JSON で null になるので、型どおりに届かない値をここで受け止める。
// 表示の計算は整数の演算だけで書く。
import type { FinalLog, RoundResult, StoryItem, TermRange } from "../../protocol/index.ts";
import { letterKind } from "../../ui/letters/letterKinds.ts";
import { letterRaftTerm } from "../../ui/terms/glossary.ts";

export type Stars = 0 | 1 | 2 | 3;
export type TechnicalView = Readonly<{
  lettersDelivered: Readonly<Record<string, number>>;
  leaderChanges: number;
  avgCommitLatencyMs: number;
  longestSilenceMs: number;
  finalLogs: readonly FinalLog[];
}>;
export type ResultView = Readonly<{
  total: number;
  breakdown: Readonly<{ requests: number; expired: number; ferryCost: number; fairness: number }>;
  stars: Stars;
  succeeded: number;
  rejected: number;
  expired: number;
  technical: TechnicalView | null; // T-208 の値が無い（null で届いた）とき null
  story: readonly StoryItem[];
}>;

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function finiteOrZero(value: unknown): number {
  return typeof value === "number" && Number.isFinite(value) ? value : 0;
}

function field(record: unknown, key: string): unknown {
  return isRecord(record) ? record[key] : undefined;
}

export function clampStars(value: unknown): Stars {
  if (typeof value !== "number" || !Number.isFinite(value)) return 0;
  const whole = Math.trunc(value);
  if (whole <= 0) return 0;
  if (whole >= 3) return 3;
  return whole === 1 ? 1 : 2;
}

// ★2 以上で花火大会（FR-SCORE-03）。要件の決まりで、ステージで変える数値ではない。
export const FIREWORKS_MIN_STARS = 2;

export function hasFireworks(stars: Stars): boolean {
  return stars >= FIREWORKS_MIN_STARS;
}

function normalizeDelivered(value: Record<string, unknown>): Record<string, number> {
  const delivered: Record<string, number> = {};
  for (const [label, count] of Object.entries(value)) {
    if (typeof count === "number" && Number.isFinite(count)) {
      delivered[label] = count;
    }
  }
  return delivered;
}

function normalizeFinalLog(value: Record<string, unknown>): FinalLog {
  const terms = value["terms"];
  return {
    id: finiteOrZero(value["id"]),
    lastIndex: finiteOrZero(value["lastIndex"]),
    commit: finiteOrZero(value["commit"]),
    // 要素の形は logBands が確かめる（壊れた要素はそこで描かない）。
    terms: Array.isArray(terms) ? (terms.filter((t) => Array.isArray(t)) as TermRange[]) : [],
  };
}

function normalizeTechnical(stats: unknown): TechnicalView | null {
  const delivered = field(stats, "lettersDelivered");
  const logs = field(stats, "finalLogs");
  if (!isRecord(delivered) && !Array.isArray(logs)) return null;
  const finalLogs = Array.isArray(logs)
    ? logs
        .filter((log): log is Record<string, unknown> => isRecord(log))
        .map(normalizeFinalLog)
        .sort((a, b) => a.id - b.id)
    : [];
  return {
    lettersDelivered: isRecord(delivered) ? normalizeDelivered(delivered) : {},
    leaderChanges: finiteOrZero(field(stats, "leaderChanges")),
    avgCommitLatencyMs: finiteOrZero(field(stats, "avgCommitLatencyMs")),
    longestSilenceMs: finiteOrZero(field(stats, "longestSilenceMs")),
    finalLogs,
  };
}

function normalizeStory(story: unknown): StoryItem[] {
  if (!Array.isArray(story)) return [];
  const items: StoryItem[] = [];
  for (const item of story) {
    const text = field(item, "text");
    if (typeof text !== "string") continue;
    const concepts = field(item, "concepts");
    items.push({
      t: finiteOrZero(field(item, "t")),
      text,
      concepts: Array.isArray(concepts)
        ? concepts.filter((c): c is string => typeof c === "string")
        : [],
    });
  }
  return items;
}

export function normalizeResult(result: RoundResult): ResultView {
  const raw: unknown = result;
  const score = field(raw, "score");
  const breakdown = field(score, "breakdown");
  const statsValue = field(raw, "stats");
  const stats = isRecord(statsValue) ? statsValue : {};
  return {
    total: finiteOrZero(field(score, "total")),
    breakdown: {
      requests: finiteOrZero(field(breakdown, "requests")),
      expired: finiteOrZero(field(breakdown, "expired")),
      ferryCost: finiteOrZero(field(breakdown, "ferryCost")),
      fairness: finiteOrZero(field(breakdown, "fairness")),
    },
    stars: clampStars(field(raw, "stars")),
    succeeded: finiteOrZero(stats["succeeded"]),
    rejected: finiteOrZero(stats["rejected"]),
    expired: finiteOrZero(stats["expired"]),
    technical: normalizeTechnical(stats),
    story: normalizeStory(field(raw, "story")),
  };
}

const MINUS = "−";

// 例: (1300, true) →「+1,300」、(-100, true) →「−100」、(0, true) →「0」。
export function formatPoints(value: number, signed: boolean): string {
  const digits = Math.abs(value).toLocaleString("ja-JP");
  if (value < 0) return `${MINUS}${digits}`;
  if (value > 0 && signed) return `+${digits}`;
  return digits;
}

// 0.1 秒に切り捨てて「s.d 秒」。余りを引いてから割るので、商はいつも整数になる。
export function formatSeconds(ms: number): string {
  const value = ms < 0 ? 0 : ms;
  const tenths = (value - (value % 100)) / 100;
  const decimal = tenths % 10;
  const seconds = (tenths - decimal) / 10;
  return `${seconds}.${decimal} 秒`;
}

export function formatSecondsOrDash(ms: number): string {
  return ms <= 0 ? "—" : formatSeconds(ms);
}

// ゲームデザイン §2.1 の順。
const LETTER_ORDER: readonly string[] = [
  "heartbeat",
  "heartbeat_resp",
  "append",
  "append_probe",
  "append_resp",
  "append_reject",
  "prevote",
  "prevote_resp",
  "vote",
  "vote_resp",
  "request",
  "other",
];

function compareCodePoints(a: string, b: string): number {
  const x = Array.from(a);
  const y = Array.from(b);
  const n = Math.min(x.length, y.length);
  for (let i = 0; i < n; i++) {
    const d = (x[i]?.codePointAt(0) ?? 0) - (y[i]?.codePointAt(0) ?? 0);
    if (d !== 0) return d;
  }
  return x.length - y.length;
}

export type DeliveredRow = Readonly<{
  label: string;
  name: string;
  raft: string | null;
  count: number;
}>;

export function deliveredRows(delivered: Readonly<Record<string, number>>): {
  rows: DeliveredRow[];
  total: number;
} {
  const labels = Object.keys(delivered).filter((label) => (delivered[label] ?? 0) > 0);
  const known = LETTER_ORDER.filter((label) => labels.includes(label));
  const unknown = labels.filter((label) => !LETTER_ORDER.includes(label)).sort(compareCodePoints);
  const rows = [...known, ...unknown].map((label) => ({
    label,
    name: LETTER_ORDER.includes(label) ? letterKind(label).name : `その他（${label}）`,
    raft: letterRaftTerm(label),
    count: delivered[label] ?? 0,
  }));
  return { rows, total: rows.reduce((sum, row) => sum + row.count, 0) };
}

// 数が 3 つで 1 <= first <= last の要素だけを、届いた順のまま返す。
export function termRanges(log: FinalLog): TermRange[] {
  const ranges: TermRange[] = [];
  for (const range of log.terms as readonly unknown[]) {
    if (!Array.isArray(range) || range.length !== 3) continue;
    const [term, first, last] = range as unknown[];
    if (!Number.isInteger(term) || !Number.isInteger(first) || !Number.isInteger(last)) continue;
    const t = term as number;
    const f = first as number;
    const l = last as number;
    if (f < 1 || f > l) continue;
    ranges.push([t, f, l]);
  }
  return ranges;
}

export type LogSegment = Readonly<{
  term: number;
  first: number;
  last: number;
  committed: boolean;
  size: number;
}>;

export function logScale(logs: readonly FinalLog[]): number {
  let scale = 0;
  for (const log of logs) {
    scale = Math.max(scale, finiteOrZero(log.lastIndex), finiteOrZero(log.commit));
    for (const range of log.terms as readonly unknown[]) {
      if (Array.isArray(range)) scale = Math.max(scale, finiteOrZero(range[2]));
    }
  }
  return scale;
}

export function logBands(
  log: FinalLog,
  scale: number,
): { offset: number; segments: LogSegment[]; padding: number } {
  const ranges = termRanges(log);
  const firstRange = ranges[0];
  const lastRange = ranges[ranges.length - 1];
  if (firstRange === undefined || lastRange === undefined) {
    return { offset: 0, segments: [], padding: scale };
  }
  const commit = log.commit;
  const segments: LogSegment[] = [];
  for (const [term, first, last] of ranges) {
    const committedLast = Math.min(last, commit);
    if (first <= committedLast) {
      segments.push({
        term,
        first,
        last: committedLast,
        committed: true,
        size: committedLast - first + 1,
      });
    }
    const pendingFirst = Math.max(first, commit + 1);
    if (pendingFirst <= last) {
      segments.push({
        term,
        first: pendingFirst,
        last,
        committed: false,
        size: last - pendingFirst + 1,
      });
    }
  }
  return {
    offset: firstRange[1] - 1,
    segments,
    padding: Math.max(0, scale - lastRange[2]),
  };
}

// Term ごとの帯の色（8 色をくり返す）。
export const TERM_COLORS: readonly string[] = [
  "#2563EB",
  "#DC2626",
  "#16A34A",
  "#CA8A04",
  "#9333EA",
  "#0891B2",
  "#EA580C",
  "#DB2777",
];

export function termColor(term: number): string {
  const n = TERM_COLORS.length;
  return TERM_COLORS[((term % n) + n) % n] ?? "#6B7280";
}
