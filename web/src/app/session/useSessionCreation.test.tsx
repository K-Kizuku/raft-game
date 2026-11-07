// @vitest-environment jsdom
import { act, cleanup, renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it, vi, type Mock } from "vitest";

import type { SessionHandle } from "../types.ts";
import type { SessionRequest } from "./api.ts";
import { SessionCreateError } from "./errors.ts";
import type { TurnstileClient } from "./turnstile.ts";
import {
  useSessionCreation,
  type SessionCreationDeps,
  type SessionCreationState,
} from "./useSessionCreation.ts";

const SESSION: SessionHandle = {
  sessionId: "s_abc",
  token: "T".repeat(22),
  wsPath: "/ws/sessions/s_abc",
};
const REQUEST = { stageId: "stage1", seed: 42, demo: false };

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

function fakeTurnstile(getToken: TurnstileClient["getToken"] = async () => "tok") {
  return {
    getToken: vi.fn<TurnstileClient["getToken"]>(getToken),
    reset: vi.fn<TurnstileClient["reset"]>(),
  };
}

type FakeTurnstile = ReturnType<typeof fakeTurnstile>;
type PostSessionMock = Mock<SessionCreationDeps["postSession"]>;

function setup(
  overrides: {
    postSession?: PostSessionMock;
    turnstile?: FakeTurnstile | null;
    getHost?: () => HTMLElement | null;
  } = {},
) {
  const host = document.createElement("div");
  const postSession: PostSessionMock =
    overrides.postSession ?? vi.fn<SessionCreationDeps["postSession"]>(async () => SESSION);
  const turnstile = overrides.turnstile === undefined ? fakeTurnstile() : overrides.turnstile;
  const deps: SessionCreationDeps = {
    postSession,
    turnstile,
    getHost: overrides.getHost ?? (() => host),
  };
  const states: SessionCreationState[] = [];
  const hook = renderHook(() => {
    const creation = useSessionCreation(deps);
    states.push(creation.state);
    return creation;
  });
  // turnstile が null になるのは、null を渡したテストだけ（そのテストは turnstile を見ない）。
  return { hook, host, postSession, turnstile: turnstile as FakeTurnstile, deps, states };
}

function statuses(states: SessionCreationState[]): string[] {
  const out: string[] = [];
  for (const state of states) {
    if (out[out.length - 1] !== state.status) {
      out.push(state.status);
    }
  }
  return out;
}

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("useSessionCreation", () => {
  it("H-1 成功: トークンを付けて 1 回送り、終わったら reset する", async () => {
    const pending = deferred<SessionHandle>();
    const { hook, host, postSession, turnstile, states } = setup({
      postSession: vi.fn<SessionCreationDeps["postSession"]>(() => pending.promise),
    });

    let creating!: Promise<SessionHandle | null>;
    await act(async () => {
      creating = hook.result.current.create(REQUEST);
    });
    expect(hook.result.current.state).toEqual({ status: "pending" });
    expect(turnstile.reset).not.toHaveBeenCalled();
    await act(async () => {
      pending.resolve(SESSION);
      await creating;
    });

    await expect(creating).resolves.toEqual(SESSION);
    expect(turnstile.getToken).toHaveBeenCalledTimes(1);
    expect(turnstile.getToken).toHaveBeenCalledWith(host);
    expect(postSession).toHaveBeenCalledTimes(1);
    expect(postSession).toHaveBeenCalledWith<[SessionRequest]>({
      ...REQUEST,
      turnstileToken: "tok",
    });
    expect(turnstile.reset).toHaveBeenCalledTimes(1);
    expect(postSession.mock.invocationCallOrder[0]).toBeLessThan(
      turnstile.reset.mock.invocationCallOrder[0] ?? 0,
    );
    expect(statuses(states)).toEqual(["idle", "pending", "idle"]);
  });

  it("H-2 turnstile が null → turnstileToken: null で送る", async () => {
    const { hook, postSession } = setup({ turnstile: null });

    await act(async () => {
      await hook.result.current.create({ ...REQUEST, demo: true });
    });

    expect(postSession).toHaveBeenCalledWith<[SessionRequest]>({
      ...REQUEST,
      demo: true,
      turnstileToken: null,
    });
    expect(hook.result.current.state).toEqual({ status: "idle" });
  });

  it("H-3 getToken が失敗 → 送らずに reset し、challenge_failed", async () => {
    const turnstile = fakeTurnstile(async () => {
      throw new Error("timeout");
    });
    const { hook, postSession } = setup({ turnstile });

    let result: SessionHandle | null = SESSION;
    await act(async () => {
      result = await hook.result.current.create(REQUEST);
    });

    expect(result).toBeNull();
    expect(postSession).not.toHaveBeenCalled();
    expect(turnstile.reset).toHaveBeenCalledTimes(1);
    expect(hook.result.current.state).toEqual({ status: "error", kind: "challenge_failed" });
  });

  it("H-3 getHost() が null → 送らずに reset し、challenge_failed", async () => {
    const turnstile = fakeTurnstile();
    const { hook, postSession } = setup({ turnstile, getHost: () => null });

    await act(async () => {
      await hook.result.current.create(REQUEST);
    });

    expect(turnstile.getToken).not.toHaveBeenCalled();
    expect(postSession).not.toHaveBeenCalled();
    expect(turnstile.reset).toHaveBeenCalledTimes(1);
    expect(hook.result.current.state).toEqual({ status: "error", kind: "challenge_failed" });
  });

  it.each<[string, unknown, string]>([
    ["SessionCreateError", new SessionCreateError("budget_exhausted"), "budget_exhausted"],
    ["ほかの例外", new Error("boom"), "unavailable"],
  ])("H-4 postSession が%s → error と reset 1 回", async (_label, error, kind) => {
    const postSession = vi.fn<SessionCreationDeps["postSession"]>(async () => {
      throw error;
    });
    const { hook, turnstile } = setup({ postSession });

    let result: SessionHandle | null = SESSION;
    await act(async () => {
      result = await hook.result.current.create(REQUEST);
    });

    expect(result).toBeNull();
    expect(turnstile.reset).toHaveBeenCalledTimes(1);
    expect(hook.result.current.state).toEqual({ status: "error", kind });
  });

  it("H-5 作成中の 2 回目の create はすぐに null", async () => {
    const pending = deferred<SessionHandle>();
    const postSession = vi.fn<SessionCreationDeps["postSession"]>(() => pending.promise);
    const { hook, turnstile } = setup({ postSession });

    let first!: Promise<SessionHandle | null>;
    let second!: Promise<SessionHandle | null>;
    await act(async () => {
      first = hook.result.current.create(REQUEST);
      // 描画を待たずに 2 回目を呼ぶ。
      second = hook.result.current.create({ ...REQUEST, stageId: "stage2" });
    });
    await expect(second).resolves.toBeNull();
    expect(hook.result.current.state).toEqual({ status: "pending" });

    // 描画の後に呼んでも同じ。
    await act(async () => {
      await expect(hook.result.current.create(REQUEST)).resolves.toBeNull();
    });

    expect(turnstile.getToken).toHaveBeenCalledTimes(1);
    expect(postSession).toHaveBeenCalledTimes(1);
    await act(async () => {
      pending.resolve(SESSION);
      await first;
    });
    await expect(first).resolves.toEqual(SESSION);
    expect(hook.result.current.state).toEqual({ status: "idle" });

    // 終わった後は、また作れる。
    await act(async () => {
      await hook.result.current.create(REQUEST);
    });
    expect(postSession).toHaveBeenCalledTimes(2);
  });

  it("H-6 clearError は error のときだけ idle に戻す", async () => {
    const pending = deferred<SessionHandle>();
    const postSession = vi
      .fn<SessionCreationDeps["postSession"]>()
      .mockImplementationOnce(() => pending.promise)
      .mockRejectedValueOnce(new SessionCreateError("rate_limited"));
    const { hook } = setup({ postSession });

    let first!: Promise<SessionHandle | null>;
    await act(async () => {
      first = hook.result.current.create(REQUEST);
    });
    act(() => {
      hook.result.current.clearError();
    });
    expect(hook.result.current.state).toEqual({ status: "pending" });
    await act(async () => {
      pending.resolve(SESSION);
      await first;
    });

    await act(async () => {
      await hook.result.current.create(REQUEST);
    });
    expect(hook.result.current.state).toEqual({ status: "error", kind: "rate_limited" });
    act(() => {
      hook.result.current.clearError();
    });
    expect(hook.result.current.state).toEqual({ status: "idle" });
  });

  it("H-6 外した後に終わっても、状態を変えず警告も出ない", async () => {
    const errorSpy = vi.spyOn(console, "error").mockImplementation(() => {});
    const warnSpy = vi.spyOn(console, "warn").mockImplementation(() => {});
    const pending = deferred<SessionHandle>();
    const postSession = vi.fn<SessionCreationDeps["postSession"]>(() => pending.promise);
    const { hook, states } = setup({ postSession });

    let first!: Promise<SessionHandle | null>;
    await act(async () => {
      first = hook.result.current.create(REQUEST);
    });
    const count = states.length;
    hook.unmount();
    pending.resolve(SESSION);
    await first;

    expect(states.length).toBe(count);
    expect(errorSpy).not.toHaveBeenCalled();
    expect(warnSpy).not.toHaveBeenCalled();
  });
});
