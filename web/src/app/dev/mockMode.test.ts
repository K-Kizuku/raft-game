import { describe, expect, it, vi } from "vitest";

import type { StageSummary } from "../types.ts";
import { isMockEnabled, withMockStage } from "./mockMode.ts";

const stage1: StageSummary = {
  id: "stage1",
  title: "はじめての郵便船",
  region: "瀬戸内",
  islands: 3,
  ferries: 1,
  durationMs: 180000,
};

const mock: StageSummary = {
  id: "mini",
  title: "テストの海（開発用の記録）",
  region: "開発用",
  islands: 3,
  ferries: 1,
  durationMs: 60000,
};

describe("isMockEnabled", () => {
  it.each([
    { env: { MODE: "development" }, want: true },
    { env: { MODE: "development", VITE_GAME_MOCK: "off" }, want: false },
    { env: { MODE: "development", VITE_GAME_MOCK: "on" }, want: true },
    { env: { MODE: "development", VITE_GAME_MOCK: "" }, want: true },
    { env: { MODE: "test" }, want: false },
    { env: { MODE: "production" }, want: false },
  ])("MM-1 $env → $want", ({ env, want }) => {
    expect(isMockEnabled(env)).toBe(want);
  });
});

describe("withMockStage", () => {
  it("MM-2 一覧の末尾に開発用のステージを足す", async () => {
    const load = withMockStage(
      async () => [stage1],
      async () => mock,
    );
    await expect(load(new AbortController().signal)).resolves.toEqual([stage1, mock]);
  });

  it("MM-2 同じ id があれば足さない", async () => {
    const same = { ...mock, title: "サーバーの mini" };
    const load = withMockStage(
      async () => [stage1, same],
      async () => mock,
    );
    await expect(load(new AbortController().signal)).resolves.toEqual([stage1, same]);
  });

  it("MM-2 一覧の失敗はそのまま失敗にし、開発用のステージを読まない", async () => {
    const error = new Error("boom");
    const loadMockStage = vi.fn(async () => mock);
    const load = withMockStage(async () => {
      throw error;
    }, loadMockStage);
    await expect(load(new AbortController().signal)).rejects.toBe(error);
    expect(loadMockStage).not.toHaveBeenCalled();
  });

  it("MM-2 中断したら結果を返さない", async () => {
    const controller = new AbortController();
    const load = withMockStage(
      async () => [stage1],
      async () => {
        controller.abort();
        return mock;
      },
    );
    await expect(load(controller.signal)).rejects.toMatchObject({ name: "AbortError" });
  });
});
