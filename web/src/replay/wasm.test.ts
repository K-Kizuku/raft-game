import { afterEach, describe, expect, it, vi } from "vitest";

import {
  loadReplayEngine,
  REPLAY_WASM_URL,
  ReplayLoadError,
  resetReplayEngineForTest,
  WASM_EXEC_URL,
  type ReplayGlobal,
  type ReplayLoaderDeps,
} from "./wasm.ts";

type FakeScript = {
  src: string;
  async: boolean;
  onload: (() => void) | null;
  onerror: (() => void) | null;
  remove(): void;
};

function setup(
  options: {
    scriptFails?: boolean;
    goDefined?: boolean;
    defineRaftReplay?: boolean;
    streamingFails?: boolean;
    noStreaming?: boolean;
    status?: number;
  } = {},
) {
  const global: ReplayGlobal = {};
  const ran: unknown[] = [];
  class FakeGo {
    importObject = { go: {} };
    run(instance: unknown): Promise<void> {
      ran.push(instance);
      if (options.defineRaftReplay !== false) {
        global.raftReplay = (text: string) => `echo:${text}`;
      }
      return new Promise(() => {});
    }
  }
  if (options.goDefined === true) {
    global.Go = FakeGo;
  }
  const scripts: FakeScript[] = [];
  const document = {
    createElement(): FakeScript {
      return { src: "", async: false, onload: null, onerror: null, remove() {} };
    },
    head: {
      appendChild(script: FakeScript) {
        scripts.push(script);
        queueMicrotask(() => {
          if (options.scriptFails === true) {
            script.onerror?.();
          } else {
            global.Go = FakeGo;
            script.onload?.();
          }
        });
      },
    },
  };
  const instance = { exports: {} } as WebAssembly.Instance;
  const status = options.status ?? 200;
  const fetch = vi.fn(async (url: string) => {
    void url;
    return {
      ok: status >= 200 && status < 300,
      status,
      arrayBuffer: async () => new ArrayBuffer(8),
    } as Response;
  });
  const instantiateStreaming = vi.fn(async (source: Response | PromiseLike<Response>) => {
    await source;
    if (options.streamingFails === true || status !== 200) {
      throw new TypeError("Incorrect response MIME type");
    }
    return { instance, module: {} as WebAssembly.Module };
  });
  const instantiate = vi.fn(async () => ({ instance, module: {} as WebAssembly.Module }));
  const deps: Partial<ReplayLoaderDeps> = {
    fetch,
    document: document as unknown as Document,
    WebAssembly:
      options.noStreaming === true ? { instantiate } : { instantiateStreaming, instantiate },
    global,
  };
  return { deps, global, scripts, fetch, instantiateStreaming, instantiate, ran, instance };
}

afterEach(() => {
  resetReplayEngineForTest();
});

describe("loadReplayEngine", () => {
  it("wasm_exec.js を読み、WASM を起動して raftReplay を呼ぶ。2 回目は同じものを返す", async () => {
    const s = setup();
    const first = loadReplayEngine(s.deps);
    const engine = await first;
    expect(s.scripts.map((x) => x.src)).toEqual([WASM_EXEC_URL]);
    expect(s.fetch).toHaveBeenCalledWith(REPLAY_WASM_URL);
    expect(s.instantiateStreaming).toHaveBeenCalledTimes(1);
    expect(s.instantiate).not.toHaveBeenCalled();
    expect(s.ran).toEqual([s.instance]);
    expect(engine.run("log")).toBe("echo:log");

    expect(loadReplayEngine(s.deps)).toBe(first);
    expect(s.scripts).toHaveLength(1);
  });

  it("Go があればスクリプトを足さない", async () => {
    const s = setup({ goDefined: true });
    await loadReplayEngine(s.deps);
    expect(s.scripts).toHaveLength(0);
  });

  it("instantiateStreaming が失敗したら arrayBuffer と instantiate で読み直す", async () => {
    const s = setup({ streamingFails: true });
    const engine = await loadReplayEngine(s.deps);
    expect(s.fetch).toHaveBeenCalledTimes(2);
    expect(s.instantiate).toHaveBeenCalledTimes(1);
    expect(engine.run("x")).toBe("echo:x");
  });

  it("instantiateStreaming がなければ instantiate で読む", async () => {
    const s = setup({ noStreaming: true });
    await loadReplayEngine(s.deps);
    expect(s.fetch).toHaveBeenCalledTimes(1);
    expect(s.instantiate).toHaveBeenCalledTimes(1);
  });

  it("404 なら ReplayLoadError。次の呼び出しでやり直せる", async () => {
    const s = setup({ status: 404 });
    await expect(loadReplayEngine(s.deps)).rejects.toBeInstanceOf(ReplayLoadError);
    expect(s.instantiate).not.toHaveBeenCalled();

    const ok = setup();
    await expect(loadReplayEngine(ok.deps)).resolves.toBeDefined();
  });

  it("wasm_exec.js を読めなければ ReplayLoadError", async () => {
    const s = setup({ scriptFails: true });
    await expect(loadReplayEngine(s.deps)).rejects.toBeInstanceOf(ReplayLoadError);
    expect(s.fetch).not.toHaveBeenCalled();
  });

  it("raftReplay が定義されなければ ReplayLoadError", async () => {
    const s = setup({ defineRaftReplay: false });
    await expect(loadReplayEngine(s.deps)).rejects.toBeInstanceOf(ReplayLoadError);
  });

  it("raftReplay の戻り値が文字列でなければ run が投げる", async () => {
    const s = setup();
    const engine = await loadReplayEngine(s.deps);
    s.global.raftReplay = () => 1;
    expect(() => engine.run("x")).toThrow(ReplayLoadError);
  });
});
