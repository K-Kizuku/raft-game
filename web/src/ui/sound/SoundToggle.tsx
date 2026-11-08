// HUD の隅の「効果音」のオン・オフのボタン（T-434）。名前は「効果音」のまま、状態は aria-pressed と絵（🔊・🔇）で示す。
// 押したこと自体が最初の操作になるので、ここでも unlock する（オンにした直後から鳴らせる）。
import type { ReactElement } from "react";

import { soundEngine } from "./engine.ts";
import { setSoundEnabled, useSoundPreference } from "./preference.ts";
import "./SoundToggle.css";

export function SoundToggle({ className }: { className?: string }): ReactElement {
  const { enabled } = useSoundPreference();
  return (
    <button
      type="button"
      className={className === undefined ? "sound-toggle" : `sound-toggle ${className}`}
      aria-pressed={enabled}
      title={enabled ? "効果音：オン（押すと消す）" : "効果音：オフ（押すと鳴らす）"}
      onClick={() => {
        soundEngine.unlock();
        setSoundEnabled(!enabled);
      }}
    >
      <span aria-hidden="true" className="sound-toggle-icon">
        {enabled ? "🔊" : "🔇"}
      </span>
      効果音
    </button>
  );
}
