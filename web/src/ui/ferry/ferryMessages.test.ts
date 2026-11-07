import { describe, expect, it } from "vitest";

import type { Ack, AckCode } from "../../protocol/index.ts";
import {
  FERRY_OP_NAMES,
  blockerText,
  ferryResultMessage,
  viaHintText,
  viaLabel,
} from "./ferryMessages.ts";
import type { FerryOp } from "./ferryMessages.ts";
import type { FerryBlocker } from "./ferryView.ts";

const OK: Ack = { cmdId: "c-1", ok: true, t: 29000 };

function fail(code?: string): Ack {
  // 知らない code も届く（プロトコル §8）ので、型の外の値も試す。
  return { cmdId: "c-1", ok: false, t: 29000, ...(code === undefined ? {} : { code }) } as Ack;
}

describe("経由の文（#318）", () => {
  it("案内と札", () => {
    expect(viaHintText({ toName: "小豆島", viaName: "豊島" })).toBe(
      "小豆島あての手紙は豊島を経由して運びます。まず豊島へ",
    );
    expect(viaLabel("豊島")).toBe("豊島経由");
  });
});

describe("FERRY_OP_NAMES", () => {
  it("操作の名前", () => {
    expect(FERRY_OP_NAMES).toEqual({
      load: "積み込み",
      unload: "荷降ろし",
      autoload: "おまかせ積み込み",
      depart: "出航",
    });
  });
});

describe("ferryResultMessage", () => {
  const codeRows: [FerryOp, AckCode, string][] = [
    ["load", "ferry_not_docked", "船が港にいないため、積み込みできません。"],
    ["depart", "ferry_not_docked", "船が港にいないため、出航できません。"],
    ["depart", "ferry_not_ready", "停泊の時間がまだ終わっていません。少し待ってください。"],
    ["unload", "ferry_broken", "船が故障しているため、荷降ろしできません。"],
    ["autoload", "capacity_exceeded", "積み荷がいっぱいです。"],
    ["load", "letter_not_here", "その手紙はもう港にありません。"],
    ["unload", "letter_not_here", "その手紙はもう船にありません。"],
    ["autoload", "letter_not_here", "港に二の島宛ての手紙がありません。"],
    ["depart", "letter_not_here", "出航できませんでした。"],
    ["depart", "no_route", "二の島へは直接の航路がありません。"],
    ["depart", "route_blocked", "嵐で二の島への航路が通れません。"],
    ["autoload", "phase_invalid", "いまはおまかせ積み込みできません。"],
    ["load", "rate_limited", "操作が多すぎます。少し待ってください。"],
    ["load", "bad_request", "積み込みできませんでした。"],
  ];

  it.each<[FerryOp, string]>([
    ["load", "手紙を積みました。"],
    ["unload", "手紙を港に戻しました。"],
    ["autoload", "おまかせで積みました。"],
    ["depart", "二の島へ出航しました。"],
  ])("FM-1 ok: true・%s →「%s」", (op, want) => {
    expect(ferryResultMessage(op, OK, "二の島")).toBe(want);
  });

  it.each(codeRows)("FM-1 %s・%s →「%s」", (op, code, want) => {
    expect(ferryResultMessage(op, fail(code), "二の島")).toBe(want);
  });

  it("FM-1 code の 10 種類をすべて試している", () => {
    expect(new Set(codeRows.map(([, code]) => code)).size).toBe(10);
  });

  it.each<[FerryOp, Ack | "rejected", string]>([
    ["autoload", fail("whirlpool"), "おまかせ積み込みできませんでした。"],
    ["depart", fail(), "出航できませんでした。"],
    ["load", "rejected", "サーバーにつながっていないため、積み込みできませんでした。"],
    ["depart", "rejected", "サーバーにつながっていないため、出航できませんでした。"],
  ])("FM-1 %s・知らない code・code なし・reject", (op, result, want) => {
    expect(ferryResultMessage(op, result, "二の島")).toBe(want);
  });

  it.each<[FerryOp, Ack, string]>([
    ["depart", OK, "行き先へ出航しました。"],
    ["autoload", fail("letter_not_here"), "港に行き先宛ての手紙がありません。"],
    ["depart", fail("no_route"), "行き先へは直接の航路がありません。"],
    ["depart", fail("route_blocked"), "嵐で行き先への航路が通れません。"],
  ])("FM-1 toName が null → 「行き先」（%s）", (op, result, want) => {
    expect(ferryResultMessage(op, result, null)).toBe(want);
  });
});

describe("blockerText", () => {
  it.each<[FerryBlocker, number, string]>([
    ["sailing", 0, "航行中は操作できません。"],
    ["no_destination", 0, "行き先を選んでください。"],
    ["not_ready", 1500, "出航まで あと 0:02"],
    ["not_ready", 61000, "出航まで あと 1:01"],
    ["route_blocked", 0, "嵐で航路が通れません。"],
  ])("FM-2 %s・%i →「%s」", (blocker, readyInMs, want) => {
    expect(blockerText(blocker, readyInMs)).toBe(want);
  });
});
