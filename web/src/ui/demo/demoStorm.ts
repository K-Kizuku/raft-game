// デモの嵐の紹介の文を出すかの判定（T-305b・プロトコル §5.1 の stage.demoStorm）。純粋な関数。
// 世界の状態（snapshot と events）だけから決める（ADR-0009）。キー操作の結果（ack）では変えない。
import type { StageText } from "../../protocol/index.ts";
import type { World } from "../../state/index.ts";

// デモの嵐の紹介の文のうち、いま出すもの。出さないときは null。
// world があり、phase が "sailing" で、stage.demoStorm の flavor が null でなく、
// routeIds の航路のどれかの routeStates[id].blockedUntil が null でないときだけ、その flavor を返す。
// 返す値は world.stage.demoStorm.flavor そのもの（同じ参照。ストアのセレクタで毎回新しい値を作らない）。
export function activeDemoStormFlavor(world: World | null): StageText | null {
  if (world === null || world.phase !== "sailing") return null;
  const storm = world.stage.demoStorm;
  if (storm === undefined || storm.flavor === null) return null;
  const blocked = storm.routeIds.some((id) => {
    const state = world.routeStates[id];
    return state !== undefined && state.blockedUntil !== null;
  });
  return blocked ? storm.flavor : null;
}
