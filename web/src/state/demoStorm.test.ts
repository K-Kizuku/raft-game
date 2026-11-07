// snapshot.stage.demoStorm を世界の状態に写す（T-305b・プロトコル §5.1）。
import { describe, expect, it } from "vitest";

import type { DemoStormView } from "../protocol/index.ts";
import { applyEvents, worldFromSnapshot } from "./applyEvent.ts";
import { loadMiniRound } from "./fixtures/loadRecord.ts";

const DEMO_STORM: DemoStormView = {
  routeIds: ["1-2"],
  flavor: {
    text: "海が荒れています",
    provenance: "derived",
    source: {
      dataset: "fixture-2026",
      region: "星砂地域",
      field: "DISASTER_LAND_CONSERVATION",
      quote: "引用",
    },
  },
};

describe("stage.demoStorm", () => {
  it("WS-1 demoStorm のある snapshot は world.stage.demoStorm に同じ値が入り、イベントで変わらない", () => {
    const { snapshot, events } = loadMiniRound();
    const withStorm = { ...snapshot, stage: { ...snapshot.stage, demoStorm: DEMO_STORM } };
    const world = worldFromSnapshot(withStorm, 0);
    expect(world.stage.demoStorm).toEqual(DEMO_STORM);
    expect(world.stage.demoStorm).toBe(DEMO_STORM);

    const after = applyEvents(world, events);
    expect(after.stage.demoStorm).toBe(DEMO_STORM);
  });

  it("WS-1 demoStorm のない snapshot はキーを作らない", () => {
    const { snapshot, events } = loadMiniRound();
    const world = worldFromSnapshot(snapshot, 0);
    expect("demoStorm" in world.stage).toBe(false);
    expect("demoStorm" in applyEvents(world, events).stage).toBe(false);
  });
});
