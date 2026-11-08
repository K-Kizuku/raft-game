// ストアの世界の変化から、鳴らす効果音を選ぶ（T-434）。ストアは届いたイベントを写すだけなので、
// 前と後の World を比べて、出来事（ferry.departed・ferry.arrived・letter.delivered・
// request.updated の確定・forecast の嵐）を見つける。Raft やゲームの結果をここで決めない。
// snapshot を当てはめたとき（最初・つなぎ直し・リプレイの頭出し）は、過去の出来事なので鳴らさない。
import type { World } from "../../state/index.ts";
import { SOUND_KINDS, type SoundKind } from "./recipes.ts";

export function soundCues(prev: World | null, next: World | null): SoundKind[] {
  if (prev === null || next === null || prev === next) return [];
  // snapshot は stage を作り直す。イベントでは stage はそのまま（同じオブジェクト）。
  if (prev.stage !== next.stage || prev.session.id !== next.session.id) return [];

  const found = new Set<SoundKind>();

  if (prev.ferries !== next.ferries) {
    for (const id of next.ferryIds) {
      const before = prev.ferries[id];
      const after = next.ferries[id];
      if (before === undefined || after === undefined || before === after) continue;
      if (
        after.state === "sailing" &&
        (before.state !== "sailing" || before.departAt !== after.departAt)
      ) {
        found.add("depart");
      } else if (after.state === "docked" && before.state === "sailing") {
        found.add("arrive");
      }
    }
  }

  if (prev.letterExits !== next.letterExits) {
    for (const exit of next.letterExits) {
      if (exit.seq > prev.lastEventSeq && exit.outcome === "delivered") {
        found.add("deliver");
        break;
      }
    }
  }

  if (prev.requests !== next.requests) {
    for (const id of next.requestIds) {
      const after = next.requests[id];
      if (after?.status !== "succeeded") continue;
      if (prev.requests[id]?.status !== "succeeded") {
        found.add("confirm");
        break;
      }
    }
  }

  if (prev.forecasts !== next.forecasts) {
    for (const forecast of next.forecasts) {
      if (forecast.seq > prev.lastEventSeq && forecast.event === "storm") {
        found.add("storm");
        break;
      }
    }
  }

  return SOUND_KINDS.filter((kind) => found.has(kind));
}
