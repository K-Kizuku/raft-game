// createDtnSession のテスト（DTN-12）: 予約の失敗で DO を呼ばない・/init の失敗で cancel・対象外のステージ。

import { describe, expect, it } from "vitest";

import type { ReserveResult, ReserveUnit } from "../admin/client.ts";
import type { RequestContext, VerifiedSessionId } from "../routes/ports.ts";
import { DTN_STAGE_INFO } from "./catalog.ts";
import { createDtnSession, type CreateDtnDeps, type DtnInitBody } from "./create.ts";

const SID = "s_created" as VerifiedSessionId;
const ctx = {
  request: new Request("https://raft-dtn.kizuku-hackathon.work/api/admin/dtn/probe"),
  url: new URL("https://raft-dtn.kizuku-hackathon.work/api/admin/dtn/probe"),
  env: {},
  clientIp: null,
} satisfies RequestContext;

function fakeDeps(reserve: ReserveResult = "ok", initError: Error | null = null) {
  const calls: string[] = [];
  const reserved: ReserveUnit[][] = [];
  const inits: DtnInitBody[] = [];
  const deps: CreateDtnDeps = {
    catalog: DTN_STAGE_INFO,
    instance: "lite",
    issueSession: async () => {
      calls.push("issue");
      return { sessionId: SID, token: "t", tokenHash: "h" };
    },
    reserveUnits: async (_id, units) => {
      calls.push("reserve");
      reserved.push([...units]);
      return reserve;
    },
    cancel: async () => {
      calls.push("cancel");
    },
    limits: async () => ({ maxMinutes: 15, maxAlarms: 20 }),
    init: async (body) => {
      calls.push("init");
      inits.push(body);
      if (initError !== null) throw initError;
    },
  };
  return { deps, calls, reserved, inits };
}

describe("createDtnSession", () => {
  it("stage1: 内訳つきで予約（Container 5・DO 5）してから DtnSession の /init を呼ぶ", async () => {
    const f = fakeDeps();
    const result = await createDtnSession({ stageId: "stage1", seed: 7, demo: true }, ctx, f.deps);
    expect(result).toMatchObject({ ok: true, stageId: "stage1", seed: 7 });
    expect(f.calls).toEqual(["issue", "reserve", "init"]);
    expect(f.reserved[0]).toEqual([
      { kind: "container", instance: "lite", count: 5 },
      { kind: "do", count: 5 },
    ]);
    expect(f.inits[0]).toEqual({
      sessionId: SID,
      stageId: "stage1",
      seed: 7,
      demo: true,
      maxMinutes: 15,
      maxAlarms: 20,
    });
  });

  it("demo-izu は 13・13。probe を /init に渡す", async () => {
    const f = fakeDeps();
    await createDtnSession(
      { stageId: "demo-izu", seed: 1, demo: true, probe: { bot: "greedy", speed: 2 } },
      ctx,
      f.deps,
    );
    expect(f.reserved[0]?.map((u) => u.count)).toEqual([13, 13]);
    expect(f.inits[0]?.probe).toEqual({ bot: "greedy", speed: 2 });
  });

  it("stage3（欠航）は dtn_unsupported_stage。sessionId も予約も作らない", async () => {
    const f = fakeDeps();
    expect(
      await createDtnSession({ stageId: "stage3", seed: 1, demo: false }, ctx, f.deps),
    ).toEqual({
      ok: false,
      code: "dtn_unsupported_stage",
    });
    expect(f.calls).toEqual([]);
  });

  it("知らないステージは bad_request", async () => {
    const f = fakeDeps();
    expect(
      (await createDtnSession({ stageId: "nope", seed: 1, demo: false }, ctx, f.deps)).ok,
    ).toBe(false);
    expect(f.calls).toEqual([]);
  });

  it.each(["budget_exhausted", "sessions_disabled"] as const)(
    "予約が %s なら DO を呼ばない",
    async (code) => {
      const f = fakeDeps(code);
      expect(
        await createDtnSession({ stageId: "stage1", seed: 1, demo: false }, ctx, f.deps),
      ).toEqual({ ok: false, code });
      expect(f.calls).toEqual(["issue", "reserve"]);
    },
  );

  it("/init が失敗したら予約を取り消して投げ直す", async () => {
    const f = fakeDeps("ok", new Error("init failed"));
    await expect(
      createDtnSession({ stageId: "stage1", seed: 1, demo: false }, ctx, f.deps),
    ).rejects.toThrow("init failed");
    expect(f.calls).toEqual(["issue", "reserve", "init", "cancel"]);
  });
});
