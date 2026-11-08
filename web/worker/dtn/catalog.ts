// DTN のステージの目録（DTN-12・docs/dtn/environment.md §1）。stage-catalog.gen.json の dtn の欄。
// Actor の名前と対象外のステージの判定は、この表と署名済みの sessionId からだけ決める。

import catalogJson from "../routes/stage-catalog.gen.json";
import { parseDtnCatalog, type DtnStageInfo } from "../routes/stage-catalog.ts";

export type { DtnStageInfo } from "../routes/stage-catalog.ts";

export const DTN_STAGE_INFO: ReadonlyMap<string, DtnStageInfo> = parseDtnCatalog(catalogJson);

// 予約の内訳（Container = 島＋船＋Coordinator、Durable Object = ActorHost（島＋船）＋ DtnSession）。
export function actorUnits(info: DtnStageInfo): number {
  return info.actors.length + 1;
}
