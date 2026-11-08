// エンジニアパネルの「Raft ログ」「Progress」のタブの中身（T-711・T-438・FR-ENG-05・FR-ENG-06・ゲームデザイン §7.4）。
// データは props で受けず、ストアの nodeDetail（node.detail）から読む（ADR-0010・DesignDoc §13.3）。
// 値はサーバーのまま出し、Leader や Commit をここで決めない（ADR-0009）。表示の決まりは nodeDetailView.ts の先頭。
// Raft ログは、パネルの中を巻き取らずに収まるよう、新しい順に ENGINEER_LOG_LIMIT 件まで出す（T-438）。
import { useMemo } from "react";
import type { ReactElement } from "react";

import { gameStore, useGameStore } from "../../state/index.ts";
import type { World } from "../../state/index.ts";
import { nodeName } from "./engineerView.ts";
import { ENGINEER_LOG_LIMIT, nodeDetailView, newestLogRows } from "./nodeDetailView.ts";
import type { LogRow, NodeDetailView, ProgressRow } from "./nodeDetailView.ts";

const LOG_COLUMNS = ["Index", "Term", "Type", "Summary", "Commit"];
const PROGRESS_COLUMNS = ["Node", "Match", "Next", "State", "Active", "遅れ"];
const NOT_YET = "ノードの詳細はまだ届いていません（サーバーにつながっているときだけ表示します）。";

type DetailProps = { world: World; targetId: number | null };

// 選んでいるノードの詳細（届いていない・別のノードの詳細なら null）。
function useNodeDetailView(world: World, targetId: number | null): NodeDetailView | null {
  const detail = useGameStore((s) => s.nodeDetail);
  return useMemo(
    () => (detail !== null && detail.id === targetId ? nodeDetailView(world, detail) : null),
    [world, detail, targetId],
  );
}

// 「Raft ログ」のタブの中身。
export function RaftLogSection({ world, targetId }: DetailProps): ReactElement {
  const view = useNodeDetailView(world, targetId);
  if (view === null) return <NotYet title="Raft ログ" />;
  return <LogSection view={view} />;
}

// 「Progress」のタブの中身。
export function ProgressDetailSection({ world, targetId }: DetailProps): ReactElement {
  const view = useNodeDetailView(world, targetId);
  if (view === null) return <NotYet title="Progress" />;
  return <ProgressSection world={world} view={view} />;
}

function NotYet({ title }: { title: string }): ReactElement {
  return (
    <section className="engineer-section">
      <h3>{title}</h3>
      <p>{NOT_YET}</p>
    </section>
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

function logNote(view: NodeDetailView, shown: number): string | null {
  if (shown < view.log.length) {
    return `新しい ${String(shown)} 件だけ出しています（古いエントリは省いています）。`;
  }
  if (view.logTruncated) {
    return `古いエントリは省いています（末尾の ${String(view.log.length)} 件）。`;
  }
  return null;
}

function LogSection({ view }: { view: NodeDetailView }): ReactElement {
  const rows = newestLogRows(view.log, ENGINEER_LOG_LIMIT);
  const note = logNote(view, rows.length);
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
          {note !== null && <p className="engineer-note">{note}</p>}
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
