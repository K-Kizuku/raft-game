// 次のアラームの時刻の計算（T-308・DesignDoc §12.5。純粋な関数）。

import { IDLE_MS } from "./limits.ts";

export interface AlarmFields {
  readonly deadlineAt: number; // created_at + maxMinutes 分（G2）
  readonly idleSince: number | null; // ブラウザの接続がなくなった時刻。null = 接続中
}

// 次に鳴らしたい時刻。接続中なら deadline_at、接続がなければ min(deadline_at, idle_since + IDLE_MS)。
// idle_since が NULL で接続もない（DO が作り直された）ときは now を idle_since とみなす。
export function desiredAlarmAt(row: AlarmFields, connected: boolean, now: number): number {
  if (connected) return row.deadlineAt;
  const idleSince = row.idleSince ?? now;
  return Math.min(row.deadlineAt, idleSince + IDLE_MS);
}
