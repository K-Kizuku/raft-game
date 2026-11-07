import { describe, expect, it } from "vitest";

import { worldFromSnapshot } from "../../state/index.ts";
import type { NoticeView, World } from "../../state/index.ts";
import { NOTICE_KEYS, noticeText } from "./noticeText.ts";
import { makeRequest, makeSnapshot } from "./testing/world.ts";

function world(): World {
  return worldFromSnapshot(
    makeSnapshot({
      requests: [
        makeRequest("r-stall", 1, { resource: "stall:main" }),
        makeRequest("r-fire", 2, { resource: "firework:a", critical: true }),
        makeRequest("r-lost", 3, { resource: "unknown:x" }),
      ],
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
  ])("NT-3 既定の言葉: %s %j", (key, params, want) => {
    expect(noticeText(notice(key, params), world(), false)).toBe(want);
  });

  it("NT-4 知らない key は null、NOTICE_KEYS は表の 9 個", () => {
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
    ]);
    for (const key of NOTICE_KEYS) {
      expect(noticeText(notice(key), world(), false), key).not.toBeNull();
    }
  });
});
