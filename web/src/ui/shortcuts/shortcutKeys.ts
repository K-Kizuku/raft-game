// キーボードショートカットの判定（T-702・FR-UI-09・ゲームデザイン §7.7）。純粋な関数だけを置く。
// キーは event.code で判定する（キーボードの配列・IME のオン・CapsLock に左右されない。DemoKeys と同じ）。
import { isSpaceActivated, isTextInput } from "../demo/demoKeys.ts";

export type ShortcutAction = { kind: "pause" } | { kind: "ferry"; index: number }; // index は 0〜3

export type ShortcutKeyEvent = Pick<
  KeyboardEvent,
  | "code"
  | "ctrlKey"
  | "metaKey"
  | "altKey"
  | "repeat"
  | "isComposing"
  | "defaultPrevented"
  | "target"
>;

const FERRY_KEYS: Readonly<Record<string, number>> = {
  Digit1: 0,
  Digit2: 1,
  Digit3: 2,
  Digit4: 3,
  Numpad1: 0,
  Numpad2: 1,
  Numpad3: 2,
  Numpad4: 3,
};

// keydown がショートカットのどれかを返す。当てはまらなければ null。
// demo が真（デモのセッション）なら Space は DemoKeys が受けるので null。Shift は見ない。
export function shortcutActionOf(event: ShortcutKeyEvent, demo: boolean): ShortcutAction | null {
  if (event.defaultPrevented) return null;
  if (event.isComposing) return null;
  if (event.repeat) return null;
  if (event.ctrlKey || event.metaKey || event.altKey) return null;
  if (isTextInput(event.target)) return null;
  if (event.code === "Space") {
    if (demo) return null;
    // フォーカスのあるボタンを Space で押す動きを奪わない。
    if (isSpaceActivated(event.target)) return null;
    return { kind: "pause" };
  }
  if (Object.hasOwn(FERRY_KEYS, event.code)) {
    const index = FERRY_KEYS[event.code];
    if (index !== undefined) return { kind: "ferry", index };
  }
  return null;
}
