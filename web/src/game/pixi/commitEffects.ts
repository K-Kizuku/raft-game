// 依頼の確定の演出（../animations/commitEffects.ts が決める）を PixiJS の表示物に写す薄い層（T-435）。
// 代表の島から依頼した島への光る線と、依頼した島の紙吹雪。マップの celebrations の層に載せる。ストアに書かない。
// 演出 1 つにつき Graphics を 2 つ（線・紙吹雪）だけ使い、毎フレーム描き直す（同時に描くのは maxEffects まで）。
import { Container, Graphics } from "pixi.js";

import type { World } from "../../state/index.ts";
import {
  commitEffectFrame,
  createCommitTracker,
  planCommitEffects,
} from "../animations/commitEffects.ts";
import type { CommitEffect, CommitFrame } from "../animations/commitEffects.ts";
import { COMMIT_EFFECT_STYLE } from "../animations/style.ts";
import type { MapScene } from "../model/scene.ts";

export type CommitEffectsLayer = Readonly<{
  update(world: World, scene: MapScene, now: number): void;
  destroy(): void;
}>;

const C = COMMIT_EFFECT_STYLE;

type EffectView = { root: Container; line: Graphics; confetti: Graphics };

function createView(parent: Container): EffectView {
  const root = new Container();
  const line = new Graphics();
  const confetti = new Graphics();
  root.addChild(line, confetti);
  parent.addChild(root);
  return { root, line, confetti };
}

function draw(view: EffectView, frame: CommitFrame, k: number): void {
  view.line.clear();
  if (frame.line !== null) {
    const { start, head, alpha } = frame.line;
    view.line
      .moveTo(start.x, start.y)
      .lineTo(head.x, head.y)
      .stroke({
        width: C.glowWidth * k,
        color: C.colors.glow,
        alpha: C.glowAlpha * alpha,
        cap: "round",
      });
    view.line
      .moveTo(start.x, start.y)
      .lineTo(head.x, head.y)
      .stroke({ width: C.lineWidth * k, color: C.colors.line, alpha, cap: "round" });
    view.line.circle(head.x, head.y, C.headRadius * k).fill({ color: C.colors.line, alpha });
  }
  view.confetti.clear();
  const w = C.confettiWidth * k;
  const h = C.confettiHeight * k;
  for (const piece of frame.confetti) {
    // 回した長方形の 4 隅を求めて描く（1 枚ずつ表示物を作らない）。
    const cos = Math.cos(piece.rotation);
    const sin = Math.sin(piece.rotation);
    const corner = (x: number, y: number): [number, number] => [
      piece.at.x + x * cos - y * sin,
      piece.at.y + x * sin + y * cos,
    ];
    const [ax, ay] = corner(-w / 2, -h / 2);
    const [bx, by] = corner(w / 2, -h / 2);
    const [cx, cy] = corner(w / 2, h / 2);
    const [dx, dy] = corner(-w / 2, h / 2);
    view.confetti
      .poly([ax, ay, bx, by, cx, cy, dx, dy])
      .fill({ color: piece.color, alpha: piece.alpha });
  }
}

export function createCommitEffectsLayer(
  parent: Container,
  options: Readonly<{ reducedMotion: () => boolean }>,
): CommitEffectsLayer {
  const tracker = createCommitTracker();
  const views = new Map<string, EffectView>();
  let active: CommitEffect[] = [];
  let destroyed = false;

  const drop = (key: string, view: EffectView): void => {
    view.root.destroy({ children: true });
    views.delete(key);
  };

  return {
    update(world, scene, now) {
      if (destroyed) return;
      const reducedMotion = options.reducedMotion();
      active = planCommitEffects(active, tracker.take(world), { world, now, reducedMotion });
      const k = scene.markScale ?? 1;
      const drawn = new Set<string>();
      for (const effect of active) {
        const frame = commitEffectFrame(effect, scene, now, reducedMotion);
        if (frame === null) continue;
        drawn.add(effect.key);
        let view = views.get(effect.key);
        if (view === undefined) {
          view = createView(parent);
          views.set(effect.key, view);
        }
        draw(view, frame, k);
      }
      for (const [key, view] of views) {
        if (!drawn.has(key)) drop(key, view);
      }
    },
    destroy() {
      if (destroyed) return;
      destroyed = true;
      for (const [key, view] of views) drop(key, view);
      active = [];
    },
  };
}
