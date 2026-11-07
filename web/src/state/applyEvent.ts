// snapshot と events を世界の状態に写す純粋な関数（T-403 / DesignDoc §13.2・プロトコル §5〜§8）。
// 引数を書き換えず、変わらなかった部分は前と同じオブジェクトを返す（構造の共有）。
// 時刻は引数の receivedAt だけを使う。Raft の結果をここで決めず、届いた値をそのまま写す（ADR-0009）。
import type {
  Clock,
  Island,
  IslandState,
  Letter,
  Loc,
  NodeSummary,
  Request,
  Route,
  RouteState,
  ServerEvent,
  Snapshot,
} from "../protocol/index.ts";
import { WORLD_LIMITS } from "./world.ts";
import type { FerryView, World } from "./world.ts";

type Mutable<T> = { -readonly [K in keyof T]: T[K] };
type FerryRecord = World["ferries"];
type LetterRecord = World["letters"];

// indexBy は ID で引ける Record と、最初に出てきた順の ID の一覧を作る（同じ ID は後の値を使う）。
function indexBy<K extends string | number, V>(
  items: readonly V[],
  keyOf: (item: V) => K,
): { record: Record<K, V>; ids: K[] } {
  const record = {} as Record<K, V>;
  const ids: K[] = [];
  const seen = new Set<K>();
  for (const item of items) {
    const key = keyOf(item);
    record[key] = item;
    if (!seen.has(key)) {
      seen.add(key);
      ids.push(key);
    }
  }
  return { record, ids };
}

function isHeld(loc: Loc): boolean {
  return loc.type === "port" || loc.type === "ferry" || loc.type === "relay";
}

// ferryOf は loc が船の上ならその船の ID を返す。
function ferryOf(loc: Loc): string | undefined {
  return loc.type === "ferry" ? loc.ferryId : undefined;
}

export function worldFromSnapshot(snapshot: Snapshot, receivedAt: number): World {
  const islands = indexBy<number, Island>(snapshot.stage.islands, (i) => i.id);
  const routes = indexBy<string, Route>(snapshot.stage.routes, (r) => r.id);
  const ferries = indexBy<string, FerryView>(
    snapshot.ferries.map((f) => ({ ...f, brokenUntil: f.brokenUntil ?? null })),
    (f) => f.id,
  );
  const requests = indexBy<string, Request>(snapshot.requests, (r) => r.id);
  const letters = indexBy<number, Letter>(
    snapshot.letters.filter((l) => isHeld(l.loc)),
    (l) => l.id,
  );
  const { clock } = snapshot;
  return {
    session: snapshot.session,
    phase: snapshot.phase,
    clock: {
      simTime: clock.t,
      speed: clock.speed,
      paused: clock.paused,
      endsAt: clock.endsAt,
      allowedSpeeds: clock.allowedSpeeds,
      receivedAt,
    },
    stage: {
      title: snapshot.stage.title,
      islands: islands.record,
      islandIds: islands.ids,
      routes: routes.record,
      routeIds: routes.ids,
      resources: snapshot.stage.resources,
      ...(snapshot.stage.demoStorm !== undefined && { demoStorm: snapshot.stage.demoStorm }),
    },
    islandStates: indexBy<number, IslandState>(snapshot.islandsState, (s) => s.id).record,
    routeStates: indexBy<string, RouteState>(snapshot.routesState, (s) => s.id).record,
    forecasts: [],
    ferries: ferries.record,
    ferryIds: ferries.ids,
    letters: letters.record,
    requests: requests.record,
    requestIds: requests.ids,
    nodes: indexBy<number, NodeSummary>(snapshot.nodes, (n) => n.id).record,
    score: snapshot.score,
    notices: [],
    letterExits: [],
    leaderChanges: [],
    lastEventSeq: snapshot.lastSeq,
  };
}

// append は一覧の末尾に足し、上限を超えた分を先頭から捨てる。
function append<T>(list: readonly T[], item: T, limit: number): readonly T[] {
  const next = [...list, item];
  if (next.length > limit) next.splice(0, next.length - limit);
  return next;
}

function setEntry<K extends string | number, V>(
  record: Readonly<Record<K, V>>,
  key: K,
  value: V,
): Readonly<Record<K, V>> {
  const next = { ...record } as Record<K, V>;
  next[key] = value;
  return next;
}

function deleteEntry<K extends string | number, V>(
  record: Readonly<Record<K, V>>,
  key: K,
): Readonly<Record<K, V>> {
  const next = { ...record } as Record<K, V>;
  delete next[key];
  return next;
}

// removeFromCargo は船の cargo から手紙を外す（残りの順はそのまま）。船が無い・積んでいなければそのまま。
function removeFromCargo(ferries: FerryRecord, ferryId: string, letterId: number): FerryRecord {
  const ferry = ferries[ferryId];
  if (ferry === undefined || !ferry.cargo.includes(letterId)) return ferries;
  return setEntry(ferries, ferryId, {
    ...ferry,
    cargo: ferry.cargo.filter((id) => id !== letterId),
  });
}

// addToCargo は船の cargo の末尾に手紙を足す。船が無い・すでに積んでいればそのまま。
function addToCargo(ferries: FerryRecord, ferryId: string, letterId: number): FerryRecord {
  const ferry = ferries[ferryId];
  if (ferry === undefined || ferry.cargo.includes(letterId)) return ferries;
  return setEntry(ferries, ferryId, { ...ferry, cargo: [...ferry.cargo, letterId] });
}

// placeLetter は手紙を新しい値（loc）に置く。前に船の上なら cargo から外し、新しく船の上なら cargo に足す。
// 新しい loc が delivered・dropped なら letters から消す。
function placeLetter(
  world: World,
  prev: Letter | undefined,
  letter: Letter,
): { letters: LetterRecord; ferries: FerryRecord } {
  let ferries = world.ferries;
  const prevFerry = prev === undefined ? undefined : ferryOf(prev.loc);
  if (prevFerry !== undefined) ferries = removeFromCargo(ferries, prevFerry, letter.id);
  const nextFerry = ferryOf(letter.loc);
  if (nextFerry !== undefined) ferries = addToCargo(ferries, nextFerry, letter.id);
  let letters = world.letters;
  if (isHeld(letter.loc)) {
    letters = setEntry(letters, letter.id, letter);
  } else if (prev !== undefined) {
    letters = deleteEntry(letters, letter.id);
  }
  return { letters, ferries };
}

// removeLetter は手紙を letters から消し、船の上なら cargo からも外す。
function removeLetter(
  world: World,
  letter: Letter,
): { letters: LetterRecord; ferries: FerryRecord } {
  const ferryId = ferryOf(letter.loc);
  return {
    letters: deleteEntry(world.letters, letter.id),
    ferries:
      ferryId === undefined ? world.ferries : removeFromCargo(world.ferries, ferryId, letter.id),
  };
}

function setRouteStates(
  routeStates: World["routeStates"],
  routeIds: readonly string[],
  blockedUntil: number | null,
): World["routeStates"] {
  const next: Record<string, RouteState> = { ...routeStates };
  for (const id of routeIds) next[id] = { id, blockedUntil };
  return next;
}

export function applyEvent(world: World, event: ServerEvent): World {
  const seq = event.seq;
  if (seq <= world.lastEventSeq) return world;
  const next = (patch: Partial<World>): World => ({ ...world, ...patch, lastEventSeq: seq });

  switch (event.type) {
    case "phase.changed":
      return next({ phase: event.phase });

    case "letter.created": {
      const prev = world.letters[event.letter.id];
      if (prev === undefined && !isHeld(event.letter.loc)) return next({});
      return next(placeLetter(world, prev, event.letter));
    }

    case "letter.moved": {
      const prev = world.letters[event.letterId];
      if (prev === undefined) return next({});
      return next(placeLetter(world, prev, { ...prev, loc: event.loc }));
    }

    case "letter.delivered": {
      const letter = world.letters[event.letterId];
      if (letter === undefined) return next({});
      return next({
        ...removeLetter(world, letter),
        letterExits: append(
          world.letterExits,
          {
            seq,
            t: event.t,
            outcome: "delivered",
            letter,
            islandId: event.islandId,
            accepted: event.accepted,
          },
          WORLD_LIMITS.letterExits,
        ),
      });
    }

    case "letter.dropped": {
      const letter = world.letters[event.letterId];
      if (letter === undefined) return next({});
      return next({
        ...removeLetter(world, letter),
        letterExits: append(
          world.letterExits,
          { seq, t: event.t, outcome: "dropped", letter, reason: event.reason },
          WORLD_LIMITS.letterExits,
        ),
      });
    }

    case "letter.stale": {
      const letter = world.letters[event.letterId];
      if (letter === undefined || letter.raft === undefined || letter.raft.stale) return next({});
      return next({
        letters: setEntry(world.letters, letter.id, {
          ...letter,
          raft: { ...letter.raft, stale: true },
        }),
      });
    }

    case "ferry.departed": {
      const ferry = world.ferries[event.ferryId];
      if (ferry === undefined) return next({});
      const sailing: Mutable<FerryView> = {
        ...ferry,
        state: "sailing",
        from: event.from,
        to: event.to,
        departAt: event.departAt,
        arriveAt: event.arriveAt,
      };
      delete sailing.islandId;
      return next({ ferries: setEntry(world.ferries, ferry.id, sailing) });
    }

    case "ferry.arrived": {
      const ferry = world.ferries[event.ferryId];
      if (ferry === undefined) return next({});
      const docked: Mutable<FerryView> = {
        ...ferry,
        state: "docked",
        islandId: event.islandId,
        readyAt: event.readyAt,
        departAt: 0,
        arriveAt: 0,
      };
      delete docked.from;
      delete docked.to;
      return next({ ferries: setEntry(world.ferries, ferry.id, docked) });
    }

    case "ferry.broken":
    case "ferry.repaired": {
      const ferry = world.ferries[event.ferryId];
      if (ferry === undefined) return next({});
      const brokenUntil = event.type === "ferry.broken" ? event.until : null;
      return next({ ferries: setEntry(world.ferries, ferry.id, { ...ferry, brokenUntil }) });
    }

    case "route.blocked": {
      const t = event.t;
      const forecasts = world.forecasts.some((f) => f.at <= t)
        ? world.forecasts.filter((f) => f.at > t)
        : world.forecasts;
      return next({
        routeStates: setRouteStates(world.routeStates, event.routeIds, event.until),
        forecasts,
      });
    }

    case "route.unblocked":
      return next({ routeStates: setRouteStates(world.routeStates, event.routeIds, null) });

    case "forecast":
      return next({
        forecasts: append(
          world.forecasts,
          { seq, t: event.t, event: event.event, at: event.at, routeIds: event.routeIds },
          WORLD_LIMITS.forecasts,
        ),
      });

    case "node.state": {
      const node: NodeSummary = {
        id: event.id,
        role: event.role,
        term: event.term,
        vote: event.vote,
        lead: event.lead,
        commit: event.commit,
        applied: event.applied,
        lastIndex: event.lastIndex,
      };
      return next({ nodes: setEntry(world.nodes, node.id, node) });
    }

    case "node.leader_elected":
      return next({
        leaderChanges: append(
          world.leaderChanges,
          { seq, t: event.t, kind: "elected", id: event.id, term: event.term },
          WORLD_LIMITS.leaderChanges,
        ),
      });

    case "node.stepped_down":
      return next({
        leaderChanges: append(
          world.leaderChanges,
          {
            seq,
            t: event.t,
            kind: "stepped_down",
            id: event.id,
            term: event.term,
            reason: event.reason,
          },
          WORLD_LIMITS.leaderChanges,
        ),
      });

    case "request.created": {
      const { request } = event;
      const known = world.requests[request.id] !== undefined;
      return next({
        requests: setEntry(world.requests, request.id, request),
        requestIds: known ? world.requestIds : [...world.requestIds, request.id],
      });
    }

    case "request.updated": {
      const request = world.requests[event.id];
      if (request === undefined) return next({});
      return next({
        requests: setEntry(world.requests, request.id, {
          ...request,
          status: event.status,
          expired: event.expired,
          result: event.result,
        }),
      });
    }

    case "ledger.applied":
      return next({});

    case "island.updated":
      return next({
        islandStates: setEntry(world.islandStates, event.id, {
          id: event.id,
          satisfaction: event.satisfaction,
          lastDeliveredAt: event.lastDeliveredAt,
        }),
      });

    case "score.updated":
      return next({
        score: {
          ...world.score,
          total: event.total,
          successes: event.successes,
          criticalSecured: event.criticalSecured,
        },
      });

    case "notice":
      return next({
        notices: append(
          world.notices,
          { seq, t: event.t, islandId: event.islandId, key: event.key, params: event.params },
          WORLD_LIMITS.notices,
        ),
      });

    default:
      // 知らない type（プロトコル §8）は seq だけを進める。
      return next({});
  }
}

export function applyEvents(world: World, events: readonly ServerEvent[]): World {
  let current = world;
  for (const event of events) current = applyEvent(current, event);
  return current;
}

export function withClock(world: World, clock: Clock, receivedAt: number): World {
  return {
    ...world,
    clock: {
      simTime: clock.t,
      speed: clock.speed,
      paused: clock.paused,
      endsAt: clock.endsAt,
      allowedSpeeds: world.clock.allowedSpeeds,
      receivedAt,
    },
  };
}
