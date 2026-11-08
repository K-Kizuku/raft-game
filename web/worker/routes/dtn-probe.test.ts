// 管理者の試運転の口のテスト（DTN-12）。

import { describe, expect, it } from "vitest";

import type { CreateDtnInput } from "../dtn/create.ts";
import type { DtnPorts } from "../dtn/ports.ts";
import type { Env } from "./env.ts";
import type { SecurityPorts, VerifiedSessionId, WorkerDeps } from "./ports.ts";
import { createHandler } from "./router.ts";

const TOKEN = "admin-token-0123456789abcdef0123456789abcdef";
const HOST = "https://raft-dtn.kizuku-hackathon.work";
const SID = "s_probe" as VerifiedSessionId;

function setup(options: { dtn?: boolean; verify?: boolean } = {}) {
  const created: CreateDtnInput[] = [];
  const probed: string[] = [];
  const dtn: DtnPorts = {
    create: async (input) => {
      created.push(input);
      if (input.stageId === "stage3") return { ok: false, code: "dtn_unsupported_stage" };
      return {
        ok: true,
        session: { sessionId: SID, token: "t", tokenHash: "h" },
        stageId: input.stageId,
        seed: input.seed,
      };
    },
    probe: async (sessionId) => {
      probed.push(sessionId);
      return { status: 200, body: { sessionId, phase: "ready" } };
    },
  };
  const security = {
    verifySessionId: async (raw: string) =>
      options.verify === false ? null : (raw as VerifiedSessionId),
  } as unknown as SecurityPorts;
  const deps: WorkerDeps = { stages: [], security, ...(options.dtn === false ? {} : { dtn }) };
  const handler = createHandler(() => deps);
  return { handler, created, probed };
}

function req(path: string, init: RequestInit & { token?: string } = {}): Request {
  const headers = new Headers(init.headers);
  if (init.token !== undefined) headers.set("authorization", `Bearer ${init.token}`);
  return new Request(`${HOST}${path}`, { ...init, headers });
}

const ENV: Env = { ADMIN_TOKEN: TOKEN, DEMO_CONTROLS: "1" };

function postProbe(body: unknown, token = TOKEN): Request {
  return req("/api/admin/dtn/probe", {
    method: "POST",
    token,
    headers: { "content-type": "application/json" },
    body: JSON.stringify(body),
  });
}

describe("POST /api/admin/dtn/probe", () => {
  it("ADMIN_TOKEN がなければ 404", async () => {
    const { handler } = setup();
    expect((await handler(postProbe({ stageId: "stage1", bot: "greedy" }), {})).status).toBe(404);
  });

  it("トークンが違えば 401", async () => {
    const { handler, created } = setup();
    const res = await handler(postProbe({ stageId: "stage1", bot: "greedy" }, "x".repeat(40)), ENV);
    expect(res.status).toBe(401);
    expect(created).toEqual([]);
  });

  it("DTN の部品がなければ 404", async () => {
    const { handler } = setup({ dtn: false });
    expect((await handler(postProbe({ stageId: "stage1", bot: "greedy" }), ENV)).status).toBe(404);
  });

  it("201 で sessionId と結果のパスを返す。bot と speed を渡す", async () => {
    const { handler, created } = setup();
    const res = await handler(
      postProbe({ stageId: "demo-izu", seed: 3, bot: "greedy", speed: 2 }),
      ENV,
    );
    expect(res.status).toBe(201);
    expect(await res.json()).toEqual({
      sessionId: SID,
      stageId: "demo-izu",
      seed: 3,
      probePath: `/api/admin/dtn/probe/${SID}`,
    });
    expect(created[0]).toEqual({
      stageId: "demo-izu",
      seed: 3,
      demo: true,
      probe: { bot: "greedy", speed: 2 },
    });
  });

  it.each([
    [{ stageId: "stage1" }],
    [{ stageId: "stage1", bot: "random" }],
    [{ stageId: "stage1", bot: "greedy", speed: 9 }],
    [{ stageId: "stage1", bot: "greedy", extra: 1 }],
  ])("不正な本文 %j は 400", async (body) => {
    const { handler } = setup();
    expect((await handler(postProbe(body), ENV)).status).toBe(400);
  });

  it("stage3 は 400 dtn_unsupported_stage", async () => {
    const { handler } = setup();
    const res = await handler(postProbe({ stageId: "stage3", bot: "greedy" }), ENV);
    expect(res.status).toBe(400);
    expect(await res.json()).toMatchObject({ code: "dtn_unsupported_stage" });
  });
});

describe("GET /api/admin/dtn/probe/{sessionId}", () => {
  it("署名を確かめてから DtnSession を呼び、結果を返す", async () => {
    const { handler, probed } = setup();
    const res = await handler(req(`/api/admin/dtn/probe/${SID}`, { token: TOKEN }), ENV);
    expect(res.status).toBe(200);
    expect(await res.json()).toEqual({ sessionId: SID, phase: "ready" });
    expect(probed).toEqual([SID]);
  });

  it("署名が合わない sessionId は DO を呼ばずに 404（G3）", async () => {
    const { handler, probed } = setup({ verify: false });
    const res = await handler(req("/api/admin/dtn/probe/s_fake", { token: TOKEN }), ENV);
    expect(res.status).toBe(404);
    expect(probed).toEqual([]);
  });

  it("認証の前に DO を呼ばない", async () => {
    const { handler, probed } = setup();
    expect((await handler(req(`/api/admin/dtn/probe/${SID}`), ENV)).status).toBe(401);
    expect(probed).toEqual([]);
  });
});
