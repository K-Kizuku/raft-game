// 手紙の処分・差し替えのアニメーションの定数（T-410 / FR-MAIL-04・ゲームデザイン §2.3）。
// 画面のための値で、ゲームの数値ではない（ステージ定義に置かない）。
// ほかのファイルにアニメーションの数を直接書かず、ここから読む。
import { palette } from "../../ui/theme/tokens.ts";

export const LETTER_EFFECT_STYLE = Object.freeze({
  driftMs: 1400, // 処分（海に流れる）の長さ（実時間）
  swapMs: 700, // 差し替えの長さ（実時間）
  reducedMs: 600, // 動きを減らす設定のときの長さ（どちらも）
  driftDistance: 48, // 港から外へ流れる距離（px）
  driftSink: 16, // 流れながら下へ沈む距離（px）
  driftWobble: 0.35, // 揺れの角度（ラジアン）
  driftStrokeAlpha: 0.6, // 流れる封筒の縁の濃さ（少し薄く）
  swapOffset: 14, // 古い手紙がずれて消える距離（px）
  swapInScale: 0.6, // 新しい手紙が現れるときの最初の大きさ
  captionOffset: 30, // 山の上に出す文字の高さ（px）
  captionFontSize: 11,
  captionCooldownMs: 4000, // 同じ山・同じ種類の文字を出し直すまでの間（実時間）
  maxEffects: 16, // 同時に描くアニメーションの上限
  maxLagMs: 3000, // これより古い（ゲーム内時刻で）処分は描かない
  // 色はデザイントークン（web/src/ui/theme/tokens.ts）から取る。16 進の色をここに直接書かない。
  colors: {
    caption: palette.ink, // 山の上に出す文字
    captionHalo: palette.paper, // 文字の縁
    drift: palette.seaDeep, // 流れる封筒の縁（海の色に溶ける）
  },
});

export const LETTER_EFFECT_CAPTIONS = Object.freeze({
  overflow: "古い手紙を処分しました",
  superseded: "新しい手紙に差し替わりました",
});
