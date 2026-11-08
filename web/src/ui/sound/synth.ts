// レシピ（recipes.ts）を Web Audio の発振器とゲインのエンベロープで鳴らす（T-434）。
// 1 つの音符ごとに OscillatorNode → GainNode → 出口 とつなぎ、鳴り終わったら止める（ノードは止まれば捨てられる）。
import { ATTACK_SECONDS, SILENT_GAIN, type Tone } from "./recipes.ts";

export type SynthContext = Pick<
  BaseAudioContext,
  "currentTime" | "destination" | "createOscillator" | "createGain"
>;

// tones を ctx の今の時刻から鳴らす。volume は全体の音量の倍率（0〜1）。
export function playTones(ctx: SynthContext, tones: readonly Tone[], volume = 1): void {
  const now = ctx.currentTime;
  for (const tone of tones) {
    const start = now + tone.start;
    const end = start + tone.duration;
    const peak = Math.max(SILENT_GAIN, tone.peak * volume);

    const osc = ctx.createOscillator();
    osc.type = tone.wave;
    osc.frequency.setValueAtTime(tone.from, start);
    if (tone.to !== tone.from) {
      osc.frequency.exponentialRampToValueAtTime(tone.to, end);
    }

    const gain = ctx.createGain();
    gain.gain.setValueAtTime(0, start);
    gain.gain.linearRampToValueAtTime(peak, start + Math.min(ATTACK_SECONDS, tone.duration / 2));
    gain.gain.exponentialRampToValueAtTime(SILENT_GAIN, end);

    osc.connect(gain);
    gain.connect(ctx.destination);
    osc.start(start);
    osc.stop(end + 0.02);
  }
}
