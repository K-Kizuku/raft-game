import { describe, expect, it } from "vitest";

import type { StageCatalogEntry } from "../types.ts";
import type { StageProgress } from "./progress.ts";
import { lockStateOf, newlyUnlocked, wantsUnlockAll } from "./unlock.ts";

function entry(id: string, unlock: string | null): StageCatalogEntry {
  return {
    id,
    title: `${id} のタイトル`,
    region: "瀬戸内",
    islands: 3,
    ferries: 1,
    durationMs: 180000,
    demo: false,
    intro: { text: "紹介文", provenance: "creative", source: null },
    learning: [],
    unlock,
  };
}

const stage1 = entry("stage1", null);
const stage2 = entry("stage2", "stage1");
const stage3 = entry("stage3", "stage2");
const other = entry("other", "stage1");
const STAGES = [stage1, stage2, stage3, other];

function progressOf(bestStars: Record<string, number>): StageProgress {
  return { bestStars, persistent: true };
}

describe("lockStateOf", () => {
  it("UL-1 unlock が null なら解放済み、前のステージをクリアするまで未解放", () => {
    expect(lockStateOf(stage1, STAGES, progressOf({}), false)).toEqual({ locked: false });
    expect(lockStateOf(stage2, STAGES, progressOf({}), false)).toEqual({
      locked: true,
      requires: stage1,
    });
    expect(lockStateOf(stage2, STAGES, progressOf({ stage1: 1 }), false)).toEqual({
      locked: false,
    });
  });

  it("UL-1 unlock は 1 段の条件で、前のステージの前提はたどらない", () => {
    expect(lockStateOf(stage3, STAGES, progressOf({ stage2: 1 }), false)).toEqual({
      locked: false,
    });
    expect(lockStateOf(stage3, STAGES, progressOf({ stage1: 3 }), false)).toEqual({
      locked: true,
      requires: stage2,
    });
  });

  it("UL-1 unlock を省略した値は null と同じ", () => {
    const withoutUnlock: StageCatalogEntry = { ...stage2 };
    delete withoutUnlock.unlock;
    expect(lockStateOf(withoutUnlock, STAGES, progressOf({}), false)).toEqual({ locked: false });
  });

  it("UL-2 unlock が一覧に無ければ解放済み", () => {
    expect(lockStateOf(stage2, [stage2, stage3], progressOf({}), false)).toEqual({
      locked: false,
    });
  });

  it("UL-3 unlockAll ならすべて解放済み", () => {
    for (const stage of STAGES) {
      expect(lockStateOf(stage, STAGES, progressOf({}), true)).toEqual({ locked: false });
    }
  });
});

describe("newlyUnlocked", () => {
  it("UL-4 unlock がそのステージのものを一覧の順に返す", () => {
    expect(newlyUnlocked("stage1", STAGES)).toEqual([stage2, other]);
    expect(newlyUnlocked("stage1", [stage1, stage2, stage3])).toEqual([stage2]);
    expect(newlyUnlocked("stage3", STAGES)).toEqual([]);
  });
});

describe("wantsUnlockAll", () => {
  it("window が無ければ偽", () => {
    expect(wantsUnlockAll()).toBe(false);
  });
});
