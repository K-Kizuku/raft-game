// @vitest-environment jsdom
// デモの嵐の紹介の文（T-305b）。
import { act, cleanup, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";

import type { ServerEvent, Snapshot } from "../../protocol/index.ts";
import { gameStore } from "../../state/index.ts";
import { GameCommandProvider } from "../commands/GameCommands.tsx";
import { DemoKeys } from "./DemoKeys.tsx";
import { DemoStormFlavor } from "./DemoStormFlavor.tsx";
import { DEMO_STORM_FLAVOR, makeDemoStormSnapshot, makeSnapshot } from "./testing/world.ts";

const DERIVED_LABEL = "国土交通省『離島に関するデータ』をもとに作成";

beforeEach(() => {
  gameStore.getState().reset();
});

afterEach(() => {
  cleanup();
  gameStore.getState().reset();
  document.body.replaceChildren();
});

function load(snapshot: Snapshot): void {
  act(() => {
    gameStore.getState().applySnapshot(snapshot, 0);
  });
}

function apply(event: ServerEvent): void {
  const world = gameStore.getState().world;
  if (world === null) throw new Error("world is null");
  act(() => {
    gameStore.getState().applyEvents(
      {
        t: event.t,
        speed: world.clock.speed,
        paused: world.clock.paused,
        endsAt: world.clock.endsAt,
      },
      [event],
      0,
    );
  });
}

const BLOCKED: ServerEvent = {
  seq: 1,
  t: 30000,
  type: "route.blocked",
  routeIds: ["1-2"],
  until: 50000,
};
const UNBLOCKED: ServerEvent = {
  seq: 2,
  t: 50000,
  type: "route.unblocked",
  routeIds: ["1-2"],
  until: 50000,
};

function renderDemoKeys(): void {
  render(
    <GameCommandProvider send={null}>
      <DemoKeys />
    </GameCommandProvider>,
  );
}

describe("DemoStormFlavor", () => {
  it("DF-2 遮断の間だけ、嵐の知らせに文・引用・出典区分が出る", () => {
    load(makeDemoStormSnapshot());
    renderDemoKeys();
    expect(screen.queryByRole("region", { name: "嵐の知らせ" })).toBeNull();

    apply(BLOCKED);
    const region = screen.getByRole("region", { name: "嵐の知らせ" });
    expect(region.textContent).toContain(DEMO_STORM_FLAVOR.text);
    expect(region.textContent).toContain(`「${String(DEMO_STORM_FLAVOR.source?.quote)}」`);
    expect(region.textContent).toContain(DERIVED_LABEL);

    apply(UNBLOCKED);
    expect(screen.queryByRole("region", { name: "嵐の知らせ" })).toBeNull();
  });

  it("DF-2 引用がなければ引用の行を出さない", () => {
    const noQuote = {
      routeIds: ["1-2"],
      flavor: { text: "海が荒れています", provenance: "creative" as const, source: null },
    };
    load(makeDemoStormSnapshot(noQuote));
    const { container } = render(<DemoStormFlavor />);
    apply(BLOCKED);
    expect(screen.getByRole("region", { name: "嵐の知らせ" }).textContent).toContain(
      "海が荒れています",
    );
    expect(container.querySelector(".demo-storm-flavor-quote")).toBeNull();
  });

  it("DF-2 source があっても quote が null なら引用の行を出さない", () => {
    const nullQuote = {
      routeIds: ["1-2"],
      flavor: {
        ...DEMO_STORM_FLAVOR,
        source: { dataset: "fixture-2026", region: "デモ地域", field: "DISASTER", quote: null },
      },
    };
    load(makeDemoStormSnapshot(nullQuote));
    const { container } = render(<DemoStormFlavor />);
    apply(BLOCKED);
    expect(screen.getByRole("region", { name: "嵐の知らせ" })).toBeTruthy();
    expect(container.querySelector(".demo-storm-flavor-quote")).toBeNull();
  });

  it("DF-3 デモでないセッションでは、遮断中でも文が出ない", () => {
    const base = makeDemoStormSnapshot();
    load({ ...base, session: { ...base.session, demo: false } });
    renderDemoKeys();
    apply(BLOCKED);
    expect(screen.queryByRole("region", { name: "嵐の知らせ" })).toBeNull();
    expect(screen.queryByText(DEMO_STORM_FLAVOR.text)).toBeNull();
  });

  it("DF-3 demoStorm のないステージでは、デモのセッションでも文が出ない", () => {
    load(makeSnapshot());
    renderDemoKeys();
    apply(BLOCKED);
    expect(screen.queryByRole("region", { name: "嵐の知らせ" })).toBeNull();
  });
});
