// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Mock } from "vitest";

import { gameStore } from "../state/index.ts";
import { engineerModeStore } from "../state/engineerMode.ts";
import { onIslandPress } from "../ui/ferry/islandPress.ts";
import { setShowRaftTerms } from "../ui/terms/preference.ts";
import { MapView } from "./MapView.tsx";
import type { MapHandle } from "./pixi/mapRenderer.ts";
import { mountMap } from "./pixi/mapRenderer.ts";
import { makeSnapshot } from "./testing/snapshot.ts";

// jsdom で PixiJS（WebGL）を読み込まない。
vi.mock("./pixi/mapRenderer.ts", () => ({ mountMap: vi.fn() }));

const mountMock = vi.mocked(mountMap);

function handle(): { destroy: Mock<() => void> } {
  return { destroy: vi.fn<() => void>() };
}

function deferred<T>(): {
  promise: Promise<T>;
  resolve: (v: T) => void;
  reject: (e: unknown) => void;
} {
  let resolve: (v: T) => void = () => {};
  let reject: (e: unknown) => void = () => {};
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

function load(): void {
  act(() => {
    gameStore.getState().applySnapshot(makeSnapshot(), 0);
  });
}

function islandButtons(): HTMLElement[] {
  return within(screen.getByRole("navigation", { name: "島を選ぶ" })).getAllByRole("button");
}

beforeEach(() => {
  gameStore.getState().reset();
  mountMock.mockReset();
});

afterEach(() => {
  cleanup();
  gameStore.getState().reset();
  setShowRaftTerms(false);
});

describe("MapView", () => {
  it("MV-1 world が null なら「マップ（準備中）」だけで、mountMap を呼ばない", async () => {
    render(<MapView />);
    expect(screen.getByText("マップ（準備中）")).toBeDefined();
    expect(screen.queryByRole("navigation", { name: "島を選ぶ" })).toBeNull();
    await act(async () => {
      await Promise.resolve();
    });
    expect(mountMock).not.toHaveBeenCalled();
  });

  it("MV-2 applySnapshot の後、mountMap を 1 回だけ呼び、島のボタンを stage の順に出す", async () => {
    const h = handle();
    mountMock.mockResolvedValue(h);
    render(<MapView />);
    load();

    await waitFor(() => expect(mountMock).toHaveBeenCalledTimes(1));
    const [host, deps] = mountMock.mock.calls[0] ?? [];
    expect(host).toBeInstanceOf(HTMLElement);
    expect(host?.classList.contains("map-view__canvas")).toBe(true);
    expect(host?.getAttribute("aria-hidden")).toBe("true");
    // ストアはそのものでなく、島を選ぶと押したことも知らせる包み（MV-7）。読む状態は同じ。
    expect(deps?.store.getState().world).toBe(gameStore.getState().world);
    expect(deps?.store.subscribe).toBe(gameStore.subscribe);
    expect(typeof deps?.now).toBe("function");
    expect(screen.queryByText("マップ（準備中）")).toBeNull();
    expect(islandButtons().map((b) => b.textContent)).toEqual([
      "一の島 👑代表",
      "二の島",
      "三の島",
    ]);

    // イベントが届いても載せ直さない。
    act(() => {
      gameStore.getState().applyEvents({ t: 1000, speed: 1, paused: false, endsAt: 180000 }, [], 0);
    });
    await act(async () => {
      await Promise.resolve();
    });
    expect(mountMock).toHaveBeenCalledTimes(1);
    expect(h.destroy).not.toHaveBeenCalled();
  });

  it("MV-3 ボタンで島を選び、代表の島に 👑 と「代表」を出す", async () => {
    mountMock.mockResolvedValue(handle());
    render(<MapView />);
    load();
    await waitFor(() => expect(mountMock).toHaveBeenCalledTimes(1));

    const two = screen.getByRole("button", { name: "二の島" });
    expect(two.getAttribute("aria-pressed")).toBe("false");
    fireEvent.click(two);
    expect(gameStore.getState().selection.islandId).toBe(2);
    expect(screen.getByRole("button", { name: "二の島" }).getAttribute("aria-pressed")).toBe(
      "true",
    );

    const leader = screen.getByRole("button", { name: /一の島/ });
    expect(leader.getAttribute("aria-pressed")).toBe("false");
    expect(within(leader).getByText("👑").getAttribute("aria-hidden")).toBe("true");
    expect(within(leader).getByText("代表")).toBeDefined();
    expect(leader.textContent).not.toContain("Leader");

    act(() => {
      setShowRaftTerms(true);
    });
    expect(screen.getByRole("button", { name: /一の島/ }).textContent).toContain("代表（Leader）");
  });

  it("MV-4 アンマウントで destroy を 1 回呼ぶ", async () => {
    const h = handle();
    mountMock.mockResolvedValue(h);
    const { unmount } = render(<MapView />);
    load();
    await waitFor(() => expect(mountMock).toHaveBeenCalledTimes(1));
    await act(async () => {
      await Promise.resolve();
    });
    unmount();
    expect(h.destroy).toHaveBeenCalledTimes(1);
  });

  it("MV-4 mountMap の Promise が解ける前にアンマウント → 解けた後に destroy を 1 回呼ぶ", async () => {
    const h = handle();
    const pending = deferred<MapHandle>();
    mountMock.mockReturnValue(pending.promise);
    const { unmount } = render(<MapView />);
    load();
    await waitFor(() => expect(mountMock).toHaveBeenCalledTimes(1));
    unmount();
    expect(h.destroy).not.toHaveBeenCalled();
    await act(async () => {
      pending.resolve(h);
      await pending.promise;
    });
    await waitFor(() => expect(h.destroy).toHaveBeenCalledTimes(1));
  });

  it("MV-5 mountMap が reject → 描けなかったことを出し、島のボタンは残る", async () => {
    const error = vi.spyOn(console, "error").mockImplementation(() => {});
    mountMock.mockRejectedValue(new Error("WebGL が無い"));
    render(<MapView />);
    load();
    expect(
      await screen.findByText("このブラウザではマップを描けませんでした。島はボタンで選べます。"),
    ).toBeDefined();
    error.mockRestore();
    expect(screen.getByRole("status").textContent).toContain(
      "このブラウザではマップを描けませんでした。",
    );
    expect(islandButtons()).toHaveLength(3);
    expect(document.querySelector(".map-view__canvas")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "三の島" }));
    expect(gameStore.getState().selection.islandId).toBe(3);
  });

  it("MV-5b reject のとき console.error に理由を出し、開発のときは画面の文の下にも理由を出す", async () => {
    const error = vi.spyOn(console, "error").mockImplementation(() => {});
    const reason = new Error("app.init が 3 回とも失敗: webgl: context lost");
    mountMock.mockRejectedValue(reason);
    try {
      render(<MapView />);
      load();
      await screen.findByRole("status");
      expect(error).toHaveBeenCalledWith("[map] mountMap failed", reason);
      expect(import.meta.env.DEV).toBe(true);
      expect(screen.getByTestId("map-failure-reason").textContent).toBe(
        "理由: app.init が 3 回とも失敗: webgl: context lost",
      );
    } finally {
      error.mockRestore();
    }
  });

  it("MV-5c 開発でないときは、理由を画面に出さない（console には出す）", async () => {
    const error = vi.spyOn(console, "error").mockImplementation(() => {});
    vi.stubEnv("DEV", false);
    mountMock.mockRejectedValue(new Error("WebGL が無い"));
    try {
      render(<MapView />);
      load();
      await screen.findByRole("status");
      expect(error).toHaveBeenCalledWith("[map] mountMap failed", expect.any(Error));
      expect(screen.queryByTestId("map-failure-reason")).toBeNull();
      expect(screen.getByRole("status").textContent).not.toContain("WebGL が無い");
    } finally {
      vi.unstubAllEnvs();
      error.mockRestore();
    }
  });

  it("MV-6 描いている途中で reset() → destroy を呼び、「マップ（準備中）」に戻る", async () => {
    const h = handle();
    mountMock.mockResolvedValue(h);
    render(<MapView />);
    load();
    await waitFor(() => expect(mountMock).toHaveBeenCalledTimes(1));
    await act(async () => {
      await Promise.resolve();
    });
    act(() => {
      gameStore.getState().reset();
    });
    expect(h.destroy).toHaveBeenCalledTimes(1);
    expect(screen.getByText("マップ（準備中）")).toBeDefined();
    expect(screen.queryByRole("navigation", { name: "島を選ぶ" })).toBeNull();
  });

  it("MV-7 島のボタン・マップで島を押すと、選ぶうえに押したことを知らせる（同じ島を押し直しても）", async () => {
    mountMock.mockResolvedValue(handle());
    render(<MapView />);
    load();
    await waitFor(() => expect(mountMock).toHaveBeenCalledTimes(1));
    const pressed: number[] = [];
    const off = onIslandPress((id) => pressed.push(id));
    try {
      fireEvent.click(screen.getByRole("button", { name: "二の島" }));
      fireEvent.click(screen.getByRole("button", { name: "二の島" }));
      expect(gameStore.getState().selection.islandId).toBe(2);

      const [, deps] = mountMock.mock.calls[0] ?? [];
      const mapState = deps?.store.getState();
      act(() => {
        mapState?.selectIsland(3);
      });
      expect(gameStore.getState().selection.islandId).toBe(3);
      // 状態が変わらなければ同じオブジェクト（毎フレーム読むため）。
      expect(deps?.store.getState()).toBe(deps?.store.getState());
      act(() => {
        deps?.store.getState().selectIsland(3);
        deps?.store.getState().selectIsland(null);
      });
      expect(pressed).toEqual([2, 2, 3, 3]);
    } finally {
      off();
    }
  });

  it("MV-M1 mountMap に engineer を渡し、engineerModeStore の値を返す", async () => {
    mountMock.mockResolvedValue(handle());
    render(<MapView />);
    load();
    await waitFor(() => expect(mountMock).toHaveBeenCalledTimes(1));
    const [, deps] = mountMock.mock.calls[0] ?? [];
    const engineer = deps?.engineer;
    expect(typeof engineer).toBe("function");
    try {
      engineerModeStore.getState().setOpen(false);
      expect(engineer?.()).toBe(false);
      engineerModeStore.getState().setOpen(true);
      expect(engineer?.()).toBe(true);
    } finally {
      engineerModeStore.getState().setOpen(false);
    }
  });
});
