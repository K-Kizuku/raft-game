import { afterEach, describe, expect, it, vi } from "vitest";

import type { SessionHandle } from "../types.ts";
import { fetchInputLog } from "./inputLog.ts";

const TOKEN = "Tok_en-abcdefghijklmno"; // 22 文字
const SESSION: SessionHandle = {
  sessionId: "s_abc.def",
  token: TOKEN,
  wsPath: "/ws/sessions/s_abc.def",
};
const BODY = '{"format":1,"build":"b1","stageId":"stage1","stageHash":"h","seed":7,"commands":[]}';

function fakeFetch(respond: () => Response | Promise<Response>) {
  return vi.fn<typeof fetch>(async () => respond());
}

function logResponse(body: string, status = 200, headers: Record<string, string> = {}): Response {
  return new Response(body, {
    status,
    headers: { "content-type": "application/json; charset=utf-8", ...headers },
  });
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe("fetchInputLog", () => {
  it("IF-1 200・format: 1 → ok。ファイル名は content-disposition、トークンはヘッダーだけ", async () => {
    const fetchFn = fakeFetch(() =>
      logResponse(BODY, 200, {
        "content-disposition": 'attachment; filename="raft-game-stage1-7.json"',
      }),
    );
    const result = await fetchInputLog(SESSION, fetchFn);

    expect(result.ok).toBe(true);
    if (!result.ok) return;
    expect(result.filename).toBe("raft-game-stage1-7.json");
    expect(await result.blob.text()).toBe(BODY);
    expect(fetchFn).toHaveBeenCalledTimes(1);
    const [url, init] = fetchFn.mock.calls[0] ?? [];
    expect(url).toBe("/api/sessions/s_abc.def/input-log");
    expect(String(url)).not.toContain(TOKEN);
    expect(init?.method).toBe("GET");
    expect(new Headers(init?.headers).get("X-Session-Token")).toBe(TOKEN);
  });

  it("sessionId は URL のエンコードをする", async () => {
    const fetchFn = fakeFetch(() => logResponse(BODY));
    await fetchInputLog({ ...SESSION, sessionId: "a/b c" }, fetchFn);

    expect(fetchFn.mock.calls[0]?.[0]).toBe("/api/sessions/a%2Fb%20c/input-log");
  });

  it.each<[string, Record<string, string>]>([
    ["content-disposition なし", {}],
    ["filename なし", { "content-disposition": "attachment" }],
    ["使えない文字", { "content-disposition": 'attachment; filename="../x y.json"' }],
  ])("%s → raft-game-input-log.json", async (_name, headers) => {
    const result = await fetchInputLog(
      SESSION,
      fakeFetch(() => logResponse(BODY, 200, headers)),
    );

    expect(result).toMatchObject({ ok: true, filename: "raft-game-input-log.json" });
  });

  describe("IF-2 失敗", () => {
    it("404 → not_found", async () => {
      const fetchFn = fakeFetch(() => logResponse('{"code":"not_found"}', 404));
      expect(await fetchInputLog(SESSION, fetchFn)).toEqual({ ok: false, reason: "not_found" });
    });

    it("fetch が投げる → network。トークンを console に出さない", async () => {
      const spies = (["log", "info", "warn", "error", "debug"] as const).map((name) =>
        vi.spyOn(console, name).mockImplementation(() => undefined),
      );
      const fetchFn = vi.fn<typeof fetch>(async () => {
        throw new TypeError("Failed to fetch");
      });
      expect(await fetchInputLog(SESSION, fetchFn)).toEqual({ ok: false, reason: "network" });
      for (const spy of spies) {
        expect(spy).not.toHaveBeenCalled();
      }
    });

    it.each<[string, () => Response]>([
      ["200 で JSON でない", () => logResponse("oops")],
      ["200 で format が 1 でない", () => logResponse('{"format":2,"commands":[]}')],
      ["200 で配列", () => logResponse("[1]")],
      ["403", () => logResponse('{"code":"forbidden_origin"}', 403)],
      ["500", () => logResponse('{"code":"internal_error"}', 500)],
    ])("%s → bad_response", async (_name, respond) => {
      expect(await fetchInputLog(SESSION, fakeFetch(respond))).toEqual({
        ok: false,
        reason: "bad_response",
      });
    });
  });
});
