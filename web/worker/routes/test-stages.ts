// テストで使うステージの一覧の 1 行（T-311）。テストのファイルだけが import する。

import type { StageSummary } from "./stages.ts";

export function stageFixture(overrides: Partial<StageSummary> & { id: string }): StageSummary {
  return {
    title: overrides.id,
    region: "r",
    islands: 3,
    ferries: 1,
    durationMs: 180000,
    unlock: null,
    intro: { text: "紹介", provenance: "creative", source: null },
    learning: [],
    demo: false,
    ...overrides,
  };
}
