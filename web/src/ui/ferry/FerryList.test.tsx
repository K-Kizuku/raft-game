// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";

import { gameStore } from "../../state/index.ts";
import type { FerryTab } from "./ferryView.ts";
import { FerryList } from "./FerryList.tsx";
import { FerryPanel } from "./FerryPanel.tsx";
import { makeFerrySnapshot } from "./testing/world.ts";

beforeEach(() => {
  gameStore.getState().reset();
});

afterEach(() => {
  cleanup();
  gameStore.getState().reset();
});

function tab(overrides: Partial<FerryTab>): FerryTab {
  return {
    id: "f1",
    name: "ごとう丸",
    selected: false,
    state: "docked",
    placeName: "一の島",
    etaMs: null,
    cargoCount: 0,
    capacity: 4,
    broken: false,
    ...overrides,
  };
}

function card(id: string): HTMLElement {
  const el = document.querySelector<HTMLElement>(`.ferry-list__card[data-ferry="${id}"]`);
  if (el === null) throw new Error(`船 ${id} のカードが無い`);
  return el;
}

describe("FerryList", () => {
  it("FL-1 名前・場所・積み荷を出す（文言は船のタブと同じ）", () => {
    render(
      <FerryList
        tabs={[
          tab({}),
          tab({
            id: "f2",
            name: "しまかぜ",
            state: "sailing",
            placeName: "三の島",
            etaMs: 2000,
            cargoCount: 1,
            broken: true,
          }),
          tab({ id: "f3", name: "なぞ丸", placeName: null }),
        ]}
      />,
    );
    const group = screen.getByRole("group", { name: "船を選ぶ" });
    // チュートリアルが指す className（DesignDoc §13.8）を外枠に残す。
    expect(group.classList.contains("ferry-panel__tabs")).toBe(true);
    const names = within(group)
      .getAllByRole("button")
      .map((b) => b.querySelector(".ferry-list__name")?.textContent);
    expect(names).toEqual(["⛴ ごとう丸", "⛴ しまかぜ", "⛴ なぞ丸"]);
    expect(card("f1").querySelector(".ferry-list__place")?.textContent).toBe("⚓ 一の島 停泊中");
    expect(card("f1").querySelector(".ferry-list__cargo")?.textContent).toBe("積み荷 0/4");
    expect(card("f2").querySelector(".ferry-list__place")?.textContent).toBe("→ 三の島 あと 0:02");
    expect(card("f2").querySelector(".ferry-list__cargo")?.textContent).toBe("積み荷 1/4");
    expect(card("f2").querySelector(".ferry-list__broken")?.textContent).toBe("🔧 故障中");
    expect(card("f1").querySelector(".ferry-list__broken")).toBeNull();
    expect(card("f3").querySelector(".ferry-list__place")?.textContent).toBe("停泊中");
    expect(card("f1").getAttribute("aria-pressed")).toBe("false");
  });

  it("FL-2 船が 0 隻なら「船がありません。」", () => {
    render(<FerryList tabs={[]} />);
    expect(screen.getByText("船がありません。")).toBeDefined();
    expect(screen.queryByRole("group")).toBeNull();
  });

  it("FL-3 押すとその船が選ばれる（ストアの selectFerry）", () => {
    act(() => {
      gameStore.getState().applySnapshot(makeFerrySnapshot(), 0);
    });
    render(<FerryList tabs={[tab({}), tab({ id: "f2", name: "しまかぜ" })]} />);
    expect(gameStore.getState().selection.ferryId).toBeNull();
    fireEvent.click(card("f2"));
    expect(gameStore.getState().selection.ferryId).toBe("f2");
  });

  it("FL-4 フェリー操作: 何も選んでいないときは一覧、押すと船のタブと操作に替わる", () => {
    act(() => {
      gameStore.getState().applySnapshot(makeFerrySnapshot(), 0);
    });
    render(<FerryPanel />);
    expect(document.querySelector(".ferry-list")).not.toBeNull();
    expect(document.querySelector(".ferry-panel__tab")).toBeNull();
    fireEvent.click(card("f1"));
    expect(gameStore.getState().selection.ferryId).toBe("f1");
    expect(document.querySelector(".ferry-list")).toBeNull();
    expect(document.querySelectorAll(".ferry-panel__tab")).toHaveLength(2);
  });
});
