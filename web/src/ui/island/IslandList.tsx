// 島の一覧（T-426）。島の詳細（IslandPanel）で何も選んでいないときに出す紙のカードの列。
// 島の色・名前・港の手紙の数・満足度を出し、押すとその島を選ぶ（ストアの selectIsland。マップで選ぶのと同じ）。
// 値はストアに届いたものをそのまま出す（ADR-0009）。
import { useMemo } from "react";
import type { ReactElement } from "react";

import { gameStore, lettersAtPort } from "../../state/index.ts";
import type { World } from "../../state/index.ts";
import "./IslandList.css";

type IslandRow = Readonly<{
  id: number;
  name: string;
  color: string;
  portCount: number;
  satisfaction: number | null; // 届いていなければ null
}>;

function islandRows(world: World): IslandRow[] {
  const rows: IslandRow[] = [];
  for (const id of world.stage.islandIds) {
    const island = Object.hasOwn(world.stage.islands, id) ? world.stage.islands[id] : undefined;
    if (island === undefined) continue;
    const state = Object.hasOwn(world.islandStates, id) ? world.islandStates[id] : undefined;
    rows.push({
      id,
      name: island.name,
      color: island.color,
      portCount: lettersAtPort(world, id).length,
      satisfaction: state?.satisfaction ?? null,
    });
  }
  return rows;
}

export function IslandList({ world }: { world: World }): ReactElement | null {
  const rows = useMemo(() => islandRows(world), [world]);
  if (rows.length === 0) return null;
  return (
    <ul aria-label="島を選ぶ" className="island-list">
      {rows.map((row) => (
        <li key={row.id}>
          <button
            type="button"
            className="island-list__card"
            data-island={row.id}
            onClick={() => {
              gameStore.getState().selectIsland(row.id);
            }}
          >
            <span
              aria-hidden="true"
              className="island-list__dot"
              style={{ backgroundColor: row.color }}
            />
            <span className="island-list__name">{row.name}</span>
            <span className="island-list__stats">
              <span className="island-list__letters">{`港の手紙 ${String(row.portCount)} 通`}</span>
              <span className="island-list__satisfaction">
                {`満足度 ${row.satisfaction === null ? "—" : String(row.satisfaction)}`}
              </span>
            </span>
          </button>
        </li>
      ))}
    </ul>
  );
}
