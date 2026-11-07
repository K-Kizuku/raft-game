import { describe, expect, it } from "vitest";

import { worldFromSnapshot } from "../../state/index.ts";
import type { Postmaster, Snapshot } from "../../protocol/index.ts";
import type { NoticeView, World } from "../../state/index.ts";
import { NOTICE_KEYS, noticeText, postmasterTone, speakerName } from "./noticeText.ts";
import type { PostmasterTone } from "./noticeText.ts";
import { island, makeRequest, makeSnapshot } from "./testing/world.ts";

// 島 2 で故障中の船 f1（ferry.broken の {船}）。
const FERRIES: Snapshot["ferries"] = [
  {
    id: "f1",
    name: "ごとう丸",
    capacity: 4,
    state: "docked",
    islandId: 2,
    departAt: 0,
    arriveAt: 0,
    readyAt: 0,
    brokenUntil: 25000,
    cargo: [],
  },
];

function world(): World {
  return worldFromSnapshot(
    makeSnapshot({
      requests: [
        makeRequest("r-stall", 1, { resource: "stall:main" }),
        makeRequest("r-fire", 2, { resource: "firework:a", critical: true }),
        makeRequest("r-lost", 3, { resource: "unknown:x" }),
      ],
      ferries: FERRIES,
    }),
    0,
  );
}

function notice(
  key: string,
  params: Record<string, unknown> = {},
  islandId = 2,
  t = 5000,
): NoticeView {
  return { seq: 1, t, islandId, key, params };
}

describe("noticeText", () => {
  it.each<[string, Record<string, unknown>, string]>([
    ["request.created", { reqId: "r-fire" }, "大花火 Aの予約をお願いしたいんだ！"],
    ["request.succeeded", { reqId: "r-stall" }, "やった！ 屋台が取れたよ！"],
    ["request.rejected", { reqId: "r-fire" }, "えっ、大花火 Aはもう他の島が予約してたの？"],
    ["request.expired", { reqId: "r-stall" }, "屋台、間に合わなかったなあ…"],
    ["island.silent", {}, "最近、だれからも手紙が来ないなあ…"],
    ["node.campaign", {}, "代表と連絡がつかない…わたしが代表に立候補します！"],
    ["leader.elected", {}, "これからは二の島が代表です。よろしく！"],
    ["leader.stepped_down", {}, "みんなの返事が来ない…いったん代表をおります"],
    ["storm.forecast", { routeIds: ["2-3"], at: 10000 }, "5 秒後に二の島と三の島の間が荒れるぞ！"],
    [
      "storm.forecast",
      { routeIds: ["1-2", "1-3"], at: 15000 },
      "10 秒後に一の島と二の島の間、一の島と三の島の間が荒れるぞ！",
    ],
    // NX-R1 注文ラッシュの予報。
    ["rush.forecast", { at: 10000 }, "5 秒後に、あちこちの島から注文がどっと来るぞ！"],
    [
      "ferry.broken",
      { ferryId: "f1", until: 25000 },
      "ごとう丸が故障してしまった…20 秒は港から出られないよ",
    ],
  ])("NT-1 %s → セリフ", (key, params, want) => {
    expect(noticeText(notice(key, params), world(), false)).toBe(want);
  });

  it("NT-1 leader.elected の島がステージに無ければ「島 id」", () => {
    expect(noticeText(notice("leader.elected", {}, 9), world(), false)).toBe(
      "これからは島 9が代表です。よろしく！",
    );
  });

  it("NT-2 show が true なら最初の「代表」だけに Leader を併記する", () => {
    const w = world();
    expect(noticeText(notice("leader.elected"), w, true)).toBe(
      "これからは二の島が代表（Leader）です。よろしく！",
    );
    expect(noticeText(notice("node.campaign"), w, true)).toBe(
      "代表（Leader）と連絡がつかない…わたしが代表に立候補します！",
    );
    expect(noticeText(notice("leader.stepped_down"), w, true)).toBe(
      "みんなの返事が来ない…いったん代表（Leader）をおります",
    );
    expect(noticeText(notice("request.succeeded", { reqId: "r-stall" }), w, true)).toBe(
      "やった！ 屋台が取れたよ！",
    );
  });

  it.each<[string, Record<string, unknown>, string]>([
    ["request.succeeded", { reqId: "r-none" }, "やった！ 依頼が取れたよ！"],
    ["request.succeeded", { reqId: 3 }, "やった！ 依頼が取れたよ！"],
    ["request.succeeded", {}, "やった！ 依頼が取れたよ！"],
    ["request.succeeded", { reqId: "toString" }, "やった！ 依頼が取れたよ！"],
    ["request.created", { reqId: "r-lost" }, "依頼の予約をお願いしたいんだ！"],
    ["storm.forecast", { routeIds: [], at: 10000 }, "5 秒後にどこかの海が荒れるぞ！"],
    ["storm.forecast", { routeIds: ["9-9"], at: 10000 }, "5 秒後にどこかの海が荒れるぞ！"],
    ["storm.forecast", { routeIds: "2-3", at: 10000 }, "5 秒後にどこかの海が荒れるぞ！"],
    ["storm.forecast", { routeIds: ["2-3", 4], at: 10000 }, "5 秒後にどこかの海が荒れるぞ！"],
    [
      "storm.forecast",
      { routeIds: ["9-9", "2-3"], at: 10000 },
      "5 秒後に二の島と三の島の間が荒れるぞ！",
    ],
    ["storm.forecast", { routeIds: ["2-3"] }, "まもなく二の島と三の島の間が荒れるぞ！"],
    [
      "storm.forecast",
      { routeIds: ["2-3"], at: "10000" },
      "まもなく二の島と三の島の間が荒れるぞ！",
    ],
    [
      "storm.forecast",
      { routeIds: ["2-3"], at: Infinity },
      "まもなく二の島と三の島の間が荒れるぞ！",
    ],
    ["storm.forecast", { routeIds: ["2-3"], at: 5300 }, "1 秒後に二の島と三の島の間が荒れるぞ！"],
    ["storm.forecast", { routeIds: ["2-3"], at: 1000 }, "1 秒後に二の島と三の島の間が荒れるぞ！"],
    // NX-R1 at が無い・数でなければ「まもなく」。秒は切り上げ（1 以上）。
    ["rush.forecast", {}, "まもなく、あちこちの島から注文がどっと来るぞ！"],
    ["rush.forecast", { at: "10000" }, "まもなく、あちこちの島から注文がどっと来るぞ！"],
    ["rush.forecast", { at: 5300 }, "1 秒後に、あちこちの島から注文がどっと来るぞ！"],
    // NX-B1 船が分からなければ「船」、until が無ければ「しばらく」。秒は切り上げ（1 以上）。
    [
      "ferry.broken",
      { ferryId: "f9", until: 25000 },
      "船が故障してしまった…20 秒は港から出られないよ",
    ],
    ["ferry.broken", { until: 25000 }, "船が故障してしまった…20 秒は港から出られないよ"],
    [
      "ferry.broken",
      { ferryId: "toString", until: 25000 },
      "船が故障してしまった…20 秒は港から出られないよ",
    ],
    ["ferry.broken", { ferryId: "f1" }, "ごとう丸が故障してしまった…しばらくは港から出られないよ"],
    [
      "ferry.broken",
      { ferryId: "f1", until: "25000" },
      "ごとう丸が故障してしまった…しばらくは港から出られないよ",
    ],
    [
      "ferry.broken",
      { ferryId: "f1", until: 5001 },
      "ごとう丸が故障してしまった…1 秒は港から出られないよ",
    ],
    [
      "ferry.broken",
      { ferryId: "f1", until: 1000 },
      "ごとう丸が故障してしまった…1 秒は港から出られないよ",
    ],
    [
      "ferry.broken",
      { ferryId: "f1", until: 15500 },
      "ごとう丸が故障してしまった…11 秒は港から出られないよ",
    ],
  ])("NT-3 既定の言葉: %s %j", (key, params, want) => {
    expect(noticeText(notice(key, params), world(), false)).toBe(want);
  });

  it("NT-4 知らない key は null、NOTICE_KEYS は表の 11 個", () => {
    expect(noticeText(notice("future.key"), world(), false)).toBeNull();
    expect(noticeText(notice("toString"), world(), true)).toBeNull();
    expect([...NOTICE_KEYS]).toEqual([
      "request.created",
      "request.succeeded",
      "request.rejected",
      "request.expired",
      "island.silent",
      "node.campaign",
      "leader.elected",
      "leader.stepped_down",
      "storm.forecast",
      "rush.forecast",
      "ferry.broken",
    ]);
    for (const key of NOTICE_KEYS) {
      expect(noticeText(notice(key), world(), false), key).not.toBeNull();
    }
  });
});

// 島 2 の郵便局長を postmaster にした世界（ほかの値は world() と同じ）。
function worldWithPostmaster(postmaster: Postmaster | undefined): World {
  const base = makeSnapshot({
    requests: [
      makeRequest("r-stall", 1, { resource: "stall:main" }),
      makeRequest("r-fire", 2, { resource: "firework:a", critical: true }),
    ],
    ferries: FERRIES,
  });
  const islands = base.stage.islands.map((i) =>
    i.id === 2 ? island(i.id, i.name, i.color, i.layout.x, i.layout.y, postmaster) : i,
  );
  return worldFromSnapshot({ ...base, stage: { ...base.stage, islands } }, 0);
}

// 11 個の key の params（NT-1 と同じ値）。
const TONE_PARAMS: Readonly<Record<string, Record<string, unknown>>> = {
  "request.created": { reqId: "r-fire" },
  "request.succeeded": { reqId: "r-stall" },
  "request.rejected": { reqId: "r-fire" },
  "request.expired": { reqId: "r-stall" },
  "island.silent": {},
  "node.campaign": {},
  "leader.elected": {},
  "leader.stepped_down": {},
  "storm.forecast": { routeIds: ["2-3"], at: 10000 },
  "rush.forecast": { at: 10000 },
  "ferry.broken": { ferryId: "f1", until: 25000 },
};

function toneText(key: string, w: World, show = false): string | null {
  return noticeText(notice(key, TONE_PARAMS[key]), w, show);
}

describe("noticeText の口調（T-406c）", () => {
  it("NT-T1 postmaster の無い島は今の 11 個のセリフ（casual）", () => {
    const w = worldWithPostmaster(undefined);
    expect(postmasterTone(w, 2)).toBe("casual");
    expect(NOTICE_KEYS.map((key) => toneText(key, w))).toEqual([
      "大花火 Aの予約をお願いしたいんだ！",
      "やった！ 屋台が取れたよ！",
      "えっ、大花火 Aはもう他の島が予約してたの？",
      "屋台、間に合わなかったなあ…",
      "最近、だれからも手紙が来ないなあ…",
      "代表と連絡がつかない…わたしが代表に立候補します！",
      "これからは二の島が代表です。よろしく！",
      "みんなの返事が来ない…いったん代表をおります",
      "5 秒後に二の島と三の島の間が荒れるぞ！",
      "5 秒後に、あちこちの島から注文がどっと来るぞ！",
      "ごとう丸が故障してしまった…20 秒は港から出られないよ",
    ]);
  });

  it.each<[PostmasterTone, string, string]>([
    ["polite", "request.created", "大花火 Aの予約をお願いします！"],
    ["polite", "request.succeeded", "やりました！ 屋台が取れました！"],
    ["polite", "request.rejected", "えっ、大花火 Aはもう他の島が予約していたのですか？"],
    ["polite", "request.expired", "屋台、間に合いませんでした…"],
    ["polite", "island.silent", "最近、だれからも手紙が来ません…"],
    ["polite", "node.campaign", "代表と連絡がつきません…わたくしが代表に立候補いたします！"],
    ["polite", "leader.elected", "これからは二の島が代表です。よろしくお願いします！"],
    ["polite", "leader.stepped_down", "皆さんの返事が来ません…いったん代表をおります"],
    ["polite", "storm.forecast", "5 秒後に二の島と三の島の間が荒れます！"],
    ["polite", "rush.forecast", "5 秒後に、あちこちの島から注文がどっと来ます！"],
    ["polite", "ferry.broken", "ごとう丸が故障してしまいました…20 秒は港から出られません"],
    ["elder", "request.created", "大花火 Aの予約を頼みたいんじゃ！"],
    ["elder", "request.succeeded", "やったのう！ 屋台が取れたぞい！"],
    ["elder", "request.rejected", "なんと、大花火 Aはもう他の島が予約しておったのか？"],
    ["elder", "request.expired", "屋台、間に合わなんだのう…"],
    ["elder", "island.silent", "近ごろ、だれからも手紙が来んのう…"],
    ["elder", "node.campaign", "代表と連絡がつかん…わしが代表に立候補するぞい！"],
    ["elder", "leader.elected", "これからは二の島が代表じゃ。よろしく頼むぞい！"],
    ["elder", "leader.stepped_down", "みなの返事が来ん…いったん代表をおりるとしよう"],
    ["elder", "storm.forecast", "5 秒後に二の島と三の島の間が荒れるぞい！"],
    ["elder", "rush.forecast", "5 秒後に、あちこちの島から注文がどっと来るぞい！"],
    ["elder", "ferry.broken", "ごとう丸が故障してしもうた…20 秒は港から出られんのう"],
  ])("NT-T2 %s の %s → セリフ", (tone, key, want) => {
    const w = worldWithPostmaster({ name: "テスト局長", tone });
    expect(postmasterTone(w, 2)).toBe(tone);
    expect(toneText(key, w)).toBe(want);
  });

  it("NT-T3 知らない tone は casual のセリフ", () => {
    const w = worldWithPostmaster({ name: "テスト局長", tone: "dialect-x" });
    expect(postmasterTone(w, 2)).toBe("casual");
    expect(postmasterTone(w, 9)).toBe("casual");
    expect(toneText("request.succeeded", w)).toBe("やった！ 屋台が取れたよ！");
    expect(toneText("island.silent", w)).toBe("最近、だれからも手紙が来ないなあ…");
  });

  it("NT-T4 併記がオンでも、polite・elder の node.campaign の「代表（Leader）」は最初の 1 か所だけ", () => {
    const polite = worldWithPostmaster({ name: "テスト局長", tone: "polite" });
    const elder = worldWithPostmaster({ name: "テスト局長", tone: "elder" });
    expect(toneText("node.campaign", polite, true)).toBe(
      "代表（Leader）と連絡がつきません…わたくしが代表に立候補いたします！",
    );
    expect(toneText("node.campaign", elder, true)).toBe(
      "代表（Leader）と連絡がつかん…わしが代表に立候補するぞい！",
    );
  });

  it("NT-T5 speakerName", () => {
    expect(speakerName(worldWithPostmaster({ name: "テスト局長", tone: "polite" }), 2)).toBe(
      "二の島のテスト局長",
    );
    expect(speakerName(worldWithPostmaster(undefined), 2)).toBe("二の島の郵便局長");
    expect(speakerName(worldWithPostmaster({ name: "  ", tone: "polite" }), 2)).toBe(
      "二の島の郵便局長",
    );
    expect(speakerName(worldWithPostmaster(undefined), 9)).toBe("島 9の郵便局長");
  });
});
