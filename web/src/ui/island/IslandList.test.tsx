// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";

import type { Snapshot } from "../../protocol/index.ts";
import { gameStore } from "../../state/index.ts";
import type { World } from "../../state/index.ts";
import { island, makeSnapshot, portLetter } from "../hud/testing/world.ts";
import { IslandList } from "./IslandList.tsx";
import { IslandPanel } from "./IslandPanel.tsx";

beforeEach(() => {
  gameStore.getState().reset();
});

afterEach(() => {
  cleanup();
  gameStore.getState().reset();
});

function load(snapshot: Snapshot): World {
  act(() => {
    gameStore.getState().applySnapshot(snapshot, 0);
  });
  const world = gameStore.getState().world;
  if (world === null) throw new Error("world が無い");
  return world;
}

function card(id: number): HTMLElement {
  const el = document.querySelector<HTMLElement>(`.island-list__card[data-island="${String(id)}"]`);
  if (el === null) throw new Error(`島 ${String(id)} のカードが無い`);
  return el;
}

describe("IslandList", () => {
  it("IL-1 島の色・名前・港の手紙の数・満足度を stage の順に出す", () => {
    const world = load(
      makeSnapshot({
        letters: [portLetter(1, 1, 2), portLetter(2, 1, 3), portLetter(3, 2, 1)],
        islandsState: [
          { id: 1, satisfaction: 72, lastDeliveredAt: null },
          { id: 2, satisfaction: 60, lastDeliveredAt: null },
        ],
      }),
    );
    render(<IslandList world={world} />);
    const list = screen.getByRole("list", { name: "島を選ぶ" });
    const names = within(list)
      .getAllByRole("button")
      .map((b) => b.querySelector(".island-list__name")?.textContent);
    expect(names).toEqual(["一の島", "二の島", "三の島"]);

    expect(card(1).querySelector(".island-list__letters")?.textContent).toBe("港の手紙 2 通");
    expect(card(1).querySelector(".island-list__satisfaction")?.textContent).toBe("満足度 72");
    expect(card(2).querySelector(".island-list__letters")?.textContent).toBe("港の手紙 1 通");
    // 満足度が届いていない島は「—」。
    expect(card(3).querySelector(".island-list__letters")?.textContent).toBe("港の手紙 0 通");
    expect(card(3).querySelector(".island-list__satisfaction")?.textContent).toBe("満足度 —");
    // 島の色の丸。
    const dot = card(1).querySelector<HTMLElement>(".island-list__dot");
    expect(dot?.getAttribute("aria-hidden")).toBe("true");
    expect(dot?.style.backgroundColor).not.toBe("");
  });

  it("IL-2 押すとその島が選ばれる（ストアの selectIsland）", () => {
    const world = load(makeSnapshot());
    render(<IslandList world={world} />);
    expect(gameStore.getState().selection.islandId).toBeNull();
    fireEvent.click(card(2));
    expect(gameStore.getState().selection.islandId).toBe(2);
  });

  it("IL-3 島が無ければ何も出さない", () => {
    const base = makeSnapshot();
    const world = load({ ...base, stage: { ...base.stage, islands: [], routes: [] } });
    const { container } = render(<IslandList world={world} />);
    expect(container.innerHTML).toBe("");
  });

  it("IL-5 6 島（Stage 3）でも、1 枚を 1 行（色の丸・名前・数）にして情報を減らさずに出す", () => {
    const base = makeSnapshot();
    const names = ["大島", "利島", "三宅島", "御蔵島", "八丈島", "青ヶ島"];
    const islands = names.map((name, i) => island(i + 1, name, "#123456", 100 * i, 100));
    const world = load({
      ...base,
      stage: { ...base.stage, islands, routes: [] },
      routesState: [],
      letters: [portLetter(1, 6, 1), portLetter(2, 6, 2)],
      islandsState: islands.map((is) => ({ id: is.id, satisfaction: 60, lastDeliveredAt: null })),
    });
    render(<IslandList world={world} />);
    const list = screen.getByRole("list", { name: "島を選ぶ" });
    expect(within(list).getAllByRole("button")).toHaveLength(6);
    names.forEach((name, i) => {
      const el = card(i + 1);
      // 1 行の並び: 色の丸・名前・数（港の手紙・満足度）。
      expect(Array.from(el.children).map((c) => c.className)).toEqual([
        "island-list__dot",
        "island-list__name",
        "island-list__stats",
      ]);
      expect(el.querySelector(".island-list__name")?.textContent).toBe(name);
      expect(el.querySelector(".island-list__satisfaction")?.textContent).toBe("満足度 60");
    });
    expect(card(6).querySelector(".island-list__letters")?.textContent).toBe("港の手紙 2 通");
    expect(card(5).querySelector(".island-list__letters")?.textContent).toBe("港の手紙 0 通");
  });

  it("IL-4 島の詳細: 何も選んでいないときは案内と一覧、押すと詳細に替わる", () => {
    load(makeSnapshot());
    render(<IslandPanel />);
    expect(screen.getByText("マップの島を選ぶと、港の手紙と依頼がここに出ます。")).toBeDefined();
    expect(screen.getByRole("list", { name: "島を選ぶ" })).toBeDefined();
    fireEvent.click(card(1));
    expect(screen.queryByRole("list", { name: "島を選ぶ" })).toBeNull();
    expect(screen.getByRole("heading", { level: 2 }).textContent).toBe("一の島");
  });
});
