import { describe, expect, it, vi } from "vitest";

import { createSession, type SessionRequest } from "./api.ts";
import { SessionCreateError, type SessionErrorKind } from "./errors.ts";

// Worker の形（s_ + 28 文字 + . + 43 文字）と standalone の形（s_ + 22 文字）。
const WORKER_ID = `s_${"a".repeat(28)}.${"B".repeat(43)}`;
const STANDALONE_ID = `s_${"c".repeat(22)}`;
const TOKEN = "T".repeat(22);

const BASE: SessionRequest = { stageId: "stage1", seed: 42, demo: false, turnstileToken: null };

function jsonResponse(body: unknown, status: number): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json; charset=utf-8" },
  });
}

function textResponse(body: string, status: number): Response {
  return new Response(body, { status, headers: { "content-type": "text/html" } });
}

function fakeFetch(respond: () => Response) {
  return vi.fn<typeof fetch>(async () => respond());
}

async function errorOf(promise: Promise<unknown>): Promise<SessionCreateError> {
  const error: unknown = await promise.then(
    () => undefined,
    (e: unknown) => e,
  );
  expect(error).toBeInstanceOf(SessionCreateError);
  return error as SessionCreateError;
}

describe("createSession", () => {
  it.each<[string, SessionRequest, string]>([
    ["demo: false・トークン null", BASE, '{"stageId":"stage1","seed":42}'],
    [
      "demo: true・トークンあり",
      { stageId: "demo-izu", seed: 0, demo: true, turnstileToken: "tok" },
      '{"stageId":"demo-izu","seed":0,"demo":true,"turnstileToken":"tok"}',
    ],
  ])("A-1 要求の形（%s）", async (_label, request, body) => {
    const fetchMock = fakeFetch(() =>
      jsonResponse(
        { sessionId: STANDALONE_ID, token: TOKEN, wsPath: `/ws/sessions/${STANDALONE_ID}` },
        201,
      ),
    );

    await createSession(request, { fetch: fetchMock });

    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [input, init] = fetchMock.mock.calls[0] ?? [];
    expect(input).toBe("/api/sessions");
    expect(init?.method).toBe("POST");
    const headers = new Headers(init?.headers);
    expect(headers.get("content-type")).toBe("application/json");
    expect(headers.get("accept")).toBe("application/json");
    expect(init?.body).toBe(body);
    expect(init).not.toHaveProperty("signal");
    expect(init).not.toHaveProperty("credentials");
    expect(init).not.toHaveProperty("mode");
  });

  it.each([
    ["Worker の形", WORKER_ID],
    ["standalone の形", STANDALONE_ID],
  ])("A-2 201 の成功（%s）", async (_label, sessionId) => {
    const fetchMock = fakeFetch(() =>
      jsonResponse(
        { sessionId, token: TOKEN, wsPath: `/ws/sessions/${sessionId}`, extra: 1, seed: 3 },
        201,
      ),
    );

    const handle = await createSession(BASE, { fetch: fetchMock });

    expect(handle).toStrictEqual({ sessionId, token: TOKEN, wsPath: `/ws/sessions/${sessionId}` });
  });

  const ok = { sessionId: STANDALONE_ID, token: TOKEN, wsPath: `/ws/sessions/${STANDALONE_ID}` };
  it.each<[string, () => Response]>([
    ["JSON でない", () => textResponse("{oops", 201)],
    ["token がない", () => jsonResponse({ ...ok, token: undefined }, 201)],
    ["token が 21 文字", () => jsonResponse({ ...ok, token: "T".repeat(21) }, 201)],
    ["token が + を含む", () => jsonResponse({ ...ok, token: `${"T".repeat(21)}+` }, 201)],
    [
      "sessionId が / を含む",
      () => jsonResponse({ ...ok, sessionId: "s/1", wsPath: "/ws/sessions/s/1" }, 201),
    ],
    [
      "sessionId が 257 文字",
      () => {
        const id = "a".repeat(257);
        return jsonResponse({ ...ok, sessionId: id, wsPath: `/ws/sessions/${id}` }, 201);
      },
    ],
    ["wsPath が別の id", () => jsonResponse({ ...ok, wsPath: "/ws/sessions/other" }, 201)],
    [
      "wsPath が別のホスト",
      () => jsonResponse({ ...ok, wsPath: `//evil.example/ws/sessions/${STANDALONE_ID}` }, 201),
    ],
  ])("A-3 201 の誤り（%s）→ unavailable", async (_label, respond) => {
    const error = await errorOf(createSession(BASE, { fetch: fakeFetch(respond) }));
    expect(error.kind).toBe("unavailable");
  });

  it.each<[string, () => Response, SessionErrorKind]>([
    ["400 bad_request", () => jsonResponse({ code: "bad_request" }, 400), "bad_request"],
    [
      "403 forbidden_origin",
      () => jsonResponse({ code: "forbidden_origin" }, 403),
      "forbidden_origin",
    ],
    [
      "403 turnstile_failed",
      () => jsonResponse({ code: "turnstile_failed", message: "x" }, 403),
      "turnstile_failed",
    ],
    ["429 rate_limited", () => jsonResponse({ code: "rate_limited" }, 429), "rate_limited"],
    ["429 で JSON でない本文", () => textResponse("Too Many Requests", 429), "rate_limited"],
    ["500 internal_error", () => jsonResponse({ code: "internal_error" }, 500), "unavailable"],
    [
      "503 budget_exhausted",
      () => jsonResponse({ code: "budget_exhausted" }, 503),
      "budget_exhausted",
    ],
    [
      "503 sessions_disabled",
      () => jsonResponse({ code: "sessions_disabled" }, 503),
      "sessions_disabled",
    ],
    ["503 guards_invalid", () => jsonResponse({ code: "guards_invalid" }, 503), "unavailable"],
    ["404 not_found", () => jsonResponse({ code: "not_found" }, 404), "unavailable"],
    ["200", () => jsonResponse(ok, 200), "unavailable"],
    ["502 で HTML の本文", () => textResponse("<html>Bad Gateway</html>", 502), "unavailable"],
  ])("A-4 %s", async (_label, respond, kind) => {
    const error = await errorOf(createSession(BASE, { fetch: fakeFetch(respond) }));
    expect(error.kind).toBe(kind);
  });

  it("A-5 fetch が投げる → unavailable", async () => {
    const fetchMock = vi.fn<typeof fetch>(async () => {
      throw new TypeError("Failed to fetch");
    });
    const error = await errorOf(createSession(BASE, { fetch: fetchMock }));
    expect(error.kind).toBe("unavailable");
    expect(error.name).toBe("SessionCreateError");
  });

  it.each<[string, () => Response]>([
    ["403", () => jsonResponse({ code: "turnstile_failed", message: "secret-body" }, 403)],
    ["201 の形の誤り", () => jsonResponse({ ...ok, wsPath: "/secret-body" }, 201)],
    ["502", () => textResponse("secret-body", 502)],
    [
      "fetch が投げる",
      () => {
        throw new TypeError("secret-body");
      },
    ],
  ])("A-5 例外のメッセージにトークンと本文を含めない（%s）", async (_label, respond) => {
    const request: SessionRequest = { ...BASE, turnstileToken: "tok" };
    const error = await errorOf(createSession(request, { fetch: fakeFetch(respond) }));
    expect(error.message).not.toContain("tok");
    expect(error.message).not.toContain("secret-body");
    expect(error.message).not.toContain(TOKEN);
    expect(error.message).toBe(`セッションを作れませんでした（${error.kind}）`);
  });
});
