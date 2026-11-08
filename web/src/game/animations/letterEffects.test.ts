import { describe, expect, it } from "vitest";

import type { DropReason, Letter, ServerEvent } from "../../protocol/index.ts";
import { applyEvents, worldFromSnapshot } from "../../state/index.ts";
import type { LetterExit, UiSelection, World } from "../../state/index.ts";
import { buildMapScene } from "../model/scene.ts";
import type { MapScene } from "../model/scene.ts";
import { MAP_STYLE } from "../model/style.ts";
import type { Point } from "../model/viewport.ts";
import { COLORS, ferryLetter, makeSnapshot, portLetter } from "../testing/snapshot.ts";
import { createExitTracker, effectFrame, pileAnchor, planLetterEffects } from "./letterEffects.ts";
import type { LetterEffect } from "./letterEffects.ts";
import { LETTER_EFFECT_CAPTIONS, LETTER_EFFECT_STYLE } from "./style.ts";

const SIZE = { width: 800, height: 600 };
const NONE: UiSelection = { islandId: null, ferryId: null };
const EMPTY: ReadonlyMap<string, number> = new Map();

function worldWith(letters: Letter[]): World {
  return worldFromSnapshot(makeSnapshot({ letters }), 0);
}

function dropEvent(seq: number, t: number, letterId: number, reason: DropReason): ServerEvent {
  return { seq, t, type: "letter.dropped", letterId, reason };
}

function sceneOf(world: World, simNow = 0): MapScene {
  return buildMapScene(world, NONE, simNow, SIZE);
}

function centerOf(scene: MapScene, id: number): Point {
  const at = scene.islands.find((i) => i.id === id)?.at;
  if (at === undefined) throw new Error(`島 ${String(id)} が scene に無い`);
  return at;
}

// letters を置いた世界で、events を当てはめた後の letterExits を返す。
function exitsOf(letters: Letter[], events: ServerEvent[]): { world: World; exits: LetterExit[] } {
  const world = applyEvents(worldWith(letters), events);
  return { world, exits: [...world.letterExits] };
}

function onlyExit(letter: Letter, reason: DropReason, t = 0, seq = 101): LetterExit[] {
  return exitsOf([letter], [dropEvent(seq, t, letter.id, reason)]).exits;
}

function ctx(
  world: World,
  over: Partial<{
    now: number;
    simNow: number;
    reducedMotion: boolean;
    captionAt: ReadonlyMap<string, number>;
  }> = {},
) {
  return { world, now: 0, simNow: 0, reducedMotion: false, captionAt: EMPTY, ...over };
}

function effect(over: Partial<LetterEffect> = {}): LetterEffect {
  return {
    id: "1",
    kind: "drift",
    reason: "overflow",
    islandId: 1,
    to: 2,
    color: COLORS[2],
    request: false,
    caption: null,
    startedAt: 0,
    durationMs: LETTER_EFFECT_STYLE.driftMs,
    ...over,
  };
}

describe("LE-1 createExitTracker", () => {
  it("最初は過去の処分を返さず、その後は新しい処分（dropped）だけを古い順に返す", () => {
    const base = worldWith([
      portLetter(10, 1, 2),
      portLetter(11, 1, 3),
      portLetter(12, 2, 1),
      portLetter(13, 2, 3),
    ]);
    const w1 = applyEvents(base, [
      dropEvent(101, 0, 10, "overflow"),
      dropEvent(102, 0, 11, "overflow"),
    ]);
    const [a, b] = w1.letterExits;
    const w2 = applyEvents(w1, [dropEvent(103, 0, 12, "superseded")]);
    const c = w2.letterExits[2];
    const w3 = applyEvents(w2, [
      { seq: 104, t: 0, type: "letter.delivered", letterId: 13, islandId: 3, accepted: true },
    ]);
    const d = w3.letterExits[3];
    if (a === undefined || b === undefined || c === undefined || d === undefined) {
      throw new Error("letterExits が足りない");
    }
    expect(d.outcome).toBe("delivered");

    const tracker = createExitTracker();
    expect(tracker.take([a, b])).toEqual([]);
    expect(tracker.take([a, b, c])).toEqual([c]);
    expect(tracker.take([a, b, c])).toEqual([]);
    expect(tracker.take([d])).toEqual([]);

    // スナップショットで世界が置き換わると letterExits は []。その後の処分は新しいオブジェクト。
    const replaced = worldWith([portLetter(20, 3, 1)]);
    expect(replaced.letterExits).toEqual([]);
    expect(tracker.take(replaced.letterExits)).toEqual([]);
    const w4 = applyEvents(replaced, [dropEvent(201, 0, 20, "overflow")]);
    const e = w4.letterExits[0];
    if (e === undefined) throw new Error("処分が無い");
    expect(tracker.take(w4.letterExits)).toEqual([e]);
  });
});

describe("LE-2 planLetterEffects（種類・長さ・文字・色）", () => {
  const cases: {
    name: string;
    letter: Letter;
    reason: DropReason;
    reducedMotion: boolean;
    want: Partial<LetterEffect>;
  }[] = [
    {
      name: "overflow → drift",
      letter: portLetter(10, 1, 2),
      reason: "overflow",
      reducedMotion: false,
      want: {
        kind: "drift",
        durationMs: 1400,
        caption: "古い手紙を処分しました",
        islandId: 1,
        to: 2,
        color: COLORS[2],
        request: false,
      },
    },
    {
      name: "superseded → swap",
      letter: portLetter(10, 2, 3),
      reason: "superseded",
      reducedMotion: false,
      want: {
        kind: "swap",
        durationMs: 700,
        caption: "新しい手紙に差し替わりました",
        islandId: 2,
        to: 3,
        color: COLORS[3],
      },
    },
    {
      name: "依頼書 → request",
      letter: portLetter(10, 1, 3, "request"),
      reason: "overflow",
      reducedMotion: false,
      want: { kind: "drift", request: true },
    },
    {
      name: "動きを減らす（drift）",
      letter: portLetter(10, 1, 2),
      reason: "overflow",
      reducedMotion: true,
      want: { kind: "drift", durationMs: 600 },
    },
    {
      name: "動きを減らす（swap）",
      letter: portLetter(10, 1, 2),
      reason: "superseded",
      reducedMotion: true,
      want: { kind: "swap", durationMs: 600 },
    },
    {
      name: "宛先がステージに無い → 灰の縁",
      letter: portLetter(10, 1, 9),
      reason: "overflow",
      reducedMotion: false,
      want: { islandId: 1, to: 9, color: MAP_STYLE.colors.unknownIsland },
    },
  ];
  it.each(cases)("$name", ({ letter, reason, reducedMotion, want }) => {
    const { world, exits } = exitsOf([letter], [dropEvent(101, 0, letter.id, reason)]);
    const plan = planLetterEffects([], exits, ctx(world, { now: 500, reducedMotion }));
    expect(plan.effects).toHaveLength(1);
    expect(plan.effects[0]).toMatchObject({ id: "101", reason, startedAt: 500, ...want });
  });
});

describe("LE-3 同じ山の差し替えは置き換える", () => {
  const world = worldWith([]);
  const old = effect({
    id: "50",
    kind: "swap",
    reason: "superseded",
    startedAt: 0,
    durationMs: 700,
  });
  const cases: { name: string; letter: Letter; reason: DropReason; wantIds: string[] }[] = [
    {
      name: "同じ山の superseded → 新しいもの 1 つ",
      letter: portLetter(10, 1, 2),
      reason: "superseded",
      wantIds: ["101"],
    },
    {
      name: "別の山の superseded → 2 つ",
      letter: portLetter(10, 1, 3),
      reason: "superseded",
      wantIds: ["50", "101"],
    },
    {
      name: "同じ山の overflow（drift）→ 置き換えない",
      letter: portLetter(10, 1, 2),
      reason: "overflow",
      wantIds: ["50", "101"],
    },
  ];
  it.each(cases)("$name", ({ letter, reason, wantIds }) => {
    const exits = onlyExit(letter, reason);
    const plan = planLetterEffects([old], exits, ctx(world, { now: 300 }));
    expect(plan.effects.map((e) => e.id)).toEqual(wantIds);
    const fresh = plan.effects.find((e) => e.id === "101");
    expect(fresh?.startedAt).toBe(300);
    expect(
      plan.effects.filter((e) => e.kind === "swap" && e.to === 2 && e.islandId === 1).length,
    ).toBeLessThanOrEqual(1);
  });
});

describe("LE-4 文字の間引き", () => {
  const world = worldWith([]);
  // 1 つ目を now 0 で出した後の captionAt で、2 つ目の caption を見る。
  const first = planLetterEffects([], onlyExit(portLetter(10, 1, 2), "overflow"), ctx(world));
  const cases: {
    name: string;
    letter: Letter;
    reason: DropReason;
    now: number;
    captioned: boolean;
  }[] = [
    {
      name: "同じ山・同じ種類・1000",
      letter: portLetter(11, 1, 2),
      reason: "overflow",
      now: 1000,
      captioned: false,
    },
    {
      name: "同じ山・同じ種類・4000",
      letter: portLetter(11, 1, 2),
      reason: "overflow",
      now: 4000,
      captioned: true,
    },
    {
      name: "別の種類",
      letter: portLetter(11, 1, 2),
      reason: "superseded",
      now: 1000,
      captioned: true,
    },
    {
      name: "別の山",
      letter: portLetter(11, 2, 1),
      reason: "overflow",
      now: 1000,
      captioned: true,
    },
  ];
  it("1 つ目は文字が付く", () => {
    expect(first.effects[0]?.caption).toBe(LETTER_EFFECT_CAPTIONS.overflow);
    expect(first.captionAt.get("1:2:drift")).toBe(0);
  });
  it.each(cases)("$name", ({ letter, reason, now, captioned }) => {
    const plan = planLetterEffects(
      first.effects,
      onlyExit(letter, reason, 0, 102),
      ctx(world, { now, captionAt: first.captionAt }),
    );
    const second = plan.effects.find((e) => e.id === "102");
    expect(second?.caption).toBe(captioned ? LETTER_EFFECT_CAPTIONS[reason] : null);
  });
});

describe("LE-5 古い処分は描かない", () => {
  const cases = [
    { name: "simNow - 3001 → 描かない", t: 6999, want: 0 },
    { name: "simNow - 3000 → 描く", t: 7000, want: 1 },
  ];
  it.each(cases)("$name", ({ t, want }) => {
    const { world, exits } = exitsOf([portLetter(10, 1, 2)], [dropEvent(101, t, 10, "overflow")]);
    const plan = planLetterEffects([], exits, ctx(world, { simNow: 10000 }));
    expect(plan.effects).toHaveLength(want);
  });
});

describe("LE-6 数の上限と終わったもの", () => {
  it("20 件の処分 → 新しい 16 件", () => {
    const letters = Array.from({ length: 20 }, (_, k) =>
      portLetter(100 + k, 1 + (k % 3), 1 + ((k + 1) % 3)),
    );
    const events = letters.map((l, k) => dropEvent(1000 + k, 0, l.id, "overflow"));
    const { world, exits } = exitsOf(letters, events);
    expect(exits).toHaveLength(20);
    const plan = planLetterEffects([], exits, ctx(world, { now: 100 }));
    expect(plan.effects).toHaveLength(LETTER_EFFECT_STYLE.maxEffects);
    expect(plan.effects.map((e) => e.id)).toEqual(
      Array.from({ length: 16 }, (_, k) => String(1004 + k)),
    );
  });
  it("active の古いものから捨てる", () => {
    const active = Array.from({ length: 16 }, (_, k) =>
      effect({ id: `a${String(k)}`, startedAt: k * 10, durationMs: 10000 }),
    );
    const world = worldWith([]);
    const plan = planLetterEffects(
      active,
      onlyExit(portLetter(10, 1, 2), "overflow"),
      ctx(world, { now: 500 }),
    );
    expect(plan.effects).toHaveLength(16);
    expect(plan.effects.map((e) => e.id)).not.toContain("a0");
    expect(plan.effects.at(-1)?.id).toBe("101");
  });
  it("終わったものは除かれる", () => {
    const world = worldWith([]);
    const active = [
      effect({ id: "done", startedAt: 0, durationMs: 1400 }),
      effect({ id: "live", startedAt: 1, durationMs: 1400 }),
    ];
    const plan = planLetterEffects(active, [], ctx(world, { now: 1400 }));
    expect(plan.effects.map((e) => e.id)).toEqual(["live"]);
  });
});

describe("LE-7 船の上の手紙の処分", () => {
  it("islandId は letter.from", () => {
    const letter = { ...ferryLetter(10, "f1", 3), from: 2 };
    const { world, exits } = exitsOf([letter], [dropEvent(101, 0, 10, "overflow")]);
    const plan = planLetterEffects([], exits, ctx(world));
    expect(plan.effects[0]).toMatchObject({ islandId: 2, to: 3 });
  });
});

describe("LE-8 pileAnchor", () => {
  it("山があればその位置", () => {
    const scene = sceneOf(worldWith([portLetter(10, 1, 2)]));
    const pile = scene.piles.find((p) => p.islandId === 1 && p.to === 2);
    expect(pile).toBeDefined();
    expect(pileAnchor(scene, 1, 2)).toEqual(pile?.at);
  });
  it("山が無ければ島 1 の中心から島 2 の方向へ pileOffset", () => {
    const scene = sceneOf(worldWith([]));
    const c1 = centerOf(scene, 1);
    const c2 = centerOf(scene, 2);
    const dist = Math.hypot(c2.x - c1.x, c2.y - c1.y);
    const at = pileAnchor(scene, 1, 2);
    expect(at?.x).toBeCloseTo(c1.x + ((c2.x - c1.x) / dist) * 52);
    expect(at?.y).toBeCloseTo(c1.y + ((c2.y - c1.y) / dist) * 52);
    expect(MAP_STYLE.pileOffset).toBe(52);
    if (at === null) throw new Error("位置が無い");
    expect(Math.hypot(at.x - c1.x, at.y - c1.y)).toBeCloseTo(52);
  });
  it("宛先がステージに無い → 中心の真下", () => {
    const scene = sceneOf(worldWith([]));
    const c1 = centerOf(scene, 1);
    expect(pileAnchor(scene, 1, 9)).toEqual({ x: c1.x, y: c1.y + 52 });
  });
  it("島がステージに無い → null", () => {
    const scene = sceneOf(worldWith([]));
    expect(pileAnchor(scene, 9, 2)).toBeNull();
  });
});

describe("LE-9 effectFrame（drift）", () => {
  const scene = sceneOf(worldWith([]));
  const anchor = pileAnchor(scene, 1, 2);
  if (anchor === null) throw new Error("位置が無い");
  const c1 = centerOf(scene, 1);
  const len = Math.hypot(anchor.x - c1.x, anchor.y - c1.y);
  const d = { x: (anchor.x - c1.x) / len, y: (anchor.y - c1.y) / len };
  const e = effect({ caption: LETTER_EFFECT_CAPTIONS.overflow });

  it("0 → 山・alpha 1・scale 1", () => {
    const f = effectFrame(e, scene, 0, false);
    expect(f?.at.x).toBeCloseTo(anchor.x);
    expect(f?.at.y).toBeCloseTo(anchor.y);
    expect(f?.alpha).toBe(1);
    expect(f?.scale).toBe(1);
    expect(f?.incoming).toBeNull();
    expect(f?.caption).toEqual({
      text: LETTER_EFFECT_CAPTIONS.overflow,
      at: { x: anchor.x, y: anchor.y - 30 },
      alpha: 1,
    });
  });
  it("700 → 外向きへ動き、下へ 8px 沈み、alpha 0.5", () => {
    const f = effectFrame(e, scene, 700, false);
    if (f === null) throw new Error("フレームが無い");
    const ease = 0.75;
    expect(f.at.x).toBeCloseTo(anchor.x + d.x * 48 * ease);
    expect(f.at.y).toBeCloseTo(anchor.y + d.y * 48 * ease + 8);
    // 島の中心から遠ざかる
    expect(Math.hypot(f.at.x - c1.x, f.at.y - c1.y)).toBeGreaterThan(len);
    expect(f.alpha).toBeCloseTo(0.5);
    expect(f.scale).toBeCloseTo(0.85);
  });
  it("1399 → alpha がほぼ 0、1400 → null", () => {
    expect(effectFrame(e, scene, 1399, false)?.alpha).toBeLessThan(0.01);
    expect(effectFrame(e, scene, 1400, false)).toBeNull();
  });
  it("島が scene に無い → null", () => {
    expect(effectFrame(effect({ islandId: 9 }), scene, 100, false)).toBeNull();
  });
  it.each([0, 350, 700, 1000, 1399])("reducedMotion %d → 山・rotation 0", (now) => {
    const f = effectFrame(e, scene, now, true);
    expect(f?.at).toEqual(anchor);
    expect(f?.rotation).toBe(0);
    expect(f?.scale).toBe(1);
    expect(f?.alpha).toBeCloseTo(1 - now / 1400);
  });
});

describe("LE-10 effectFrame（swap）", () => {
  const scene = sceneOf(worldWith([portLetter(10, 1, 2)]));
  const anchor = pileAnchor(scene, 1, 2);
  if (anchor === null) throw new Error("位置が無い");
  const e = effect({
    kind: "swap",
    reason: "superseded",
    durationMs: 700,
    caption: LETTER_EFFECT_CAPTIONS.superseded,
  });

  it("350 → 古い手紙が左上へ 7px・alpha 0.5、新しい手紙 alpha 0.5・scale 0.8", () => {
    const f = effectFrame(e, scene, 350, false);
    if (f === null) throw new Error("フレームが無い");
    expect(f.at.x).toBeCloseTo(anchor.x - 7);
    expect(f.at.y).toBeCloseTo(anchor.y - 7);
    expect(f.alpha).toBeCloseTo(0.5);
    expect(f.scale).toBe(1);
    expect(f.rotation).toBe(0);
    expect(f.incoming?.at).toEqual(anchor);
    expect(f.incoming?.alpha).toBeCloseTo(0.5);
    expect(f.incoming?.scale).toBeCloseTo(0.8);
    expect(f.caption?.alpha).toBe(1);
  });
  it("文字は p 0.9 で alpha 0.5", () => {
    expect(effectFrame(e, scene, 630, false)?.caption?.alpha).toBeCloseTo(0.5);
    expect(effectFrame(e, scene, 559, false)?.caption?.alpha).toBe(1);
  });
  it("reducedMotion → 新しい手紙も大きさ 1", () => {
    const f = effectFrame(e, scene, 350, true);
    expect(f?.at).toEqual(anchor);
    expect(f?.incoming).toEqual({ at: anchor, alpha: 0.5, scale: 1 });
  });
});

describe("LE-11 引数を書き換えない", () => {
  it("freeze した active・captionAt で例外が出ず、captionAt は別の Map", () => {
    const world = worldWith([portLetter(10, 1, 2)]);
    const w2 = applyEvents(world, [dropEvent(101, 0, 10, "superseded")]);
    const active = Object.freeze([
      Object.freeze(effect({ id: "50", kind: "swap", reason: "superseded", durationMs: 700 })),
      Object.freeze(effect({ id: "51", startedAt: -5000 })),
    ]);
    const captionAt: ReadonlyMap<string, number> = Object.freeze(new Map([["1:2:swap", -9000]]));
    const exits = Object.freeze([...w2.letterExits]);
    const run = () => planLetterEffects(active, exits, ctx(w2, { now: 100, captionAt }));
    const plan = run();
    expect(plan.captionAt).not.toBe(captionAt);
    expect(captionAt.get("1:2:swap")).toBe(-9000);
    expect(plan.captionAt.get("1:2:swap")).toBe(100);
    expect(active.map((e) => e.id)).toEqual(["50", "51"]);
    // 同じ入力なら同じ結果
    expect(run()).toEqual(plan);
  });
});
