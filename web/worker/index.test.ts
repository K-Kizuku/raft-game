import { describe, expect, it } from "vitest";

import worker from "./index.ts";
import { STAGE_CATALOG } from "./routes/stage-catalog.ts";
import type { StageSummary } from "./routes/stages.ts";

const ORIGIN = "https://raft.example";

function fetchWorker(path: string, init: RequestInit = {}): Promise<Response> {
  return worker.fetch(new Request(`${ORIGIN}${path}`, init), {});
}

// D-1: 既定の配線（部品なし）では、全体が拒否する側に倒れる。
describe("worker（既定の配線）", () => {
  it('GET /api/healthz は 200 と {"status":"ok","build":"dev"} を返す', async () => {
    const res = await fetchWorker("/api/healthz");

    expect(res.status).toBe(200);
    expect(res.headers.get("content-type")).toMatch(/^application\/json/);
    expect(await res.text()).toBe('{"status":"ok","build":"dev"}');
  });

  it("GET /api/stages は STAGE_CATALOG から demo のステージを除いた一覧を返す", async () => {
    const res = await fetchWorker("/api/stages");

    expect(res.status).toBe(200);
    const body = (await res.json()) as { stages: StageSummary[] };
    expect(body).toEqual({
      stages: STAGE_CATALOG.filter((s) => !s.demo),
      features: { autoFerry: false },
    });
    expect(body.stages.some((s) => s.demo)).toBe(false);
  });

  it('DEMO_CONTROLS が "1" なら GET /api/stages は STAGE_CATALOG のすべてを返す', async () => {
    const res = await worker.fetch(new Request(`${ORIGIN}/api/stages`), { DEMO_CONTROLS: "1" });

    expect(res.status).toBe(200);
    expect(await res.json()).toEqual({ stages: STAGE_CATALOG, features: { autoFerry: false } });
  });

  it("POST /api/sessions は 503 sessions_disabled を返す", async () => {
    const res = await fetchWorker("/api/sessions", {
      method: "POST",
      headers: { origin: ORIGIN, "content-type": "application/json" },
      body: '{"stageId":"stage1"}',
    });

    expect(res.status).toBe(503);
    expect(await res.json()).toMatchObject({ code: "sessions_disabled" });
  });

  it("GET /ws/sessions/{id} は 404 を返す", async () => {
    const res = await fetchWorker("/ws/sessions/s_abc.def", {
      headers: { origin: ORIGIN, upgrade: "websocket" },
    });

    expect(res.status).toBe(404);
    expect(await res.json()).toMatchObject({ code: "not_found" });
  });

  it("GET /api/admin/guards は 404 を返す", async () => {
    const res = await fetchWorker("/api/admin/guards");

    expect(res.status).toBe(404);
    expect(await res.json()).toMatchObject({ code: "not_found" });
  });

  it("知らない /api/x は 404 の JSON を返す", async () => {
    const res = await fetchWorker("/api/x");

    expect(res.status).toBe(404);
    expect(res.headers.get("content-type")).toMatch(/^application\/json/);
    const body: unknown = await res.json();
    expect(body).toMatchObject({ code: "not_found" });
    expect(body).toHaveProperty("message");
  });
});
