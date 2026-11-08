import { describe, expect, it } from "vitest";

import { connectedGroups, partitionOf } from "./partition.ts";
import type { Link } from "./partition.ts";

function open(a: number, b: number): Link {
  return { a, b, open: true };
}
function closed(a: number, b: number): Link {
  return { a, b, open: false };
}

// 6 島の環（1-2-3-4-5-6-1）。
const SIX = [1, 2, 3, 4, 5, 6];

describe("connectedGroups", () => {
  it("PT-1 止まった航路で 2 つに分かれる", () => {
    expect(connectedGroups([1, 2, 3, 4], [open(1, 2), closed(2, 3), open(3, 4)])).toEqual([
      [1, 2],
      [3, 4],
    ]);
  });

  it("PT-2 迂回があれば 1 グループ", () => {
    expect(connectedGroups([1, 2, 3], [closed(1, 2), open(1, 3), open(3, 2)])).toEqual([[1, 2, 3]]);
  });

  it("PT-3 入力の順に関係なく、グループは最小の ID の昇順・中は ID の昇順", () => {
    const links = [open(6, 2), open(5, 1), open(3, 5), closed(1, 2), open(4, 6)];
    expect(connectedGroups([6, 3, 5, 2, 1, 4], links)).toEqual([
      [1, 3, 5],
      [2, 4, 6],
    ]);
    expect(connectedGroups([1, 2, 3, 4, 5, 6], [...links].reverse())).toEqual([
      [1, 3, 5],
      [2, 4, 6],
    ]);
  });
});

describe("partitionOf", () => {
  const ring = (blocked: readonly string[]): Link[] =>
    [
      [1, 2],
      [2, 3],
      [3, 4],
      [4, 5],
      [5, 6],
      [6, 1],
    ].map(([a = 0, b = 0]) => ({ a, b, open: !blocked.includes(`${String(a)}-${String(b)}`) }));

  it("PT-4 6 島が 2 と 4 に分かれる", () => {
    // 1-2 と 3-4 が止まる → {1,4,5,6} と {2,3}。
    expect(partitionOf(SIX, ring(["1-2", "3-4"]))).toEqual({
      total: 6,
      groups: [
        { label: "A", islandIds: [1, 4, 5, 6], size: 4, majority: true },
        { label: "B", islandIds: [2, 3], size: 2, majority: false },
      ],
    });
  });

  it("PT-4 グループの記号は A・B…、27 個目以降は G27…", () => {
    const ids = Array.from({ length: 28 }, (_, i) => i + 1);
    const links = ids.slice(1).map((id) => closed(id - 1, id));
    const view = partitionOf(ids, links);
    expect(view?.groups.map((g) => g.label).slice(0, 3)).toEqual(["A", "B", "C"]);
    expect(view?.groups[25]?.label).toBe("Z");
    expect(view?.groups[26]?.label).toBe("G27");
    expect(view?.groups[27]?.label).toBe("G28");
  });

  it("PT-5 3 対 3 ならどちらも過半数でない", () => {
    const view = partitionOf(SIX, ring(["3-4", "6-1"]));
    expect(view?.groups.map((g) => [g.islandIds, g.majority])).toEqual([
      [[1, 2, 3], false],
      [[4, 5, 6], false],
    ]);
  });

  it("PT-6 すべて open なら null・もともと航路の無い島だけなら null・さらに切れたら表示する", () => {
    expect(partitionOf(SIX, ring([]))).toBeNull();
    // 1 か所だけ止まっても環なので迂回できる。
    expect(partitionOf(SIX, ring(["1-2"]))).toBeNull();

    // 島 7 には航路が無い（すべて open でも 2 グループ）。
    const seven = [...SIX, 7];
    expect(partitionOf(seven, ring([]))).toBeNull();
    const view = partitionOf(seven, ring(["1-2", "3-4"]));
    expect(view?.total).toBe(7);
    expect(view?.groups.map((g) => [g.label, g.islandIds, g.majority])).toEqual([
      ["A", [1, 4, 5, 6], true],
      ["B", [2, 3], false],
      ["C", [7], false],
    ]);
  });

  it("PT-7 islandIds に無い島の Link は無視する", () => {
    // 9 を経由すればつながるが、9 は islandIds に無い。
    const links = [closed(1, 2), open(1, 9), open(9, 2)];
    expect(connectedGroups([1, 2], links)).toEqual([[1], [2]]);
    expect(partitionOf([1, 2], links)).toEqual({
      total: 2,
      groups: [
        { label: "A", islandIds: [1], size: 1, majority: false },
        { label: "B", islandIds: [2], size: 1, majority: false },
      ],
    });
  });

  it("島が無ければ null", () => {
    expect(partitionOf([], [])).toBeNull();
    expect(connectedGroups([], [])).toEqual([]);
  });
});
