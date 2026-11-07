// Cloudflare Turnstile のウィジェット（T-413・ADR-0018 の G4）。React 用のライブラリは使わずに直接扱う。
// - スクリプトはサイトキーがあるときだけ、最初に要るときに読み込む（index.html に書かない）
// - `explicit` で 1 回だけ描画し、選んだときに `execute` で確認を始める
// - トークンは 1 回しか使えないので、使ったら呼ぶ側が必ず reset() する
// - トークンは返すだけで、保存・console・URL・例外のメッセージに出さない
// スクリプトの URL はこのファイルだけに書く。

const TURNSTILE_SCRIPT_URL =
  "https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit";

export type TurnstileRenderParams = {
  sitekey: string;
  execution: "execute";
  appearance: "interaction-only";
  language: "ja";
  retry: "never";
  "refresh-expired": "never";
  "response-field": false;
  callback: (token: string) => void;
  "error-callback": () => void;
  "expired-callback": () => void;
  "timeout-callback": () => void;
  "unsupported-callback": () => void;
};

export interface TurnstileApi {
  render(container: HTMLElement, params: TurnstileRenderParams): string | null | undefined;
  execute(widgetId: string): void;
  reset(widgetId: string): void;
}

export interface TurnstileClient {
  getToken(container: HTMLElement): Promise<string>; // 失敗は TurnstileError
  reset(): void;
}

declare global {
  interface Window {
    turnstile?: TurnstileApi;
  }
}

export type TurnstileErrorReason =
  "load" | "render" | "empty" | "error" | "expired" | "timeout" | "unsupported";

export class TurnstileError extends Error {
  readonly reason: TurnstileErrorReason;

  constructor(reason: TurnstileErrorReason) {
    super(`Turnstile の確認に失敗しました（${reason}）`);
    this.name = "TurnstileError";
    this.reason = reason;
  }
}

// 読み込み中の Promise。モジュールに 1 つだけ持ち、終わったら捨てる
// （成功した後は window.turnstile を使い、失敗した後は次の呼び出しで読み込み直す）。
let loadingScript: Promise<TurnstileApi> | null = null;

export function loadTurnstileScript(): Promise<TurnstileApi> {
  const existing = window.turnstile;
  if (existing !== undefined) {
    return Promise.resolve(existing);
  }
  if (loadingScript !== null) {
    return loadingScript;
  }
  const promise = new Promise<TurnstileApi>((resolve, reject) => {
    const script = document.createElement("script");
    script.src = TURNSTILE_SCRIPT_URL;
    script.async = true;
    script.addEventListener("load", () => {
      const api = window.turnstile;
      if (api !== undefined) {
        resolve(api);
      } else {
        script.remove();
        reject(new TurnstileError("load"));
      }
    });
    script.addEventListener("error", () => {
      script.remove();
      reject(new TurnstileError("load"));
    });
    document.head.appendChild(script);
  });
  loadingScript = promise;
  const forget = () => {
    if (loadingScript === promise) {
      loadingScript = null;
    }
  };
  promise.then(forget, forget);
  return promise;
}

type Pending = {
  promise: Promise<string>;
  resolve: (token: string) => void;
  reject: (error: TurnstileError) => void;
};

export function createTurnstileClient({
  siteKey,
  loadApi = loadTurnstileScript,
}: {
  siteKey: string;
  loadApi?: () => Promise<TurnstileApi>;
}): TurnstileClient {
  let api: TurnstileApi | null = null;
  let widgetId: string | null = null;
  let pending: Pending | null = null;

  // 取得中のものを終わらせる。取得中でなければ何もしない（取得中でないときのコールバックは無視する）。
  function fail(target: Pending | null, reason: TurnstileErrorReason): void {
    if (target === null || pending !== target) {
      return;
    }
    pending = null;
    target.reject(new TurnstileError(reason));
  }

  function succeed(token: string): void {
    const target = pending;
    if (target === null) {
      return;
    }
    if (token === "") {
      fail(target, "empty");
      return;
    }
    pending = null;
    target.resolve(token);
  }

  const params: TurnstileRenderParams = {
    sitekey: siteKey,
    execution: "execute",
    appearance: "interaction-only",
    language: "ja",
    retry: "never",
    "refresh-expired": "never",
    "response-field": false,
    callback: (token: string) => {
      succeed(token);
    },
    "error-callback": () => {
      fail(pending, "error");
    },
    "expired-callback": () => {
      fail(pending, "expired");
    },
    "timeout-callback": () => {
      fail(pending, "timeout");
    },
    "unsupported-callback": () => {
      fail(pending, "unsupported");
    },
  };

  async function start(target: Pending, container: HTMLElement): Promise<void> {
    let loaded: TurnstileApi;
    try {
      loaded = await loadApi();
    } catch {
      fail(target, "load");
      return;
    }
    // 読み込みの間に reset() されたら、描画も確認もしない。
    if (pending !== target) {
      return;
    }
    if (widgetId === null) {
      let id: string | null | undefined;
      try {
        id = loaded.render(container, params);
      } catch {
        id = null;
      }
      if (typeof id !== "string") {
        fail(target, "render");
        return;
      }
      api = loaded;
      widgetId = id;
    }
    try {
      (api ?? loaded).execute(widgetId);
    } catch {
      fail(target, "error");
    }
  }

  return {
    getToken(container: HTMLElement): Promise<string> {
      if (pending !== null) {
        return pending.promise;
      }
      let resolve!: (token: string) => void;
      let reject!: (error: TurnstileError) => void;
      const promise = new Promise<string>((res, rej) => {
        resolve = res;
        reject = rej;
      });
      const target: Pending = { promise, resolve, reject };
      pending = target;
      void start(target, container);
      return promise;
    },
    reset(): void {
      if (api !== null && widgetId !== null) {
        api.reset(widgetId);
      }
      fail(pending, "error");
    },
  };
}
