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

// 依頼の確定の演出（T-435）の定数。代表の島から依頼した島へ光る線を伸ばし、依頼した島に紙吹雪を散らす。
// 画面のための値で、ゲームの数値ではない（ステージ定義に置かない）。
export const COMMIT_EFFECT_STYLE = Object.freeze({
  durationMs: 1600, // 演出の長さ（実時間）
  reducedMs: 1000, // 動きを減らす設定のときの長さ（光る線だけ。伸びずに出て、消える）
  lineGrowRatio: 0.4, // 演出の長さのうち、線が代表の島から依頼した島へ伸びきるまでの割合
  lineFadeRatio: 0.7, // 線が薄くなり始める割合
  lineInset: 26, // 線の両端を島の中心から離す距離（px。島の絵に重ねない）
  lineWidth: 3, // 光る線の芯の太さ（px）
  glowWidth: 9, // 線の周りの光の太さ（px）
  glowAlpha: 0.35,
  headRadius: 4.5, // 伸びる線の先の光の玉（px）
  confettiCount: 14, // 1 つの確定で散らす紙吹雪の数
  confettiSpread: 34, // 紙吹雪が広がる距離（px）
  confettiRise: 18, // 紙吹雪が最初に跳ね上がる高さ（px）
  confettiFall: 26, // 紙吹雪が落ちる距離（px）
  confettiWidth: 5, // 紙吹雪 1 枚の大きさ（px）
  confettiHeight: 3,
  confettiSpin: 6, // 紙吹雪の回転の上限（ラジアン）
  maxEffects: 8, // 同時に描く演出の上限（超えたら古いものから捨てる）
  // 色はデザイントークン（web/src/ui/theme/tokens.ts）から取る。16 進の色をここに直接書かない。
  colors: {
    line: palette.gold, // 光る線の芯（封筒の金）
    glow: palette.paper, // 線の周りの光
    confetti: [palette.gold, palette.postal, palette.landGreen, palette.seaLight, palette.paper],
  },
});
