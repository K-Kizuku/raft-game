// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";

import {
  createTurnstileClient,
  loadTurnstileScript,
  TurnstileError,
  type TurnstileApi,
  type TurnstileErrorReason,
  type TurnstileRenderParams,
} from "./turnstile.ts";

const SCRIPT_URL = "https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit";
const SITE_KEY = "1x00000000000000000000AA";

// 呼び出しを記録し、渡されたコールバックを後から呼べる偽の TurnstileApi。
function fakeApi(renderResult: () => string | null | undefined = () => "w-1") {
  const calls: string[] = [];
  let params: TurnstileRenderParams | null = null;
  const api = {
    render: vi.fn((_container: HTMLElement, p: TurnstileRenderParams) => {
      calls.push("render");
      params = p;
      return renderResult();
    }),
    execute: vi.fn((widgetId: string) => {
      calls.push(`execute:${widgetId}`);
    }),
    reset: vi.fn((widgetId: string) => {
      calls.push(`reset:${widgetId}`);
    }),
  } satisfies TurnstileApi;
  return {
    api,
    calls,
    params: (): TurnstileRenderParams => {
      if (params === null) {
        throw new Error("render が呼ばれていません");
      }
      return params;
    },
  };
}

// マイクロタスクを流す（loadApi の解決と execute まで）。
async function flush(): Promise<void> {
  for (let i = 0; i < 5; i++) {
    await Promise.resolve();
  }
}

async function reasonOf(promise: Promise<unknown>): Promise<TurnstileErrorReason> {
  const error: unknown = await promise.then(
    () => undefined,
    (e: unknown) => e,
  );
  expect(error).toBeInstanceOf(TurnstileError);
  return (error as TurnstileError).reason;
}

function setup(renderResult?: () => string | null | undefined) {
  const fake = fakeApi(renderResult);
  const loadApi = vi.fn(async () => fake.api);
  const client = createTurnstileClient({ siteKey: SITE_KEY, loadApi });
  const container = document.createElement("div");
  return { ...fake, loadApi, client, container };
}

afterEach(() => {
  delete window.turnstile;
  for (const script of document.head.querySelectorAll("script")) {
    script.remove();
  }
});

describe("createTurnstileClient", () => {
  it("T-1 最初の getToken: 読み込み・描画・確認を 1 回ずつ行い、トークンを返す", async () => {
    const { api, loadApi, client, container, params } = setup();

    const token = client.getToken(container);
    await flush();

    expect(loadApi).toHaveBeenCalledTimes(1);
    expect(api.render).toHaveBeenCalledTimes(1);
    expect(api.render.mock.calls[0]?.[0]).toBe(container);
    const p = params();
    expect({
      sitekey: p.sitekey,
      execution: p.execution,
      appearance: p.appearance,
      language: p.language,
      retry: p.retry,
      "refresh-expired": p["refresh-expired"],
      "response-field": p["response-field"],
    }).toEqual({
      sitekey: SITE_KEY,
      execution: "execute",
      appearance: "interaction-only",
      language: "ja",
      retry: "never",
      "refresh-expired": "never",
      "response-field": false,
    });
    expect(Object.keys(p).sort()).toEqual(
      [
        "sitekey",
        "execution",
        "appearance",
        "language",
        "retry",
        "refresh-expired",
        "response-field",
        "callback",
        "error-callback",
        "expired-callback",
        "timeout-callback",
        "unsupported-callback",
      ].sort(),
    );
    for (const key of [
      "callback",
      "error-callback",
      "expired-callback",
      "timeout-callback",
      "unsupported-callback",
    ] as const) {
      expect(typeof p[key]).toBe("function");
    }
    expect(api.execute).toHaveBeenCalledTimes(1);
    expect(api.execute).toHaveBeenCalledWith("w-1");

    p.callback("tok");
    await expect(token).resolves.toBe("tok");
  });

  it("T-2 reset() の後の getToken は描画せず、reset の後に execute する", async () => {
    const { api, calls, client, container, params } = setup();

    const first = client.getToken(container);
    await flush();
    params().callback("tok-1");
    await expect(first).resolves.toBe("tok-1");

    client.reset();
    const second = client.getToken(document.createElement("div"));
    await flush();
    params().callback("tok-2");
    await expect(second).resolves.toBe("tok-2");

    expect(api.render).toHaveBeenCalledTimes(1);
    expect(calls).toEqual(["render", "execute:w-1", "reset:w-1", "execute:w-1"]);
  });

  it.each<[keyof TurnstileRenderParams, TurnstileErrorReason]>([
    ["error-callback", "error"],
    ["expired-callback", "expired"],
    ["timeout-callback", "timeout"],
    ["unsupported-callback", "unsupported"],
  ])("T-3 取得中に %s → reason %s", async (key, reason) => {
    const { client, container, params } = setup();

    const token = client.getToken(container);
    await flush();
    (params()[key] as () => void)();

    await expect(reasonOf(token)).resolves.toBe(reason);
  });

  it("T-3 取得中でないときのコールバックは何も起こさない", async () => {
    const { api, client, container, params } = setup();

    const token = client.getToken(container);
    await flush();
    const p = params();
    p.callback("tok");
    await expect(token).resolves.toBe("tok");

    expect(() => {
      p["error-callback"]();
      p["expired-callback"]();
      p["timeout-callback"]();
      p["unsupported-callback"]();
      p.callback("late");
    }).not.toThrow();
    expect(api.execute).toHaveBeenCalledTimes(1);
    expect(api.reset).not.toHaveBeenCalled();

    // 次の取得は、前のコールバックに影響されない。
    const next = client.getToken(container);
    await flush();
    p.callback("tok-2");
    await expect(next).resolves.toBe("tok-2");
  });

  it("T-4 loadApi が失敗 → reason load。次の getToken で読み込み直す", async () => {
    const fake = fakeApi();
    const loadApi = vi
      .fn<() => Promise<TurnstileApi>>()
      .mockRejectedValueOnce(new Error("blocked"))
      .mockResolvedValueOnce(fake.api);
    const client = createTurnstileClient({ siteKey: SITE_KEY, loadApi });
    const container = document.createElement("div");

    await expect(reasonOf(client.getToken(container))).resolves.toBe("load");
    expect(fake.api.render).not.toHaveBeenCalled();

    const token = client.getToken(container);
    await flush();
    expect(loadApi).toHaveBeenCalledTimes(2);
    fake.params().callback("tok");
    await expect(token).resolves.toBe("tok");
  });

  it.each<[string, () => string | null | undefined]>([
    ["undefined を返す", () => undefined],
    ["null を返す", () => null],
    [
      "投げる",
      () => {
        throw new Error("bad sitekey");
      },
    ],
  ])("T-4 render が%s → reason render", async (_label, renderResult) => {
    const { api, client, container } = setup(renderResult);

    await expect(reasonOf(client.getToken(container))).resolves.toBe("render");
    expect(api.execute).not.toHaveBeenCalled();
  });

  it("T-5 取得中の 2 回目の getToken は同じ Promise で、execute は 1 回", async () => {
    const { api, loadApi, client, container, params } = setup();

    const first = client.getToken(container);
    const second = client.getToken(container);
    await flush();
    const third = client.getToken(container);

    expect(second).toBe(first);
    expect(third).toBe(first);
    expect(loadApi).toHaveBeenCalledTimes(1);
    expect(api.execute).toHaveBeenCalledTimes(1);
    params().callback("tok");
    await expect(first).resolves.toBe("tok");
  });

  it("T-6 callback('') → reason empty", async () => {
    const { client, container, params } = setup();

    const token = client.getToken(container);
    await flush();
    params().callback("");

    await expect(reasonOf(token)).resolves.toBe("empty");
  });

  it("T-6 取得中の reset() → reason error で失敗し、reset(widgetId) を呼ぶ", async () => {
    const { api, client, container } = setup();

    const token = client.getToken(container);
    await flush();
    client.reset();

    await expect(reasonOf(token)).resolves.toBe("error");
    expect(api.reset).toHaveBeenCalledWith("w-1");
  });

  it("描画の前の reset() は、描画も確認もしない", async () => {
    const { api, client, container } = setup();

    const token = client.getToken(container);
    client.reset();
    await flush();

    await expect(reasonOf(token)).resolves.toBe("error");
    expect(api.render).not.toHaveBeenCalled();
    expect(api.execute).not.toHaveBeenCalled();
  });

  it("例外のメッセージにトークンを含めない", () => {
    const error = new TurnstileError("empty");
    expect(error.name).toBe("TurnstileError");
    expect(error.message).toBe("Turnstile の確認に失敗しました（empty）");
  });
});

describe("loadTurnstileScript", () => {
  function scripts(): HTMLScriptElement[] {
    return [...document.head.querySelectorAll("script")];
  }

  it("T-7 スクリプトを 1 つだけ足し、load で window.turnstile を返す", async () => {
    const first = loadTurnstileScript();
    const second = loadTurnstileScript();

    expect(second).toBe(first);
    const added = scripts();
    expect(added).toHaveLength(1);
    const script = added[0];
    expect(script?.getAttribute("src")).toBe(SCRIPT_URL);
    expect(script?.async).toBe(true);

    const { api } = fakeApi();
    window.turnstile = api;
    script?.dispatchEvent(new Event("load"));

    await expect(first).resolves.toBe(api);
  });

  it("T-7 error で失敗し、要素を消し、次の呼び出しで新しい要素を足す", async () => {
    const first = loadTurnstileScript();
    const script = scripts()[0];
    script?.dispatchEvent(new Event("error"));

    const error: unknown = await first.then(
      () => undefined,
      (e: unknown) => e,
    );
    expect(error).toBeInstanceOf(TurnstileError);
    expect((error as TurnstileError).reason).toBe("load");
    expect(scripts()).toHaveLength(0);

    const again = loadTurnstileScript();
    expect(again).not.toBe(first);
    const added = scripts();
    expect(added).toHaveLength(1);
    expect(added[0]).not.toBe(script);

    const { api } = fakeApi();
    window.turnstile = api;
    added[0]?.dispatchEvent(new Event("load"));
    await expect(again).resolves.toBe(api);
  });

  it("T-7 load で window.turnstile がなければ失敗する", async () => {
    const promise = loadTurnstileScript();
    scripts()[0]?.dispatchEvent(new Event("load"));

    const error: unknown = await promise.then(
      () => undefined,
      (e: unknown) => e,
    );
    expect(error).toBeInstanceOf(TurnstileError);
  });

  it("T-7 最初から window.turnstile があれば要素を足さない", async () => {
    const { api } = fakeApi();
    window.turnstile = api;

    await expect(loadTurnstileScript()).resolves.toBe(api);
    expect(scripts()).toHaveLength(0);
  });
});
