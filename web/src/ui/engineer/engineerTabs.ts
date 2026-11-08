// エンジニアパネルのタブ（T-438）。パネルの中を巻き取らずに収めるため、表をタブに分ける。
// 「手紙」は、それまでパネルの下にあった手紙の一覧と詳細（FR-MAIL-05）を減らさずに置くタブ。
export type EngineerTabId = "nodes" | "log" | "progress" | "letters";

export type EngineerTab = Readonly<{ id: EngineerTabId; label: string }>;

export const ENGINEER_TABS: readonly EngineerTab[] = Object.freeze([
  { id: "nodes", label: "ノード一覧" },
  { id: "log", label: "Raft ログ" },
  { id: "progress", label: "Progress" },
  { id: "letters", label: "手紙" },
]);

// ← → で移るタブ（端では反対の端に回る）。ほかのキーなら null。
export function tabAfterKey(current: EngineerTabId, key: string): EngineerTabId | null {
  const step = key === "ArrowRight" ? 1 : key === "ArrowLeft" ? -1 : 0;
  if (step === 0) return null;
  const n = ENGINEER_TABS.length;
  const at = ENGINEER_TABS.findIndex((tab) => tab.id === current);
  const next = ENGINEER_TABS[(((at + step) % n) + n) % n];
  return next === undefined ? null : next.id;
}
