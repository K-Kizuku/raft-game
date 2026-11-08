// フェリー操作パネルのドラッグ＆ドロップ（T-405・ADR-0010）。パネルの中（港・倉庫 → 積み荷・積み荷 → 港・倉庫）で完結する。
// 落とした先から送るコマンドを決めるだけで、積めるかどうかはサーバーが決める。

export const CARGO_DROP_ID = "ferry-cargo";
export const PORT_DROP_ID = "ferry-port";
export const RELAY_DROP_ID = "ferry-relay"; // 中継倉庫（FR-FERRY-06）

export type LetterDragData = Readonly<{ letterId: number; source: "port" | "cargo" | "relay" }>;

export function dragId(data: LetterDragData): string {
  return `letter:${data.source}:${String(data.letterId)}`;
}

function isLetterDragData(data: unknown): data is LetterDragData {
  if (typeof data !== "object" || data === null) return false;
  const letterId: unknown = Reflect.get(data, "letterId");
  const source: unknown = Reflect.get(data, "source");
  return (
    typeof letterId === "number" && (source === "port" || source === "cargo" || source === "relay")
  );
}

// 第 1 引数はコマンドを送る船（payload の ferryId は呼び出し側が入れるので、ここでは使わない）。同じ場所・外・null に落としたら null（何もしない）。
// 積み荷を港・倉庫に落としたらどちらも unload（降ろし先はサーバーが送信元で決める。パネルが unloadTo で結果の文を分ける）。
export function dropCommand(
  _ferryId: string,
  data: unknown,
  overId: string | number | null,
): { op: "load" | "unload" | "relayLoad"; letterIds: [number] } | null {
  if (!isLetterDragData(data)) return null;
  if (data.source === "port" && overId === CARGO_DROP_ID) {
    return { op: "load", letterIds: [data.letterId] };
  }
  if (data.source === "relay" && overId === CARGO_DROP_ID) {
    return { op: "relayLoad", letterIds: [data.letterId] };
  }
  if (data.source === "cargo" && (overId === PORT_DROP_ID || overId === RELAY_DROP_ID)) {
    return { op: "unload", letterIds: [data.letterId] };
  }
  return null;
}
