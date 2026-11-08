// @vitest-environment jsdom
// mountMap の初期化（#386）。Application.init をモックし、試し直しと resolution の抑え方を確かめる。
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { GameStore } from "../../state/index.ts";
import { mountMap } from "./mapRenderer.ts";

type InitOptions = Readonly<{
  preference?: string;
  resolution?: number;
  antialias?: boolean;
  autoDensity?: boolean;
  resizeTo?: unknown;
}>;

const pixi = vi.hoisted(() => ({
  init: vi.fn<(options: InitOptions) => Promise<void>>(),
  apps: [] as { destroy: ReturnType<typeof vi.fn> }[],
}));

vi.mock("pixi.js", async (importOriginal) => {
  const actual = await importOriginal<typeof import("pixi.js")>();
  class FakeApplication {
    stage = new actual.Container();
    ticker = { add: vi.fn(), remove: vi.fn() };
    canvas = document.createElement("canvas");
    screen = { width: 800, height: 600 };
    renderer = { resolution: 1, resize: vi.fn() };
    render = vi.fn();
    queueResize = vi.fn();
    destroy = vi.fn();
    constructor() {
      pixi.apps.push(this);
    }
    init(options: InitOptions): Promise<void> {
      this.renderer.resolution = options.resolution ?? 1;
      return pixi.init(options);
    }
  }
  return { ...actual, Application: FakeApplication };
});

// world が無いストア（海だけ描く。文字を測らないので jsdom で動く）。
function emptyStore(): GameStore {
  return {
    getState: () => ({ world: null, selection: { islandId: null, ferryId: null } }),
    subscribe: () => () => {},
  } as unknown as GameStore;
}

function hostOf(width: number, height: number): HTMLElement {
  const host = document.createElement("div");
  Object.defineProperty(host, "clientWidth", { value: width, configurable: true });
  Object.defineProperty(host, "clientHeight", { value: height, configurable: true });
  document.body.appendChild(host);
  return host;
}

// WebGL の上限を返す偽の getContext（jsdom には WebGL が無い）。
function stubWebGl(maxTextureSize: number, maxRenderbufferSize: number): void {
  const gl = {
    MAX_TEXTURE_SIZE: 0x0d33,
    MAX_RENDERBUFFER_SIZE: 0x84e8,
    getParameter: (p: number) => (p === 0x0d33 ? maxTextureSize : maxRenderbufferSize),
    getExtension: () => ({ loseContext: () => {} }),
  };
  vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockImplementation(((type: string) =>
    type === "webgl2" ? gl : null) as unknown as typeof HTMLCanvasElement.prototype.getContext);
}

function setDpr(value: number): void {
  Object.defineProperty(window, "devicePixelRatio", { value, configurable: true });
}

let warn: ReturnType<typeof vi.spyOn>;

beforeEach(() => {
  pixi.init.mockReset();
  pixi.apps.length = 0;
  warn = vi.spyOn(console, "warn").mockImplementation(() => {});
  stubWebGl(16384, 16384);
  setDpr(1);
});

afterEach(() => {
  vi.restoreAllMocks();
  document.body.innerHTML = "";
});

describe("mountMap の初期化", () => {
  it("MR-1 app.init が 1 回目に失敗し、2 回目（webgpu を先に）で成功する", async () => {
    setDpr(2);
    pixi.init.mockRejectedValueOnce(new Error("WebGL の初期化に失敗")).mockResolvedValue();
    const host = hostOf(800, 600);
    const handle = await mountMap(host, { store: emptyStore(), now: () => 0 });

    expect(pixi.init).toHaveBeenCalledTimes(2);
    const [first, second] = pixi.init.mock.calls.map(([o]) => o);
    expect(first).toMatchObject({ preference: "webgl", resolution: 2, antialias: true });
    expect(second).toMatchObject({ preference: "webgpu", resolution: 2, antialias: true });
    expect(second?.resizeTo).toBe(host);
    // 失敗した Application は壊し、成功したほうのキャンバスを載せる。
    expect(pixi.apps[0]?.destroy).toHaveBeenCalledTimes(1);
    expect(pixi.apps[1]?.destroy).not.toHaveBeenCalled();
    expect(host.querySelector("canvas")).not.toBeNull();
    expect(warn).toHaveBeenCalledWith(
      "[map] app.init failed",
      expect.objectContaining({ preference: "webgl" }),
      expect.any(Error),
    );

    handle.destroy();
    expect(pixi.apps[1]?.destroy).toHaveBeenCalledTimes(1);
  });

  it("MR-2 3 回目は resolution 1・antialias なしで試し、それも失敗したら理由を並べて reject する", async () => {
    setDpr(2);
    pixi.init
      .mockRejectedValueOnce(new Error("一"))
      .mockRejectedValueOnce(new Error("二"))
      .mockRejectedValueOnce(new Error("三"));
    const host = hostOf(800, 600);

    const promise = mountMap(host, { store: emptyStore(), now: () => 0 });
    await expect(promise).rejects.toThrow(/3 回とも失敗/);
    await expect(promise).rejects.toThrow(/一.*二.*三/);
    expect(pixi.init).toHaveBeenCalledTimes(3);
    expect(pixi.init.mock.calls[2]?.[0]).toMatchObject({
      preference: "webgl",
      resolution: 1,
      antialias: false,
    });
    expect(pixi.apps.every((app) => app.destroy.mock.calls.length === 1)).toBe(true);
    expect(host.querySelector("canvas")).toBeNull();
  });

  it("MR-3 キャンバスの大きさ × devicePixelRatio が WebGL の上限を超えないように resolution を抑える", async () => {
    setDpr(2);
    stubWebGl(4096, 8192);
    pixi.init.mockResolvedValue();
    const handle = await mountMap(hostOf(3000, 1000), { store: emptyStore(), now: () => 0 });

    const resolution = pixi.init.mock.calls[0]?.[0].resolution ?? 0;
    expect(resolution).toBeLessThan(2);
    expect(Math.round(3000 * resolution)).toBeLessThanOrEqual(4096);
    handle.destroy();
  });

  it("MR-4 画面が大きくなったら、最初の描画で resolution を上限に収まるように直す", async () => {
    setDpr(2);
    stubWebGl(4096, 4096);
    pixi.init.mockResolvedValue();
    // 上限に収まる大きさなら、resolution を直さない。
    const handle = await mountMap(hostOf(800, 600), { store: emptyStore(), now: () => 0 });
    const app = pixi.apps[0] as unknown as {
      screen: { width: number; height: number };
      renderer: { resize: ReturnType<typeof vi.fn> };
    };
    expect(app.renderer.resize).not.toHaveBeenCalled();
    handle.destroy();

    // init の後に（resizeTo で）キャンバスが大きくなったら、最初の描画で resolution を抑える。
    pixi.apps.length = 0;
    const big = await mountMap(hostOf(800, 600), {
      store: emptyStore(),
      now: () => {
        const current = pixi.apps[0] as unknown as typeof app | undefined;
        if (current !== undefined) current.screen = { width: 4000, height: 1000 };
        return 0;
      },
    });
    const bigApp = pixi.apps[0] as unknown as typeof app;
    expect(bigApp.renderer.resize).toHaveBeenCalledTimes(1);
    const [w, h, r] = bigApp.renderer.resize.mock.calls[0] as [number, number, number];
    expect([w, h]).toEqual([4000, 1000]);
    expect(Math.round(4000 * r)).toBeLessThanOrEqual(4096);
    big.destroy();
  });

  it("MR-5 最初の描画で例外が出たら、どこで失敗したかを付けて reject し、Application を壊す", async () => {
    pixi.init.mockResolvedValue();
    const host = hostOf(800, 600);
    const promise = mountMap(host, {
      store: emptyStore(),
      now: () => {
        throw new Error("時計が壊れた");
      },
    });
    await expect(promise).rejects.toThrow("最初の描画で失敗: 時計が壊れた");
    expect(pixi.apps[0]?.destroy).toHaveBeenCalledTimes(1);
  });
});
