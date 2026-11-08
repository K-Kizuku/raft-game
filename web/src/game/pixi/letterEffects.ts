// 手紙の処分・差し替えのアニメーション（../animations/letterEffects.ts が決める）を PixiJS の表示物に写す薄い層
// （T-410 / FR-MAIL-04・DesignDoc §13.3）。マップの effects の層に載せる。ストアに書かない。
import { Container, Graphics, Text } from "pixi.js";

import type { World } from "../../state/index.ts";
import { withRaftTerm } from "../../ui/terms/glossary.ts";
import { termsPreference } from "../../ui/terms/preference.ts";
import {
  createExitTracker,
  effectFrame,
  planCaptions,
  planLetterEffects,
} from "../animations/letterEffects.ts";
import type { LetterEffect } from "../animations/letterEffects.ts";
import { LETTER_EFFECT_STYLE } from "../animations/style.ts";
import { MAP_ART_STYLE } from "../model/art/style.ts";
import type { MapScene } from "../model/scene.ts";
import { MAP_STYLE } from "../model/style.ts";
import { MAP_VIEW } from "../model/viewport.ts";
import { drawEnvelope } from "./envelope.ts";

export type LetterEffectsLayer = Readonly<{
  update(world: World, scene: MapScene, now: number, simNow: number): void;
  destroy(): void;
}>;

const S = MAP_STYLE;
const D = MAP_STYLE.draw;
const E = LETTER_EFFECT_STYLE;
const A = MAP_ART_STYLE;

// 文字の解像度。絵の拡大率（T-426）の上限まで拡大しても、ぼやけないようにする。
function textResolution(): number {
  const dpr = typeof window === "undefined" ? 1 : window.devicePixelRatio;
  return (Number.isFinite(dpr) && dpr > 0 ? dpr : 1) * MAP_VIEW.maxMarkScale;
}

// 封筒 1 通（港の手紙の山と同じ形。./envelope.ts）。依頼書なら ★ を添える。
function envelope(effect: LetterEffect, faded: boolean): Container {
  const root = new Container();
  const g = drawEnvelope(new Graphics(), {
    color: effect.color,
    alpha: faded ? E.driftStrokeAlpha : 1,
    ...(faded ? { stroke: E.colors.drift } : {}),
  });
  root.addChild(g);
  if (effect.request) {
    const star = new Text({
      text: "★",
      resolution: textResolution(),
      style: {
        fontFamily: D.fontFamily,
        fontSize: D.badgeFontSize,
        fill: S.colors.star,
        fontWeight: "bold",
        stroke: { color: S.colors.starStroke, width: A.starStrokeWidth },
      },
    });
    star.anchor.set(0.5);
    star.position.set(-D.envelopeWidth / 2, -D.envelopeHeight / 2);
    root.addChild(star);
  }
  return root;
}

function captionText(): Text {
  const text = new Text({
    text: "",
    resolution: textResolution(),
    style: {
      fontFamily: D.fontFamily,
      fontSize: E.captionFontSize,
      fill: E.colors.caption,
      fontWeight: "bold",
      stroke: { color: E.colors.captionHalo, width: D.haloWidth },
    },
  });
  text.anchor.set(0.5);
  return text;
}

type EffectView = {
  root: Container;
  outgoing: Container;
  incoming: Container | null;
};

function createView(parent: Container, effect: LetterEffect): EffectView {
  const root = new Container();
  const outgoing = envelope(effect, effect.kind === "drift");
  const incoming = effect.kind === "swap" ? envelope(effect, false) : null;
  if (incoming !== null) root.addChild(incoming);
  root.addChild(outgoing);
  parent.addChild(root);
  return { root, outgoing, incoming };
}

export function createLetterEffectsLayer(
  parent: Container,
  options: Readonly<{ reducedMotion: boolean }>,
): LetterEffectsLayer {
  const { reducedMotion } = options;
  const tracker = createExitTracker();
  const views = new Map<string, EffectView>();
  // 封筒の層と、その上の文字の層。文字は同じ島・同じ種類ごとに 1 つ（planCaptions。T-428）。
  const envelopeLayer = new Container();
  const captionLayer = new Container();
  parent.addChild(envelopeLayer, captionLayer);
  const captions = new Map<string, Text>();
  let active: LetterEffect[] = [];
  let captionAt: ReadonlyMap<string, number> = new Map();
  let destroyed = false;

  const drop = (id: string, view: EffectView): void => {
    view.root.destroy({ children: true });
    views.delete(id);
  };

  return {
    update(world, scene, now, simNow) {
      if (destroyed) return;
      const plan = planLetterEffects(active, tracker.take(world.letterExits), {
        world,
        now,
        simNow,
        reducedMotion,
        captionAt,
      });
      active = plan.effects;
      captionAt = plan.captionAt;

      const showRaftTerms = termsPreference.getSnapshot().showRaftTerms;
      const drawn = new Set<string>();
      for (const effect of active) {
        const frame = effectFrame(effect, scene, now, reducedMotion);
        if (frame === null) continue;
        drawn.add(effect.id);
        let view = views.get(effect.id);
        if (view === undefined) {
          view = createView(envelopeLayer, effect);
          views.set(effect.id, view);
        }
        view.outgoing.position.set(frame.at.x, frame.at.y);
        view.outgoing.alpha = frame.alpha;
        // 絵の拡大率（T-426）を掛け、港の手紙の山と同じ大きさで動かす。
        const k = scene.markScale ?? 1;
        view.outgoing.scale.set(frame.scale * k);
        view.outgoing.rotation = frame.rotation;
        if (view.incoming !== null) {
          if (frame.incoming === null) {
            view.incoming.visible = false;
          } else {
            view.incoming.visible = true;
            view.incoming.position.set(frame.incoming.at.x, frame.incoming.at.y);
            view.incoming.alpha = frame.incoming.alpha;
            view.incoming.scale.set(frame.incoming.scale * k);
          }
        }
      }
      const k = scene.markScale ?? 1;
      const shown = new Set<string>();
      for (const c of planCaptions(active, scene, now, reducedMotion, (text, reason) =>
        withRaftTerm(text, reason, showRaftTerms),
      )) {
        shown.add(c.key);
        let text = captions.get(c.key);
        if (text === undefined) {
          text = captionText();
          captionLayer.addChild(text);
          captions.set(c.key, text);
        }
        if (text.text !== c.text) text.text = c.text;
        text.position.set(c.at.x, c.at.y);
        text.alpha = c.alpha;
        text.scale.set(k);
      }
      for (const [key, text] of captions) {
        if (shown.has(key)) continue;
        text.destroy();
        captions.delete(key);
      }
      for (const [id, view] of views) {
        if (!drawn.has(id)) drop(id, view);
      }
    },
    destroy() {
      if (destroyed) return;
      destroyed = true;
      for (const [id, view] of views) drop(id, view);
      captions.clear();
      envelopeLayer.destroy({ children: true });
      captionLayer.destroy({ children: true });
      active = [];
    },
  };
}
