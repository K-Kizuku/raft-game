// リプレイの WASM（R2・server/cmd/replaywasm）を読む（ADR-0028 R4・T-720d）。
// /wasm/wasm_exec.js を読み、/wasm/raft-replay.wasm を起動して globalThis.raftReplay を使う。
// リプレイの画面を開いたときだけ呼ぶ。読み込みは 1 回だけ（成功した Promise を持ち続ける）。
// Go の main は止まったまま残る（画面を離れても止めない）。

export const WASM_EXEC_URL = "/wasm/wasm_exec.js";
export const REPLAY_WASM_URL = "/wasm/raft-replay.wasm";

export type ReplayEngine = Readonly<{
  // 入力ログの JSON の文字列（ファイルの中身そのまま）を受け、応答の JSON の文字列を返す（同期）。
  run(inputLogText: string): string;
}>;

export class ReplayLoadError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "ReplayLoadError";
  }
}

// wasm_exec.js の Go クラスのうち、ここで使うもの。
type GoInstance = {
  importObject: WebAssembly.Imports;
  run(instance: WebAssembly.Instance): Promise<void>;
};
type GoConstructor = new () => GoInstance;

// globalThis のうち、ここで読むもの（wasm_exec.js が Go を、Go の main が raftReplay を置く）。
export type ReplayGlobal = {
  Go?: unknown;
  raftReplay?: unknown;
};

type WebAssemblyLike = {
  instantiateStreaming?: (
    source: Response | PromiseLike<Response>,
    importObject?: WebAssembly.Imports,
  ) => Promise<WebAssembly.WebAssemblyInstantiatedSource>;
  instantiate(
    bytes: BufferSource,
    importObject?: WebAssembly.Imports,
  ): Promise<WebAssembly.WebAssemblyInstantiatedSource>;
};

export type ReplayLoaderDeps = {
  fetch: (url: string) => Promise<Response>;
  document: Document;
  WebAssembly: WebAssemblyLike;
  global: ReplayGlobal;
};

function defaultDeps(): ReplayLoaderDeps {
  return {
    fetch: (url) => globalThis.fetch(url),
    document: globalThis.document,
    WebAssembly: globalThis.WebAssembly,
    global: globalThis as unknown as ReplayGlobal,
  };
}

let cached: Promise<ReplayEngine> | null = null;

// loadReplayEngine は WASM を 1 回だけ読む。失敗したら Promise を捨てる（次の呼び出しでやり直せる）。
// deps はテストのための差し替え（省略した欄は既定）。
export function loadReplayEngine(deps: Partial<ReplayLoaderDeps> = {}): Promise<ReplayEngine> {
  if (cached !== null) {
    return cached;
  }
  const promise = load({ ...defaultDeps(), ...deps });
  cached = promise;
  promise.catch(() => {
    if (cached === promise) {
      cached = null;
    }
  });
  return promise;
}

// resetReplayEngineForTest は持っている Promise を捨てる（テスト用）。
export function resetReplayEngineForTest(): void {
  cached = null;
}

function loadScript(doc: Document, src: string): Promise<void> {
  return new Promise((resolve, reject) => {
    const script = doc.createElement("script");
    script.src = src;
    script.async = true;
    script.onload = () => {
      resolve();
    };
    script.onerror = () => {
      script.remove();
      reject(new ReplayLoadError(`${src} を読み込めません`));
    };
    doc.head.appendChild(script);
  });
}

async function instantiate(
  deps: ReplayLoaderDeps,
  importObject: WebAssembly.Imports,
): Promise<WebAssembly.Instance> {
  const wasm = deps.WebAssembly;
  if (typeof wasm.instantiateStreaming === "function") {
    try {
      const source = await wasm.instantiateStreaming(deps.fetch(REPLAY_WASM_URL), importObject);
      return source.instance;
    } catch {
      // content-type が application/wasm でないときなど。下で読み直す。
    }
  }
  const response = await deps.fetch(REPLAY_WASM_URL);
  if (!response.ok) {
    throw new ReplayLoadError(`${REPLAY_WASM_URL} を取得できません（${response.status}）`);
  }
  const bytes = await response.arrayBuffer();
  const source = await wasm.instantiate(bytes, importObject);
  return source.instance;
}

async function load(deps: ReplayLoaderDeps): Promise<ReplayEngine> {
  const global = deps.global;
  try {
    if (typeof global.Go !== "function") {
      await loadScript(deps.document, WASM_EXEC_URL);
    }
    if (typeof global.Go !== "function") {
      throw new ReplayLoadError("wasm_exec.js が Go を定義していません");
    }
    const go = new (global.Go as GoConstructor)();
    const instance = await instantiate(deps, go.importObject);
    // await しない。Go の main は raftReplay を置いて止まったまま残る。
    void go.run(instance).catch(() => {});
    const fn = global.raftReplay;
    if (typeof fn !== "function") {
      throw new ReplayLoadError("raftReplay が定義されていません");
    }
  } catch (error) {
    if (error instanceof ReplayLoadError) {
      throw error;
    }
    throw new ReplayLoadError(error instanceof Error ? error.message : String(error));
  }
  return {
    run(inputLogText) {
      const fn = global.raftReplay;
      if (typeof fn !== "function") {
        throw new ReplayLoadError("raftReplay が定義されていません");
      }
      const out: unknown = (fn as (text: string) => unknown)(inputLogText);
      if (typeof out !== "string") {
        throw new ReplayLoadError("raftReplay の戻り値が文字列ではありません");
      }
      return out;
    },
  };
}
