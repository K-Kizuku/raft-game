// デモの嵐の紹介の文を出すかの判定（T-305b）。
import { describe, expect, it } from "vitest";

import type { Phase } from "../../protocol/index.ts";
import { applyEvent, worldFromSnapshot } from "../../state/index.ts";
import type { World } from "../../state/index.ts";
import { activeDemoStormFlavor } from "./demoStorm.ts";
import { makeDemoStormSnapshot, makeSnapshot } from "./testing/world.ts";

// 航路 1-2 と 2-3 があり、demoStorm が 1-2 を遮断する世界。
function stormWorld(phase: Phase = "sailing"): World {
  const base = makeDemoStormSnapshot(undefined, { phase });
  const snapshot = {
    ...base,
    stage: {
      ...base.stage,
      routes: [
        ...base.stage.routes,
        { id: "2-3", a: 2, b: 3, travelMs: 6000, provenance: "derived" as const },
      ],
    },
    routesState: [...base.routesState, { id: "2-3", blockedUntil: null }],
  };
  return worldFromSnapshot(snapshot, 0);
}

function block(world: World, routeIds: string[]): World {
  return applyEvent(world, {
    seq: world.lastEventSeq + 1,
    t: 30000,
    type: "route.blocked",
    routeIds,
    until: 50000,
  });
}

describe("activeDemoStormFlavor", () => {
  it("DF-1 航行中で demoStorm の航路が遮断中なら flavor（同じ参照）を返す", () => {
    const world = block(stormWorld(), ["1-2"]);
    const flavor = world.stage.demoStorm?.flavor;
    expect(flavor).toBeTruthy();
    expect(activeDemoStormFlavor(world)).toBe(flavor);
  });

  it("DF-1 world が null なら null", () => {
    expect(activeDemoStormFlavor(null)).toBeNull();
  });

  it("DF-1 demoStorm がないステージなら null", () => {
    const world = block(worldFromSnapshot(makeSnapshot(), 0), ["1-2"]);
    expect(activeDemoStormFlavor(world)).toBeNull();
  });

  it("DF-1 flavor が null なら null", () => {
    const world = block(
      worldFromSnapshot(makeDemoStormSnapshot({ routeIds: ["1-2"], flavor: null }), 0),
      ["1-2"],
    );
    expect(activeDemoStormFlavor(world)).toBeNull();
  });

  it("DF-1 遮断されていなければ null", () => {
    expect(activeDemoStormFlavor(stormWorld())).toBeNull();
  });

  it("DF-1 別の航路だけが遮断されていれば null", () => {
    expect(activeDemoStormFlavor(block(stormWorld(), ["2-3"]))).toBeNull();
  });

  it.each<Phase>(["intro", "ended"])("DF-1 phase が %s なら null", (phase) => {
    expect(activeDemoStormFlavor(block(stormWorld(phase), ["1-2"]))).toBeNull();
  });
});
