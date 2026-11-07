// 開会式の結果を世界の状態から読む純粋な関数（T-415・DesignDoc §5.3・docs/05-game-progression.md §2）。
// 結果はサーバーが出した node.leader_elected（ストアの leaderChanges）だけから読む。
// node.state から Leader を推定し直さない（ゲーム側で Leader を決めない）。
import type { LeaderChange, World } from "../../state/index.ts";

export type OpeningResult = Readonly<{
  key: string; // `${world.session.id}:${elected.seq}`。演出を 1 回だけ出すための鍵
  leaderId: number;
  term: number;
  islandName: string; // stage に無い島は「島 {id}」
  color: string | null; // stage に無い島は null
  changes: readonly LeaderChange[]; // t === 0 の leaderChanges（古い順のまま）
}>;

// islandLabel は stage の島の名前か「島 {id}」を返す。
export function islandLabel(world: World, id: number): string {
  return world.stage.islands[id]?.name ?? `島 ${id}`;
}

// openingResult は時刻 0 の leaderChanges の最後の elected を開会式の当選として返す。
// 航行の最初の Raft の tick は tickMs（> 0）の後なので、時刻 0 の elected は開会式のものだけ。
// スナップショットで世界を置き換えると leaderChanges は [] になるので、再接続・復元の後は null。
export function openingResult(world: World | null): OpeningResult | null {
  if (world === null || world.phase === "intro") return null;
  const changes = world.leaderChanges.filter((c) => c.t === 0);
  let elected: LeaderChange | null = null;
  for (const change of changes) {
    if (change.kind === "elected") elected = change;
  }
  if (elected === null) return null;
  const island = world.stage.islands[elected.id];
  return {
    key: `${world.session.id}:${elected.seq}`,
    leaderId: elected.id,
    term: elected.term,
    islandName: islandLabel(world, elected.id),
    color: island?.color ?? null,
    changes,
  };
}
