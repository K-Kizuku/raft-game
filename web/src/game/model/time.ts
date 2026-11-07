// 表示に使うゲーム内時刻（T-404 / DesignDoc §13.3）。船が後ろに跳ねないようにする。
import { MAP_STYLE } from "./style.ts";

// smoothSimTime は前のフレームで使った時刻 prevShown と、今の推定 estimate から、表示に使う時刻を決める。
// 推定が少し（timeHoldMs 以内）戻っただけなら止めて待ち、大きく戻ったら（スナップショットの置き換えなど）跳ぶ。
export function smoothSimTime(prevShown: number | null, estimate: number): number {
  if (prevShown === null) return estimate;
  if (estimate >= prevShown) return estimate;
  if (prevShown - estimate <= MAP_STYLE.timeHoldMs) return prevShown;
  return estimate;
}
