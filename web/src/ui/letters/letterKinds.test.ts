import { describe, expect, it } from "vitest";

import type { LetterLabel } from "../../protocol/index.ts";
import { LETTER_KINDS, letterKind } from "./letterKinds.ts";

const NAMES: Record<LetterLabel, string> = {
  heartbeat: "お元気ですか？",
  heartbeat_resp: "元気です！",
  append: "この内容を記録して！",
  append_probe: "記録の確認",
  append_resp: "記録したよ！",
  append_reject: "記録が合わないよ…",
  prevote: "立候補してもいいかな？",
  prevote_resp: "いいよ！ / まだ代表は元気だよ",
  vote: "代表に立候補します！",
  vote_resp: "あなたに投票します！ / 投票できません",
  request: "住民の依頼書",
  other: "その他の手紙",
};

describe("letterKinds", () => {
  it("LK-1 12 種類の名前が表のとおりで、色を除いた組がすべて違う", () => {
    expect(Object.keys(LETTER_KINDS).sort()).toEqual(Object.keys(NAMES).sort());
    for (const [label, name] of Object.entries(NAMES)) {
      expect(letterKind(label).name, label).toBe(name);
    }
    const keys = Object.values(LETTER_KINDS).map(
      (k) => `${k.shape}/${k.fill}/${String(k.dashed)}/${k.mark}`,
    );
    expect(keys).toHaveLength(12);
    expect(new Set(keys).size).toBe(12);
    const badges = Object.entries(LETTER_KINDS)
      .filter(([, k]) => k.countBadge)
      .map(([label]) => label);
    expect(badges).toEqual(["append"]);
  });

  it("LK-2 知らない label は other と同じ値", () => {
    expect(letterKind("future_label")).toEqual(LETTER_KINDS.other);
    expect(letterKind("toString")).toEqual(LETTER_KINDS.other);
  });
});
