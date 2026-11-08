// エンジニアパネルの「Raft ログ」「Progress」の節の行を作る純粋な関数（T-711・FR-ENG-05・FR-ENG-06・ゲームデザイン §7.4）。
// 値はサーバーの node.detail（プロトコル §5.4）のまま出し、Leader や Commit をここで決めない（ADR-0009）。
//
// 表示の決まり:
// - ログ（log）は index の昇順に並べる（届いた順が昇順でなければ昇順に並べ直す）。表は新しいエントリを上にする
//   （描画だけ降順。LogRow の列は昇順のまま）。
// - committed・commit はサーバーの値のまま。index <= commit を画面で計算し直さない。
// - 表では Commit 済みの最大の index の行の上に「Commit Index <commit>」の区切りの行を入れる
//   （commit が 0 か、行の中に Commit 済みが無いときは入れない）。
// - ログの最初の index が 1 より大きいときは、古いエントリが省かれている（「末尾の 50 件」）。
// - summary が空なら "—"。知らない type・state・role は値のまま出す（プロトコル §8）。
// - Progress はサーバーの順（自分を除く Follower の ID の昇順）のまま。無ければ null（Leader でない）。
// - 「遅れ」（behind）は lastIndex − match（0 未満なら 0）。Leader から見てまだ複製できていないエントリの数で、
//   表示のための差だけ（判定には使わない）。
import type { NodeDetail } from "../../protocol/index.ts";
import type { World } from "../../state/index.ts";
import { nodeName, roleLabel } from "./engineerView.ts";

export type LogRow = Readonly<{
  index: number;
  term: number;
  type: string; // "normal" | "conf_change" | "conf_change_v2"（知らない値はそのまま）
  summary: string; // サーバーの値のまま。空なら "—"
  committed: boolean; // サーバーの値のまま
}>;

export type ProgressRow = Readonly<{
  id: number;
  name: string; // nodeName(world, id)
  match: number;
  next: number;
  state: string; // "Probe" | "Replicate" | "Snapshot"。知らない値はそのまま
  recentActive: boolean;
  behind: number; // detail.lastIndex − match（0 未満なら 0）
}>;

export type NodeDetailView = Readonly<{
  nodeId: number;
  name: string;
  role: string; // roleLabel(detail.role)
  log: readonly LogRow[]; // index の昇順
  logTruncated: boolean; // detail.log の最初の index が 1 より大きい
  commit: number; // detail.commit
  progress: readonly ProgressRow[] | null; // detail.progress が無ければ null（Leader でない）
  leaderId: number | null; // detail.lead が 0 でなく、自分でなければその ID
}>;

const NONE = "—";

export const PROGRESS_STATE_LABELS: Readonly<Record<string, string>> = Object.freeze({
  probe: "Probe",
  replicate: "Replicate",
  snapshot: "Snapshot",
});

function progressStateLabel(state: string): string {
  return Object.hasOwn(PROGRESS_STATE_LABELS, state)
    ? (PROGRESS_STATE_LABELS[state] ?? state)
    : state;
}

function isAscending(rows: readonly LogRow[]): boolean {
  for (let i = 1; i < rows.length; i++) {
    const prev = rows[i - 1];
    const cur = rows[i];
    if (prev !== undefined && cur !== undefined && prev.index > cur.index) return false;
  }
  return true;
}

export function nodeDetailView(world: World, detail: NodeDetail): NodeDetailView {
  const rows: LogRow[] = detail.log.map((entry) => ({
    index: entry.index,
    term: entry.term,
    type: entry.type,
    summary: entry.summary === "" ? NONE : entry.summary,
    committed: entry.committed,
  }));
  const log = isAscending(rows) ? rows : [...rows].sort((a, b) => a.index - b.index);
  const first = log[0];
  const progress =
    detail.progress === undefined
      ? null
      : detail.progress.map((p) => ({
          id: p.id,
          name: nodeName(world, p.id),
          match: p.match,
          next: p.next,
          state: progressStateLabel(p.state),
          recentActive: p.recentActive,
          behind: Math.max(0, detail.lastIndex - p.match),
        }));
  return {
    nodeId: detail.id,
    name: nodeName(world, detail.id),
    role: roleLabel(detail.role),
    log,
    logTruncated: first !== undefined && first.index > 1,
    commit: detail.commit,
    progress,
    leaderId: detail.lead !== 0 && detail.lead !== detail.id ? detail.lead : null,
  };
}
