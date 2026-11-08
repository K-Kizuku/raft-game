// エンジニアパネルの「Raft ログ」「Progress」の節（T-711・FR-ENG-05・FR-ENG-06・ゲームデザイン §7.4）。
// データは props で受けず、ストアの nodeDetail（node.detail）から読む（ADR-0010・DesignDoc §13.3）。
// 値はサーバーのまま出し、Leader や Commit をここで決めない（ADR-0009）。表示の決まりは nodeDetailView.ts の先頭。
import { useMemo } from "react";
import type { ReactElement } from "react";

import { gameStore, useGameStore } from "../../state/index.ts";
import type { World } from "../../state/index.ts";
import { nodeName } from "./engineerView.ts";
import { nodeDetailView } from "./nodeDetailView.ts";
import type { LogRow, NodeDetailView, ProgressRow } from "./nodeDetailView.ts";

const LOG_COLUMNS = ["Index", "Term", "Type", "Summary", "Commit"];
const PROGRESS_COLUMNS = ["Node", "Match", "Next", "State", "Active", "遅れ"];

export function NodeDetailSections(props: {
  world: World;
  targetId: number | null;
}): ReactElement | null {
  const { world, targetId } = props;
  const detail = useGameStore((s) => s.nodeDetail);
  const view = useMemo(
    () => (detail !== null && detail.id === targetId ? nodeDetailView(world, detail) : null),
    [world, detail, targetId],
  );

  if (view === null) {
    return (
      <section className="engineer-section">
        <h3>Raft ログ・Progress</h3>
        <p>ノードの詳細はまだ届いていません（サーバーにつながっているときだけ表示します）。</p>
      </section>
    );
  }

  return (
    <>
      <LogSection view={view} />
      <ProgressSection world={world} view={view} />
    </>
  );
}

function HeaderRow({ columns }: { columns: readonly string[] }): ReactElement {
  return (
    <thead>
      <tr>
        {columns.map((name) => (
          <th key={name} scope="col">
            {name}
          </th>
        ))}
      </tr>
    </thead>
  );
}

// Commit 済みの最大の index（行の中に無ければ null）。
function maxCommittedIndex(log: readonly LogRow[]): number | null {
  let max: number | null = null;
  for (const row of log) {
    if (row.committed && (max === null || row.index > max)) max = row.index;
  }
  return max;
}

function LogSection({ view }: { view: NodeDetailView }): ReactElement {
  const rows = [...view.log].reverse();
  const boundary = view.commit === 0 ? null : maxCommittedIndex(view.log);
  // 区切りを入れる行（描画の順で、Commit 済みの最大の index の最初の行）。無ければ -1。
  const boundaryAt =
    boundary === null ? -1 : rows.findIndex((row) => row.committed && row.index === boundary);
  return (
    <section className="engineer-section">
      <h3>
        Raft ログ: {view.nodeId} {view.name}
      </h3>
      {rows.length === 0 ? (
        <p>ログはまだありません。</p>
      ) : (
        <>
          <table aria-label="Raft ログ" className="engineer-table engineer-log">
            <HeaderRow columns={LOG_COLUMNS} />
            <tbody>
              {rows.flatMap((row, i) => {
                const cells = (
                  <tr
                    key={`${String(row.index)}-${String(i)}`}
                    data-index={row.index}
                    data-committed={row.committed ? "true" : "false"}
                  >
                    <td>{row.index}</td>
                    <td>{row.term}</td>
                    <td>{row.type}</td>
                    <td>{row.summary}</td>
                    <td>{row.committed ? "✓" : "未"}</td>
                  </tr>
                );
                if (i !== boundaryAt) return [cells];
                return [
                  <tr key={`commit-${String(i)}`} className="engineer-log-commit">
                    <td colSpan={5}>Commit Index {view.commit}</td>
                  </tr>,
                  cells,
                ];
              })}
            </tbody>
          </table>
          {view.logTruncated && (
            <p>{`古いエントリは省いています（末尾の ${String(view.log.length)} 件）。`}</p>
          )}
        </>
      )}
    </section>
  );
}

function ProgressSection({ world, view }: { world: World; view: NodeDetailView }): ReactElement {
  return (
    <section className="engineer-section">
      <h3>Progress（Leader から見た複製の進み具合）</h3>
      {view.progress === null ? (
        <>
          <p>このノードは Leader ではないので、Progress はありません。</p>
          {view.leaderId !== null && <LeaderButton world={world} leaderId={view.leaderId} />}
        </>
      ) : view.progress.length === 0 ? (
        <p>Follower はいません。</p>
      ) : (
        <ProgressTable rows={view.progress} />
      )}
    </section>
  );
}

function LeaderButton({ world, leaderId }: { world: World; leaderId: number }): ReactElement {
  return (
    <button
      type="button"
      onClick={() => {
        gameStore.getState().selectIsland(leaderId);
      }}
    >
      Leader（{leaderId} {nodeName(world, leaderId)}）を見る
    </button>
  );
}

function ProgressTable({ rows }: { rows: readonly ProgressRow[] }): ReactElement {
  return (
    <table aria-label="Progress" className="engineer-table engineer-progress">
      <HeaderRow columns={PROGRESS_COLUMNS} />
      <tbody>
        {rows.map((row, i) => (
          <tr
            key={`${String(row.id)}-${String(i)}`}
            data-node={row.id}
            data-inactive={row.recentActive ? undefined : "true"}
          >
            <td>
              <button
                type="button"
                className="engineer-node"
                onClick={() => {
                  gameStore.getState().selectIsland(row.id);
                }}
              >
                {row.id} {row.name}
              </button>
            </td>
            <td>{row.match}</td>
            <td>{row.next}</td>
            <td>{row.state}</td>
            <td>{row.recentActive ? "yes" : "no"}</td>
            <td>{row.behind}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}
