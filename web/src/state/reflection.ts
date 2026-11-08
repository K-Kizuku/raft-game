// リザルトの「振り返り」の数を集める純粋な関数（T-432）。
// ストアの世界の状態（World）の移り変わり（前の world と次の world の差）を観察して数えるだけで、
// Raft の結果やスコアを決めない（ADR-0009）。時刻は world.clock.simTime（届いた時計のゲーム内時刻）だけを使い、
// 実時間を読まない。引数は書き換えない。数の意味:
//   - 積まずに出航: 港に手紙があり、船に空きがあるのに、その港で 1 通も積まずに出航した回数（出航した島ごと）
//   - 代表への返事の待ち: 代表（Leader）への返事（heartbeat_resp・append_resp・append_reject）が、
//     島の港で続けて待っていた時間の最長（返事が 1 通でもある間を 1 回と数える。合流・処分で入れ替わっても続ける）
//   - 期限切れの依頼: 届いた依頼のうち expired のもの
//   - 港であふれた手紙: 港の上限を超えて処分された（letter.dropped の overflow）手紙
// 次に試すことは、原因に近いもの（積まずに出航・返事の待ち）を先に、結果（期限切れ・あふれ）を後に、決まった順で選ぶ。
import type { LetterLabel } from "../protocol/index.ts";
import type { World } from "./world.ts";

// 代表への返事の区分（ゲームデザイン §2.1。どれも代表が出した手紙への返事で、宛先は代表）。
const REPLY_LABELS: ReadonlySet<LetterLabel> = new Set<LetterLabel>([
  "heartbeat_resp",
  "append_resp",
  "append_reject",
]);

// 船 1 隻の、いまの停泊の間の記録。
type FerryStay = Readonly<{ islandId: number | null; loaded: boolean }>;

export type ReflectionTally = Readonly<{
  sessionId: string | null; // 数え始めたセッション。違うセッションの world が来たら数え直す
  emptyDepartures: Readonly<Record<number, number>>; // 島 ID → 積まずに出航した回数
  ferryStays: Readonly<Record<string, FerryStay>>; // 船 ID → いまの停泊
  replySince: Readonly<Record<number, number>>; // 島 ID → 代表への返事が港で待ち始めた時刻（いま待っている島だけ）
  longestReplyWait: Readonly<{ ms: number; islandId: number }> | null; // 待ち終えたものの最長
  overflowDrops: number;
  lastExitSeq: number; // 数え終えた letterExits の seq の最大
}>;

export const EMPTY_REFLECTION_TALLY: ReflectionTally = Object.freeze({
  sessionId: null,
  emptyDepartures: Object.freeze({}),
  ferryStays: Object.freeze({}),
  replySince: Object.freeze({}),
  longestReplyWait: null,
  overflowDrops: 0,
  lastExitSeq: 0,
});

function isReply(label: string): boolean {
  return REPLY_LABELS.has(label as LetterLabel);
}

// 港に手紙がある島の ID と、代表への返事が港にある島の ID。
function portIslands(world: World): { any: Set<number>; reply: Set<number> } {
  const any = new Set<number>();
  const reply = new Set<number>();
  for (const letter of Object.values(world.letters)) {
    if (letter.loc.type !== "port" || letter.loc.islandId === undefined) continue;
    any.add(letter.loc.islandId);
    if (isReply(letter.label)) reply.add(letter.loc.islandId);
  }
  return { any, reply };
}

function dockedAt(world: World, ferryId: string): number | null {
  const ferry = world.ferries[ferryId];
  if (ferry === undefined || ferry.state !== "docked") return null;
  return ferry.islandId ?? null;
}

function maxExitSeq(world: World): number {
  let max = 0;
  for (const exit of world.letterExits) if (exit.seq > max) max = exit.seq;
  return max;
}

function longer(
  current: ReflectionTally["longestReplyWait"],
  ms: number,
  islandId: number,
): ReflectionTally["longestReplyWait"] {
  if (ms <= 0) return current;
  if (current === null || ms > current.ms) return { ms, islandId };
  return current;
}

// startTally は新しいセッションの world から数え始める（それより前の出来事は数えない）。
function startTally(world: World): ReflectionTally {
  const now = world.clock.simTime;
  const ferryStays: Record<string, FerryStay> = {};
  for (const id of world.ferryIds)
    ferryStays[id] = { islandId: dockedAt(world, id), loaded: false };
  const replySince: Record<number, number> = {};
  for (const islandId of portIslands(world).reply) replySince[islandId] = now;
  return {
    sessionId: world.session.id,
    emptyDepartures: {},
    ferryStays,
    replySince,
    longestReplyWait: null,
    overflowDrops: 0,
    lastExitSeq: maxExitSeq(world),
  };
}

// stepReflection は world が prev から next に変わったときの記録を返す。
// next が null（ストアの reset）なら空に戻す。セッションが変わったら next から数え直す。
// 同じセッションのスナップショット（つなぎ直し）は、差として続けて数える。
export function stepReflection(
  tally: ReflectionTally,
  prev: World | null,
  next: World | null,
): ReflectionTally {
  if (next === null) return EMPTY_REFLECTION_TALLY;
  if (prev === next && tally.sessionId === next.session.id) return tally;
  if (prev === null || tally.sessionId !== next.session.id) return startTally(next);

  const now = next.clock.simTime;
  const ports = portIslands(next);

  // 船に積んだ手紙（前は港・中継倉庫、次はその船の上）。積んだ船の停泊に印を付ける。
  const loadedFerries = new Set<string>();
  for (const letter of Object.values(next.letters)) {
    if (letter.loc.type !== "ferry" || letter.loc.ferryId === undefined) continue;
    const before = prev.letters[letter.id];
    if (before === undefined) continue;
    if (before.loc.type === "port" || before.loc.type === "relay") {
      loadedFerries.add(letter.loc.ferryId);
    }
  }

  const emptyDepartures: Record<number, number> = { ...tally.emptyDepartures };
  const ferryStays: Record<string, FerryStay> = {};
  for (const id of next.ferryIds) {
    const ferry = next.ferries[id];
    if (ferry === undefined) continue;
    const stay = tally.ferryStays[id] ?? { islandId: dockedAt(prev, id), loaded: false };
    const loaded = stay.loaded || loadedFerries.has(id);
    const before = prev.ferries[id];
    const departed =
      before !== undefined &&
      before.state === "docked" &&
      ferry.state === "sailing" &&
      ferry.from !== undefined;
    if (departed) {
      const from = ferry.from as number;
      if (!loaded && ports.any.has(from) && ferry.cargo.length < ferry.capacity) {
        emptyDepartures[from] = (emptyDepartures[from] ?? 0) + 1;
      }
      ferryStays[id] = { islandId: null, loaded: false };
      continue;
    }
    const islandId = dockedAt(next, id);
    // 着いた・別の島に移った（つなぎ直し）なら、新しい停泊として数え直す。
    ferryStays[id] =
      islandId !== stay.islandId ? { islandId, loaded: false } : { islandId, loaded };
  }

  // 代表への返事の待ち。港に返事がある間を 1 回とし、無くなったら長さを記録する。
  let longestReplyWait = tally.longestReplyWait;
  const replySince: Record<number, number> = {};
  for (const [key, since] of Object.entries(tally.replySince)) {
    const islandId = Number(key);
    if (ports.reply.has(islandId)) replySince[islandId] = since;
    else longestReplyWait = longer(longestReplyWait, now - since, islandId);
  }
  for (const islandId of ports.reply) {
    if (replySince[islandId] === undefined) replySince[islandId] = now;
  }

  // 港であふれた手紙（新しく届いた letterExits の分だけ）。
  let overflowDrops = tally.overflowDrops;
  let lastExitSeq = tally.lastExitSeq;
  for (const exit of next.letterExits) {
    if (exit.seq <= tally.lastExitSeq) continue;
    if (exit.outcome === "dropped" && exit.reason === "overflow") overflowDrops += 1;
    if (exit.seq > lastExitSeq) lastExitSeq = exit.seq;
  }

  return {
    sessionId: tally.sessionId,
    emptyDepartures,
    ferryStays,
    replySince,
    longestReplyWait,
    overflowDrops,
    lastExitSeq,
  };
}

export type ReflectionTipKind = "emptyDepartures" | "replyWait" | "expiredRequests" | "overflow";

export type IslandCount = Readonly<{ islandId: number; count: number }>;

export type Reflection = Readonly<{
  emptyDepartures: Readonly<{ total: number; byIsland: readonly IslandCount[] }>; // byIsland は多い順（同じなら ID の昇順）
  longestReplyWait: Readonly<{ ms: number; islandId: number }> | null; // ラウンドの終わりに待っていた分も含める
  replyWaitLimitMs: number; // 「長く待った」とみなす長さ（ステージのいちばん長い航路の船旅の時間）
  expiredRequests: number;
  overflowDrops: number;
  tips: readonly ReflectionTipKind[]; // 次に試すこと（最大 2 つ。決まった順）
}>;

export const MAX_REFLECTION_TIPS = 2;

function longestTravelMs(world: World | null): number {
  if (world === null) return 0;
  let max = 0;
  for (const id of world.stage.routeIds) {
    const route = world.stage.routes[id];
    if (route !== undefined && route.travelMs > max) max = route.travelMs;
  }
  return max;
}

// summarizeReflection は記録と最後の world から、リザルトに出す数と次に試すことを作る。
// world が記録と違うセッションなら、その world の値（期限切れの依頼）だけを使う。
export function summarizeReflection(tally: ReflectionTally, world: World | null): Reflection {
  const same = world !== null && tally.sessionId === world.session.id;
  const counted = same ? tally : EMPTY_REFLECTION_TALLY;

  const byIsland = Object.entries(counted.emptyDepartures)
    .map(([key, count]) => ({ islandId: Number(key), count }))
    .filter((item) => item.count > 0)
    .sort((a, b) => b.count - a.count || a.islandId - b.islandId);
  const total = byIsland.reduce((sum, item) => sum + item.count, 0);

  let longestReplyWait = counted.longestReplyWait;
  if (same) {
    const now = world.clock.simTime;
    const waiting = Object.entries(counted.replySince)
      .map(([key, since]) => ({ islandId: Number(key), since }))
      .sort((a, b) => a.islandId - b.islandId);
    for (const { islandId, since } of waiting) {
      longestReplyWait = longer(longestReplyWait, now - since, islandId);
    }
  }

  let expiredRequests = 0;
  if (world !== null) {
    for (const id of world.requestIds) if (world.requests[id]?.expired === true) expiredRequests++;
  }

  const replyWaitLimitMs = longestTravelMs(world);
  const tips: ReflectionTipKind[] = [];
  if (total > 0) tips.push("emptyDepartures");
  if (longestReplyWait !== null && longestReplyWait.ms > replyWaitLimitMs) tips.push("replyWait");
  if (expiredRequests > 0) tips.push("expiredRequests");
  if (counted.overflowDrops > 0) tips.push("overflow");

  return {
    emptyDepartures: { total, byIsland },
    longestReplyWait,
    replyWaitLimitMs,
    expiredRequests,
    overflowDrops: counted.overflowDrops,
    tips: tips.slice(0, MAX_REFLECTION_TIPS),
  };
}
