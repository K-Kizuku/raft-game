// エンジニアモードを切り替える E キーの判定（T-407・FR-ENG-01）。
// 入力欄にフォーカスがあるとき・修飾キーと一緒のとき・押しっぱなし・IME の変換中は切り替えない。
export type ToggleKeyEvent = Pick<
  KeyboardEvent,
  "key" | "ctrlKey" | "metaKey" | "altKey" | "repeat" | "isComposing" | "target"
>;

function isTextInput(target: EventTarget | null): boolean {
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

export function isEngineerToggleKey(event: ToggleKeyEvent): boolean {
  if (event.key !== "e" && event.key !== "E") return false;
  if (event.ctrlKey || event.metaKey || event.altKey) return false;
  if (event.repeat || event.isComposing) return false;
  return !isTextInput(event.target);
}
