// ゲーム画面のスロット「島の詳細」（T-406・ゲームデザイン §7.2 の右・FR-UI-04）。
// マップで選んだ島の役割・満足度・港の手紙（宛先別の山と 1 通ずつのアイコン）・依頼を出す。
// 値はストア（T-403）に届いたものをそのまま出し、役割や依頼の状態をここで決めない（ADR-0009）。
import { useMemo } from "react";
import type { ReactElement } from "react";

import type { Role } from "../../protocol/index.ts";
import { portPiles, useGameStore } from "../../state/index.ts";
import type { World } from "../../state/index.ts";
import { formatRemaining } from "../hud/format.ts";
import { islandName } from "../hud/noticeText.ts";
import { useSimNow } from "../hud/useSimNow.ts";
import { LetterIcon } from "../letters/LetterIcon.tsx";
import { RaftTerm } from "../terms/RaftTerm.tsx";
import { IslandList } from "./IslandList.tsx";
import { isFinished, islandRequests, requestStatusView } from "./requestView.ts";
import "./IslandPanel.css";

// 山の中で 1 通ずつ出すアイコンの数（画面のための値）。残りは「+k」。
const PILE_ICON_LIMIT = 10;
// 宛先の島がステージに無いときの縁の色。
const UNKNOWN_ISLAND_COLOR = "#9CA3AF";

export function IslandPanel(): ReactElement {
  const world = useGameStore((s) => s.world);
  const islandId = useGameStore((s) => s.selection.islandId);
  if (world === null) {
    return <p>島の詳細（準備中）</p>;
  }
  if (islandId === null) {
    // 何も選んでいないときは、島の一覧（T-426）を出す。押すとその島を選ぶ。
    return (
      <div className="island-panel">
        <p className="island-hint">マップの島を選ぶと、港の手紙と依頼がここに出ます。</p>
        <IslandList world={world} />
      </div>
    );
  }
  return <IslandDetail world={world} islandId={islandId} />;
}

function RoleLabel({ role }: { role: Role }): ReactElement {
  switch (role) {
    case "leader":
      return (
        <>
          <span aria-hidden="true">👑</span> <RaftTerm id="leader" />
        </>
      );
    case "candidate":
      return (
        <>
          <span aria-hidden="true">📣</span> <RaftTerm id="candidate" />
        </>
      );
    case "pre_candidate":
      return (
        <>
          <span aria-hidden="true">💭</span> <RaftTerm id="preCandidate" />
        </>
      );
    case "follower":
      return <RaftTerm id="follower" />;
  }
}

function IslandDetail({ world, islandId }: { world: World; islandId: number }): ReactElement {
  const simNow = useSimNow() ?? world.clock.simTime;
  const piles = useMemo(() => portPiles(world, islandId), [world, islandId]);
  const requests = useMemo(() => islandRequests(world, islandId), [world, islandId]);
  const island = Object.hasOwn(world.stage.islands, islandId)
    ? world.stage.islands[islandId]
    : undefined;
  const node = Object.hasOwn(world.nodes, islandId) ? world.nodes[islandId] : undefined;
  const state = Object.hasOwn(world.islandStates, islandId)
    ? world.islandStates[islandId]
    : undefined;

  return (
    <div className="island-panel">
      <header className="island-header">
        {island !== undefined && (
          <span
            aria-hidden="true"
            className="island-color-dot"
            style={{ backgroundColor: island.color }}
          />
        )}
        <h2>{islandName(world, islandId)}</h2>
        {island !== undefined && island.municipality !== "" && (
          <small className="island-municipality">{island.municipality}</small>
        )}
      </header>

      {node !== undefined && (
        <p className="island-role">
          <RoleLabel role={node.role} />
        </p>
      )}

      <p className="island-satisfaction">
        満足度
        {state === undefined ? (
          " —"
        ) : (
          <>
            <meter min={0} max={100} value={state.satisfaction} aria-label="満足度" />
            {String(state.satisfaction)}
          </>
        )}
      </p>

      <section className="island-section">
        <h3>
          港の<RaftTerm id="letter">手紙</RaftTerm>
        </h3>
        {piles.length === 0 ? (
          <p>港に手紙はありません。</p>
        ) : (
          <ul className="island-piles">
            {piles.map((pile) => {
              const to = Object.hasOwn(world.stage.islands, pile.to)
                ? world.stage.islands[pile.to]
                : undefined;
              const color = to?.color ?? UNKNOWN_ISLAND_COLOR;
              const rest = pile.letters.length - PILE_ICON_LIMIT;
              return (
                <li key={pile.to} className="island-pile" data-to={pile.to}>
                  <span className="island-pile-to">→ {islandName(world, pile.to)}</span>
                  <span className="island-pile-count">{`${String(pile.letters.length)} 通`}</span>
                  <span className="island-pile-icons">
                    {pile.letters.slice(0, PILE_ICON_LIMIT).map((letter) => (
                      <LetterIcon key={letter.id} letter={letter} borderColor={color} />
                    ))}
                    {rest > 0 && <span className="island-pile-rest">{`+${String(rest)}`}</span>}
                  </span>
                </li>
              );
            })}
          </ul>
        )}
      </section>

      <section className="island-section">
        <h3>依頼</h3>
        {requests.length === 0 ? (
          <p>依頼はありません。</p>
        ) : (
          <ul className="island-requests">
            {requests.map((request) => {
              const status = requestStatusView(request.status);
              const resource =
                world.stage.resources.find((r) => r.id === request.resource)?.label ?? request.kind;
              const showRemaining = !isFinished(request) && !request.expired;
              return (
                <li key={request.id} className="island-request" data-request={request.id}>
                  <span aria-hidden="true" className="island-request-mark">
                    {request.critical ? "★" : "・"}
                  </span>
                  {request.critical && <span className="visually-hidden">重要</span>}
                  <span className="island-request-resource">{resource}</span>
                  <span className="island-request-status">
                    <span aria-hidden="true">{status.icon}</span>
                    {status.text}
                  </span>
                  {showRemaining && (
                    <span className="island-request-remaining">
                      あと {formatRemaining(request.deadline - simNow)}
                    </span>
                  )}
                  {request.expired && <span className="island-request-expired">⌛期限切れ</span>}
                </li>
              );
            })}
          </ul>
        )}
      </section>
    </div>
  );
}
