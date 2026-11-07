// フェリー操作パネルのドラッグ＆ドロップ（T-405・ADR-0010）。パネルの中（港 → 積み荷・積み荷 → 港）で完結する。
// 落とした先から送るコマンドを決めるだけで、積めるかどうかはサーバーが決める。

export const CARGO_DROP_ID = "ferry-cargo";
export const PORT_DROP_ID = "ferry-port";

export type LetterDragData = Readonly<{ letterId: number; source: "port" | "cargo" }>;

export function dragId(data: LetterDragData): string {
  return `letter:${data.source}:${String(data.letterId)}`;
}

function isLetterDragData(data: unknown): data is LetterDragData {
  if (typeof data !== "object" || data === null) return false;
  const letterId: unknown = Reflect.get(data, "letterId");
  const source: unknown = Reflect.get(data, "source");
  return typeof letterId === "number" && (source === "port" || source === "cargo");
}

// 第 1 引数はコマンドを送る船（payload の ferryId は呼び出し側が入れるので、ここでは使わない）。同じ場所・外・null に落としたら null（何もしない）。
export function dropCommand(
  _ferryId: string,
  data: unknown,
  overId: string | number | null,
): { op: "load" | "unload"; letterIds: [number] } | null {
  if (!isLetterDragData(data)) return null;
  if (data.source === "port" && overId === CARGO_DROP_ID) {
    return { op: "load", letterIds: [data.letterId] };
  }
  if (data.source === "cargo" && overId === PORT_DROP_ID) {
    return { op: "unload", letterIds: [data.letterId] };
  }
  return null;
}
