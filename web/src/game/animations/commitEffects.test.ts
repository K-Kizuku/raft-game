import { describe, expect, it } from "vitest";

import type { Request, RequestStatus, ServerEvent } from "../../protocol/index.ts";
import { applyEvents, worldFromSnapshot } from "../../state/index.ts";
import type { UiSelection, World } from "../../state/index.ts";
import { buildMapScene } from "../model/scene.ts";
import type { MapScene } from "../model/scene.ts";
import type { Point } from "../model/viewport.ts";
import { COLORS, makeSnapshot, node } from "../testing/snapshot.ts";
import {
  commitEffectFrame,
  commitLine,
  confettiPieces,
  createCommitTracker,
  leaderIslandOf,
  planCommitEffects,
} from "./commitEffects.ts";
import type { CommitEffect } from "./commitEffects.ts";
import { COMMIT_EFFECT_STYLE } from "./style.ts";

const C = COMMIT_EFFECT_STYLE;
const SIZE = { width: 800, height: 600 };
const NONE: UiSelection = { islandId: null, ferryId: null };

function request(id: string, origin: number, status: RequestStatus = "in_transit"): Request {
  return {
    id,
    origin,
    kind: "stall.reserve",
    resource: "stall",
    critical: false,
    createdAt: 0,
    deadline: 60000,
    status,
    expired: false,
    result: null,
  };
}

function worldWith(requests: Request[], nodes = makeSnapshot().nodes): World {
  return worldFromSnapshot(makeSnapshot({ requests, nodes }), 0);
}

let seq = 100;
function update(world: World, id: string, status: RequestStatus): World {
  seq += 1;
  const event: ServerEvent = {
    seq,
    t: 0,
    type: "request.updated",
    id,
    status,
    expired: false,
    result: status === "succeeded" ? "ok" : null,
  };
  return applyEvents(world, [event]);
}

function sceneOf(world: World): MapScene {
  return buildMapScene(world, NONE, 0, SIZE);
}

function centerOf(scene: MapScene, id: number): Point {
  const at = scene.islands.find((i) => i.id === id)?.at;
  if (at === undefined) throw new Error(`島 ${String(id)} が scene に無い`);
  return at;
}

function effect(over: Partial<CommitEffect> = {}): CommitEffect {
  return {
    key: "r1@0",
    requestId: "r1",
    from: 1,
    to: 3,
    color: COLORS[3],
    seed: 7,
    startedAt: 0,
    durationMs: C.durationMs,
    ...over,
  };
}

function distance(a: Point, b: Point): number {
  return Math.hypot(a.x - b.x, a.y - b.y);
}

describe("createCommitTracker", () => {
  it("最初の take は、すでに確定している依頼を返さない", () => {
    const tracker = createCommitTracker();
    expect(tracker.take(worldWith([request("r1", 2, "succeeded")]))).toEqual([]);
  });

  it("確定（succeeded）に変わった依頼だけを返し、同じ依頼を 2 度返さない", () => {
    const tracker = createCommitTracker();
    let world = worldWith([request("r1", 2), request("r2", 3)]);
    tracker.take(world);
    world = update(world, "r1", "committed");
    expect(tracker.take(world)).toEqual([]);
    world = update(world, "r1", "succeeded");
    expect(tracker.take(world).map((r) => r.id)).toEqual(["r1"]);
    expect(tracker.take(world)).toEqual([]);
    world = update(world, "r2", "rejected");
    expect(tracker.take(world)).toEqual([]);
  });

  it("同時に確定した依頼を出てきた順に返す", () => {
    const tracker = createCommitTracker();
    let world = worldWith([request("r1", 2), request("r2", 3)]);
    tracker.take(world);
    world = update(update(world, "r2", "succeeded"), "r1", "succeeded");
    expect(tracker.take(world).map((r) => r.id)).toEqual(["r1", "r2"]);
  });

  it("見たことのない依頼が、はじめから確定していたら返さない", () => {
    const tracker = createCommitTracker();
    tracker.take(worldWith([]));
    expect(tracker.take(worldWith([request("r9", 2, "succeeded")]))).toEqual([]);
  });
});

describe("leaderIslandOf", () => {
  it("代表（roleMarkOf が leader）の島を返す", () => {
    expect(leaderIslandOf(worldWith([]))).toBe(1);
  });

  it("任期の古い代表は選ばず、代表がいなければ null", () => {
    const stale = worldWith(
      [],
      [node(1, "leader", 2), node(2, "leader", 3), node(3, "follower", 3)],
    );
    expect(leaderIslandOf(stale)).toBe(2);
    const none = worldWith(
      [],
      [node(1, "candidate", 3), node(2, "follower", 3), node(3, "follower", 3)],
    );
    expect(leaderIslandOf(none)).toBeNull();
  });
});

describe("planCommitEffects", () => {
  it("代表の島から依頼した島への演出を作る", () => {
    const world = worldWith([request("r1", 3, "succeeded")]);
    const [made] = planCommitEffects([], [request("r1", 3, "succeeded")], {
      world,
      now: 500,
      reducedMotion: false,
    });
    expect(made).toMatchObject({
      requestId: "r1",
      from: 1,
      to: 3,
      color: COLORS[3],
      startedAt: 500,
      durationMs: C.durationMs,
    });
  });

  it("依頼した島が代表なら線を引かない（紙吹雪だけ）", () => {
    const world = worldWith([]);
    const [made] = planCommitEffects([], [request("r1", 1, "succeeded")], {
      world,
      now: 0,
      reducedMotion: false,
    });
    expect(made?.from).toBeNull();
  });

  it("動きを減らす設定では短くし、線が無い演出は作らない", () => {
    const world = worldWith([]);
    const made = planCommitEffects([], [request("r1", 2), request("r2", 1)], {
      world,
      now: 0,
      reducedMotion: true,
    });
    expect(made.map((e) => [e.requestId, e.durationMs])).toEqual([["r1", C.reducedMs]]);
  });

  it("終わった演出を捨て、同時に描くのは maxEffects まで（新しいものを残す）", () => {
    const world = worldWith([]);
    const old = effect({ key: "old", requestId: "old", startedAt: 0 });
    const fresh = Array.from({ length: C.maxEffects + 3 }, (_, i) => request(`n${String(i)}`, 2));
    const later = planCommitEffects([old], fresh, { world, now: 10, reducedMotion: false });
    expect(later).toHaveLength(C.maxEffects);
    expect(later.some((e) => e.requestId === "old")).toBe(false);
    expect(later.at(-1)?.requestId).toBe(`n${String(C.maxEffects + 2)}`);
    expect(
      planCommitEffects(later, [], { world, now: 10 + C.durationMs, reducedMotion: false }),
    ).toEqual([]);
  });

  it("同じ依頼の演出は重ねず置き換える", () => {
    const world = worldWith([]);
    const first = planCommitEffects([], [request("r1", 2)], {
      world,
      now: 0,
      reducedMotion: false,
    });
    const second = planCommitEffects(first, [request("r1", 2)], {
      world,
      now: 100,
      reducedMotion: false,
    });
    expect(second.map((e) => e.startedAt)).toEqual([100]);
  });

  it("引数を書き換えない", () => {
    const world = worldWith([]);
    const active = Object.freeze([effect()]);
    planCommitEffects(active, [request("r2", 2)], { world, now: 0, reducedMotion: false });
    expect(active).toHaveLength(1);
  });
});

describe("commitLine", () => {
  it("線の両端を島の中心から inset だけ離す", () => {
    const ends = commitLine({ x: 0, y: 0 }, { x: 100, y: 0 }, 20);
    expect(ends).toEqual({ start: { x: 20, y: 0 }, end: { x: 80, y: 0 } });
  });

  it("島が近すぎて両端が重なるなら null", () => {
    expect(commitLine({ x: 0, y: 0 }, { x: 30, y: 0 }, 20)).toBeNull();
  });
});

describe("confettiPieces", () => {
  it("依頼した島の周りに散らし、同じ種なら同じ散り方になる", () => {
    const center = { x: 100, y: 100 };
    const pieces = confettiPieces(center, 7, 0.5, 1);
    expect(pieces).toHaveLength(C.confettiCount);
    const reach = C.confettiSpread + C.confettiRise + C.confettiFall;
    for (const piece of pieces) expect(distance(piece.at, center)).toBeLessThanOrEqual(reach);
    expect(confettiPieces(center, 7, 0.5, 1)).toEqual(pieces);
    expect(confettiPieces(center, 8, 0.5, 1)).not.toEqual(pieces);
  });

  it("始まる前と終わった後は何も出さない", () => {
    expect(confettiPieces({ x: 0, y: 0 }, 1, 0, 1)).toEqual([]);
    expect(confettiPieces({ x: 0, y: 0 }, 1, 1, 1)).toEqual([]);
  });
});

describe("commitEffectFrame", () => {
  const world = worldWith([]);
  const scene = sceneOf(world);
  const leader = centerOf(scene, 1);
  const origin = centerOf(scene, 3);
  const k = scene.markScale ?? 1;

  it("線は代表の島の側から伸び始め、依頼した島の側で止まる", () => {
    const start = commitEffectFrame(effect(), scene, 0, false);
    expect(start?.line).not.toBeNull();
    const line = start?.line;
    if (line == null) return;
    expect(distance(line.start, leader)).toBeCloseTo(C.lineInset * k, 5);
    expect(distance(line.end, origin)).toBeCloseTo(C.lineInset * k, 5);
    expect(line.head).toEqual(line.start);
    expect(start?.confetti).toEqual([]);

    const reached = commitEffectFrame(effect(), scene, C.durationMs * C.lineGrowRatio, false);
    expect(reached?.line?.head.x).toBeCloseTo(line.end.x, 5);
    expect(reached?.line?.head.y).toBeCloseTo(line.end.y, 5);
  });

  it("線が届いてから、依頼した島に紙吹雪を出し、最後は薄くなって終わる", () => {
    const mid = commitEffectFrame(effect(), scene, C.durationMs * 0.7, false);
    expect(mid?.confetti).toHaveLength(C.confettiCount);
    const near = mid?.confetti.every((p) => distance(p.at, origin) < distance(p.at, leader));
    expect(near).toBe(true);
    const late = commitEffectFrame(effect(), scene, C.durationMs * 0.95, false);
    expect(late?.line?.alpha ?? 1).toBeLessThan(0.5);
    expect(commitEffectFrame(effect(), scene, C.durationMs, false)).toBeNull();
  });

  it("依頼した島が代表なら、線を引かずに紙吹雪だけ出す", () => {
    const frame = commitEffectFrame(effect({ from: null, to: 1 }), scene, C.durationMs / 2, false);
    expect(frame?.line).toBeNull();
    expect(frame?.confetti.length).toBe(C.confettiCount);
  });

  it("動きを減らす設定では、伸ばさずに線だけを出す（紙吹雪なし）", () => {
    const e = effect({ durationMs: C.reducedMs });
    const frame = commitEffectFrame(e, scene, 0, true);
    expect(frame?.line?.head).toEqual(frame?.line?.end);
    expect(frame?.line?.alpha).toBe(1);
    expect(commitEffectFrame(e, scene, C.reducedMs / 2, true)?.confetti).toEqual([]);
  });

  it("依頼した島が scene に無ければ null", () => {
    expect(commitEffectFrame(effect({ to: 99 }), scene, 0, false)).toBeNull();
  });

  it("絵の拡大率（markScale）に合わせて線の端を離す", () => {
    const big: MapScene = { ...scene, markScale: 2 };
    const line = commitEffectFrame(effect(), big, 0, false)?.line;
    expect(line == null ? null : distance(line.start, leader)).toBeCloseTo(C.lineInset * 2, 5);
  });
});
