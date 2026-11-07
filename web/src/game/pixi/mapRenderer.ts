// MapScene を PixiJS の表示物に写す薄い層（T-404 / DesignDoc §13.3・ADR-0010）。
// pixi.js を import するのはこのディレクトリだけ。MapView が動的 import で読み込む（最初の bundle に入れない）。
// 毎フレーム gameStore.getState() を読み、React の再描画を経由しない。ストアに書くのは選択だけ。
import { Application, Container, Graphics, Text } from "pixi.js";

import { formatDuration } from "../../app/format.ts";
import { estimateSimTime } from "../../state/index.ts";
import type { GameStore } from "../../state/index.ts";
import { dashSegments } from "../model/dash.ts";
import { hitTest } from "../model/hitTest.ts";
import { buildMapScene } from "../model/scene.ts";
import type { FerrySprite, IslandNode, LetterPile, MapScene, RouteLine } from "../model/scene.ts";
import { MAP_STYLE } from "../model/style.ts";
import { smoothSimTime } from "../model/time.ts";

export type MapDeps = Readonly<{ store: GameStore; now: () => number }>; // now は performance.now
export type MapHandle = Readonly<{ destroy(): void }>;

const S = MAP_STYLE;
const D = MAP_STYLE.draw;

// 絵文字は縁を付けず（"glyph"）、文字は白い縁を付けて読みやすくする（"text"）。
function label(size: number, kind: "glyph" | "text" = "text", fill: string = S.colors.text): Text {
  const text = new Text({
    text: "",
    style: {
      fontFamily: D.fontFamily,
      fontSize: size,
      fill,
      fontWeight: kind === "text" ? "bold" : "normal",
      ...(kind === "text" ? { stroke: { color: S.colors.halo, width: D.haloWidth } } : {}),
    },
  });
  text.anchor.set(0.5);
  return text;
}

function setText(text: Text, value: string): void {
  if (text.text !== value) text.text = value;
}

// 破線の円（候補の輪）。線の形で選択（実線）と区別する（NFR-10）。
function dashedCircle(g: Graphics, r: number): void {
  const step = D.candidateDash + D.candidateDashGap;
  const count = Math.max(1, Math.floor((2 * Math.PI * r) / step));
  const unit = (2 * Math.PI) / count;
  const arc = unit * (D.candidateDash / step);
  for (let i = 0; i < count; i++) {
    const start = i * unit;
    g.moveTo(Math.cos(start) * r, Math.sin(start) * r);
    g.arc(0, 0, r, start, start + arc);
  }
  g.stroke({ width: D.candidateRingWidth, color: S.colors.candidate });
}

// --- 航路 ---------------------------------------------------------------------------------------

type RouteView = { line: Graphics; storm: Container; cloud: Graphics; glyph: Text; key: string };

function createRoute(layers: Layers): RouteView {
  const line = new Graphics();
  const storm = new Container();
  const cloud = new Graphics();
  const glyph = label(D.stormFontSize, "glyph");
  storm.addChild(cloud, glyph);
  layers.routes.addChild(line);
  layers.storms.addChild(storm);
  return { line, storm, cloud, glyph, key: "" };
}

function updateRoute(view: RouteView, r: RouteLine): void {
  const key = `${r.a.x},${r.a.y},${r.b.x},${r.b.y},${r.storm ?? ""}`;
  if (view.key === key) return;
  view.key = key;
  const { line, cloud } = view;
  line.clear();
  if (r.storm === "blocked") {
    for (const [p, q] of dashSegments(r.a, r.b, S.dash, S.dashGap)) {
      line.moveTo(p.x, p.y).lineTo(q.x, q.y);
    }
    line.stroke({ width: D.routeWidth, color: S.colors.routeBlocked });
  } else {
    line.moveTo(r.a.x, r.a.y).lineTo(r.b.x, r.b.y);
    line.stroke({ width: D.routeWidth, color: S.colors.route });
  }
  view.storm.position.set(r.mid.x, r.mid.y);
  cloud.clear();
  if (r.storm === "blocked") {
    for (const dx of [-D.cloudSpread, 0, D.cloudSpread]) {
      cloud.ellipse(dx, dx === 0 ? -D.cloudRadiusY / 2 : 0, D.cloudRadiusX, D.cloudRadiusY);
    }
    cloud.fill({ color: S.colors.cloud });
    setText(view.glyph, "⛈");
    view.storm.alpha = 1;
    view.storm.visible = true;
  } else if (r.storm === "forecast") {
    setText(view.glyph, "☁");
    view.storm.alpha = D.forecastAlpha;
    view.storm.visible = true;
  } else {
    view.storm.visible = false;
  }
}

function destroyRoute(view: RouteView): void {
  view.line.destroy();
  view.storm.destroy({ children: true });
}

// --- 島 -----------------------------------------------------------------------------------------

type IslandView = { root: Container; body: Graphics; name: Text; mark: Text; key: string };

function createIsland(layers: Layers): IslandView {
  const root = new Container();
  const body = new Graphics();
  const name = label(D.labelFontSize);
  name.anchor.set(0.5, 0);
  name.y = S.islandRadius + D.labelGap;
  const mark = label(D.markFontSize, "glyph");
  mark.anchor.set(0.5, 1);
  mark.y = -S.islandRadius;
  root.addChild(body, name, mark);
  layers.islands.addChild(root);
  return { root, body, name, mark, key: "" };
}

const ROLE_GLYPH: Record<NonNullable<IslandNode["roleMark"]>, string> = {
  leader: "👑",
  leader_stale: "👑",
  candidate: "📣",
  pre_candidate: "💭",
};

function updateIsland(view: IslandView, island: IslandNode): void {
  view.root.position.set(island.at.x, island.at.y);
  const key = `${island.color}|${island.selected}|${island.candidate}`;
  if (view.key !== key) {
    view.key = key;
    const g = view.body;
    g.clear();
    if (island.candidate) dashedCircle(g, S.islandRadius + D.candidateRingGap);
    if (island.selected) {
      g.circle(0, 0, S.islandRadius + D.selectedRingGap);
      g.stroke({ width: D.selectedRingWidth, color: S.colors.selected });
    }
    g.circle(0, 0, S.islandRadius);
    g.fill({ color: island.color });
    g.stroke({ width: D.islandStrokeWidth, color: S.colors.islandStroke });
  }
  setText(view.name, island.name);
  setText(view.mark, island.roleMark === null ? "" : ROLE_GLYPH[island.roleMark]);
  view.mark.alpha = island.roleMark === "leader_stale" ? D.staleLeaderAlpha : 1;
}

// --- 手紙の山 -----------------------------------------------------------------------------------

type PileView = { root: Container; envelope: Graphics; count: Text; star: Text; key: string };

function createPile(layers: Layers): PileView {
  const root = new Container();
  const envelope = new Graphics();
  const count = label(D.badgeFontSize, "glyph");
  count.position.set(D.envelopeWidth / 2, -D.envelopeHeight / 2);
  const star = label(D.badgeFontSize, "text", S.colors.star);
  star.position.set(-D.envelopeWidth / 2, -D.envelopeHeight / 2);
  star.text = "★";
  root.addChild(envelope, count, star);
  layers.piles.addChild(root);
  return { root, envelope, count, star, key: "" };
}

function updatePile(view: PileView, pile: LetterPile): void {
  view.root.position.set(pile.at.x, pile.at.y);
  const layers = pile.count >= D.envelopeLayers ? D.envelopeLayers : Math.max(1, pile.count);
  const key = `${pile.color}|${layers}`;
  if (view.key !== key) {
    view.key = key;
    const g = view.envelope;
    g.clear();
    const w = D.envelopeWidth;
    const h = D.envelopeHeight;
    for (let i = layers - 1; i >= 0; i--) {
      const shift = i * D.envelopeShift;
      g.roundRect(-w / 2 + shift, -h / 2 - shift, w, h, D.envelopeRadius);
      g.fill({ color: S.colors.envelope });
      g.stroke({ width: D.envelopeStrokeWidth, color: pile.color });
    }
    g.moveTo(-w / 2, -h / 2)
      .lineTo(0, 0)
      .lineTo(w / 2, -h / 2);
    g.stroke({ width: D.envelopeStrokeWidth, color: pile.color });
    g.circle(w / 2, -h / 2, D.badgeRadius);
    g.fill({ color: S.colors.badge });
    g.stroke({ width: D.envelopeStrokeWidth, color: pile.color });
  }
  setText(view.count, String(pile.count));
  view.star.visible = pile.hasRequest;
}

// --- 船 -----------------------------------------------------------------------------------------

type FerryView = {
  root: Container;
  ring: Graphics;
  glyph: Text;
  name: Text;
  cargo: Text;
  eta: Text;
  broken: Text;
  selected: boolean | null;
};

function createFerry(layers: Layers): FerryView {
  const root = new Container();
  const ring = new Graphics();
  const glyph = label(D.ferryFontSize, "glyph");
  const name = label(D.ferryLabelFontSize);
  name.anchor.set(0.5, 1);
  name.y = -D.ferryRingRadius;
  const cargo = label(D.ferryLabelFontSize);
  cargo.anchor.set(0.5, 0);
  cargo.y = D.ferryRingRadius;
  const eta = label(D.ferryLabelFontSize);
  eta.anchor.set(0.5, 0);
  eta.y = D.ferryRingRadius + D.ferryLineHeight;
  const broken = label(D.brokenFontSize, "glyph");
  broken.position.set(D.ferryRingRadius, -D.ferryRingRadius / 2);
  broken.text = "🔧";
  root.addChild(ring, glyph, name, cargo, eta, broken);
  layers.ferries.addChild(root);
  return { root, ring, glyph, name, cargo, eta, broken, selected: null };
}

function updateFerry(view: FerryView, ferry: FerrySprite): void {
  view.root.position.set(ferry.at.x, ferry.at.y);
  if (view.selected !== ferry.selected) {
    view.selected = ferry.selected;
    view.ring.clear();
    if (ferry.selected) {
      view.ring.circle(0, 0, D.ferryRingRadius);
      view.ring.stroke({ width: D.ferryRingWidth, color: S.colors.ferryRing });
    }
  }
  // 絵文字を逆さにしないよう、左へ向かうときだけ左右を反転する。
  const facing = Math.cos(ferry.heading) < 0 ? -1 : 1;
  if (view.glyph.scale.x !== facing) view.glyph.scale.x = facing;
  setText(view.glyph, "⛴");
  setText(view.name, ferry.name);
  setText(view.cargo, `${ferry.cargoCount}/${ferry.capacity}`);
  const eta =
    ferry.state === "sailing" && ferry.etaMs !== null
      ? `あと ${formatDuration(Math.ceil(ferry.etaMs / 1000) * 1000)}`
      : "";
  setText(view.eta, eta);
  view.broken.visible = ferry.broken;
}

// --- 全体 ---------------------------------------------------------------------------------------

type Layers = Readonly<{
  sea: Container;
  routes: Container;
  storms: Container;
  islands: Container;
  piles: Container;
  ferries: Container;
  effects: Container;
}>;

// syncViews は scene の並びに合わせて、ID ごとの表示物を使い回す（無くなったものだけ壊し、新しいものだけ作る）。
function syncViews<K, V, T>(
  views: Map<K, V>,
  items: readonly T[],
  keyOf: (item: T) => K,
  create: () => V,
  update: (view: V, item: T) => void,
  destroy: (view: V) => void,
): void {
  const seen = new Set<K>();
  for (const item of items) {
    const key = keyOf(item);
    seen.add(key);
    let view = views.get(key);
    if (view === undefined) {
      view = create();
      views.set(key, view);
    }
    update(view, item);
  }
  for (const [key, view] of views) {
    if (!seen.has(key)) {
      destroy(view);
      views.delete(key);
    }
  }
}

export async function mountMap(host: HTMLElement, deps: MapDeps): Promise<MapHandle> {
  const app = new Application();
  try {
    await app.init({
      resizeTo: host,
      antialias: true,
      autoDensity: true,
      resolution: window.devicePixelRatio,
      background: S.colors.sea,
    });
  } catch (error) {
    try {
      app.destroy({ removeView: true }, { children: true });
    } catch {
      // 作りかけの Application は壊せないことがある（renderer が無い）。元の失敗を返す。
    }
    throw error;
  }
  host.appendChild(app.canvas);

  // 下から sea・routes・storms・islands・piles・ferries・effects。海は背景の色で描く。
  // effects は空のまま置く（T-410 が手紙の処分のアニメーションに使う）。
  const layers: Layers = {
    sea: new Container(),
    routes: new Container(),
    storms: new Container(),
    islands: new Container(),
    piles: new Container(),
    ferries: new Container(),
    effects: new Container(),
  };
  app.stage.addChild(
    layers.sea,
    layers.routes,
    layers.storms,
    layers.islands,
    layers.piles,
    layers.ferries,
    layers.effects,
  );

  const routeViews = new Map<string, RouteView>();
  const islandViews = new Map<number, IslandView>();
  const pileViews = new Map<string, PileView>();
  const ferryViews = new Map<string, FerryView>();
  let shown: number | null = null;
  let lastScene: MapScene | null = null;

  const tick = (): void => {
    const { world, selection } = deps.store.getState();
    if (world === null) return;
    shown = smoothSimTime(shown, estimateSimTime(world.clock, deps.now()));
    const scene = buildMapScene(world, selection, shown, {
      width: app.screen.width,
      height: app.screen.height,
    });
    lastScene = scene;
    syncViews(
      routeViews,
      scene.routes,
      (r) => r.id,
      () => createRoute(layers),
      updateRoute,
      destroyRoute,
    );
    syncViews(
      islandViews,
      scene.islands,
      (i) => i.id,
      () => createIsland(layers),
      updateIsland,
      (v) => v.root.destroy({ children: true }),
    );
    syncViews(
      pileViews,
      scene.piles,
      (p) => `${p.islandId}:${p.to}`,
      () => createPile(layers),
      updatePile,
      (v) => v.root.destroy({ children: true }),
    );
    syncViews(
      ferryViews,
      scene.ferries,
      (f) => f.id,
      () => createFerry(layers),
      updateFerry,
      (v) => v.root.destroy({ children: true }),
    );
  };

  const onPointerDown = (event: PointerEvent): void => {
    if (lastScene === null) return;
    const rect = app.canvas.getBoundingClientRect();
    const sx = rect.width > 0 ? app.screen.width / rect.width : 1;
    const sy = rect.height > 0 ? app.screen.height / rect.height : 1;
    const hit = hitTest(lastScene, {
      x: (event.clientX - rect.left) * sx,
      y: (event.clientY - rect.top) * sy,
    });
    if (hit === null) return;
    const state = deps.store.getState();
    if (hit.kind === "ferry") state.selectFerry(hit.id);
    else state.selectIsland(hit.id);
  };

  app.ticker.add(tick);
  app.canvas.addEventListener("pointerdown", onPointerDown);

  let destroyed = false;
  return {
    destroy() {
      if (destroyed) return;
      destroyed = true;
      app.canvas.removeEventListener("pointerdown", onPointerDown);
      app.ticker.remove(tick);
      routeViews.clear();
      islandViews.clear();
      pileViews.clear();
      ferryViews.clear();
      app.destroy({ removeView: true }, { children: true });
    },
  };
}
