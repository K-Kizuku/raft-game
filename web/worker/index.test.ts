import { describe, expect, it } from "vitest";

import worker from "./index.ts";

function get(path: string): Promise<Response> {
  return worker.fetch(new Request(`https://raft.example${path}`));
}

describe("worker", () => {
  it('GET /api/healthz は 200 と {"ok":true} を返す', async () => {
    const res = await get("/api/healthz");

    expect(res.status).toBe(200);
    expect(res.headers.get("content-type")).toMatch(/^application\/json/);
    expect(await res.text()).toBe('{"ok":true}');
  });

  it("知らない /api/x は 404 の JSON を返す", async () => {
    const res = await get("/api/x");

    expect(res.status).toBe(404);
    expect(res.headers.get("content-type")).toMatch(/^application\/json/);
    expect(await res.json()).toEqual({ ok: false, error: "not_found" });
  });
});
