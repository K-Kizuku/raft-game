// @vitest-environment jsdom
import { act, cleanup, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { Island, Snapshot } from "../protocol/index.ts";
import { gameStore } from "../state/index.ts";
import { setShowRaftTerms } from "../ui/terms/preference.ts";
import { makeSnapshot } from "./testing/snapshot.ts";
import { PartitionLegend } from "./PartitionLegend.tsx";

let nowMs = 1000;

beforeEach(() => {
  gameStore.getState().reset();
  nowMs = 1000;
  vi.useFakeTimers({ toFake: ["setInterval", "clearInterval", "setTimeout", "clearTimeout"] });
  vi.spyOn(performance, "now").mockImplementation(() => nowMs);
});

afterEach(() => {
  cleanup();
  gameStore.getState().reset();
  vi.useRealTimers();
  vi.restoreAllMocks();
  setShowRaftTerms(false);
});

const NAMES = ["北島", "東島", "南島", "西島", "中島", "沖島"];

function island(id: number): Island {
  return {
    id,
    name: NAMES[id - 1] ?? `島${String(id)}`,
    municipality: "テスト町",
    layout: { x: id * 100, y: (id % 2) * 100 },
    population: { value: 100, asOf: "令和8年4月1日", provenance: "creative", source: "test" },
    color: `hsl(${String(id * 60)} 50% 50%)`,
  };
}

// 6 島の環（1-2-3-4-5-6-1）。blocked の航路は 20000 まで止まる。
function sixIslands(blocked: readonly string[]): Snapshot {
  const ids = [1, 2, 3, 4, 5, 6];
  const routes = ids.map((a) => {
    const b = (a % 6) + 1;
    return {
      id: `${String(a)}-${String(b)}`,
      a,
      b,
      travelMs: 4000,
      provenance: "creative" as const,
    };
  });
  return makeSnapshot({
    stage: { title: "六島", islands: ids.map(island), routes, resources: [] },
    routesState: routes.map((r) => ({
      id: r.id,
      blockedUntil: blocked.includes(r.id) ? 20000 : null,
    })),
    nodes: [],
  });
}

function load(snapshot: Snapshot): void {
  act(() => {
    gameStore.getState().applySnapshot(snapshot, performance.now());
  });
}

function rows(): HTMLElement[] {
  return within(screen.getByRole("region", { name: "島のつながり" })).getAllByRole("listitem");
}

describe("PartitionLegend", () => {
  it("PL-1 分かれていない世界では何も描かない", () => {
    const view = render(<PartitionLegend />);
    expect(view.container.innerHTML).toBe("");
    load(sixIslands([]));
    expect(screen.queryByRole("region", { name: "島のつながり" })).toBeNull();
    // 1 か所だけ止まっても迂回できる。
    load(sixIslands(["1-2"]));
    expect(screen.queryByRole("region", { name: "島のつながり" })).toBeNull();
  });

  it("PL-2 分かれた世界では、見出し・グループごとの行・島の名前・島の数・過半数の印を出す", () => {
    render(<PartitionLegend />);
    // 1-2 と 3-4 が止まる → A {1,4,5,6}（4/6 過半数）・B {2,3}（2/6）。
    load(sixIslands(["1-2", "3-4"]));
    expect(screen.getByRole("heading", { level: 2 }).textContent).toBe("嵐で島が分かれています");
    const [a, b] = rows();
    expect(rows()).toHaveLength(2);
    expect(a?.textContent).toContain("A");
    expect(a?.textContent).toContain("北島・西島・中島・沖島");
    expect(a?.textContent).toContain("4/6 島");
    expect(a?.textContent).toContain("過半数の島がつながっている");
    expect(b?.textContent).toContain("B");
    expect(b?.textContent).toContain("東島・南島");
    expect(b?.textContent).toContain("2/6 島");
    expect(b?.textContent).not.toContain("過半数");
  });

  it("PL-2 3 対 3 ならどの行にも過半数の印が無い", () => {
    render(<PartitionLegend />);
    load(sixIslands(["3-4", "6-1"]));
    expect(rows().map((r) => r.textContent?.includes("3/6 島"))).toEqual([true, true]);
    for (const row of rows()) expect(row.textContent).not.toContain("過半数");
  });

  it("PL-2 推定時刻が嵐の終わりを過ぎたら消える", () => {
    render(<PartitionLegend />);
    load(sixIslands(["1-2", "3-4"]));
    expect(screen.getByRole("region", { name: "島のつながり" })).toBeTruthy();
    nowMs += 19000; // 推定時刻 0 → 19000（嵐の終わり 20000 の前）
    act(() => {
      vi.advanceTimersByTime(250);
    });
    expect(screen.getByRole("region", { name: "島のつながり" })).toBeTruthy();
    nowMs += 1000; // 推定時刻 20000
    act(() => {
      vi.advanceTimersByTime(250);
    });
    expect(screen.queryByRole("region", { name: "島のつながり" })).toBeNull();
  });

  it("PL-3 用語の併記をオンにすると、見出しに Network Partition、過半数の行に Quorum を添える", () => {
    setShowRaftTerms(true);
    render(<PartitionLegend />);
    load(sixIslands(["1-2", "3-4"]));
    expect(screen.getByRole("heading", { level: 2 }).textContent).toContain(
      "嵐（Network Partition）で島が分かれています",
    );
    const [a, b] = rows();
    expect(a?.textContent).toContain("過半数（Quorum）の島がつながっている");
    expect(b?.textContent).not.toContain("Quorum");
  });
});
