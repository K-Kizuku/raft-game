import { describe, expect, it } from "vitest";

import { formatRoute, parsePath, type KnownRoute, type Route } from "./routes.ts";

describe("parsePath", () => {
  // RT-1
  const cases: ReadonlyArray<[string, Route]> = [
    ["/", { name: "title" }],
    ["/howto", { name: "howto" }],
    ["/credits", { name: "credits" }],
    ["/settings", { name: "settings" }],
    ["/stages", { name: "stageSelect" }],
    ["/play", { name: "game" }],
    ["/result", { name: "result" }],
    ["/replay", { name: "replay" }], // RS-1
    ["/replay/", { name: "replay" }],
    ["/replay/x", { name: "notFound" }],
    ["/stages/", { name: "stageSelect" }],
    ["/howto/", { name: "howto" }],
    ["/stages/stage1", { name: "stageIntro", stageId: "stage1" }],
    ["/stages/demo-izu", { name: "stageIntro", stageId: "demo-izu" }],
    ["/stages/demo-izu/", { name: "stageIntro", stageId: "demo-izu" }],
    [`/stages/a${"b".repeat(31)}`, { name: "stageIntro", stageId: `a${"b".repeat(31)}` }],
    ["/stages/Stage1", { name: "notFound" }],
    ["/stages/a_b", { name: "notFound" }],
    ["/stages/%61", { name: "notFound" }],
    ["/stages/-a", { name: "notFound" }],
    [`/stages/a${"b".repeat(32)}`, { name: "notFound" }],
    ["/stages/a/b", { name: "notFound" }],
    ["/stages//", { name: "notFound" }],
    ["/play/x", { name: "notFound" }],
    ["/Stages", { name: "notFound" }],
    ["/foo", { name: "notFound" }],
    ["//", { name: "notFound" }],
    ["", { name: "notFound" }],
    ["/toString", { name: "notFound" }],
  ];
  it.each(cases)("%s", (pathname, want) => {
    expect(parsePath(pathname)).toEqual(want);
  });
});

describe("formatRoute", () => {
  // RT-2
  const cases: ReadonlyArray<[KnownRoute, string]> = [
    [{ name: "title" }, "/"],
    [{ name: "howto" }, "/howto"],
    [{ name: "credits" }, "/credits"],
    [{ name: "settings" }, "/settings"],
    [{ name: "stageSelect" }, "/stages"],
    [{ name: "stageIntro", stageId: "stage1" }, "/stages/stage1"],
    [{ name: "stageIntro", stageId: "demo-izu" }, "/stages/demo-izu"],
    [{ name: "game" }, "/play"],
    [{ name: "result" }, "/result"],
    [{ name: "replay" }, "/replay"], // RS-1
  ];
  it.each(cases)("%j → %s", (route, want) => {
    expect(formatRoute(route)).toBe(want);
    expect(parsePath(formatRoute(route))).toEqual(route);
  });

  it.each(["A", "", "a_b", "a/b"])("stageIntro の不正な ID %j で投げる", (stageId) => {
    expect(() => formatRoute({ name: "stageIntro", stageId })).toThrow(Error);
  });
});
