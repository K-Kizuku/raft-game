import { describe, expect, it, vi } from "vitest";

import type {
  CostGuardPorts,
  SecurityPorts,
  SessionPorts,
  VerifiedSessionId,
  WorkerDeps,
} from "./ports.ts";
import { createHandler } from "./router.ts";
import { STAGE_CATALOG } from "./stage-catalog.ts";
import type { StageSummary } from "./stages.ts";
import { stageFixture } from "./test-stages.ts";

const ORIGIN = "https://raft.example";

const STAGES: readonly StageSummary[] = [
  stageFixture({
    id: "stage1",
    title: "はじめての郵便船",
    region: "瀬戸内（香川県）",
    islands: 3,
    ferries: 1,
    durationMs: 180000,
    unlock: null,
  }),
  stageFixture({
    id: "stage2",
    title: "嵐の海峡",
    region: "関門海峡",
    islands: 5,
    ferries: 2,
    durationMs: 240000,
    unlock: "stage1",
  }),
];

// 呼ばれたら必ず失敗する部品（呼ばれないことを確かめる）。
function untouchable(): Required<Pick<WorkerDeps, "security" | "costGuard" | "session">> {
  const fail = async (): Promise<never> => {
    throw new Error("must not be called");
  };
  const security: SecurityPorts = {
    checkRateLimit: fail,
    verifyTurnstile: fail,
    issueSession: fail,
    verifySessionId: fail,
  };
  const costGuard: CostGuardPorts = { reserve: fail, cancel: fail, handleAdmin: fail };
  const session: SessionPorts = { init: fail, connect: fail };
  return { security, costGuard, session };
}

function req(method: string, path: string, headers: Record<string, string> = {}): Request {
  return new Request(`${ORIGIN}${path}`, { method, headers });
}

describe("router", () => {
  describe("R-1 GET /api/healthz", () => {
    it('env = {} では {"status":"ok","build":"dev"}', async () => {
      const res = await createHandler(() => ({ stages: [] }))(req("GET", "/api/healthz"), {});

      expect(res.status).toBe(200);
      expect(res.headers.get("content-type")).toMatch(/^application\/json/);
      expect(res.headers.get("cache-control")).toBe("no-store");
      expect(await res.text()).toBe('{"status":"ok","build":"dev"}');
    });

    it("BUILD_ID があればその値", async () => {
      const res = await createHandler(() => ({ stages: [] }))(req("GET", "/api/healthz"), {
        BUILD_ID: "git:3f2c1ab",
      });

      expect(await res.text()).toBe('{"status":"ok","build":"git:3f2c1ab"}');
    });

    it("BUILD_ID が空なら dev", async () => {
      const res = await createHandler(() => ({ stages: [] }))(req("GET", "/api/healthz"), {
        BUILD_ID: "",
      });

      expect(await res.text()).toBe('{"status":"ok","build":"dev"}');
    });
  });

  describe("R-2 知らないパス", () => {
    const paths = [
      "/api/x",
      "/api",
      "/api/",
      "/api/healthz/",
      "/API/healthz",
      "/ws/x",
      "/ws/sessions/",
      "/ws/sessions/a/b",
      "/",
    ];
    it.each(paths)("%s → 404 not_found", async (path) => {
      const res = await createHandler(() => ({ stages: [], ...untouchable() }))(
        req("GET", path),
        {},
      );

      expect(res.status).toBe(404);
      expect(res.headers.get("cache-control")).toBe("no-store");
      expect(await res.json()).toMatchObject({ code: "not_found" });
    });

    it.each(paths)("%s は許可されない Origin を付けても 404", async (path) => {
      const res = await createHandler(() => ({ stages: [], ...untouchable() }))(
        req("POST", path, { origin: "https://evil.example" }),
        {},
      );

      expect(res.status).toBe(404);
      expect(await res.json()).toMatchObject({ code: "not_found" });
    });
  });

  describe("R-3 メソッド", () => {
    it.each([
      ["POST", "/api/healthz", "GET"],
      ["HEAD", "/api/stages", "GET"],
      ["GET", "/api/sessions", "POST"],
      ["OPTIONS", "/api/sessions", "POST"],
      ["DELETE", "/api/admin/guards", "GET, PATCH"],
      ["POST", "/ws/sessions/abc", "GET"],
    ])("%s %s → 405 と Allow: %s", async (method, path, allow) => {
      const res = await createHandler(() => ({ stages: [], ...untouchable() }))(
        req(method, path, { origin: ORIGIN }),
        {},
      );

      expect(res.status).toBe(405);
      expect(res.headers.get("allow")).toBe(allow);
      if (method !== "HEAD") {
        expect(await res.json()).toMatchObject({ code: "method_not_allowed" });
      }
    });
  });

  describe("R-4 GET /api/stages", () => {
    it("渡した一覧を返す", async () => {
      const res = await createHandler(() => ({ stages: STAGES }))(req("GET", "/api/stages"), {});

      expect(res.status).toBe(200);
      expect(await res.json()).toEqual({ stages: STAGES });
    });

    it("既定の STAGE_CATALOG から demo を除いたもの", async () => {
      const res = await createHandler(() => ({ stages: STAGE_CATALOG }))(
        req("GET", "/api/stages"),
        {},
      );

      expect(await res.text()).toBe(
        JSON.stringify({ stages: STAGE_CATALOG.filter((stage) => !stage.demo) }),
      );
    });
  });

  describe("R-5 例外", () => {
    const SECRET_MESSAGE = "boom-secret-message";
    const SECRET_TOKEN = "secret-token-value";
    const throwing = (): WorkerDeps => {
      const fail = async (): Promise<never> => {
        throw new TypeError(SECRET_MESSAGE);
      };
      return {
        stages: [],
        security: {
          checkRateLimit: async () => "ok",
          verifyTurnstile: async () => "ok",
          issueSession: fail,
          verifySessionId: async (raw) => raw as VerifiedSessionId,
        },
        costGuard: { reserve: fail, cancel: fail, handleAdmin: fail },
        session: { init: fail, connect: fail },
      };
    };

    it.each([
      ["connect", `/ws/sessions/s_abc.def?token=${SECRET_TOKEN}`, { upgrade: "websocket" }],
      ["handleAdmin", `/api/admin/guards?token=${SECRET_TOKEN}`, {}],
    ])(
      "%s が例外 → 500 internal_error。ログにクエリと例外のメッセージを出さない",
      async (_n, path, h) => {
        const consoleError = vi.spyOn(console, "error").mockImplementation(() => undefined);
        try {
          const res = await createHandler(throwing)(req("GET", path, { origin: ORIGIN, ...h }), {});

          expect(res.status).toBe(500);
          const body = await res.text();
          expect(JSON.parse(body)).toMatchObject({ code: "internal_error" });
          expect(body).not.toContain(SECRET_MESSAGE);
          expect(consoleError).toHaveBeenCalledTimes(1);
          const logged = consoleError.mock.calls[0]?.map(String).join(" ") ?? "";
          expect(logged).not.toContain(SECRET_TOKEN);
          expect(logged).not.toContain("?token=");
          expect(logged).not.toContain(SECRET_MESSAGE);
          expect(JSON.parse(logged)).toEqual({
            level: "error",
            event: "worker.unhandled",
            path: new URL(`${ORIGIN}${path}`).pathname,
            error: "TypeError",
          });
        } finally {
          consoleError.mockRestore();
        }
      },
    );

    it("正常な処理と 4xx ではログを出さない", async () => {
      const consoleError = vi.spyOn(console, "error").mockImplementation(() => undefined);
      try {
        const handler = createHandler(() => ({ stages: [] }));
        await handler(req("GET", "/api/healthz"), {});
        await handler(req("GET", "/api/x"), {});
        await handler(req("POST", "/api/sessions", { origin: "https://evil.example" }), {});

        expect(consoleError).not.toHaveBeenCalled();
      } finally {
        consoleError.mockRestore();
      }
    });
  });
});

describe("/api/admin/guards", () => {
  describe("A-1", () => {
    function withAdmin() {
      const calls: string[] = [];
      const response = new Response('{"guards":{}}', { status: 200 });
      const base = untouchable();
      const costGuard: CostGuardPorts = {
        ...base.costGuard,
        handleAdmin: async (ctx) => {
          calls.push(ctx.request.method);
          return response;
        },
      };
      return { calls, response, deps: { stages: [], costGuard } satisfies WorkerDeps };
    }

    it("costGuard がない → 404", async () => {
      const res = await createHandler(() => ({ stages: [] }))(req("GET", "/api/admin/guards"), {});

      expect(res.status).toBe(404);
      expect(await res.json()).toMatchObject({ code: "not_found" });
    });

    it.each(["GET", "PATCH"])("%s は handleAdmin の応答をそのまま返す", async (method) => {
      const admin = withAdmin();
      const res = await createHandler(() => admin.deps)(req(method, "/api/admin/guards"), {});

      expect(res).toBe(admin.response);
      expect(admin.calls).toEqual([method]);
    });

    it("許可された Origin は渡す", async () => {
      const admin = withAdmin();
      const res = await createHandler(() => admin.deps)(
        req("GET", "/api/admin/guards", { origin: ORIGIN }),
        {},
      );

      expect(res).toBe(admin.response);
    });

    it("許可されない Origin → 403 で handleAdmin を呼ばない", async () => {
      const admin = withAdmin();
      const res = await createHandler(() => admin.deps)(
        req("GET", "/api/admin/guards", { origin: "https://evil.example" }),
        {},
      );

      expect(res.status).toBe(403);
      expect(await res.json()).toMatchObject({ code: "forbidden_origin" });
      expect(admin.calls).toEqual([]);
    });
  });
});
