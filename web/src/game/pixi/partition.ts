// 分断のグループの縁取りと記号（T-704・FR-ENG-08）。scene.partition を描くだけで、クリックは受けない
// （当たり判定は hitTest のまま）。動かさない（prefers-reduced-motion に関係なく静止）。
// 過半数のグループは実線・success の色、そうでないグループは破線・storm の色（色だけでなく線の形で区別する。NFR-10）。
import { Container, Graphics, Text } from "pixi.js";

import { palette } from "../../ui/theme/tokens.ts";
import type { MapScene, PartitionHalo } from "../model/scene.ts";
import { MAP_STYLE } from "../model/style.ts";

// 画面の値（ゲームの数値ではない）。MAP_STYLE は変えず、ここに置く。
export const PARTITION_STYLE = {
  haloGap: 14, // 島の半径から縁取りの円までの余白（px）
  fillAlpha: 0.18, // 縁取りの塗りの透明度
  strokeWidth: 3,
  strokeAlpha: 0.9,
  dash: 9, // 過半数でないグループの破線（px）
  dashGap: 6,
  badgeRadius: 12, // 記号の丸い札の半径（px）
  badgeStrokeWidth: 2.5,
  badgeFontSize: 14,
} as const;

const P = PARTITION_STYLE;

export type PartitionLayer = Readonly<{
  update(partition: MapScene["partition"]): void;
  destroy(): void;
}>;

function colorOf(halo: PartitionHalo): number {
  return halo.majority ? palette.success : palette.storm;
}

// 破線の円（中心 (cx, cy)・半径 r）。
function dashedRing(g: Graphics, cx: number, cy: number, r: number): void {
  const step = P.dash + P.dashGap;
  const count = Math.max(1, Math.floor((2 * Math.PI * r) / step));
  const unit = (2 * Math.PI) / count;
  const arc = unit * (P.dash / step);
  for (let i = 0; i < count; i++) {
    const start = i * unit;
    g.moveTo(cx + Math.cos(start) * r, cy + Math.sin(start) * r);
    g.arc(cx, cy, r, start, start + arc);
  }
}

function drawHalo(layer: Container, halo: PartitionHalo): void {
  const color = colorOf(halo);
  const r = MAP_STYLE.islandRadius + P.haloGap;
  const ring = new Graphics();
  for (const at of halo.islands) ring.circle(at.x, at.y, r);
  ring.fill({ color, alpha: P.fillAlpha });
  if (halo.majority) {
    for (const at of halo.islands) ring.circle(at.x, at.y, r);
  } else {
    for (const at of halo.islands) dashedRing(ring, at.x, at.y, r);
  }
  ring.stroke({ width: P.strokeWidth, color, alpha: P.strokeAlpha, cap: "round" });

  const badge = new Graphics()
    .circle(halo.badgeAt.x, halo.badgeAt.y, P.badgeRadius)
    .fill({ color: palette.paper })
    .stroke({ width: P.badgeStrokeWidth, color });
  const text = new Text({
    text: halo.label,
    style: {
      fontFamily: MAP_STYLE.draw.fontFamily,
      fontSize: P.badgeFontSize,
      fill: palette.ink,
      fontWeight: "bold",
    },
  });
  text.anchor.set(0.5);
  text.position.set(halo.badgeAt.x, halo.badgeAt.y);
  layer.addChild(ring, badge, text);
}

function keyOf(partition: MapScene["partition"]): string {
  if (partition === null) return "";
  return partition.halos
    .map(
      (h) =>
        `${h.label}|${String(h.majority)}|${h.badgeAt.x},${h.badgeAt.y}|` +
        h.islands.map((p) => `${String(p.x)},${String(p.y)}`).join(";"),
    )
    .join("/");
}

// layer はマップのレイヤー（島より下）。描くものは少ないので、形が変わったら全部作り直す。
export function createPartitionLayer(layer: Container): PartitionLayer {
  let key = "";
  const clear = (): void => {
    for (const child of layer.removeChildren()) child.destroy();
  };
  return {
    update(partition) {
      const next = keyOf(partition);
      if (next === key) return;
      key = next;
      clear();
      if (partition === null) return;
      for (const halo of partition.halos) drawHalo(layer, halo);
    },
    destroy() {
      clear();
      key = "";
    },
  };
}
