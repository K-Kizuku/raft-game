import { afterEach, describe, expect, it, vi } from "vitest";

import type { Ack } from "../../protocol/index.ts";
import type { GameCommandSender } from "../commands/GameCommands.tsx";
import {
  isAutoFerryFeatureEnabled,
  loadAutoFerryFeature,
  parseAutoFerryFeature,
  resetAutoFerryFeature,
  sendFerryAuto,
  setAutoFerryFeature,
} from "./autoFerryFeature.ts";

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function stagesFetch(autoFerry: unknown): ReturnType<typeof vi.fn<typeof fetch>> {
  return vi.fn<typeof fetch>(() =>
    Promise.resolve(jsonResponse({ stages: [], features: { autoFerry } })),
  );
}

afterEach(() => {
  resetAutoFerryFeature();
});

describe("parseAutoFerryFeature", () => {
  it.each<[string, unknown, boolean]>([
    ["true", { stages: [], features: { autoFerry: true } }, true],
    ["false", { stages: [], features: { autoFerry: false } }, false],
    ["features が無い（古い Worker）", { stages: [] }, false],
    ["真偽値でない", { stages: [], features: { autoFerry: "1" } }, false],
    ["features が配列", { stages: [], features: [] }, false],
    ["本文が null", null, false],
  ])("AF-1 %s", (_, body, expected) => {
    expect(parseAutoFerryFeature(body)).toBe(expected);
  });
});

describe("loadAutoFerryFeature", () => {
  it("AF-2 読み込む前は false。/api/stages を 1 回だけ読み、features.autoFerry で決める", async () => {
    expect(isAutoFerryFeatureEnabled()).toBe(false);
    const fetchImpl = stagesFetch(true);
    await loadAutoFerryFeature(fetchImpl);
    await loadAutoFerryFeature(fetchImpl);
    expect(isAutoFerryFeatureEnabled()).toBe(true);
    expect(fetchImpl).toHaveBeenCalledTimes(1);
    expect(fetchImpl.mock.calls[0]?.[0]).toBe("/api/stages");
  });

  it("AF-2 features.autoFerry が false（AUTO_FERRY が無効）なら false", async () => {
    await loadAutoFerryFeature(stagesFetch(false));
    expect(isAutoFerryFeatureEnabled()).toBe(false);
  });

  it("AF-3 失敗（通信・200 以外）なら false のまま、次に呼ばれたら読み直す", async () => {
    const fail = vi.fn<typeof fetch>(() => Promise.reject(new TypeError("offline")));
    await loadAutoFerryFeature(fail);
    expect(isAutoFerryFeatureEnabled()).toBe(false);
    const status = vi.fn<typeof fetch>(() => Promise.resolve(jsonResponse({}, 503)));
    await loadAutoFerryFeature(status);
    expect(isAutoFerryFeatureEnabled()).toBe(false);
    const ok = stagesFetch(true);
    await loadAutoFerryFeature(ok);
    expect(isAutoFerryFeatureEnabled()).toBe(true);
    expect([fail, status, ok].map((f) => f.mock.calls.length)).toEqual([1, 1, 1]);
  });

  it("AF-4 setAutoFerryFeature で決めた後は読まない。読み込み中に決めたら、読んだ結果を捨てる", async () => {
    setAutoFerryFeature(false);
    const unused = stagesFetch(true);
    await loadAutoFerryFeature(unused);
    expect(unused).not.toHaveBeenCalled();

    resetAutoFerryFeature();
    const late = stagesFetch(true);
    const loading = loadAutoFerryFeature(late);
    setAutoFerryFeature(false);
    await loading;
    expect(isAutoFerryFeatureEnabled()).toBe(false);
  });
});

describe("sendFerryAuto", () => {
  it("AF-5 cmd.ferry.auto を payload のまま送る", async () => {
    const ack: Ack = { cmdId: "c-1", ok: true, t: 0 };
    const sender = vi.fn<GameCommandSender>(() => Promise.resolve(ack));
    await expect(sendFerryAuto(sender, { ferryId: "f2", enabled: true })).resolves.toBe(ack);
    expect(sender).toHaveBeenCalledWith("cmd.ferry.auto", { ferryId: "f2", enabled: true });
  });
});
