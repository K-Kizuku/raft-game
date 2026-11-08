// 効果音のレシピ（T-434）。外部の音声ファイルを使わず、Web Audio の発振器とエンベロープで作る音の設計図。
// 値は画面（耳）のための値で、ゲームの数値ではない（ステージ定義に置かない）。
// 1 つの音は短い「音符（Tone）」の並び。start・duration は秒、from・to は周波数（Hz）、peak は音量（0〜1）。

export type SoundKind = "depart" | "arrive" | "deliver" | "confirm" | "storm";

// 鳴らす順（同じ瞬間に複数あるときの順でもある）。
export const SOUND_KINDS: readonly SoundKind[] = [
  "depart",
  "arrive",
  "deliver",
  "confirm",
  "storm",
];

export type Tone = Readonly<{
  wave: OscillatorType;
  from: number; // 鳴り始めの周波数
  to: number; // 鳴り終わりの周波数（from と同じなら一定）
  start: number; // 音の頭からの遅れ（秒）
  duration: number; // 長さ（秒）
  peak: number; // いちばん大きいときの音量（0〜1）
}>;

// エンベロープの立ち上がり（秒）。プツッという音を避けるため、0 から短く上げる。
export const ATTACK_SECONDS = 0.012;
// 指数の減衰は 0 にできないので、ここまで下げてから止める。
export const SILENT_GAIN = 0.0001;

export const SOUND_RECIPES: Readonly<Record<SoundKind, readonly Tone[]>> = {
  // 出航: 低い汽笛（ぼーっ）。三角波と 5 度上の正弦波を重ね、少しだけ下げる。
  depart: [
    { wave: "triangle", from: 220, to: 208, start: 0, duration: 0.42, peak: 0.22 },
    { wave: "sine", from: 330, to: 312, start: 0, duration: 0.42, peak: 0.08 },
  ],
  // 到着: 港の鐘（ちーん・ちーん）。上がる 2 音。
  arrive: [
    { wave: "sine", from: 659, to: 659, start: 0, duration: 0.2, peak: 0.2 },
    { wave: "sine", from: 880, to: 880, start: 0.11, duration: 0.3, peak: 0.18 },
  ],
  // 配達: ポストに落ちる軽い音（ぽん）。高い三角波を短く下げる。
  deliver: [
    { wave: "triangle", from: 1175, to: 784, start: 0, duration: 0.12, peak: 0.2 },
    { wave: "sine", from: 1568, to: 1568, start: 0.05, duration: 0.14, peak: 0.08 },
  ],
  // 確定: 明るい上りの分散和音（ド・ミ・ソ・ド）。
  confirm: [
    { wave: "sine", from: 523, to: 523, start: 0, duration: 0.16, peak: 0.18 },
    { wave: "sine", from: 659, to: 659, start: 0.07, duration: 0.16, peak: 0.18 },
    { wave: "sine", from: 784, to: 784, start: 0.14, duration: 0.16, peak: 0.18 },
    { wave: "triangle", from: 1047, to: 1047, start: 0.21, duration: 0.34, peak: 0.2 },
  ],
  // 嵐の予報: 下がる 2 音の注意と、低いうなり（ごろごろ）。
  storm: [
    { wave: "triangle", from: 494, to: 494, start: 0, duration: 0.18, peak: 0.18 },
    { wave: "triangle", from: 370, to: 370, start: 0.2, duration: 0.24, peak: 0.18 },
    { wave: "sawtooth", from: 82, to: 55, start: 0.05, duration: 0.6, peak: 0.06 },
  ],
};

// 音の長さ（秒）。いちばん遅く終わる音符の終わり。
export function recipeLength(tones: readonly Tone[]): number {
  let end = 0;
  for (const tone of tones) end = Math.max(end, tone.start + tone.duration);
  return end;
}
