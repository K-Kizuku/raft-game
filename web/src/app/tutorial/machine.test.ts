import { describe, expect, it } from "vitest";

import type { World } from "../../state/index.ts";
import {
  advance,
  expand,
  finish,
  initialTutorialState,
  observeFacts,
  pressPrimary,
  resolveIslands,
} from "./machine.ts";
import type { TutorialFacts, TutorialIslands, TutorialState } from "./machine.ts";
import { STAGE1_TUTORIAL } from "./script.ts";
import {
  FAR,
  LEADER,
  RELAY,
  ferryLetter,
  portLetter,
  sailingFerry,
  stage1Ferry,
  stage1Request,
  stage1Snapshot,
  stage1World,
  withFerry,
  withLetters,
  withRequests,
  withRole,
} from "./testing/stage1World.ts";

const script = STAGE1_TUTORIAL;
const ISLANDS: TutorialIslands = { leader: LEADER, relay: RELAY, far: FAR };

function observe(
  world: World,
  facts: TutorialFacts = initialTutorialState().facts,
  opts: Partial<{ simNow: number; openingActive: boolean }> = {},
): TutorialFacts {
  return observeFacts(facts, {
    world,
    islands: ISLANDS,
    simNow: opts.simNow ?? 0,
    openingActive: opts.openingActive ?? false,
    t8AfterMs: script.t8AfterMs,
  });
}

// with は事実を立てて advance する（事実は戻らないので、足すだけ）。
function withFacts(state: TutorialState, facts: Partial<TutorialFacts>): TutorialState {
  return advance({ ...state, facts: { ...state.facts, ...facts } }, script);
}

function shown(state: TutorialState): string | null {
  if (state.current === null) return null;
  return `${state.current.id}${state.current.expanded ? "" : "（畳む）"}`;
}

// T1 を出して「次へ」まで進めた状態。
function afterT1(): TutorialState {
  const s = withFacts(initialTutorialState(), { opened: true });
  expect(shown(s)).toBe("T1");
  return pressPrimary(s, script);
}

describe("resolveIslands", () => {
  it("MA-1 台本の名前から島の ID を引き、無ければ null", () => {
    expect(resolveIslands(stage1World(), script)).toEqual({ leader: 1, relay: 2, far: 3 });
    const base = stage1Snapshot();
    const renamed = stage1Snapshot({
      stage: {
        ...base.stage,
        islands: base.stage.islands.map((i) => (i.id === RELAY ? { ...i, name: "手島" } : i)),
      },
    });
    expect(resolveIslands(stage1World(renamed), script)).toBeNull();
  });

  it("同じ名前が 2 つあれば最初のもの", () => {
    const base = stage1Snapshot();
    const twice = stage1Snapshot({
      stage: {
        ...base.stage,
        islands: base.stage.islands.map((i) => (i.id === FAR ? { ...i, name: "小豆島" } : i)),
      },
    });
    expect(resolveIslands(stage1World(twice), script)).toBeNull();
    const dup = stage1Snapshot({
      stage: {
        ...base.stage,
        islands: [...base.stage.islands, { ...base.stage.islands[0]!, id: 9 }],
      },
    });
    expect(resolveIslands(stage1World(dup), script)).toEqual({ leader: 1, relay: 2, far: 3 });
  });
});

describe("observeFacts", () => {
  const base = stage1Snapshot();
  const cases: readonly {
    fact: keyof TutorialFacts;
    yes: () => World;
    no: () => World;
    simNow?: [number, number];
  }[] = [
    {
      fact: "opened",
      yes: () => stage1World(base),
      no: () => stage1World(stage1Snapshot({ phase: "opening" })),
    },
    {
      fact: "requestSeen",
      yes: () => stage1World(withRequests(base, [stage1Request("r1", LEADER)])),
      no: () => stage1World(base),
    },
    {
      fact: "loaded",
      yes: () =>
        stage1World(
          withLetters(withFerry(base, stage1Ferry({ cargo: [5] })), [ferryLetter(5, 1, 2)]),
        ),
      no: () => stage1World(withLetters(base, [portLetter(5, 1, 2)])),
    },
    {
      fact: "departed",
      yes: () => stage1World(withFerry(base, sailingFerry(LEADER, RELAY))),
      no: () => stage1World(base),
    },
    {
      fact: "atRelay",
      yes: () => stage1World(withFerry(base, stage1Ferry({ islandId: RELAY }))),
      no: () => stage1World(withFerry(base, sailingFerry(LEADER, RELAY))),
    },
    {
      fact: "repliedFromRelay",
      yes: () =>
        stage1World(
          withLetters(withFerry(base, sailingFerry(RELAY, LEADER, [7])), [
            ferryLetter(7, RELAY, LEADER),
          ]),
        ),
      no: () =>
        stage1World(
          withLetters(withFerry(base, sailingFerry(RELAY, LEADER, [7])), [
            ferryLetter(7, RELAY, FAR),
          ]),
        ),
    },
    {
      fact: "firstSuccess",
      yes: () => stage1World(withRequests(base, [stage1Request("r1", LEADER, "succeeded")])),
      no: () => stage1World(withRequests(base, [stage1Request("r1", LEADER, "committed")])),
    },
    {
      fact: "farRequest",
      yes: () => stage1World(withRequests(base, [stage1Request("r1", FAR)])),
      no: () => stage1World(withRequests(base, [stage1Request("r1", RELAY)])),
    },
    {
      fact: "farProposed",
      yes: () => stage1World(withRequests(base, [stage1Request("r1", FAR, "proposed")])),
      no: () => stage1World(withRequests(base, [stage1Request("r1", FAR, "in_transit")])),
    },
    {
      fact: "farCommitted",
      yes: () => stage1World(withRequests(base, [stage1Request("r1", FAR, "committed")])),
      no: () => stage1World(withRequests(base, [stage1Request("r1", LEADER, "committed")])),
    },
    {
      fact: "farSettled",
      yes: () => stage1World(withRequests(base, [stage1Request("r1", FAR, "rejected")])),
      no: () => stage1World(withRequests(base, [stage1Request("r1", FAR, "committed")])),
    },
    {
      fact: "afterT8Time",
      yes: () => stage1World(base),
      no: () => stage1World(base),
      simNow: [120000, 119999],
    },
    {
      fact: "farPreVote",
      yes: () => stage1World(withRole(base, FAR, "pre_candidate")),
      no: () => stage1World(withRole(base, FAR, "candidate")),
    },
    {
      fact: "farPreVote",
      yes: () => stage1World(withLetters(base, [portLetter(4, FAR, LEADER, { label: "prevote" })])),
      no: () =>
        stage1World(withLetters(base, [portLetter(4, RELAY, LEADER, { label: "prevote" })])),
    },
  ];

  it.each(cases)("MA-2 $fact が成り立つ世界・成り立たない世界", ({ fact, yes, no, simNow }) => {
    const [yesNow, noNow] = simNow ?? [0, 0];
    const empty = initialTutorialState().facts;
    expect(observe(no(), empty, { simNow: noNow })[fact]).toBe(false);
    const seen = observe(yes(), empty, { simNow: yesNow });
    expect(seen[fact]).toBe(true);
    // 一度 true の事実は、成り立たない世界を観察しても true のまま。
    expect(observe(no(), seen, { simNow: noNow })[fact]).toBe(true);
  });

  it("MA-2 opened は openingActive が真なら偽", () => {
    expect(observe(stage1World(), undefined, { openingActive: true }).opened).toBe(false);
    expect(observe(stage1World(), undefined, { openingActive: false }).opened).toBe(true);
  });

  it("何も変わらなければ同じオブジェクトを返す", () => {
    const facts = observe(stage1World());
    expect(observe(stage1World(), facts)).toBe(facts);
  });
});

describe("advance・pressPrimary", () => {
  it("MA-3 本筋の通し", () => {
    let s = initialTutorialState();
    s = advance(s, script);
    expect(shown(s)).toBeNull();
    s = withFacts(s, { opened: true });
    expect(shown(s)).toBe("T1");
    s = pressPrimary(s, script);
    expect(shown(s)).toBeNull();
    s = withFacts(s, { requestSeen: true });
    expect(shown(s)).toBe("T2");
    s = withFacts(s, { loaded: true });
    expect(shown(s)).toBe("T3");
    s = withFacts(s, { departed: true });
    expect(shown(s)).toBeNull();
    s = withFacts(s, { atRelay: true });
    expect(shown(s)).toBe("T4a");
    s = pressPrimary(s, script);
    expect(shown(s)).toBe("T4");
    s = withFacts(s, { repliedFromRelay: true });
    expect(shown(s)).toBeNull();
    s = withFacts(s, { firstSuccess: true });
    expect(shown(s)).toBe("T5");
    s = pressPrimary(s, script);
    expect(shown(s)).toBeNull();
    s = withFacts(s, { farRequest: true });
    expect(shown(s)).toBe("T6");
    s = withFacts(s, { farProposed: true });
    expect(shown(s)).toBeNull();
    s = withFacts(s, { farCommitted: true });
    expect(shown(s)).toBe("T7");
    s = withFacts(s, { farSettled: true });
    expect(shown(s)).toBeNull();
    expect(s.next).toBe(8);
  });

  it("MA-4 終わる条件がすでに成り立つ action の段は出さない", () => {
    let s = afterT1();
    s = withFacts(s, { requestSeen: true, loaded: true, departed: true });
    expect(shown(s)).toBeNull();
    expect(s.next).toBe(3); // T4a
    s = withFacts(s, { atRelay: true, repliedFromRelay: true, firstSuccess: true });
    // T4a は「次へ」で進む段なので、T4 が終わっていても出す。
    expect(shown(s)).toBe("T4a");
    s = pressPrimary(s, script);
    expect(shown(s)).toBe("T5");
    s = pressPrimary(s, script);
    s = withFacts(s, { farRequest: true, farProposed: true, farSettled: true });
    expect(shown(s)).toBeNull();
    expect(s.next).toBe(8);
  });

  it("T4a は豊島に初めて着いたときに出て、着いたのを見逃しても返事を運び出したら出る", () => {
    let s = afterT1();
    s = withFacts(s, { requestSeen: true, loaded: true, departed: true });
    expect(shown(s)).toBeNull();
    s = withFacts(s, { atRelay: true });
    expect(shown(s)).toBe("T4a");

    s = afterT1();
    s = withFacts(s, { requestSeen: true, loaded: true, departed: true });
    s = withFacts(s, { repliedFromRelay: true });
    expect(shown(s)).toBe("T4a");
    s = pressPrimary(s, script);
    expect(shown(s)).toBeNull();
    expect(s.next).toBe(5); // T5
  });

  it("firstSuccess が T4 より先なら、T4 の後に T5 がすぐ出る", () => {
    let s = afterT1();
    s = withFacts(s, { requestSeen: true, loaded: true, departed: true, firstSuccess: true });
    expect(shown(s)).toBeNull();
    s = withFacts(s, { atRelay: true });
    expect(shown(s)).toBe("T4a");
    s = pressPrimary(s, script);
    expect(shown(s)).toBe("T4");
    s = withFacts(s, { repliedFromRelay: true });
    expect(shown(s)).toBe("T5");
  });

  it("MA-5 action の段は「わかった」で畳み、畳んだまま条件で進み、expand で広がる", () => {
    let s = withFacts(afterT1(), { requestSeen: true });
    expect(shown(s)).toBe("T2");
    s = pressPrimary(s, script);
    expect(s.current).toEqual({ id: "T2", expanded: false });
    s = expand(s);
    expect(s.current).toEqual({ id: "T2", expanded: true });
    s = pressPrimary(s, script);
    s = withFacts(s, { loaded: true });
    expect(s.current).toEqual({ id: "T3", expanded: true });
    // 畳んでいる段の「わかった」（押せない）は何もしない。
    const collapsed = pressPrimary(s, script);
    expect(pressPrimary(collapsed, script)).toBe(collapsed);
  });

  it("MA-6 任意の段", () => {
    // T1 の前には出さない。
    let s = withFacts(initialTutorialState(), { afterT8Time: true });
    expect(shown(s)).toBeNull();
    s = withFacts(s, { opened: true });
    expect(shown(s)).toBe("T1");
    s = pressPrimary(s, script);
    expect(shown(s)).toBe("T8"); // T1 の後、本筋が出せないので T8

    // T2 が畳まれているときに T8 → 広がり、「次へ」で T2（畳まれたまま）に戻る。
    s = withFacts(afterT1(), { requestSeen: true });
    s = pressPrimary(s, script);
    expect(shown(s)).toBe("T2（畳む）");
    s = withFacts(s, { afterT8Time: true });
    expect(shown(s)).toBe("T8");
    s = pressPrimary(s, script);
    expect(shown(s)).toBe("T2（畳む）");
    expect(s.doneOptional.T8).toBe(true);
    // 2 回は出ない。
    s = advance(s, script);
    expect(shown(s)).toBe("T2（畳む）");

    // T2 が広がっているときは T8 を待つ。
    s = withFacts(afterT1(), { requestSeen: true, afterT8Time: true });
    expect(shown(s)).toBe("T2");
    s = pressPrimary(s, script);
    expect(shown(s)).toBe("T8");

    // 本筋の出す条件と T9 の条件が同時 → 本筋が先。
    s = withFacts(afterT1(), { requestSeen: true, farPreVote: true });
    expect(shown(s)).toBe("T2");
    s = pressPrimary(s, script);
    expect(shown(s)).toBe("T9");
    s = pressPrimary(s, script);
    expect(shown(s)).toBe("T2（畳む）");
    s = withFacts(s, { farPreVote: true });
    expect(shown(s)).toBe("T2（畳む）");
  });

  it("任意の段の間に、下の本筋の段が終わったら、次の本筋の段が後に出る", () => {
    let s = withFacts(afterT1(), { requestSeen: true });
    s = pressPrimary(s, script);
    s = withFacts(s, { afterT8Time: true });
    expect(shown(s)).toBe("T8");
    s = withFacts(s, { loaded: true });
    expect(shown(s)).toBe("T8");
    expect(s.next).toBe(2);
    expect(s.parked).toBe(false);
    s = pressPrimary(s, script);
    expect(s.current).toEqual({ id: "T3", expanded: true });
  });

  it("MA-7 finish の後は何も出さない", () => {
    let s = withFacts(afterT1(), { requestSeen: true });
    s = finish(s);
    expect(s.current).toBeNull();
    expect(s.finished).toBe(true);
    s = withFacts(s, { loaded: true, departed: true, atRelay: true, afterT8Time: true });
    expect(s.current).toBeNull();
    expect(pressPrimary(s, script)).toBe(s);
    expect(expand(s)).toBe(s);
  });

  it("MA-8 引数を書き換えない", () => {
    const deepFreeze = <T>(value: T): T => {
      if (value !== null && typeof value === "object") {
        for (const v of Object.values(value)) deepFreeze(v);
        Object.freeze(value);
      }
      return value;
    };
    const world = deepFreeze(
      stage1World(
        withRequests(withLetters(stage1Snapshot(), [portLetter(1, FAR, LEADER)]), [
          stage1Request("r1", FAR, "committed"),
        ]),
      ),
    );
    const facts = deepFreeze(initialTutorialState().facts);
    const observed = deepFreeze(observe(world, facts, { simNow: 200000 }));
    expect(facts.opened).toBe(false);
    const s0 = deepFreeze({ ...initialTutorialState(), facts: observed });
    const s1 = deepFreeze(advance(s0, script));
    expect(shown(s1)).toBe("T1");
    const s2 = deepFreeze(pressPrimary(s1, script));
    const s3 = deepFreeze(expand(s2));
    deepFreeze(finish(s3));
    expect(s0.current).toBeNull();
    expect(advance(s0, script)).toEqual(s1);
    expect(advance(s1, script)).toBe(s1);
  });
});
