// パーセンタイル（最近傍の順位）。Durable Object の ping と、測定のスクリプト（scripts/lib.ts）が使う。

/** p パーセンタイル（0〜100、最近傍の順位）。空なら null */
export function percentile(values: readonly number[], p: number): number | null {
  if (values.length === 0) return null;
  const sorted = [...values].sort((a, b) => a - b);
  const rank = Math.min(Math.max(Math.ceil((p / 100) * sorted.length), 1), sorted.length);
  return sorted[rank - 1] ?? null;
}

export interface Summary {
  n: number;
  p50: number | null;
  p95: number | null;
  p99: number | null;
  max: number | null;
}

export function summarize(values: readonly number[]): Summary {
  return {
    n: values.length,
    p50: percentile(values, 50),
    p95: percentile(values, 95),
    p99: percentile(values, 99),
    max: percentile(values, 100),
  };
}
