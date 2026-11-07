// チュートリアルで強調する UI を DOM から探す（T-409）。ほかのモジュールを import せず、クラス名だけで指す。
// .ferry-panel__* は DesignDoc §13.8 で固定。.hud-score・.island-requests・.game-island・.game-map・
// .map-view__islands は今のクラス名で、無くなっても強調が出ないだけ（例外を出さない）。
import type { World } from "../../state/index.ts";
import type { TutorialIslands } from "./machine.ts";
import type { TargetSpec } from "./script.ts";

const ISLAND_BUTTONS = ".map-view__islands button";

function query(root: ParentNode, selector: string): Element | null {
  try {
    return root.querySelector(selector);
  } catch {
    return null; // 書き方の誤ったセレクター
  }
}

// findTargets は specs の順に見つかった要素を返す（見つからないものは飛ばす）。
export function findTargets(
  root: ParentNode,
  specs: readonly TargetSpec[],
  islands: TutorialIslands,
  world: World,
): Element[] {
  const found: Element[] = [];
  for (const spec of specs) {
    let element: Element | null = null;
    if (spec.kind === "selector") {
      element = query(root, spec.selector);
      if (element === null && spec.fallback !== undefined) element = query(root, spec.fallback);
    } else {
      const name = world.stage.islands[islands[spec.island]]?.name;
      if (name !== undefined && name !== "") {
        element =
          Array.from(root.querySelectorAll(ISLAND_BUTTONS)).find((button) =>
            (button.textContent ?? "").trim().startsWith(name),
          ) ?? null;
      }
    }
    if (element !== null && !found.includes(element)) found.push(element);
  }
  return found;
}
