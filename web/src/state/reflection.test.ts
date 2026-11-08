import { describe, expect, it } from "vitest";

import type { Letter, LetterLabel, Loc, Request, ServerEvent } from "../protocol/index.ts";
import { applyEvents, withClock, worldFromSnapshot } from "./applyEvent.ts";
import { loadMiniRound } from "./fixtures/loadRecord.ts";
import {
  EMPTY_REFLECTION_TALLY,
  MAX_REFLECTION_TIPS,
  stepReflection,
  summarizeReflection,
  type ReflectionTally,
} from "./reflection.ts";
import type { World } from "./world.ts";

// ミニステージ: 島 1・2・3、航路はどれも 4 秒、船 f1（定員 4）は島 1 に停泊している。
const LONGEST_TRAVEL_MS = 4000;

function letter(id: number, loc: Loc, label: LetterLabel = "heartbeat", from = 1, to = 2): Letter {
  return { id, kind: "raft", from, to, createdAt: 0, loc, label };
}

const port = (islandId: number): Loc => ({ type: "port", islandId });
const onFerry: Loc = { type: "ferry", ferryId: "f1" };

// Round は world と記録をいっしょに進める（ストアの applyEvents と同じに、イベントを当ててから時計を進める）。
class Round {
  world: World;
  tally: ReflectionTally;
  private seq: number;

  constructor(sessionId = "s1") {
    const { snapshot } = loadMiniRound();
    snapshot.session = { ...snapshot.session, id: sessionId };
    this.world = worldFromSnapshot(snapshot, 0);
    this.tally = stepReflection(EMPTY_REFLECTION_TALLY, null, this.world);
    this.seq = snapshot.lastSeq;
  }

  // batch はイベントを 1 回分として届ける。各要素は seq・t を除いたもの。
  batch(t: number, events: readonly Record<string, unknown>[]): this {
    const full = events.map((e) => ({ ...e, seq: ++this.seq, t }) as unknown as ServerEvent);
    const prev = this.world;
    const next = withClock(applyEvents(prev, full), { ...clockOf(prev), t }, t);
    this.world = next;
    this.tally = stepReflection(this.tally, prev, next);
    return this;
  }

  summary() {
    return summarizeReflection(this.tally, this.world);
  }
}

function clockOf(world: World) {
  const { simTime, speed, paused, endsAt } = world.clock;
  return { t: simTime, speed, paused, endsAt };
}

const created = (l: Letter) => ({ type: "letter.created", letter: l });
const moved = (letterId: number, loc: Loc) => ({ type: "letter.moved", letterId, loc });
const departed = (from: number, to: number, departAt: number) => ({
  type: "ferry.departed",
  ferryId: "f1",
  from,
  to,
  departAt,
  arriveAt: departAt + LONGEST_TRAVEL_MS,
});
const arrived = (islandId: number, t: number) => ({
  type: "ferry.arrived",
  ferryId: "f1",
  islandId,
  readyAt: t + 1000,
});

function request(id: string): Request {
  return {
    id,
    origin: 2,
    kind: "stall.reserve",
    resource: "stall",
    critical: false,
    createdAt: 0,
    deadline: 30000,
    status: "waiting",
    expired: false,
    result: null,
  };
}

describe("積まずに出航した回数", () => {
  it("港に手紙があるのに積まずに出航したら、出航した島で 1 回と数える", () => {
    const round = new Round()
      .batch(1000, [created(letter(1, port(1)))])
      .batch(2000, [departed(1, 2, 2000)]);
    expect(round.summary().emptyDepartures).toEqual({
      total: 1,
      byIsland: [{ islandId: 1, count: 1 }],
    });
  });

  it("積んでから出航したら数えない（別の回でも、同じ回でも）", () => {
    const separate = new Round()
      .batch(1000, [created(letter(1, port(1))), created(letter(2, port(1)))])
      .batch(1500, [moved(1, onFerry)])
      .batch(2000, [departed(1, 2, 2000)]);
    expect(separate.summary().emptyDepartures.total).toBe(0);

    const sameBatch = new Round()
      .batch(1000, [created(letter(1, port(1))), created(letter(2, port(1)))])
      .batch(2000, [moved(1, onFerry), departed(1, 2, 2000)]);
    expect(sameBatch.summary().emptyDepartures.total).toBe(0);
  });

  it("港に手紙がない・船に空きがないときは数えない", () => {
    const noLetters = new Round().batch(2000, [departed(1, 2, 2000)]);
    expect(noLetters.summary().emptyDepartures.total).toBe(0);

    // 定員 4 を、島 1 で積んだ手紙で埋めて出航し、島 2 では積めないまま出航する。
    const full = new Round()
      .batch(
        1000,
        [1, 2, 3, 4].map((id) => created(letter(id, port(1), "append", 1, 3))),
      )
      .batch(
        1500,
        [1, 2, 3, 4].map((id) => moved(id, onFerry)),
      )
      .batch(2000, [departed(1, 2, 2000)])
      .batch(6000, [arrived(2, 6000), created(letter(5, port(2), "append_resp", 2, 1))])
      .batch(8000, [departed(2, 3, 8000)]);
    expect(full.summary().emptyDepartures.total).toBe(0);
  });

  it("着いたら数え直す。前の島で積んでいても、通り道の島で積まなければ数える", () => {
    const round = new Round()
      .batch(1000, [created(letter(1, port(1), "append", 1, 3))])
      .batch(1500, [moved(1, onFerry)])
      .batch(2000, [departed(1, 2, 2000)])
      .batch(6000, [arrived(2, 6000), created(letter(2, port(2), "append_resp", 2, 1))])
      .batch(8000, [departed(2, 3, 8000)])
      .batch(12000, [arrived(3, 12000)])
      .batch(13000, [created(letter(3, port(3), "append_resp", 3, 1))])
      .batch(14000, [departed(3, 2, 14000)])
      .batch(18000, [arrived(2, 18000)])
      .batch(20000, [departed(2, 1, 20000)]);
    // 島 2 で 2 回（手紙 2 は港に残ったまま）、島 3 で 1 回。多い順に並ぶ。
    expect(round.summary().emptyDepartures).toEqual({
      total: 3,
      byIsland: [
        { islandId: 2, count: 2 },
        { islandId: 3, count: 1 },
      ],
    });
  });
});

describe("代表への返事が港で待った時間", () => {
  it("返事が港にある間を 1 回とし、積まれたら長さを記録する", () => {
    const round = new Round()
      .batch(1000, [created(letter(1, port(2), "heartbeat_resp", 2, 1))])
      .batch(5000, [])
      .batch(9000, [moved(1, { type: "relay", islandId: 2 })]);
    expect(round.summary().longestReplyWait).toEqual({ ms: 8000, islandId: 2 });
  });

  it("合流で入れ替わっても、港に返事がある間は続けて数える", () => {
    const round = new Round()
      .batch(1000, [created(letter(1, port(3), "append_resp", 3, 1))])
      .batch(4000, [
        { type: "letter.dropped", letterId: 1, reason: "superseded" },
        created(letter(2, port(3), "append_resp", 3, 1)),
      ])
      .batch(11000, [{ type: "letter.dropped", letterId: 2, reason: "superseded" }]);
    expect(round.summary().longestReplyWait).toEqual({ ms: 10000, islandId: 3 });
  });

  it("返事でない手紙は数えない。ラウンドの終わりに待っていた分も含める", () => {
    const round = new Round()
      .batch(1000, [
        created(letter(1, port(1), "heartbeat", 1, 2)),
        created(letter(2, port(2), "append_reject", 2, 1)),
      ])
      .batch(4000, [created(letter(3, port(3), "heartbeat_resp", 3, 1))])
      .batch(15000, []);
    // 島 2 は 14 秒、島 3 は 11 秒待っている（どちらもまだ港にある）。
    expect(round.summary().longestReplyWait).toEqual({ ms: 14000, islandId: 2 });
  });

  it("返事が一度も港に来なければ null", () => {
    const round = new Round().batch(1000, [created(letter(1, port(1)))]);
    expect(round.summary().longestReplyWait).toBeNull();
  });
});

describe("期限切れの依頼・港であふれた手紙", () => {
  it("期限切れの依頼と、overflow で処分された手紙を数える（superseded は数えない）", () => {
    const round = new Round()
      .batch(1000, [
        { type: "request.created", request: request("r1") },
        { type: "request.created", request: request("r2") },
        { type: "request.created", request: request("r3") },
        created(letter(1, port(1))),
        created(letter(2, port(1))),
        created(letter(3, port(1))),
      ])
      .batch(2000, [
        { type: "letter.dropped", letterId: 1, reason: "overflow" },
        { type: "letter.dropped", letterId: 2, reason: "superseded" },
      ])
      .batch(31000, [
        { type: "request.updated", id: "r1", status: "waiting", expired: true, result: null },
        { type: "request.updated", id: "r2", status: "waiting", expired: true, result: null },
        { type: "letter.dropped", letterId: 3, reason: "overflow" },
      ]);
    const summary = round.summary();
    expect(summary.expiredRequests).toBe(2);
    expect(summary.overflowDrops).toBe(2);
  });
});

describe("次に試すこと", () => {
  it("積まずに出航 → 返事の待ち → 期限切れ → あふれ の順に、最大 2 つ選ぶ", () => {
    const round = new Round()
      .batch(1000, [
        created(letter(1, port(1))),
        created(letter(2, port(2), "heartbeat_resp", 2, 1)),
        { type: "request.created", request: request("r1") },
      ])
      .batch(2000, [departed(1, 3, 2000)])
      .batch(31000, [
        { type: "request.updated", id: "r1", status: "waiting", expired: true, result: null },
      ]);
    const summary = round.summary();
    expect(summary.tips).toEqual(["emptyDepartures", "replyWait"]);
    expect(summary.tips.length).toBeLessThanOrEqual(MAX_REFLECTION_TIPS);
  });

  it("返事の待ちは、いちばん長い船旅の時間を超えたときだけ選ぶ", () => {
    const short = new Round()
      .batch(1000, [created(letter(1, port(2), "heartbeat_resp", 2, 1))])
      .batch(1000 + LONGEST_TRAVEL_MS, [moved(1, { type: "relay", islandId: 2 })]);
    expect(short.summary().replyWaitLimitMs).toBe(LONGEST_TRAVEL_MS);
    expect(short.summary().longestReplyWait?.ms).toBe(LONGEST_TRAVEL_MS);
    expect(short.summary().tips).toEqual([]);

    const long = new Round()
      .batch(1000, [created(letter(1, port(2), "heartbeat_resp", 2, 1))])
      .batch(1001 + LONGEST_TRAVEL_MS, []);
    expect(long.summary().tips).toEqual(["replyWait"]);
  });

  it("詰まりが無ければ空", () => {
    expect(new Round().summary().tips).toEqual([]);
  });
});

describe("数え直し", () => {
  it("world が null（ストアの reset）なら空に戻し、別のセッションが来たら数え直す", () => {
    const round = new Round("s1")
      .batch(1000, [created(letter(1, port(1)))])
      .batch(2000, [departed(1, 2, 2000)]);
    expect(round.summary().emptyDepartures.total).toBe(1);

    expect(stepReflection(round.tally, round.world, null)).toBe(EMPTY_REFLECTION_TALLY);

    const other = new Round("s2").world;
    const restarted = stepReflection(round.tally, round.world, other);
    expect(restarted.sessionId).toBe("s2");
    expect(summarizeReflection(restarted, other).emptyDepartures.total).toBe(0);
  });

  it("記録と違うセッションの world では、記録の数を使わない", () => {
    const round = new Round("s1")
      .batch(1000, [created(letter(1, port(1)))])
      .batch(2000, [departed(1, 2, 2000)]);
    const other = new Round("s2").world;
    expect(summarizeReflection(round.tally, other).emptyDepartures.total).toBe(0);
    expect(summarizeReflection(round.tally, null).tips).toEqual([]);
  });

  it("world が変わらなければ同じ記録を返し、引数を書き換えない", () => {
    const round = new Round().batch(1000, [created(letter(1, port(2), "append_resp", 2, 1))]);
    expect(stepReflection(round.tally, round.world, round.world)).toBe(round.tally);

    const tally = round.tally;
    const prev = round.world;
    const before = JSON.stringify({ tally, prev });
    round.batch(5000, [moved(1, { type: "relay", islandId: 2 })]);
    expect(round.tally.longestReplyWait).toEqual({ ms: 4000, islandId: 2 });
    expect(JSON.stringify({ tally, prev })).toBe(before);
  });
});
