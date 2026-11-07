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

interface Call {
  readonly name: string;
  readonly args: readonly unknown[];
}

function makeFakes(verifyResult: VerifiedSessionId | null = VERIFIED) {
  const calls: Call[] = [];
  const connected = new Response("{}", { status: 200 });
  const security: SecurityPorts = {
    checkRateLimit: async () => "ok",
    verifyTurnstile: async () => "ok",
    issueSession: async () => {
      throw new Error("not used");
    },
    verifySessionId: async (raw, ctx) => {
      calls.push({ name: "verifySessionId", args: [raw, ctx] });
      return verifyResult;
    },
  };
  const session: SessionPorts = {
    init: async () => undefined,
    connect: async (id, ctx) => {
      calls.push({ name: "connect", args: [id, ctx] });
      return connected;
    },
  };
  return {
    calls,
    connected,
    names: () => calls.map((call) => call.name),
    deps: { stages: [], security, session } satisfies WorkerDeps,
  };
}

function wsRequest(id: string, headers: Record<string, string> = { upgrade: "websocket" }) {
  return new Request(`${ORIGIN}/ws/sessions/${id}?token=abc`, {
    headers: { origin: ORIGIN, ...headers },
  });
}

describe("GET /ws/sessions/{id}", () => {
  describe("W-1 拒否", () => {
    it.each<[string, Record<string, string>]>([
      ["Upgrade なし", {}],
      ["Upgrade: h2c", { upgrade: "h2c" }],
    ])("%s → 426 upgrade_required", async (_name, headers) => {
      const fakes = makeFakes();
      const res = await createHandler(() => fakes.deps)(wsRequest("s_abc.def", headers), {});

      expect(res.status).toBe(426);
      expect(await res.json()).toMatchObject({ code: "upgrade_required" });
      expect(fakes.calls).toEqual([]);
    });

    it.each([
      ["a%2Fb", "a%2Fb"],
      ["a b（エンコード済み）", "a%20b"],
      ["257 文字", "a".repeat(257)],
    ])("id が %s → 404 で verifySessionId を呼ばない", async (_name, id) => {
      const fakes = makeFakes();
      const res = await createHandler(() => fakes.deps)(wsRequest(id), {});

      expect(res.status).toBe(404);
      expect(await res.json()).toMatchObject({ code: "not_found" });
      expect(fakes.calls).toEqual([]);
    });

    it("256 文字の id は検証に進む", async () => {
      const fakes = makeFakes();
      const res = await createHandler(() => fakes.deps)(wsRequest("a".repeat(256)), {});

      expect(res).toBe(fakes.connected);
    });

    it.each<[string, (f: ReturnType<typeof makeFakes>) => WorkerDeps]>([
      ["security がない", (f) => ({ stages: [], session: f.deps.session })],
      ["session がない", (f) => ({ stages: [], security: f.deps.security })],
    ])("%s → 404 で部品を呼ばない", async (_name, pick) => {
      const fakes = makeFakes();
      const res = await createHandler(() => pick(fakes))(wsRequest("s_abc.def"), {});

      expect(res.status).toBe(404);
      expect(await res.json()).toMatchObject({ code: "not_found" });
      expect(fakes.calls).toEqual([]);
    });

    it("verifySessionId が null → 404 で connect を呼ばない", async () => {
      const fakes = makeFakes(null);
      const res = await createHandler(() => fakes.deps)(wsRequest("s_abc.def"), {});

      expect(res.status).toBe(404);
      expect(await res.json()).toMatchObject({ code: "not_found" });
      expect(fakes.names()).toEqual(["verifySessionId"]);
    });
  });

  describe("W-2 転送", () => {
    it.each(["websocket", "WebSocket"])(
      "Upgrade: %s で、検証した値と元の Request で connect を呼び、応答をそのまま返す",
      async (upgrade) => {
        const fakes = makeFakes();
        const request = wsRequest("s_abc.def", { upgrade });
        const res = await createHandler(() => fakes.deps)(request, {});

        expect(res).toBe(fakes.connected);
        expect(fakes.names()).toEqual(["verifySessionId", "connect"]);
        expect(fakes.calls[0]?.args[0]).toBe("s_abc.def");
        expect(fakes.calls[1]?.args[0]).toBe(VERIFIED);
        expect((fakes.calls[1]?.args[1] as RequestContext).request).toBe(request);
      },
    );
  });
});
