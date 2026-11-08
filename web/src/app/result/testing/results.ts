// テスト用の RoundResult（T-408）。テストだけが import する（本番のコードから import しない）。
import type { RoundResult } from "../../../protocol/index.ts";

// プロトコル §5.5 の例と同じ値。
export const FULL_RESULT: RoundResult = {
  score: {
    total: 1520,
    breakdown: { requests: 1300, expired: -100, ferryCost: -180, fairness: 500 },
  },
  stars: 2,
  stats: {
    succeeded: 7,
    rejected: 2,
    expired: 2,
    lettersDelivered: { heartbeat: 40, append: 22, append_resp: 19, vote: 3, request: 9 },
    leaderChanges: 1,
    avgCommitLatencyMs: 18400,
    longestSilenceMs: 41000,
    finalLogs: [
      {
        id: 1,
        lastIndex: 24,
        commit: 24,
        terms: [
          [1, 1, 9],
          [3, 10, 24],
        ],
      },
    ],
  },
  story: [
    {
      t: 72000,
      text: "嵐で福江島が孤立しました。福江島は過半数と連絡が取れなくなったので、自分から代表を降りました。",
      concepts: ["CheckQuorum"],
    },
  ],
  inputLogAvailable: true,
};

// T-203 だけが入り T-208 がまだの形。Go の nil の map・slice が JSON で null になって届くので、
// 型（RoundResult）には合わない。そのため as unknown as RoundResult で作る。
export const SCORE_ONLY_RESULT = {
  score: { total: 396, breakdown: { requests: 100, expired: 0, ferryCost: -4, fairness: 300 } },
  stars: 1,
  stats: {
    succeeded: 1,
    rejected: 0,
    expired: 0,
    lettersDelivered: null,
    leaderChanges: 0,
    avgCommitLatencyMs: 0,
    longestSilenceMs: 0,
    finalLogs: null,
  },
  story: null,
  inputLogAvailable: false,
} as unknown as RoundResult;

// FULL_RESULT の一部を差し替えた新しい値（テストどうしで同じオブジェクトを共有しない）。
export function resultWith(overrides: Partial<RoundResult>): RoundResult {
  const copy = JSON.parse(JSON.stringify(FULL_RESULT)) as RoundResult;
  return { ...copy, ...overrides };
}
