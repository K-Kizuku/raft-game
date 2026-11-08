import { describe, expect, it } from "vitest";

import { labelScale } from "./art/labelScale.ts";
import {
  FERRY_LABELS,
  ferryTextBoxes,
  layoutLabels,
  overlapArea,
  pickCandidate,
  placementCost,
} from "./placement.ts";
import type { Box, LabelInput, Placed } from "./placement.ts";
import { MAP_STYLE } from "./style.ts";
import { layoutMap, toScreen } from "./viewport.ts";
import type { Point, Size } from "./viewport.ts";

// リポジトリのステージ定義（data/stages/*.yaml）と島の名前（data/islands.json）を node:fs で読む。
// src の tsconfig は Node の型を読まないので、使う関数の型だけを書く。
type NodeFs = { readFileSync(path: URL, encoding: "utf8"): string };
const nodeFs = "node:fs";
const { readFileSync } = (await import(/* @vite-ignore */ nodeFs)) as NodeFs;
const DATA = new URL("../../../../data/", import.meta.url);

type StageData = Readonly<{
  islands: readonly { ref: string; name: string; layout: Point }[];
  ferries: readonly { id: string; name: string; home: string; capacity: number }[];
}>;

const ISLAND_NAMES: ReadonlyMap<string, string> = new Map(
  (
    JSON.parse(readFileSync(new URL("islands.json", DATA), "utf8")) as {
      islands: { id: string; name: string }[];
    }
  ).islands.map((i) => [i.id, i.name]),
);

function loadStage(id: string): StageData {
  const yaml = readFileSync(new URL(`stages/${id}.yaml`, DATA), "utf8");
  const islands = [
    ...yaml.matchAll(/- ref: ([a-z-]+)[^\n]*\n\s+layout: \{ x: (-?\d+), y: (-?\d+) \}/g),
  ].map((m) => ({
    ref: m[1] ?? "",
    name: ISLAND_NAMES.get(m[1] ?? "") ?? "",
    layout: { x: Number(m[2]), y: Number(m[3]) },
  }));
  const ferries = [
    ...yaml.matchAll(/- \{ id: (\w+), name: ([^,]+), home: ([a-z-]+), capacity: (\d+)/g),
  ].map((m) => ({ id: m[1] ?? "", name: m[2] ?? "", home: m[3] ?? "", capacity: Number(m[4]) }));
  return { islands, ferries };
}

// 1470×860 のブラウザの表示領域でのマップの大きさ（T-428 のスクリーンショットで測った値）と文字の倍率。
const MAP_1470: Size = { width: 1081, height: 559 };
const LS_1470 = labelScale(1470);
// 1280×720 のときのマップの大きさ（同じく測った値）。
const MAP_1280: Size = { width: 937, height: 419 };
const LS_1280 = labelScale(1280);

const STAGES = ["stage1", "stage2", "stage3", "demo-izu", "demo-goto"] as const;

// 船の置き方: home は母港に停泊、all は全部の船を島 all に停泊。piles が false なら手紙の山は無い。
type Scenario = Readonly<{ docked: "home" | { all: number }; piles?: boolean }>;

function inputOf(stage: StageData, size: Size, ls: number, scenario: Scenario): LabelInput {
  const { transform, markScale } = layoutMap(
    stage.islands.map((i) => i.layout),
    size,
  );
  const islands = stage.islands.map((isl, i) => ({
    id: i + 1,
    at: toScreen(transform, isl.layout),
    name: isl.name,
  }));
  const idOf = (ref: string): number => stage.islands.findIndex((i) => i.ref === ref) + 1;
  // どの島にも、ほかのすべての島への手紙の山がある（いちばん混んだとき）。
  const piles =
    scenario.piles === false
      ? []
      : islands.flatMap((a) =>
          islands
            .filter((b) => b.id !== a.id)
            .map((b) => ({ islandId: a.id, to: b.id, known: true })),
        );
  const docked = stage.ferries.map((f) => ({
    id: f.id,
    islandId: scenario.docked === "home" ? idOf(f.home) : scenario.docked.all,
    text: { name: f.name, cargo: `${String(f.capacity)}/${String(f.capacity)}`, eta: null },
  }));
  return { size, k: markScale, ls, islands, piles, docked, sailing: [] };
}

// 重なっている箱の組（同じ持ち主どうし・停泊中の船の絵と自分の島の絵は数えない）。
function overlaps(placed: readonly Placed[], allowed: (a: Placed, b: Placed) => boolean): string[] {
  const out: string[] = [];
  for (let i = 0; i < placed.length; i++) {
    for (let j = i + 1; j < placed.length; j++) {
      const a = placed[i];
      const b = placed[j];
      if (a === undefined || b === undefined || a.owner === b.owner) continue;
      if (allowed(a, b) || allowed(b, a)) continue;
      const area = overlapArea(a.box, b.box);
      if (area > 0.5) out.push(`${a.owner} × ${b.owner}（${area.toFixed(1)}）`);
    }
  }
  return out;
}

// 停泊中の船の絵（箱の最初の 1 つ）と、その船が停泊する島の絵の重なりは数えない。
function dockedAllowed(input: LabelInput, placed: readonly Placed[]) {
  const shipBoxes = new Set<Box>();
  const seen = new Set<string>();
  for (const p of placed) {
    if (p.owner.startsWith("ferry:") && !seen.has(p.owner)) {
      seen.add(p.owner);
      shipBoxes.add(p.box);
    }
  }
  const islandOf = new Map(
    input.docked.map((f) => [`ferry:${f.id}`, `island:${String(f.islandId)}`]),
  );
  return (a: Placed, b: Placed): boolean =>
    shipBoxes.has(a.box) && islandOf.get(a.owner) === b.owner;
}

function inside(box: Box, size: Size): boolean {
  return box.left >= 0 && box.top >= 0 && box.right <= size.width && box.bottom <= size.height;
}

describe("pickCandidate", () => {
  const placed: Placed[] = [{ box: { left: 0, top: 0, right: 10, bottom: 10 }, owner: "x" }];
  const at = (x: number): Box => ({ left: x, top: 0, right: x + 10, bottom: 10 });

  it("PL-1 重なる候補を飛ばし、重ならない最初の候補を選ぶ", () => {
    const chosen = pickCandidate(
      [
        { value: "a", boxes: [at(5)] },
        { value: "b", boxes: [at(10)] },
        { value: "c", boxes: [at(30)] },
      ],
      placed,
      null,
    );
    // 辺が接するだけなら重ならない。
    expect(chosen?.value).toBe("b");
  });

  it("PL-2 どれも重なるなら重なりがいちばん小さい候補（同じなら先のもの）", () => {
    const chosen = pickCandidate(
      [
        { value: "a", boxes: [at(2)] },
        { value: "b", boxes: [at(8)] },
        { value: "c", boxes: [at(-8)] },
      ],
      placed,
      null,
    );
    expect(chosen?.value).toBe("b");
    expect(pickCandidate([], placed, null)).toBeUndefined();
  });

  it("PL-3 描画領域からはみ出す候補も避ける", () => {
    const bounds: Box = { left: 0, top: 0, right: 100, bottom: 100 };
    const chosen = pickCandidate(
      [
        { value: "out", boxes: [at(95)] },
        { value: "in", boxes: [at(50)] },
      ],
      [],
      bounds,
    );
    expect(chosen?.value).toBe("in");
    expect(placementCost([at(95)], [], bounds)).toBe(50);
  });

  it("PL-4 ignore の持ち主の箱とは重なりを数えない", () => {
    expect(placementCost([at(5)], placed, null)).toBe(50);
    expect(placementCost([at(5)], placed, null, "x")).toBe(0);
  });
});

describe("ferryTextBoxes", () => {
  it("PL-5 どの置き方でも、文字の箱は船の絵（中心から 16.5 px）と重ならない", () => {
    const ship: Box = { left: -16.5, top: -16.5, right: 16.5, bottom: 16.5 };
    for (const labels of FERRY_LABELS) {
      for (const eta of [null, "あと 0:05"]) {
        const boxes = ferryTextBoxes(
          { x: 0, y: 0 },
          1,
          1.2,
          { name: "橘丸", cargo: "0/4", eta },
          labels,
        );
        expect(boxes).toHaveLength(eta === null ? 2 : 3);
        for (const box of boxes) expect(overlapArea(box, ship)).toBe(0);
      }
    }
  });
});

describe("layoutLabels", () => {
  it("PL-6 重ならないときは今までの位置（山は宛先の向き、船は右上の桟橋、名札は島の下）", () => {
    const input: LabelInput = {
      size: { width: 800, height: 600 },
      k: 1,
      ls: 1,
      islands: [
        { id: 1, at: { x: 200, y: 300 }, name: "一の島" },
        { id: 2, at: { x: 600, y: 300 }, name: "二の島" },
      ],
      // 山は二の島から一の島（左）へ、船は一の島の右上。
      piles: [{ islandId: 2, to: 1, known: true }],
      docked: [{ id: "f1", islandId: 1, text: { name: "f1丸", cargo: "0/4", eta: null } }],
      sailing: [],
    };
    const out = layoutLabels(input);
    expect(out.tags.get(1)).toEqual({ x: 0, y: 26 });
    expect(out.piles.get("2:1")).toEqual({ x: 548, y: 300 });
    expect(out.ferries.get("f1")?.labels).toBe("split");
    const f1 = out.ferries.get("f1")?.at;
    expect(f1?.x).toBeCloseTo(200 + MAP_STYLE.islandRadius + MAP_STYLE.dockOffset);
    expect(f1?.y).toBeCloseTo(300 - MAP_STYLE.islandRadius);
  });

  it("PL-7 宛先が同じ向きの 2 つの山は、重ならない位置にずらす", () => {
    const out = layoutLabels({
      size: { width: 1000, height: 600 },
      k: 1,
      ls: 1,
      islands: [
        { id: 1, at: { x: 100, y: 300 }, name: "一" },
        { id: 2, at: { x: 500, y: 300 }, name: "二" },
        { id: 3, at: { x: 900, y: 301 }, name: "三" },
      ],
      piles: [
        { islandId: 1, to: 2, known: true },
        { islandId: 1, to: 3, known: true },
      ],
      docked: [],
      sailing: [],
    });
    expect(out.piles.get("1:2")).toEqual({ x: 152, y: 300 });
    const other = out.piles.get("1:3");
    expect(other).toBeDefined();
    expect(other).not.toEqual(out.piles.get("1:2"));
    expect(overlaps(out.placed, () => false)).toEqual([]);
  });

  it("PL-8 同じ港の複数の船は、島の中心から同じ距離に等間隔に並ぶ", () => {
    const input: LabelInput = {
      size: { width: 1200, height: 800 },
      k: 1,
      ls: 1,
      islands: [{ id: 1, at: { x: 600, y: 400 }, name: "一の島" }],
      piles: [],
      docked: ["f1", "f2", "f3", "f4"].map((id) => ({
        id,
        islandId: 1,
        text: { name: `${id}丸`, cargo: "0/3", eta: null },
      })),
      sailing: [],
    };
    const out = layoutLabels(input);
    const pts = ["f1", "f2", "f3", "f4"].map((id) => out.ferries.get(id)?.at ?? { x: 0, y: 0 });
    const radii = pts.map((p) => Math.hypot(p.x - 600, p.y - 400));
    const angles = pts.map((p) => Math.atan2(p.y - 400, p.x - 600));
    for (const r of radii) expect(r).toBeCloseTo(radii[0] ?? 0);
    for (let i = 1; i < 4; i++) {
      const d = ((angles[i] ?? 0) - (angles[i - 1] ?? 0) + 4 * Math.PI) % (2 * Math.PI);
      expect(d).toBeCloseTo(Math.PI / 2);
    }
    expect(overlaps(out.placed, dockedAllowed(input, out.placed))).toEqual([]);
  });

  it("PL-9 航行中の船の文字は、島の名札と重ならない置き方を選ぶ", () => {
    const input: LabelInput = {
      size: { width: 800, height: 600 },
      k: 1,
      ls: 1,
      islands: [{ id: 1, at: { x: 400, y: 300 }, name: "一の島" }],
      piles: [],
      docked: [],
      // 名札（島の下）の少し下を通る船。split だと名前が名札に重なる。
      sailing: [
        {
          id: "s1",
          at: { x: 400, y: 370 },
          text: { name: "とても長い名前の船", cargo: "1/3", eta: "あと 0:05" },
        },
      ],
    };
    const out = layoutLabels(input);
    expect(out.ferries.get("s1")?.labels).not.toBe("split");
    expect(out.ferries.get("s1")?.at).toEqual({ x: 400, y: 370 });
    expect(overlaps(out.placed, () => false)).toEqual([]);
  });

  it("PL-10 同じ入力なら同じ結果で、入力を書き換えない", () => {
    const input = inputOf(loadStage("stage3"), MAP_1470, LS_1470, { docked: "home" });
    const frozen = JSON.stringify(input);
    expect(layoutLabels(input)).toEqual(layoutLabels(input));
    expect(JSON.stringify(input)).toBe(frozen);
  });
});

// 1470×860（基準）と 1280×720 で、ステージの島・船・札が重ならない（T-428 の AC-1・エッジケース）。
describe.each([
  { view: "1470×860", size: MAP_1470, ls: LS_1470 },
  { view: "1280×720", size: MAP_1280, ls: LS_1280 },
])("リポジトリのステージ（$view）", ({ size, ls }) => {
  it.each(STAGES)(
    "PL-11 %s: 船が母港に停泊し、どの島にもすべての宛先の山があっても重ならない",
    (id) => {
      const input = inputOf(loadStage(id), size, ls, { docked: "home" });
      const out = layoutLabels(input);
      expect(overlaps(out.placed, dockedAllowed(input, out.placed))).toEqual([]);
      for (const p of out.placed) {
        expect(inside(p.box, size), `${p.owner} がはみ出す`).toBe(true);
      }
      // 札が減っていない: 名札は島の数、山は島の組の数、船は船の数だけある。
      expect(out.tags.size).toBe(input.islands.length);
      expect(out.piles.size).toBe(input.piles.length);
      expect(out.ferries.size).toBe(input.docked.length);
    },
  );

  // 山がすべての宛先にあり、さらに 6 隻が 1 つの港に集まると、端の島では重なりを最小にするだけになる
  // （置ける場所が足りない。T-428 の PR に書いた）。ここでは船・文字・島・名札・役のマークが重ならないことを確かめる。
  it.each(["stage3", "demo-izu"])("PL-12 %s: 6 隻がどれか 1 つの港に停泊しても重ならない", (id) => {
    const stage = loadStage(id);
    for (let island = 1; island <= stage.islands.length; island++) {
      const input = inputOf(stage, size, ls, { docked: { all: island }, piles: false });
      const out = layoutLabels(input);
      expect(
        overlaps(out.placed, dockedAllowed(input, out.placed)),
        `島 ${String(island)}`,
      ).toEqual([]);
    }
  });
});
