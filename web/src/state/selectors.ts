// 世界の状態からよく使う値を選ぶ純粋な関数（T-403 / DesignDoc §13.2・§13.3）。
// どれも呼ぶたびに新しい配列を返す。React では useGameStore((s) => s.world) で world を取り、
// useMemo の中で呼ぶ（セレクターの中で呼ぶと毎回違う値になり、描き直しが止まらない）。
// PixiJS は gameStore.getState().world で毎フレーム呼んでよい。
import type { Letter } from "../protocol/index.ts";
import type { ClockView, World } from "./world.ts";

// estimateSimTime は推定ゲーム内時刻（DesignDoc §13.3）。now は呼び出し側の performance.now()。
// 描画の補間に使うので小数のまま返す。
export function estimateSimTime(clock: ClockView, now: number): number {
  if (clock.paused) return clock.simTime;
  const estimated = clock.simTime + Math.max(0, now - clock.receivedAt) * clock.speed;
  return Math.min(estimated, clock.endsAt);
}

function byId(a: Letter, b: Letter): number {
  return a.id - b.id;
}

// lettersAtPort は島の港にある手紙（ID の昇順）。
export function lettersAtPort(world: World, islandId: number): Letter[] {
  return Object.values(world.letters)
    .filter((l) => l.loc.type === "port" && l.loc.islandId === islandId)
    .sort(byId);
}

// lettersOnFerry は船に積んだ手紙（cargo の順）。letters に無い ID は飛ばす。船が無ければ []。
export function lettersOnFerry(world: World, ferryId: string): Letter[] {
  const ferry = world.ferries[ferryId];
  if (ferry === undefined) return [];
  const letters: Letter[] = [];
  for (const id of ferry.cargo) {
    const letter = world.letters[id];
    if (letter !== undefined) letters.push(letter);
  }
  return letters;
}

// portPiles は港の手紙を宛先ごとの山にまとめる（to の昇順。山の中は ID の昇順）。
export function portPiles(world: World, islandId: number): { to: number; letters: Letter[] }[] {
  const piles = new Map<number, Letter[]>();
  for (const letter of lettersAtPort(world, islandId)) {
    const pile = piles.get(letter.to);
    if (pile === undefined) piles.set(letter.to, [letter]);
    else pile.push(letter);
  }
  return [...piles.entries()].sort(([a], [b]) => a - b).map(([to, letters]) => ({ to, letters }));
}

// leaderId は role が leader のノードのうち term が最も大きいもの（同じなら ID の小さいもの）。
// 届いた role から選ぶだけで、Leader をここで決めない。いなければ null。
export function leaderId(world: World): number | null {
  let best: { id: number; term: number } | null = null;
  for (const node of Object.values(world.nodes)) {
    if (node.role !== "leader") continue;
    if (best === null || node.term > best.term || (node.term === best.term && node.id < best.id)) {
      best = { id: node.id, term: node.term };
    }
  }
  return best === null ? null : best.id;
}

// isAutoFerry は船 ferryId が自動航行（cmd.ferry.auto。T-724・T-725）か。snapshot の session.autoFerries
// （と、切り替えの ok の ack で直した値）から読む。
export function isAutoFerry(world: World, ferryId: string): boolean {
  return world.session.autoFerries?.includes(ferryId) ?? false;
}

// withAutoFerry は船 ferryId の自動航行を enabled にした world（T-725）。変わらなければ同じ world を返す。
// 一覧は ID の昇順にし、ひとつもなければキーを省く（snapshot の session.autoFerries と同じ形。プロトコル §5.1）。
export function withAutoFerry(world: World, ferryId: string, enabled: boolean): World {
  if (isAutoFerry(world, ferryId) === enabled) return world;
  const current = world.session.autoFerries ?? [];
  const next = enabled ? [...current, ferryId].sort() : current.filter((id) => id !== ferryId);
  const session = { ...world.session };
  delete session.autoFerries;
  return { ...world, session: next.length === 0 ? session : { ...session, autoFerries: next } };
}
