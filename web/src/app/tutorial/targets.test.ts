// @vitest-environment jsdom
import { afterEach, describe, expect, it } from "vitest";

import { findTargets } from "./targets.ts";
import type { TargetSpec } from "./script.ts";
import { FAR, LEADER, RELAY, stage1World } from "./testing/stage1World.ts";

const ISLANDS = { leader: LEADER, relay: RELAY, far: FAR };

function mount(html: string): HTMLElement {
  const root = document.createElement("div");
  root.innerHTML = html;
  document.body.append(root);
  return root;
}

afterEach(() => {
  document.body.innerHTML = "";
});

describe("findTargets", () => {
  const ferrySpecs: readonly TargetSpec[] = [
    { kind: "selector", selector: ".ferry-panel__port" },
    { kind: "selector", selector: ".ferry-panel__cargo" },
  ];

  it("TG-1 セレクターで探し、無いものは飛ばし、fallback を使う", () => {
    const both = mount('<ul class="ferry-panel__port"></ul><ul class="ferry-panel__cargo"></ul>');
    expect(findTargets(both, ferrySpecs, ISLANDS, stage1World())).toEqual([
      both.querySelector(".ferry-panel__port"),
      both.querySelector(".ferry-panel__cargo"),
    ]);

    const one = mount('<ul class="ferry-panel__cargo"></ul>');
    expect(findTargets(one, ferrySpecs, ISLANDS, stage1World())).toEqual([
      one.querySelector(".ferry-panel__cargo"),
    ]);

    const fallback = mount('<aside class="game-island"></aside>');
    expect(
      findTargets(
        fallback,
        [{ kind: "selector", selector: ".island-requests", fallback: ".game-island" }],
        ISLANDS,
        stage1World(),
      ),
    ).toEqual([fallback.querySelector(".game-island")]);

    expect(findTargets(mount(""), ferrySpecs, ISLANDS, stage1World())).toEqual([]);
  });

  it("TG-2 マップの島のボタンを名前で探す", () => {
    const root = mount(
      '<nav class="map-view__islands"><button>小豆島 <span>👑</span></button><button>豊島</button><button>直島</button></nav>',
    );
    const buttons = root.querySelectorAll("button");
    expect(
      findTargets(root, [{ kind: "island", island: "leader" }], ISLANDS, stage1World()),
    ).toEqual([buttons[0]]);
    expect(findTargets(root, [{ kind: "island", island: "far" }], ISLANDS, stage1World())).toEqual([
      buttons[2],
    ]);

    const empty = mount('<nav class="map-view__islands"></nav>');
    expect(
      findTargets(
        empty,
        [
          { kind: "island", island: "leader" },
          { kind: "island", island: "far" },
        ],
        ISLANDS,
        stage1World(),
      ),
    ).toEqual([]);
  });
});
