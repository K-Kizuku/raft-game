// チュートリアルの進み方を決める純粋な関数（T-409・docs/05-game-progression.md §7）。
// 条件はストアの値（サーバーが送った船・手紙・依頼の status）を見て判定するだけで、
// Leader や Commit をここで決めない（ADR-0009）。同じ入力なら同じ結果を返し、引数を書き換えない。
import { lettersAtPort } from "../../state/index.ts";
import type { World } from "../../state/index.ts";
import type { TutorialScript, TutorialStep, TutorialStepId } from "./script.ts";

export type TutorialIslands = Readonly<{ leader: number; relay: number; far: number }>;

export type TutorialFacts = Readonly<{
  opened: boolean;
  requestSeen: boolean;
  loaded: boolean;
  departed: boolean;
  atRelay: boolean;
  repliedFromRelay: boolean;
  firstSuccess: boolean;
  farRequest: boolean;
  farProposed: boolean;
  farCommitted: boolean;
  farSettled: boolean;
  afterT8Time: boolean;
  farPreVote: boolean;
}>;

export type TutorialState = Readonly<{
  facts: TutorialFacts;
  next: number; // 次の本筋の段（steps の T1〜T7 の添字。7 なら本筋は終わり）
  current: Readonly<{ id: TutorialStepId; expanded: boolean }> | null;
  // 任意の段（T8・T9）の下に、畳んだ本筋の段（steps[next]）が残っているか。
  // 任意の段の「次へ」で、その段（畳まれたまま）に戻る。
  parked: boolean;
  doneOptional: Readonly<{ T8: boolean; T9: boolean }>;
  finished: boolean; // 「チュートリアルを終える」を押した
}>;

type ObserveInput = Readonly<{
  world: World;
  islands: TutorialIslands;
  simNow: number;
  openingActive: boolean;
  t8AfterMs: number; // 台本の t8AfterMs
}>;

const MAIN_STEPS: readonly TutorialStepId[] = ["T1", "T2", "T3", "T4", "T5", "T6", "T7"];
const OPTIONAL_STEPS = ["T8", "T9"] as const;
type OptionalStepId = (typeof OPTIONAL_STEPS)[number];

const FACT_KEYS: readonly (keyof TutorialFacts)[] = [
  "opened",
  "requestSeen",
  "loaded",
  "departed",
  "atRelay",
  "repliedFromRelay",
  "firstSuccess",
  "farRequest",
  "farProposed",
  "farCommitted",
  "farSettled",
  "afterT8Time",
  "farPreVote",
];

// resolveIslands は台本の島の名前から島の ID を引く（同じ名前が 2 つあれば最初のもの）。無ければ null。
export function resolveIslands(world: World, script: TutorialScript): TutorialIslands | null {
  const find = (name: string): number | null => {
    for (const id of world.stage.islandIds) {
      if (world.stage.islands[id]?.name === name) return id;
    }
    return null;
  };
  const leader = find(script.islands.leader);
  const relay = find(script.islands.relay);
  const far = find(script.islands.far);
  if (leader === null || relay === null || far === null) return null;
  return { leader, relay, far };
}

export function initialTutorialState(): TutorialState {
  return {
    facts: {
      opened: false,
      requestSeen: false,
      loaded: false,
      departed: false,
      atRelay: false,
      repliedFromRelay: false,
      firstSuccess: false,
      farRequest: false,
      farProposed: false,
      farCommitted: false,
      farSettled: false,
      afterT8Time: false,
      farPreVote: false,
    },
    next: 0,
    current: null,
    parked: false,
    doneOptional: { T8: false, T9: false },
    finished: false,
  };
}

function factsOf(input: ObserveInput): TutorialFacts {
  const { world, islands } = input;
  const ferries = world.ferryIds.flatMap((id) => {
    const ferry = world.ferries[id];
    return ferry === undefined ? [] : [ferry];
  });
  const requests = world.requestIds.flatMap((id) => {
    const request = world.requests[id];
    return request === undefined ? [] : [request];
  });
  const farRequests = requests.filter((r) => r.origin === islands.far);
  return {
    opened: world.phase === "sailing" && !input.openingActive,
    requestSeen: world.requestIds.length >= 1,
    loaded: ferries.some((f) => f.cargo.length > 0),
    departed: ferries.some((f) => f.state === "sailing"),
    atRelay: ferries.some((f) => f.state === "docked" && f.islandId === islands.relay),
    repliedFromRelay: ferries.some(
      (f) =>
        f.state === "sailing" &&
        f.from === islands.relay &&
        f.cargo.some((id) => {
          const letter = world.letters[id];
          return (
            letter !== undefined && letter.from === islands.relay && letter.to === islands.leader
          );
        }),
    ),
    firstSuccess: requests.some((r) => r.status === "succeeded"),
    farRequest: farRequests.length > 0,
    farProposed: farRequests.some(
      (r) =>
        r.status === "proposed" ||
        r.status === "committed" ||
        r.status === "succeeded" ||
        r.status === "rejected",
    ),
    farCommitted: farRequests.some((r) => r.status === "committed"),
    farSettled: farRequests.some((r) => r.status === "succeeded" || r.status === "rejected"),
    afterT8Time: input.simNow >= input.t8AfterMs,
    farPreVote:
      world.nodes[islands.far]?.role === "pre_candidate" ||
      lettersAtPort(world, islands.far).some((l) => l.label === "prevote"),
  };
}

// observeFacts は観察した世界で成り立った事実を足す（一度 true になった事実は戻らない）。
// 何も変わらなければ facts をそのまま返す。
export function observeFacts(facts: TutorialFacts, input: ObserveInput): TutorialFacts {
  const seen = factsOf(input);
  if (FACT_KEYS.every((key) => facts[key] || !seen[key])) return facts;
  const merged: Record<keyof TutorialFacts, boolean> = { ...facts };
  for (const key of FACT_KEYS) {
    merged[key] = facts[key] || seen[key];
  }
  return merged;
}

function stepOf(script: TutorialScript, id: TutorialStepId): TutorialStep | null {
  return script.steps.find((s) => s.id === id) ?? null;
}

// 本筋の段の「出す条件」。
function canShow(id: TutorialStepId, facts: TutorialFacts): boolean {
  switch (id) {
    case "T1":
      return facts.opened;
    case "T2":
      return facts.requestSeen;
    case "T3":
      return true; // T2 が終わった（next が T3 になった）
    case "T4":
      return facts.atRelay;
    case "T5":
      return facts.firstSuccess;
    case "T6":
      return facts.farRequest;
    case "T7":
      return facts.farCommitted;
    case "T8":
      return facts.afterT8Time;
    case "T9":
      return facts.farPreVote;
  }
}

// action の段の「終わる条件」（click の段は「次へ」で終わるので false）。
function isDone(id: TutorialStepId, facts: TutorialFacts): boolean {
  switch (id) {
    case "T2":
      return facts.loaded;
    case "T3":
      return facts.departed;
    case "T4":
      return facts.repliedFromRelay;
    case "T6":
      return facts.farProposed;
    case "T7":
      return facts.farSettled;
    default:
      return false;
  }
}

function isAction(script: TutorialScript, id: TutorialStepId): boolean {
  return stepOf(script, id)?.advance === "action";
}

function sameState(a: TutorialState, b: TutorialState): boolean {
  return (
    a.facts === b.facts &&
    a.next === b.next &&
    a.parked === b.parked &&
    a.finished === b.finished &&
    a.doneOptional.T8 === b.doneOptional.T8 &&
    a.doneOptional.T9 === b.doneOptional.T9 &&
    (a.current === b.current ||
      (a.current !== null &&
        b.current !== null &&
        a.current.id === b.current.id &&
        a.current.expanded === b.current.expanded))
  );
}

// advance は今の事実で吹き出しを進める。何も変わらなければ state をそのまま返す。
export function advance(state: TutorialState, script: TutorialScript): TutorialState {
  if (state.finished) return state;
  const { facts } = state;
  let { next, current, parked } = state;
  // 段の数は有限で、1 回の繰り返しで next が進むか、抜ける。
  for (let guard = 0; guard <= MAIN_STEPS.length + 1; guard++) {
    const mainId = MAIN_STEPS[next] ?? null;
    // 出ている（畳まれている・任意の段の下に残っている）本筋の action の段は、終わる条件で終わる。
    const mainOut = mainId !== null && (current?.id === mainId || parked);
    if (mainOut && isAction(script, mainId) && isDone(mainId, facts)) {
      if (current?.id === mainId) current = null;
      parked = false;
      next += 1;
      continue;
    }
    if (current !== null && current.expanded) break;
    // 本筋を先にする。
    if (current === null && !parked && mainId !== null) {
      const action = isAction(script, mainId);
      // 終わる条件がすでに成り立つ action の段は、出さずに終わったことにする。
      if (action && isDone(mainId, facts)) {
        next += 1;
        continue;
      }
      if (canShow(mainId, facts)) {
        current = { id: mainId, expanded: true };
        break;
      }
    }
    // 任意の段（T1 より前には出さない）。畳んだ本筋の段は、その下に残す。
    if (facts.opened && next >= 1) {
      const optional = OPTIONAL_STEPS.find(
        (id: OptionalStepId) => !state.doneOptional[id] && canShow(id, facts),
      );
      if (optional !== undefined && stepOf(script, optional) !== null) {
        if (current !== null) parked = true;
        current = { id: optional, expanded: true };
      }
    }
    break;
  }
  const result: TutorialState = { ...state, next, current, parked };
  return sameState(result, state) ? state : result;
}

function isOptional(id: TutorialStepId): id is OptionalStepId {
  return id === "T8" || id === "T9";
}

// pressPrimary は「次へ」・「わかった」を押したときの状態。
export function pressPrimary(state: TutorialState, script: TutorialScript): TutorialState {
  const { current } = state;
  if (state.finished || current === null) return state;
  if (isAction(script, current.id)) {
    if (!current.expanded) return state;
    return advance({ ...state, current: { id: current.id, expanded: false } }, script);
  }
  if (isOptional(current.id)) {
    const mainId = MAIN_STEPS[state.next] ?? null;
    const back = state.parked && mainId !== null ? { id: mainId, expanded: false } : null;
    return advance(
      {
        ...state,
        current: back,
        parked: false,
        doneOptional: { ...state.doneOptional, [current.id]: true },
      },
      script,
    );
  }
  return advance({ ...state, current: null, next: state.next + 1 }, script);
}

// expand は畳んだ吹き出しを広げる。
export function expand(state: TutorialState): TutorialState {
  const { current } = state;
  if (state.finished || current === null || current.expanded) return state;
  return { ...state, current: { id: current.id, expanded: true } };
}

// finish は「チュートリアルを終える」。以降は何も出さない。
export function finish(state: TutorialState): TutorialState {
  if (state.finished) return state;
  return { ...state, current: null, parked: false, finished: true };
}
