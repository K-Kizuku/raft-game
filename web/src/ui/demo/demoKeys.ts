// デモ操作のキーの判定（T-411・進行表 §8・ゲームデザイン §7.6）。純粋な関数だけを置く。
// キーは event.code で判定する（Shift・CapsLock・キーボードの配列・IME のオンに左右されない）。
export type DemoAction = "storm" | "fast" | "skip" | "pause";

export type DemoKeyEvent = Pick<
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

const ACTIONS: Readonly<Record<string, DemoAction>> = {
  KeyD: "storm",
  KeyF: "fast",
  KeyN: "skip",
  Space: "pause",
};

// keydown がデモ操作のどれかを返す。当てはまらなければ null。
export function demoActionOf(event: DemoKeyEvent): DemoAction | null {
  if (event.defaultPrevented) return null;
  if (event.isComposing) return null;
  if (event.repeat) return null;
  if (event.ctrlKey || event.metaKey || event.altKey) return null;
  if (isTextInput(event.target)) return null;
  if (!Object.hasOwn(ACTIONS, event.code)) return null;
  const action = ACTIONS[event.code] ?? null;
  // フォーカスのあるボタンを Space で押す動きを奪わない。
  if (action === "pause" && isSpaceActivated(event.target)) return null;
  return action;
}

// F を押している間の速度。shift なら 8、そうでなければ 4。allowed になければ、
// shift のときは 4 を試し、それもなければ null。
export function fastSpeed(shift: boolean, allowed: readonly number[]): number | null {
  if (shift && allowed.includes(8)) return 8;
  if (allowed.includes(4)) return 4;
  return null;
}

// フォーカスがキーの入力を受ける要素（入力欄・contenteditable）か。
// engineer/engineerKey.ts の同名の関数と同じ判定（そちらは import しない）。
export function isTextInput(target: EventTarget | null): boolean {
  if (target === null) return false;
  if (
    target instanceof HTMLInputElement ||
    target instanceof HTMLTextAreaElement ||
    target instanceof HTMLSelectElement
  ) {
    return true;
  }
  return (
    target instanceof HTMLElement && (target.isContentEditable || hasEditableAttribute(target))
  );
}

// isContentEditable を持たない環境（jsdom）のために、いちばん近い contenteditable の属性も見る。
function hasEditableAttribute(element: HTMLElement): boolean {
  const editable = element.closest("[contenteditable]");
  return editable !== null && editable.getAttribute("contenteditable") !== "false";
}

const SPACE_ROLES: ReadonlySet<string> = new Set([
  "button",
  "checkbox",
  "switch",
  "radio",
  "tab",
  "menuitem",
  "option",
]);

// フォーカスが Space で押される要素（button・a[href]・summary・input・select・textarea・
// role が button / checkbox / switch / radio / tab / menuitem / option の要素）か。
export function isSpaceActivated(target: EventTarget | null): boolean {
  if (!(target instanceof Element)) return false;
  if (
    target instanceof HTMLButtonElement ||
    target instanceof HTMLInputElement ||
    target instanceof HTMLSelectElement ||
    target instanceof HTMLTextAreaElement
  ) {
    return true;
  }
  const tag = target.tagName.toLowerCase();
  if (tag === "summary") return true;
  if (tag === "a" && target.hasAttribute("href")) return true;
  const role = target.getAttribute("role");
  return role !== null && SPACE_ROLES.has(role.trim().toLowerCase());
}
