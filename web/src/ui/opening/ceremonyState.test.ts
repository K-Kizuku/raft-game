import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { isCeremonyActive, openingCeremonyState } from "./ceremonyState.ts";
import type { CeremonySnapshot } from "./ceremonyState.ts";
import type { OpeningResult } from "./openingView.ts";

beforeEach(() => {
  openingCeremonyState.reset();
});

afterEach(() => {
  openingCeremonyState.reset();
});

describe("openingCeremonyState", () => {
  it("CS-1 begin・finish・reset と購読者への知らせ", () => {
    const first = openingCeremonyState.getSnapshot();
    expect(first.showingKey).toBeNull();
    expect(first.doneKeys.size).toBe(0);
    expect(openingCeremonyState.getSnapshot()).toBe(first);

    const listener = vi.fn();
    const unsubscribe = openingCeremonyState.subscribe(listener);

    openingCeremonyState.begin("a");
    expect(openingCeremonyState.getSnapshot().showingKey).toBe("a");
    expect(listener).toHaveBeenCalledTimes(1);
    const showing = openingCeremonyState.getSnapshot();
    expect(showing).not.toBe(first);

    openingCeremonyState.begin("a");
    expect(listener).toHaveBeenCalledTimes(1);
    expect(openingCeremonyState.getSnapshot()).toBe(showing);

    openingCeremonyState.finish("a");
    const done = openingCeremonyState.getSnapshot();
    expect(done.showingKey).toBeNull();
    expect(done.doneKeys.has("a")).toBe(true);
    expect(listener).toHaveBeenCalledTimes(2);

    openingCeremonyState.finish("a");
    expect(listener).toHaveBeenCalledTimes(2);
    expect(openingCeremonyState.getSnapshot()).toBe(done);

    openingCeremonyState.reset();
    const reset = openingCeremonyState.getSnapshot();
    expect(reset.showingKey).toBeNull();
    expect(reset.doneKeys.size).toBe(0);
    expect(listener).toHaveBeenCalledTimes(3);

    unsubscribe();
    openingCeremonyState.begin("b");
    expect(listener).toHaveBeenCalledTimes(3);
  });

  it("CS-1 別の鍵の finish では showingKey を残す", () => {
    openingCeremonyState.begin("a");
    openingCeremonyState.finish("b");
    const snapshot = openingCeremonyState.getSnapshot();
    expect(snapshot.showingKey).toBe("a");
    expect([...snapshot.doneKeys]).toEqual(["b"]);
  });
});

function result(key: string): OpeningResult {
  return { key, leaderId: 1, term: 2, islandName: "一の島", color: null, changes: [] };
}

function snapshot(showingKey: string | null, done: string[]): CeremonySnapshot {
  return { showingKey, doneKeys: new Set(done) };
}

describe("isCeremonyActive", () => {
  it.each([
    { name: "結果なし・何もない", r: null, s: snapshot(null, []), want: false },
    { name: "結果 a・何もない", r: result("a"), s: snapshot(null, []), want: true },
    { name: "結果 a・done に a", r: result("a"), s: snapshot(null, ["a"]), want: false },
    { name: "結果なし・showing b", r: null, s: snapshot("b", []), want: true },
    { name: "結果 a・done に a・showing b", r: result("a"), s: snapshot("b", ["a"]), want: true },
  ])("CS-2 $name → $want", ({ r, s, want }) => {
    expect(isCeremonyActive(r, s)).toBe(want);
  });
});
