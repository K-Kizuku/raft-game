// 手紙の処分・差し替えのアニメーションを決める純粋な関数（T-410 / FR-MAIL-04・DesignDoc §13.3）。
// サーバーが出した letter.dropped（ストアの world.letterExits）を見せるだけで、港キューの規則
// （DesignDoc §6.2 の R1〜R6）をここで当てはめ直さない。ストアに書かない。
// 引数を書き換えず、時刻は引数（now = 実時間の performance.now、simNow = ゲーム内時刻）だけを使う。
//
// 画面の作り方の判断:
// - マップを開いたときに残っている処分は流さない（createExitTracker の最初の take は []）。
// - 進みは実時間（now）で、ゲームの速度・一時停止に関係なく同じ速さで動く。古すぎる処分を捨てる
//   判断（maxLagMs）だけゲーム内時刻を使う（タブが裏にあった・再接続でまとめて届いたとき）。
// - 同じ山の差し替え（swap）は重ねず、新しいもので置き換える（定期便は 1 秒ごとに差し替わる）。
// - 文字は同じ山・同じ種類で captionCooldownMs に 1 回だけ添える（間引いたものは null）。
// - 同時に描くのは maxEffects まで。超えたら startedAt の古いものから捨てる。
import type { DropReason } from "../../protocol/index.ts";
import type { LetterExit, World } from "../../state/index.ts";
import type { MapScene } from "../model/scene.ts";
import { MAP_STYLE } from "../model/style.ts";
import type { Point } from "../model/viewport.ts";
import { LETTER_EFFECT_CAPTIONS, LETTER_EFFECT_STYLE } from "./style.ts";

const E = LETTER_EFFECT_STYLE;

export type LetterEffect = Readonly<{
  id: string; // 処分の seq（文字列）
  kind: "drift" | "swap"; // overflow → drift、superseded → swap
  reason: DropReason;
  islandId: number; // 処分した港の島
  to: number; // 手紙の宛先（山の向き）
  color: string; // 宛先の島の色（stage に無ければ MAP_STYLE.colors.unknownIsland）
  request: boolean; // 依頼書なら ★ を添える
  caption: string | null; // LETTER_EFFECT_CAPTIONS の文字。間引いたら null
  startedAt: number; // 実時間（performance.now）
  durationMs: number;
}>;

export type ExitTracker = Readonly<{ take(exits: readonly LetterExit[]): LetterExit[] }>;

// createExitTracker は、まだ見ていない処分（outcome が "dropped"）を古い順に返す。
// ストアは構造を共有するので、同じ処分は同じオブジェクト（DesignDoc §13.2）。WeakSet で覚える。
export function createExitTracker(): ExitTracker {
  const seen = new WeakSet<LetterExit>();
  let first = true;
  return {
    take(exits) {
      const fresh: LetterExit[] = [];
      for (const exit of exits) {
        if (seen.has(exit)) continue;
        seen.add(exit);
        if (!first && exit.outcome === "dropped") fresh.push(exit);
      }
      first = false;
      return fresh;
    },
  };
}

export type EffectPlan = Readonly<{
  effects: LetterEffect[];
  captionAt: ReadonlyMap<string, number>;
}>;

function isFinished(effect: LetterEffect, now: number): boolean {
  return now >= effect.startedAt + effect.durationMs;
}

export function planLetterEffects(
  active: readonly LetterEffect[],
  exits: readonly LetterExit[],
  ctx: Readonly<{
    world: World;
    now: number;
    simNow: number;
    reducedMotion: boolean;
    captionAt: ReadonlyMap<string, number>;
  }>,
): EffectPlan {
  const { world, now, simNow, reducedMotion } = ctx;
  const captionAt = new Map(ctx.captionAt);
  let effects = active.filter((e) => !isFinished(e, now));

  for (const exit of exits) {
    if (exit.outcome !== "dropped") continue;
    if (exit.t < simNow - E.maxLagMs) continue;
    const { letter } = exit;
    const islandId =
      letter.loc.type === "port" && letter.loc.islandId !== undefined
        ? letter.loc.islandId
        : letter.from;
    const to = letter.to;
    const kind = exit.reason === "overflow" ? "drift" : "swap";
    const durationMs = reducedMotion ? E.reducedMs : kind === "drift" ? E.driftMs : E.swapMs;
    if (kind === "swap") {
      effects = effects.filter(
        (e) => !(e.kind === "swap" && e.islandId === islandId && e.to === to),
      );
    }
    const captionKey = `${String(islandId)}:${String(to)}:${kind}`;
    const last = captionAt.get(captionKey);
    let caption: string | null = null;
    if (last === undefined || now - last >= E.captionCooldownMs) {
      caption = LETTER_EFFECT_CAPTIONS[exit.reason];
      captionAt.set(captionKey, now);
    }
    effects.push({
      id: String(exit.seq),
      kind,
      reason: exit.reason,
      islandId,
      to,
      color: world.stage.islands[to]?.color ?? MAP_STYLE.colors.unknownIsland,
      request: letter.kind === "request",
      caption,
      startedAt: now,
      durationMs,
    });
  }

  if (effects.length > E.maxEffects) {
    // 安定な並べ替えなので、startedAt が同じなら後から来たもの（新しい処分）が残る。
    effects = [...effects].sort((a, b) => a.startedAt - b.startedAt).slice(-E.maxEffects);
  }
  return { effects, captionAt };
}

// pileAnchor は山の位置。山が無ければ（最後の 1 通が消えた）、T-404 の buildMapScene と同じ置き方で求める。
// 位置は毎フレームの scene から求めるので、画面の大きさが変わってもずれない。
export function pileAnchor(scene: MapScene, islandId: number, to: number): Point | null {
  const pile = scene.piles.find((p) => p.islandId === islandId && p.to === to);
  if (pile !== undefined) return pile.at;
  const center = scene.islands.find((i) => i.id === islandId)?.at;
  if (center === undefined) return null;
  const dest = scene.islands.find((i) => i.id === to)?.at;
  if (dest !== undefined) {
    const dx = dest.x - center.x;
    const dy = dest.y - center.y;
    const dist = Math.hypot(dx, dy);
    if (dist > 0) {
      return {
        x: center.x + (dx / dist) * MAP_STYLE.pileOffset,
        y: center.y + (dy / dist) * MAP_STYLE.pileOffset,
      };
    }
  }
  return { x: center.x, y: center.y + MAP_STYLE.pileOffset };
}

export type EffectFrame = Readonly<{
  at: Point; // 古い手紙（流れる・消える方）
  alpha: number;
  scale: number;
  rotation: number;
  incoming: Readonly<{ at: Point; alpha: number; scale: number }> | null; // 差し替えの新しい手紙
  caption: Readonly<{ text: string; at: Point; alpha: number }> | null;
}>;

function clamp01(v: number): number {
  return Math.min(1, Math.max(0, v));
}

export function effectFrame(
  effect: LetterEffect,
  scene: MapScene,
  now: number,
  reducedMotion: boolean,
): EffectFrame | null {
  if (isFinished(effect, now)) return null;
  const anchor = pileAnchor(scene, effect.islandId, effect.to);
  if (anchor === null) return null;
  const p = clamp01(effect.durationMs > 0 ? (now - effect.startedAt) / effect.durationMs : 1);
  const caption =
    effect.caption === null
      ? null
      : {
          text: effect.caption,
          at: { x: anchor.x, y: anchor.y - E.captionOffset },
          alpha: p < 0.8 ? 1 : (1 - p) / 0.2,
        };

  if (reducedMotion) {
    return {
      at: anchor,
      alpha: 1 - p,
      scale: 1,
      rotation: 0,
      incoming: effect.kind === "swap" ? { at: anchor, alpha: p, scale: 1 } : null,
      caption,
    };
  }

  if (effect.kind === "drift") {
    const center = scene.islands.find((i) => i.id === effect.islandId)?.at ?? anchor;
    const dx = anchor.x - center.x;
    const dy = anchor.y - center.y;
    const len = Math.hypot(dx, dy);
    const d = len > 0 ? { x: dx / len, y: dy / len } : { x: 0, y: 1 };
    const ease = 1 - (1 - p) * (1 - p);
    return {
      at: {
        x: anchor.x + d.x * E.driftDistance * ease,
        y: anchor.y + d.y * E.driftDistance * ease + E.driftSink * p,
      },
      alpha: 1 - p,
      scale: 1 - 0.3 * p,
      rotation: Math.sin(2 * Math.PI * p) * E.driftWobble,
      incoming: null,
      caption,
    };
  }

  return {
    at: { x: anchor.x - E.swapOffset * p, y: anchor.y - E.swapOffset * p },
    alpha: 1 - p,
    scale: 1,
    rotation: 0,
    incoming: { at: anchor, alpha: p, scale: E.swapInScale + (1 - E.swapInScale) * p },
    caption,
  };
}
