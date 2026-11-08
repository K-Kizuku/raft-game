// エンジニアパネルの表示のための純粋な関数（T-407・FR-ENG-02・FR-ENG-03・ゲームデザイン §7.4）。
// 値はストア（T-403）に届いたものをそのまま出し、Leader や Commit をここで決めない（ADR-0009）。
// 欄の名前と役割は常に Raft の用語（ゲームデザイン §8）。
import type { Letter, Loc, Role } from "../../protocol/index.ts";
import { leaderId } from "../../state/index.ts";
import type { World } from "../../state/index.ts";
import { formatDuration } from "../../app/format.ts";

export const ROLE_LABELS: Readonly<Record<Role, string>> = Object.freeze({
  follower: "Follower",
  pre_candidate: "PreCandidate",
  candidate: "Candidate",
  leader: "Leader",
});

// 知らない役割は値のまま出す（プロトコル §8）。
export function roleLabel(role: string): string {
  const labels: Readonly<Record<string, string>> = ROLE_LABELS;
  return Object.hasOwn(labels, role) ? (labels[role] ?? role) : role;
}

// ステージの島の名前。無ければ「島 <id>」。
export function nodeName(world: World, id: number): string {
  return world.stage.islands[id]?.name ?? `島 ${String(id)}`;
}

function nodeIds(world: World): number[] {
  return Object.values(world.nodes).map((n) => n.id);
}

// 購読する（詳細を見る）ノード。選んでいる島 → Leader → ID の最も小さいノードの順。
export function targetNodeId(world: World | null, selectedIslandId: number | null): number | null {
  if (world === null) return null;
  const ids = nodeIds(world);
  if (ids.length === 0) return null;
  if (selectedIslandId !== null && world.nodes[selectedIslandId] !== undefined) {
    return selectedIslandId;
  }
  const leader = leaderId(world);
  if (leader !== null) return leader;
  return ids.reduce((min, id) => (id < min ? id : min));
}

export type NodeRow = Readonly<{
  id: number;
  name: string;
  role: string;
  roleLabel: string;
  term: number;
  vote: number; // node.state の値（0 = 投票していない）
  lead: number;
  commit: number;
  applied: number;
  lastIndex: number;
  target: boolean; // targetNodeId と同じ
}>;

// ノード一覧の行。stage の島の順のうち nodes にあるもの、続けて stage に無いノードの ID の昇順。
// 値はすべて node.state（ストアの nodes）のもの。vote も全ノードについて出す。
export function nodeRows(world: World, targetId: number | null): NodeRow[] {
  const inStage = world.stage.islandIds.filter((id) => world.nodes[id] !== undefined);
  const stageSet = new Set(inStage);
  const rest = nodeIds(world)
    .filter((id) => !stageSet.has(id))
    .sort((a, b) => a - b);
  const rows: NodeRow[] = [];
  for (const id of [...inStage, ...rest]) {
    const node = world.nodes[id];
    if (node === undefined) continue;
    rows.push({
      id,
      name: nodeName(world, id),
      role: node.role,
      roleLabel: roleLabel(node.role),
      term: node.term,
      vote: node.vote,
      lead: node.lead,
      commit: node.commit,
      applied: node.applied,
      lastIndex: node.lastIndex,
      target: id === targetId,
    });
  }
  return rows;
}

export type LetterLookup = Readonly<
  | { status: "present"; letter: Letter }
  | { status: "delivered"; letter: Letter; t: number; islandId: number; accepted: boolean }
  | { status: "dropped"; letter: Letter; t: number; reason: string }
  | { status: "missing" }
>;

// 手紙を探す。港・船・中継倉庫にあれば present、消えていれば消える直前の値（新しい記録を優先）。
export function findLetter(world: World, letterId: number): LetterLookup {
  const letter = world.letters[letterId];
  if (letter !== undefined) return { status: "present", letter };
  for (let i = world.letterExits.length - 1; i >= 0; i--) {
    const exit = world.letterExits[i];
    if (exit === undefined || exit.letter.id !== letterId) continue;
    if (exit.outcome === "delivered") {
      return {
        status: "delivered",
        letter: exit.letter,
        t: exit.t,
        islandId: exit.islandId,
        accepted: exit.accepted,
      };
    }
    return { status: "dropped", letter: exit.letter, t: exit.t, reason: exit.reason };
  }
  return { status: "missing" };
}

export type FieldRow = Readonly<{ name: string; value: string }>;

const NONE = "—";

// 手紙の欄（名前は Raft の用語のまま。値は文字列）。
export function letterFields(letter: Letter): FieldRow[] {
  const from = String(letter.from);
  const to = String(letter.to);
  const rows: FieldRow[] = [];
  if (letter.kind === "request") {
    const request = letter.request;
    rows.push(
      { name: "Type", value: "依頼書（Raft のメッセージではない）" },
      { name: "From", value: from },
      { name: "To", value: to },
      { name: "ReqId", value: request === undefined ? NONE : request.reqId },
      { name: "Hops", value: request === undefined ? NONE : String(request.hops) },
    );
  } else if (letter.raft === undefined) {
    rows.push(
      { name: "Type", value: NONE },
      { name: "From", value: from },
      { name: "To", value: to },
    );
  } else {
    const raft = letter.raft;
    rows.push(
      { name: "Type", value: raft.type },
      { name: "From", value: from },
      { name: "To", value: to },
      { name: "Term", value: String(raft.term) },
      { name: "LogTerm", value: String(raft.logTerm) },
      { name: "Index", value: String(raft.index) },
      { name: "Commit", value: String(raft.commit) },
      { name: "Reject", value: String(raft.reject) },
      { name: "RejectHint", value: String(raft.rejectHint) },
      { name: "Entries", value: String(raft.entries.length) },
      { name: "Stale", value: String(raft.stale) },
    );
  }
  rows.push(
    { name: "Label", value: letter.label },
    { name: "CreatedAt", value: formatDuration(letter.createdAt) },
  );
  return rows;
}

function islandText(world: World, islandId: number | undefined): string {
  return islandId === undefined ? NONE : nodeName(world, islandId);
}

// 手紙のある場所。
export function locText(world: World, loc: Loc): string {
  const type: string = loc.type;
  switch (type) {
    case "port":
      return `港: ${islandText(world, loc.islandId)}`;
    case "ferry": {
      if (loc.ferryId === undefined) return `船: ${NONE}`;
      return `船: ${world.ferries[loc.ferryId]?.name ?? loc.ferryId}`;
    }
    case "relay":
      return `中継倉庫: ${islandText(world, loc.islandId)}`;
    case "delivered":
      return "配達済み";
    case "dropped":
      return "処分";
    default:
      return type;
  }
}

// 一覧のボタンの文字と aria-label（「#<id> <raft.type> <from>→<to>」）。
export function letterSummary(letter: Letter): string {
  let kind: string;
  if (letter.kind === "request") kind = "依頼書";
  else kind = letter.raft?.type ?? letter.label;
  return `#${String(letter.id)} ${kind} ${String(letter.from)}→${String(letter.to)}`;
}

// 1 つの一覧に並べる手紙の数の上限（画面のための値で、ゲームの数値ではない）。
export const ENGINEER_LETTER_LIMIT = 50;
