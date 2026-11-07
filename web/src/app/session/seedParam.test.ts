import { describe, expect, it } from "vitest";

import { parseSeedParam, seedUrl } from "./seedParam.ts";

describe("parseSeedParam", () => {
  it.each(["", "?other=1", "?", "other=1&seeds=2"])("SP-1 %j は none", (search) => {
    expect(parseSeedParam(search)).toEqual({ status: "none" });
  });

  it.each([
    ["?seed=0", 0],
    ["seed=42", 42],
    ["?other=1&seed=7", 7],
    ["?seed=9007199254740991", Number.MAX_SAFE_INTEGER],
  ])("SP-1 %j は ok（%d）", (search, seed) => {
    expect(parseSeedParam(search)).toEqual({ status: "ok", seed });
  });

  it.each([
    "?seed=",
    "?seed=abc",
    "?seed=-1",
    "?seed=1.5",
    "?seed=1e3",
    "?seed=007",
    "?seed=%2042",
    "?seed=42%20",
    "?seed=+42",
    "?seed=0x10",
    "?seed=9007199254740992",
    "?seed=99999999999999999",
  ])("SP-2 %j は invalid", (search) => {
    expect(parseSeedParam(search)).toEqual({ status: "invalid" });
  });

  it("SP-3 seed が複数あれば最初の値だけを見る", () => {
    expect(parseSeedParam("?seed=1&seed=2")).toEqual({ status: "ok", seed: 1 });
    expect(parseSeedParam("?seed=abc&seed=2")).toEqual({ status: "invalid" });
  });
});

describe("seedUrl", () => {
  it("SP-4 ステージ紹介の URL に ?seed= を付ける", () => {
    expect(seedUrl("https://raft.kizuku-hackathon.work", "stage1", 42)).toBe(
      "https://raft.kizuku-hackathon.work/stages/stage1?seed=42",
    );
    expect(seedUrl("http://localhost:15213", "demo-izu", 0)).toBe(
      "http://localhost:15213/stages/demo-izu?seed=0",
    );
    expect(seedUrl("https://x.example", "stage1", Number.MAX_SAFE_INTEGER)).toBe(
      "https://x.example/stages/stage1?seed=9007199254740991",
    );
  });

  it("SP-4 作った URL を parseSeedParam で読むと同じシード", () => {
    const url = new URL(seedUrl("https://raft.kizuku-hackathon.work", "stage1", 123456789));
    expect(parseSeedParam(url.search)).toEqual({ status: "ok", seed: 123456789 });
  });

  it.each([-1, 1.5, Number.NaN, Number.POSITIVE_INFINITY, Number.MAX_SAFE_INTEGER + 1])(
    "SP-4 シード %d は投げる",
    (seed) => {
      expect(() => seedUrl("https://raft.kizuku-hackathon.work", "stage1", seed)).toThrow();
    },
  );

  it("SP-4 不正なステージ ID は投げる", () => {
    expect(() => seedUrl("https://raft.kizuku-hackathon.work", "Bad_ID", 1)).toThrow();
  });
});
