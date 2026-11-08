import { describe, expect, it } from "vitest";

import type {
  RequestContext,
  SecurityPorts,
  SessionPorts,
  VerifiedSessionId,
  WorkerDeps,
} from "./ports.ts";
import { createHandler } from "./router.ts";

const ORIGIN = "https://raft.example";
const VERIFIED = "s_verified.sig" as VerifiedSessionId;
const TOKEN = "Tok_en-abcdefghijklmno"; // 22 文字

interface Call {
  readonly name: string;
  readonly args: readonly unknown[];
}

function makeFakes(verifyResult: VerifiedSessionId | null = VERIFIED) {
  const calls: Call[] = [];
  const downloaded = new Response('{"format":1}', {
    status: 200,
    headers: { "content-disposition": 'attachment; filename="raft-game-stage1-1.json"' },
  });
  const fail = async (): Promise<never> => {
    throw new Error("must not be called");
  };
  const security: SecurityPorts = {
    checkRateLimit: fail,
    verifyTurnstile: fail,
    issueSession: fail,
    verifySessionId: async (raw, ctx) => {
      calls.push({ name: "verifySessionId", args: [raw, ctx] });
      return verifyResult;
    },
  };
  const session: SessionPorts = {
    init: fail,
    connect: fail,
    inputLog: async (id, ctx) => {
      calls.push({ name: "inputLog", args: [id, ctx] });
      return downloaded;
    },
  };
  return {
    calls,
    downloaded,
    names: () => calls.map((call) => call.name),
    deps: { stages: [], security, session } satisfies WorkerDeps,
  };
}

function logRequest(id: string, headers: Record<string, string> = { "x-session-token": TOKEN }) {
  return new Request(`${ORIGIN}/api/sessions/${id}/input-log`, { headers });
}

describe("GET /api/sessions/{id}/input-log", () => {
  it("IL-1 verifySessionId が null → 404 で、inputLog を呼ばない（G3）", async () => {
    const fakes = makeFakes(null);
    const res = await createHandler(() => fakes.deps)(logRequest("s_abc.def"), {});

    expect(res.status).toBe(404);
    expect(await res.json()).toMatchObject({ code: "not_found" });
    expect(fakes.names()).toEqual(["verifySessionId"]);
  });

  it.each<[string, Record<string, string>]>([
    ["ヘッダーなし", {}],
    ["21 文字", { "x-session-token": TOKEN.slice(1) }],
    ["23 文字", { "x-session-token": `${TOKEN}a` }],
    ["使えない文字", { "x-session-token": `${TOKEN.slice(1)}.` }],
    ["空", { "x-session-token": "" }],
  ])("IL-2 トークンが %s → 404 で、verifySessionId の後でも DO を呼ばない", async (_n, headers) => {
    const fakes = makeFakes();
    const res = await createHandler(() => fakes.deps)(logRequest("s_abc.def", headers), {});

    expect(res.status).toBe(404);
    expect(await res.json()).toMatchObject({ code: "not_found" });
    expect(fakes.names()).toEqual(["verifySessionId"]);
  });

  it("IL-3 正しい id・トークン → 検証した値で inputLog を 1 回呼び、応答をそのまま返す", async () => {
    const fakes = makeFakes();
    const request = logRequest("s_abc.def");
    const res = await createHandler(() => fakes.deps)(request, {});

    expect(res).toBe(fakes.downloaded);
    expect(fakes.names()).toEqual(["verifySessionId", "inputLog"]);
    expect(fakes.calls[0]?.args[0]).toBe("s_abc.def");
    expect(fakes.calls[1]?.args[0]).toBe(VERIFIED);
    expect((fakes.calls[1]?.args[1] as RequestContext).request).toBe(request);
  });

  it.each([
    ["a%2Fb", "a%2Fb"],
    ["a b（エンコード済み）", "a%20b"],
    ["257 文字", "a".repeat(257)],
  ])("IL-4 id が %s → 404 で、どの部品も呼ばない", async (_name, id) => {
    const fakes = makeFakes();
    const res = await createHandler(() => fakes.deps)(logRequest(id), {});

    expect(res.status).toBe(404);
    expect(await res.json()).toMatchObject({ code: "not_found" });
    expect(fakes.calls).toEqual([]);
  });

  it.each<[string, (f: ReturnType<typeof makeFakes>) => WorkerDeps]>([
    ["security がない", (f) => ({ stages: [], session: f.deps.session })],
    ["session がない", (f) => ({ stages: [], security: f.deps.security })],
    [
      "session に inputLog がない",
      (f) => ({
        stages: [],
        security: f.deps.security,
        session: { init: f.deps.session.init, connect: f.deps.session.connect },
      }),
    ],
  ])("%s → 404 で部品を呼ばない", async (_name, pick) => {
    const fakes = makeFakes();
    const res = await createHandler(() => pick(fakes))(logRequest("s_abc.def"), {});

    expect(res.status).toBe(404);
    expect(await res.json()).toMatchObject({ code: "not_found" });
    expect(fakes.calls).toEqual([]);
  });
});
