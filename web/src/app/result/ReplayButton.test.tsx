// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { MAX_INPUT_LOG_BYTES } from "../../replay/limits.ts";
import { takeQueuedReplayInput } from "../../replay/pending.ts";
import type { SessionHandle } from "../types.ts";
import { ReplayButton } from "./ReplayButton.tsx";

const TOKEN = "Tok_en-abcdefghijklmno"; // 22 文字
const SESSION: SessionHandle = {
  sessionId: "s_abc.def",
  token: TOKEN,
  wsPath: "/ws/sessions/s_abc.def",
};
const LABEL = "このラウンドをリプレイ";
const LOADING = "取り出しています…";
const NOT_FOUND = "入力ログを取り出せませんでした（セッションの期限が過ぎた可能性があります）。";
const FAILED = "入力ログを取り出せませんでした。時間をおいてもう一度お試しください。";
const BODY = '{"format":1,"build":"b1","stageId":"stage1","stageHash":"h","seed":7,"commands":[]}';

function okResponse(body: string = BODY): Response {
  return new Response(body, {
    status: 200,
    headers: { "content-disposition": 'attachment; filename="raft-game-stage1-7.json"' },
  });
}

// 呼ばれた順に解決できる fetch（取得中の表示を確かめる）。
function deferredFetch() {
  let resolve: (response: Response) => void = () => undefined;
  const fetchFn = vi.fn<typeof fetch>(
    () =>
      new Promise<Response>((r) => {
        resolve = r;
      }),
  );
  return { fetchFn, resolve: (response: Response) => resolve(response) };
}

let replaceState: ReturnType<typeof vi.spyOn>;
let pushState: ReturnType<typeof vi.spyOn>;

beforeEach(() => {
  window.history.replaceState(null, "", "/result");
  takeQueuedReplayInput();
  replaceState = vi.spyOn(window.history, "replaceState");
  pushState = vi.spyOn(window.history, "pushState");
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  takeQueuedReplayInput();
  window.sessionStorage.clear();
  window.localStorage.clear();
});

describe("ReplayButton", () => {
  it.each<[string, SessionHandle | null, boolean]>([
    ["session が null", null, true],
    ["available が false", SESSION, false],
    ["両方", null, false],
  ])("RB-1 %s → 何も描かない", (_name, session, available) => {
    const { container } = render(<ReplayButton session={session} available={available} />);

    expect(container.innerHTML).toBe("");
  });

  it("説明の文を出し、トークンを画面に出さない", () => {
    const { container } = render(<ReplayButton session={SESSION} available />);

    expect((screen.getByRole("button", { name: LABEL }) as HTMLButtonElement).disabled).toBe(false);
    expect(
      screen.getByText(
        "ダウンロードしなくても、この端末の中で同じゲームをもう一度見られます（初回はリプレイの部品を約 2〜3 MB 読み込みます）。",
      ),
    ).toBeDefined();
    expect(container.innerHTML).not.toContain(TOKEN);
  });

  it("RB-2 押すと入力ログを 1 回取得し、queueReplayInput に渡して /replay へ replace で移る", async () => {
    const log = vi.spyOn(console, "log");
    const warn = vi.spyOn(console, "warn");
    const error = vi.spyOn(console, "error");
    const fetchFn = vi.fn<typeof fetch>(async () => okResponse());
    render(<ReplayButton session={SESSION} available fetchFn={fetchFn} />);

    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: LABEL }));
    });

    expect(fetchFn).toHaveBeenCalledTimes(1);
    const [url, init] = fetchFn.mock.calls[0] ?? [];
    expect(url).toBe("/api/sessions/s_abc.def/input-log");
    expect(init?.method).toBe("GET");
    expect((init?.headers as Record<string, string>)["X-Session-Token"]).toBe(TOKEN);
    expect(window.location.pathname).toBe("/replay");
    expect(replaceState).toHaveBeenCalledWith(null, "", "/replay");
    expect(pushState).not.toHaveBeenCalled();
    expect(takeQueuedReplayInput()).toBe(BODY);
    // URL・Web Storage・console に入力ログを出さない（AC-4）。
    expect(window.location.href).not.toContain("format");
    expect(window.sessionStorage.length).toBe(0);
    expect(window.localStorage.length).toBe(0);
    for (const spy of [log, warn, error]) {
      for (const call of spy.mock.calls) {
        expect(JSON.stringify(call)).not.toContain(TOKEN);
        expect(JSON.stringify(call)).not.toContain("stageHash");
      }
    }
  });

  it("RB-3 404 → not_found の文言、ボタンが戻り、ルートは変わらない", async () => {
    const fetchFn = vi.fn<typeof fetch>(
      async () => new Response('{"code":"not_found"}', { status: 404 }),
    );
    render(<ReplayButton session={SESSION} available fetchFn={fetchFn} />);

    fireEvent.click(screen.getByRole("button", { name: LABEL }));

    expect(await screen.findByText(NOT_FOUND)).toBeDefined();
    expect(screen.getByRole("status").textContent).toBe(NOT_FOUND);
    expect((screen.getByRole("button", { name: LABEL }) as HTMLButtonElement).disabled).toBe(false);
    expect(window.location.pathname).toBe("/result");
    expect(takeQueuedReplayInput()).toBeNull();
  });

  it.each<[string, () => typeof fetch]>([
    [
      "例外（network）",
      () =>
        vi.fn<typeof fetch>(async () => {
          throw new TypeError("Failed to fetch");
        }),
    ],
    ["bad_response", () => vi.fn<typeof fetch>(async () => new Response("oops", { status: 200 }))],
    ["500", () => vi.fn<typeof fetch>(async () => new Response("", { status: 500 }))],
  ])("RB-3 %s → 時間をおいての文言、ボタンが戻る", async (_name, makeFetch) => {
    render(<ReplayButton session={SESSION} available fetchFn={makeFetch()} />);

    fireEvent.click(screen.getByRole("button", { name: LABEL }));

    expect(await screen.findByText(FAILED)).toBeDefined();
    expect((screen.getByRole("button", { name: LABEL }) as HTMLButtonElement).disabled).toBe(false);
    expect(window.location.pathname).toBe("/result");
    expect(takeQueuedReplayInput()).toBeNull();
  });

  it("RB-4 取得中は disabled で「取り出しています…」、2 回押しても要求は 1 回", async () => {
    const { fetchFn, resolve } = deferredFetch();
    render(<ReplayButton session={SESSION} available fetchFn={fetchFn} />);

    fireEvent.click(screen.getByRole("button", { name: LABEL }));
    const loading = screen.getByRole("button", { name: LOADING });
    expect((loading as HTMLButtonElement).disabled).toBe(true);
    fireEvent.click(loading);
    expect(fetchFn).toHaveBeenCalledTimes(1);

    await act(async () => {
      resolve(okResponse());
    });

    expect(fetchFn).toHaveBeenCalledTimes(1);
    expect(window.location.pathname).toBe("/replay");
  });

  it("RB-5 1 MiB を超える本文 → 失敗の文言で、ルートが変わらない", async () => {
    const pad = "x".repeat(MAX_INPUT_LOG_BYTES);
    const big = `{"format":1,"pad":"${pad}"}`;
    const fetchFn = vi.fn<typeof fetch>(async () => okResponse(big));
    render(<ReplayButton session={SESSION} available fetchFn={fetchFn} />);

    fireEvent.click(screen.getByRole("button", { name: LABEL }));

    expect(await screen.findByText(FAILED)).toBeDefined();
    expect((screen.getByRole("button", { name: LABEL }) as HTMLButtonElement).disabled).toBe(false);
    expect(window.location.pathname).toBe("/result");
    expect(replaceState).not.toHaveBeenCalled();
    expect(takeQueuedReplayInput()).toBeNull();
  });

  it("取得の途中で画面を離れたら、結果を捨てる（navigate しない・入れない）", async () => {
    const { fetchFn, resolve } = deferredFetch();
    const error = vi.spyOn(console, "error");
    const { unmount } = render(<ReplayButton session={SESSION} available fetchFn={fetchFn} />);

    fireEvent.click(screen.getByRole("button", { name: LABEL }));
    unmount();
    await act(async () => {
      resolve(okResponse());
    });

    expect(window.location.pathname).toBe("/result");
    expect(replaceState).not.toHaveBeenCalled();
    expect(takeQueuedReplayInput()).toBeNull();
    expect(error).not.toHaveBeenCalled();
  });
});
