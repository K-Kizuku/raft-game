// エンジニアモードでマップの港の山と船に重ねる、メッセージ種別の札（T-712・FR-ENG-04・ゲームデザイン §7.4）。
// 届いた手紙の値（raft.type・raft.stale）を数えるだけで、Raft の結果をここで決めない。
// 引数を書き換えず、時刻を読まない（位置は scene が描いたものをそのまま使う）。毎フレーム呼ぶので、
// 手紙の数に比例する以上の計算をしない（並べ替えは種類の数だけ）。
import { lettersOnFerry, portPiles } from "../../state/index.ts";
import type { Letter } from "../../protocol/index.ts";
import type { World } from "../../state/index.ts";
import type { MapScene } from "./scene.ts";
import type { Point } from "./viewport.ts";

export type TypeCount = Readonly<{ type: string; count: number }>;
export type RaftTag = Readonly<{
  key: string; // "pile:<islandId>:<to>" か "ferry:<ferryId>"
  at: Point; // 札の左上ではなく、札の上辺の中央
  lines: readonly string[]; // 例: ["MsgApp ×3", "MsgHeartbeat", "stale ×1"]
}>;

// 種類の行の上限（超えた分は「+n 種」の 1 行にまとめる）。表示の値でゲームの数値ではない。
export const RAFT_TAG_MAX_LINES = 3;
// 山・船の中心から札の上辺までの距離（px。描画の値）。山は封筒と数のバッジ、船は積み荷と到着までの文字の下。
export const RAFT_TAG_PILE_OFFSET = 20;
export const RAFT_TAG_FERRY_OFFSET = 44;

const REQUEST_TYPE = "依頼書";
const UNKNOWN_TYPE = "?";

// 決まった順（選挙 → 複製 → 心拍 → スナップショット）。知らない種類はこの後ろに文字列の昇順。
const KNOWN_ORDER: readonly string[] = [
  "MsgVote",
  "MsgVoteResp",
  "MsgPreVote",
  "MsgPreVoteResp",
  "MsgApp",
  "MsgAppResp",
  "MsgHeartbeat",
  "MsgHeartbeatResp",
  "MsgSnap",
];
const KNOWN_RANK = new Map(KNOWN_ORDER.map((type, i) => [type, i]));
const UNKNOWN_RANK = KNOWN_ORDER.length; // 知らない Raft の種類
const REQUEST_RANK = UNKNOWN_RANK + 1;
const MISSING_RANK = UNKNOWN_RANK + 2;

function typeOf(letter: Letter): string {
  if (letter.kind === "request") return REQUEST_TYPE;
  return letter.raft?.type ?? UNKNOWN_TYPE;
}

function rankOf(type: string, request: boolean): number {
  if (request) return REQUEST_RANK;
  if (type === UNKNOWN_TYPE) return MISSING_RANK;
  return KNOWN_RANK.get(type) ?? UNKNOWN_RANK;
}

type Bucket = { type: string; count: number; rank: number };

// 手紙をメッセージ種別ごとに数える。並びは決まった順・知らない種類（昇順）・依頼書・? の順。
export function countTypes(letters: readonly Letter[]): TypeCount[] {
  // Raft の種類と依頼書は別の名前空間（Raft の type が「依頼書」でも混ぜない）。
  const buckets = new Map<string, Bucket>();
  for (const letter of letters) {
    const type = typeOf(letter);
    const request = letter.kind === "request";
    const key = `${request ? "r" : "m"}:${type}`;
    const bucket = buckets.get(key);
    if (bucket === undefined) buckets.set(key, { type, count: 1, rank: rankOf(type, request) });
    else bucket.count++;
  }
  return [...buckets.values()]
    .sort((a, b) => {
      if (a.rank !== b.rank) return a.rank - b.rank;
      if (a.type < b.type) return -1;
      return a.type > b.type ? 1 : 0;
    })
    .map(({ type, count }) => ({ type, count }));
}

// 札の行。1 通なら種類だけ、2 通以上は「種類 ×数」。上限を超える種類は「+n 種」の 1 行にまとめ、
// stale が 1 通以上なら最後に「stale ×n」を足す。
export function tagLines(counts: readonly TypeCount[], stale: number): string[] {
  const lines: string[] = [];
  const shown = counts.length > RAFT_TAG_MAX_LINES ? RAFT_TAG_MAX_LINES : counts.length;
  for (let i = 0; i < shown; i++) {
    const c = counts[i];
    if (c === undefined) continue;
    lines.push(c.count === 1 ? c.type : `${c.type} ×${String(c.count)}`);
  }
  if (counts.length > shown) lines.push(`+${String(counts.length - shown)} 種`);
  if (stale > 0) lines.push(`stale ×${String(stale)}`);
  return lines;
}

function staleCount(letters: readonly Letter[]): number {
  let n = 0;
  for (const letter of letters) if (letter.raft?.stale === true) n++;
  return n;
}

function tagOf(key: string, at: Point, letters: readonly Letter[]): RaftTag | null {
  if (letters.length === 0) return null;
  return { key, at, lines: tagLines(countTypes(letters), staleCount(letters)) };
}

// マップの山（scene.piles の順）と船（scene.ferries の順）ごとの札。手紙が無いものには作らない。
export function buildRaftTags(world: World, scene: MapScene): RaftTag[] {
  const tags: RaftTag[] = [];
  // 同じ島の山は portPiles を 1 回だけ求めて使い回す。
  const pilesByIsland = new Map<number, ReturnType<typeof portPiles>>();
  for (const pile of scene.piles) {
    let piles = pilesByIsland.get(pile.islandId);
    if (piles === undefined) {
      piles = portPiles(world, pile.islandId);
      pilesByIsland.set(pile.islandId, piles);
    }
    const letters = piles.find((p) => p.to === pile.to)?.letters ?? [];
    const tag = tagOf(
      `pile:${String(pile.islandId)}:${String(pile.to)}`,
      { x: pile.at.x, y: pile.at.y + RAFT_TAG_PILE_OFFSET },
      letters,
    );
    if (tag !== null) tags.push(tag);
  }
  for (const ferry of scene.ferries) {
    const tag = tagOf(
      `ferry:${ferry.id}`,
      { x: ferry.at.x, y: ferry.at.y + RAFT_TAG_FERRY_OFFSET },
      lettersOnFerry(world, ferry.id),
    );
    if (tag !== null) tags.push(tag);
  }
  return tags;
}
