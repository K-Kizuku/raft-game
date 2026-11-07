// エンジニアモードのメッセージ種別の札の層（T-712・FR-ENG-04）。../model/raftLabels.ts が作った札を描くだけで、
// クリックは受けない（当たり判定は hitTest のまま）。札は動かさない（prefers-reduced-motion に関係なく静止）。
// 札はキーで使い回し、文字が変わったときだけ Text と背景を描き直す（毎フレーム作り直さない）。
import { Container, Graphics, Text } from "pixi.js";

import type { RaftTag } from "../model/raftLabels.ts";
import { MAP_STYLE } from "../model/style.ts";

// 画面の値（ゲームの数値ではない）。MAP_STYLE は変えず、ここに置く。
export const RAFT_OVERLAY_STYLE = {
  fontSize: 10,
  lineHeight: 12,
  padX: 5,
  padY: 3,
  radius: 4,
  strokeWidth: 1,
  strokeAlpha: 0.45, // 縁は墨の色を薄くする
  backgroundAlpha: 0.92,
} as const;

const R = RAFT_OVERLAY_STYLE;
const S = MAP_STYLE;

export type RaftOverlay = Readonly<{
  update(tags: readonly RaftTag[] | null): void;
  destroy(): void;
}>;

type TagView = { root: Container; box: Graphics; text: Text; content: string };

function createTag(layer: Container): TagView {
  const root = new Container();
  const box = new Graphics();
  const text = new Text({
    text: "",
    style: {
      fontFamily: S.draw.numberFontFamily,
      fontSize: R.fontSize,
      lineHeight: R.lineHeight,
      fill: S.colors.text,
      align: "center",
    },
  });
  text.anchor.set(0.5, 0);
  text.y = R.padY;
  root.addChild(box, text);
  layer.addChild(root);
  return { root, box, text, content: "" };
}

function updateTag(view: TagView, tag: RaftTag): void {
  view.root.position.set(tag.at.x, tag.at.y);
  const content = tag.lines.join("\n");
  if (view.content === content) return;
  view.content = content;
  view.text.text = content;
  const w = view.text.width + 2 * R.padX;
  const h = view.text.height + 2 * R.padY;
  view.box
    .clear()
    .roundRect(-w / 2, 0, w, h, R.radius)
    .fill({ color: S.colors.badge, alpha: R.backgroundAlpha })
    .stroke({ width: R.strokeWidth, color: S.colors.text, alpha: R.strokeAlpha });
}

// layer はマップのレイヤー（船の上・手紙のアニメーションの下）。
export function createRaftOverlay(layer: Container): RaftOverlay {
  const views = new Map<string, TagView>();
  return {
    update(tags) {
      if (tags === null) {
        layer.visible = false;
        return;
      }
      layer.visible = true;
      const seen = new Set<string>();
      for (const tag of tags) {
        seen.add(tag.key);
        let view = views.get(tag.key);
        if (view === undefined) {
          view = createTag(layer);
          views.set(tag.key, view);
        }
        updateTag(view, tag);
      }
      for (const [key, view] of views) {
        if (!seen.has(key)) {
          view.root.destroy({ children: true });
          views.delete(key);
        }
      }
    },
    destroy() {
      for (const view of views.values()) view.root.destroy({ children: true });
      views.clear();
    },
  };
}
