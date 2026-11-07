// ネットワーク分断の表示（T-704・FR-ENG-08）。いま通れる航路で島をつないだ連結成分を求める純粋な関数。
// 航路のつながりを示すだけで、どのグループで代表が選ばれるか・記録が確定するかは決めない
// （実際の代表・確定は届いた node.state のまま。CLAUDE.md「ゲーム側で Leader や Commit の結果を決めない」）。
// 結果の順は島の ID で決め、Map・Set の走査の順に頼らない。

export type Link = Readonly<{ a: number; b: number; open: boolean }>;

export type PartitionGroup = Readonly<{
  label: string; // "A"・"B"…（グループの順。27 個目以降は "G27" のように番号）
  islandIds: readonly number[];
  size: number;
  majority: boolean; // size * 2 > total
}>;
export type PartitionView = Readonly<{ total: number; groups: readonly PartitionGroup[] }>;

const LETTERS = 26;

function uniqueSorted(islandIds: readonly number[]): number[] {
  const sorted = [...islandIds].sort((x, y) => x - y);
  return sorted.filter((id, i) => i === 0 || sorted[i - 1] !== id);
}

// Union-Find（配列の添字で持つ）。onlyOpen が false なら、すべての航路を通れるとみなす。
function groupsOf(ids: readonly number[], links: readonly Link[], onlyOpen: boolean): number[][] {
  const index = new Map<number, number>();
  ids.forEach((id, i) => index.set(id, i));
  const parent = ids.map((_, i) => i);
  const find = (i: number): number => {
    let root = i;
    while (parent[root] !== root) root = parent[root] ?? root;
    let cur = i;
    while (parent[cur] !== root) {
      const next = parent[cur] ?? root;
      parent[cur] = root;
      cur = next;
    }
    return root;
  };
  for (const link of links) {
    if (onlyOpen && !link.open) continue;
    const ia = index.get(link.a);
    const ib = index.get(link.b);
    if (ia === undefined || ib === undefined) continue; // 両端のどちらかが islandIds に無い
    const ra = find(ia);
    const rb = find(ib);
    // 小さい添字（= 小さい ID）を根にする。
    if (ra < rb) parent[rb] = ra;
    else if (rb < ra) parent[ra] = rb;
  }
  // ID の昇順に見ていくので、グループは最小の ID の昇順・中は ID の昇順になる。
  const groupOfRoot: number[] = ids.map(() => -1);
  const groups: number[][] = [];
  ids.forEach((id, i) => {
    const root = find(i);
    let g = groupOfRoot[root] ?? -1;
    if (g < 0) {
      g = groups.length;
      groupOfRoot[root] = g;
      groups.push([]);
    }
    groups[g]?.push(id);
  });
  return groups;
}

// islandIds の島を、open な Link でつないだ連結成分。各グループは島の ID の昇順、グループは最小の ID の昇順。
export function connectedGroups(islandIds: readonly number[], links: readonly Link[]): number[][] {
  return groupsOf(uniqueSorted(islandIds), links, true);
}

function groupLabel(index: number): string {
  return index < LETTERS ? String.fromCharCode(65 + index) : `G${String(index + 1)}`;
}

// 分かれていなければ null。「分かれている」= すべての航路を通れるとしたときのグループの数より、今のグループの数が多い。
export function partitionOf(
  islandIds: readonly number[],
  links: readonly Link[],
): PartitionView | null {
  const ids = uniqueSorted(islandIds);
  const now = groupsOf(ids, links, true);
  const all = groupsOf(ids, links, false);
  if (now.length <= all.length) return null;
  const total = ids.length;
  return {
    total,
    groups: now.map((group, i) => ({
      label: groupLabel(i),
      islandIds: group,
      size: group.length,
      majority: group.length * 2 > total,
    })),
  };
}
