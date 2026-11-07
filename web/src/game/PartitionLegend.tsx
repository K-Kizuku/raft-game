// 分断の凡例（T-704・FR-ENG-08）。嵐で島が分かれている間だけ、マップの左上の隅に、グループ（A・B…）ごとの
// 島・島の数・過半数の島がつながっているかを文字で出す。エンジニアモードに関係なく出す（docs/04 §7.4）。
// 航路のつながりから求めた表示で、どのグループで代表が選ばれるか・記録が確定するかは決めない。
// 嵐の判定はマップと同じ stormOf（scene.ts）を使い、時刻は推定時刻（useSimNow）だけを使う。
import { Fragment } from "react";
import type { ReactElement } from "react";

import type { World } from "../state/index.ts";
import { useGameStore } from "../state/index.ts";
import { useSimNow } from "../ui/hud/useSimNow.ts";
import { RaftTerm } from "../ui/terms/RaftTerm.tsx";
import { partitionOf } from "./model/partition.ts";
import type { Link, PartitionView } from "./model/partition.ts";
import { stormOf } from "./model/scene.ts";
import "./PartitionLegend.css";

function partitionOfWorld(world: World, simNow: number): PartitionView | null {
  const { stage } = world;
  const present = stage.islandIds.filter((id) => stage.islands[id] !== undefined);
  const links: Link[] = [];
  for (const id of stage.routeIds) {
    const route = stage.routes[id];
    if (route === undefined) continue;
    links.push({ a: route.a, b: route.b, open: stormOf(world, id, simNow) !== "blocked" });
  }
  return partitionOf(present, links);
}

export function PartitionLegend(): ReactElement | null {
  const world = useGameStore((s) => s.world);
  const estimated = useSimNow();
  if (world === null) return null;
  const view = partitionOfWorld(world, estimated ?? world.clock.simTime);
  if (view === null) return null;
  const { islands } = world.stage;
  return (
    <section aria-label="島のつながり" className="map-partition">
      <h2 className="map-partition__title">
        <RaftTerm id="partition">嵐</RaftTerm>で島が分かれています
      </h2>
      <ul className="map-partition__groups">
        {view.groups.map((group) => (
          <li
            key={group.label}
            className={`map-partition__group ${
              group.majority ? "map-partition__group--majority" : "map-partition__group--minority"
            }`}
          >
            <span className="map-partition__badge">{group.label}</span>
            <span className="map-partition__islands">
              {group.islandIds.map((id, i) => {
                const island = islands[id];
                return (
                  <Fragment key={id}>
                    {i > 0 && "・"}
                    <span className="map-partition__island">
                      <span
                        className="map-partition__dot"
                        aria-hidden="true"
                        style={{ background: island?.color }}
                      />
                      {island?.name ?? `島 ${String(id)}`}
                    </span>
                  </Fragment>
                );
              })}
            </span>
            <span className="map-partition__size">
              {group.size}/{view.total} 島
            </span>
            {group.majority && (
              <span className="map-partition__quorum">
                <span aria-hidden="true">✓ </span>
                <RaftTerm id="quorum">過半数</RaftTerm>の島がつながっている
              </span>
            )}
          </li>
        ))}
      </ul>
    </section>
  );
}
