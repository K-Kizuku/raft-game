import { afterEach, describe, expect, it, vi } from "vitest";

import type { Env } from "../routes/env.ts";
import { TURNSTILE_DUMMY_TOKEN, TURNSTILE_PASS_SECRET } from "./test-helpers.ts";
import { TURNSTILE_SITEVERIFY_URL, verifyTurnstileToken } from "./turnstile.ts";

interface RecordedRequest {
  readonly method: string;
  readonly url: string;
  readonly contentType: string | null;
  readonly body: string;
  readonly signal: AbortSignal;
}

function fakeFetch(respond: () => Response | Promise<Response>) {
  const requests: RecordedRequest[] = [];
  const fetch = async (request: Request): Promise<Response> => {
    requests.push({
      method: request.method,
      url: request.url,
      contentType: request.headers.get("content-type"),
      body: await request.text(),
      signal: request.signal,
    });
    return respond();
  };
  return { fetch, requests };
}

const okResponse = () =>
  new Response('{"success":true,"hostname":"example.com","error-codes":[]}', { status: 200 });

const ENV: Env = { TURNSTILE_SECRET: TURNSTILE_PASS_SECRET };

afterEach(() => {
  vi.restoreAllMocks();
});

describe("V-1 正常", () => {
  it("siteverify を 1 回だけ、決まった形で呼ぶ", async () => {
    const timeout = vi.spyOn(AbortSignal, "timeout");
    const f = fakeFetch(okResponse);
    const result = await verifyTurnstileToken({
      enabled: true,
      env: ENV,
      token: TURNSTILE_DUMMY_TOKEN,
      clientIp: "203.0.113.5",
      fetch: f.fetch,
    });
    expect(result).toBe("ok");
    expect(f.requests).toHaveLength(1);
    const [request] = f.requests;
    expect(request?.method).toBe("POST");
    expect(request?.url).toBe(TURNSTILE_SITEVERIFY_URL);
    expect(request?.contentType).toBe("application/json");
    expect(JSON.parse(request?.body ?? "")).toEqual({
      secret: TURNSTILE_PASS_SECRET,
      response: TURNSTILE_DUMMY_TOKEN,
      remoteip: "203.0.113.5",
    });
    expect(request?.signal).toBeInstanceOf(AbortSignal);
    expect(timeout).toHaveBeenCalledWith(5000);
  });

  it("clientIp が null なら remoteip を入れない", async () => {
    const f = fakeFetch(okResponse);
    const result = await verifyTurnstileToken({
      enabled: true,
      env: ENV,
      token: TURNSTILE_DUMMY_TOKEN,
      clientIp: null,
      fetch: f.fetch,
    });
    expect(result).toBe("ok");
    expect(JSON.parse(f.requests[0]?.body ?? "")).toEqual({
      secret: TURNSTILE_PASS_SECRET,
      response: TURNSTILE_DUMMY_TOKEN,
    });
  });
});

function abortError(): Error {
  const error = new Error("The operation was aborted");
  error.name = "AbortError";
  return error;
}

const REJECTIONS: readonly (readonly [string, () => Response | Promise<Response>])[] = [
  [
    "success: false",
    () => new Response('{"success":false,"error-codes":["invalid-input-response"]}'),
  ],
  ['success: "true"', () => new Response('{"success":"true"}')],
  ["success: 1", () => new Response('{"success":1}')],
  ["{}", () => new Response("{}")],
  ["[]", () => new Response("[]")],
  ['"x"', () => new Response('"x"')],
  ["壊れた JSON", () => new Response('{"success":tru')],
  ["500 で success: true", () => new Response('{"success":true}', { status: 500 })],
  [
    "fetch が例外",
    () => {
      throw new TypeError("network error");
    },
  ],
  [
    "fetch が AbortError",
    () => {
      throw abortError();
    },
  ],
  [
    "fetch が DOMException（TimeoutError）",
    () => Promise.reject(new DOMException("t", "TimeoutError")),
  ],
];

describe("V-2 拒否", () => {
  it.each(REJECTIONS)("%s → turnstile_failed", async (_name, respond) => {
    const f = fakeFetch(respond);
    await expect(
      verifyTurnstileToken({
        enabled: true,
        env: ENV,
        token: TURNSTILE_DUMMY_TOKEN,
        clientIp: "203.0.113.5",
        fetch: f.fetch,
      }),
    ).resolves.toBe("turnstile_failed");
    expect(f.requests).toHaveLength(1);
  });
});

describe("V-3 siteverify を呼ばない", () => {
  it.each([
    ["トークンが undefined", true, ENV, undefined, "turnstile_failed"],
    ["トークンが空", true, ENV, "", "turnstile_failed"],
    ["TURNSTILE_SECRET が未設定", true, {}, TURNSTILE_DUMMY_TOKEN, "turnstile_failed"],
    [
      "TURNSTILE_SECRET が空",
      true,
      { TURNSTILE_SECRET: "" },
      TURNSTILE_DUMMY_TOKEN,
      "turnstile_failed",
    ],
    [
      "TURNSTILE_SECRET が空白を含む",
      true,
      { TURNSTILE_SECRET: "1x00000000 0000000000000000000000AA" },
      TURNSTILE_DUMMY_TOKEN,
      "turnstile_failed",
    ],
    [
      "TURNSTILE_SECRET が 257 文字",
      true,
      { TURNSTILE_SECRET: "x".repeat(257) },
      TURNSTILE_DUMMY_TOKEN,
      "turnstile_failed",
    ],
    [
      "TURNSTILE_SECRET が文字列でない",
      true,
      { TURNSTILE_SECRET: 1 },
      TURNSTILE_DUMMY_TOKEN,
      "turnstile_failed",
    ],
    ["無効でトークンあり", false, ENV, TURNSTILE_DUMMY_TOKEN, "ok"],
    ["無効でトークンなし", false, ENV, undefined, "ok"],
    ["無効でシークレットもない", false, {}, undefined, "ok"],
  ] as const)("%s → %s", async (_name, enabled, env, token, expected) => {
    const f = fakeFetch(okResponse);
    const result = await verifyTurnstileToken({
      enabled,
      env,
      token,
      clientIp: "203.0.113.5",
      fetch: f.fetch,
    });
    expect(result).toBe(expected);
    expect(f.requests).toHaveLength(0);
  });
});

describe("V-4 シークレット・トークンを漏らさない", () => {
  it.each(REJECTIONS)("%s でも値だけを返す", async (_name, respond) => {
    const f = fakeFetch(respond);
    const result = await verifyTurnstileToken({
      enabled: true,
      env: ENV,
      token: TURNSTILE_DUMMY_TOKEN,
      clientIp: null,
      fetch: f.fetch,
    });
    expect(result).toBe("turnstile_failed");
    expect(String(result)).not.toContain(TURNSTILE_PASS_SECRET);
    expect(String(result)).not.toContain(TURNSTILE_DUMMY_TOKEN);
  });
});
