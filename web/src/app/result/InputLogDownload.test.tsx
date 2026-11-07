// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { SessionHandle } from "../types.ts";
import { InputLogDownload } from "./InputLogDownload.tsx";

const TOKEN = "Tok_en-abcdefghijklmno"; // 22 文字
const SESSION: SessionHandle = {
  sessionId: "s_abc.def",
  token: TOKEN,
  wsPath: "/ws/sessions/s_abc.def",
};
const LABEL = "入力ログをダウンロード（JSON）";
const BODY = '{"format":1,"build":"b1","stageId":"stage1","stageHash":"h","seed":7,"commands":[]}';

function okResponse(): Response {
  return new Response(BODY, {
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

const createObjectURL = vi.fn<(blob: Blob) => string>(() => "blob:input-log");
const revokeObjectURL = vi.fn<(url: string) => void>();
let clicked: { href: string; download: string }[] = [];

beforeEach(() => {
  Object.defineProperty(URL, "createObjectURL", { configurable: true, value: createObjectURL });
  Object.defineProperty(URL, "revokeObjectURL", { configurable: true, value: revokeObjectURL });
  clicked = [];
  vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (
    this: HTMLAnchorElement,
  ) {
    clicked.push({ href: this.getAttribute("href") ?? "", download: this.download });
  });
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  createObjectURL.mockClear();
  revokeObjectURL.mockClear();
  Reflect.deleteProperty(URL, "createObjectURL");
  Reflect.deleteProperty(URL, "revokeObjectURL");
});

describe("InputLogDownload", () => {
  it.each<[string, SessionHandle | null, boolean]>([
    ["session が null", null, true],
    ["available が false", SESSION, false],
    ["両方", null, false],
  ])("ID-1 %s → 何も描かない", (_name, session, available) => {
    const { container } = render(<InputLogDownload session={session} available={available} />);

    expect(container.innerHTML).toBe("");
  });

  it("説明の文を出し、トークンを画面に出さない", () => {
    const { container } = render(<InputLogDownload session={SESSION} available />);

    expect((screen.getByRole("button", { name: LABEL }) as HTMLButtonElement).disabled).toBe(false);
    expect(
      screen.getByText(
        "操作の記録（ステージ・シード・時刻付きの操作）です。開発者向けのツール（simcli）で同じゲームを再現できます。",
      ),
    ).toBeDefined();
    expect(container.innerHTML).not.toContain(TOKEN);
  });

  it("ID-2 押すと取得中は disabled、成功で createObjectURL・revokeObjectURL が 1 回ずつ", async () => {
    const { fetchFn, resolve } = deferredFetch();
    render(<InputLogDownload session={SESSION} available fetchFn={fetchFn} />);

    fireEvent.click(screen.getByRole("button", { name: LABEL }));
    const loading = screen.getByRole("button", { name: "取り出しています…" });
    expect((loading as HTMLButtonElement).disabled).toBe(true);
    fireEvent.click(loading); // 連打しても 1 回
    expect(fetchFn).toHaveBeenCalledTimes(1);

    await act(async () => {
      resolve(okResponse());
    });

    expect(
      ((await screen.findByRole("button", { name: LABEL })) as HTMLButtonElement).disabled,
    ).toBe(false);
    expect(createObjectURL).toHaveBeenCalledTimes(1);
    expect(await createObjectURL.mock.calls[0]?.[0].text()).toBe(BODY);
    expect(revokeObjectURL).toHaveBeenCalledTimes(1);
    expect(revokeObjectURL).toHaveBeenCalledWith("blob:input-log");
    expect(clicked).toEqual([{ href: "blob:input-log", download: "raft-game-stage1-7.json" }]);
    expect(screen.getByRole("status").textContent).toBe("");
  });

  it("ID-3 not_found → 期限が過ぎた可能性の文言", async () => {
    const fetchFn = vi.fn<typeof fetch>(
      async () => new Response('{"code":"not_found"}', { status: 404 }),
    );
    render(<InputLogDownload session={SESSION} available fetchFn={fetchFn} />);

    fireEvent.click(screen.getByRole("button", { name: LABEL }));

    expect(await screen.findByText(/セッションの期限が過ぎた可能性があります/)).toBeDefined();
    expect(screen.getByRole("status").textContent).toBe(
      "入力ログを取り出せませんでした（セッションの期限が過ぎた可能性があります）。",
    );
    expect(createObjectURL).not.toHaveBeenCalled();
  });

  it.each<[string, () => typeof fetch]>([
    [
      "network",
      () =>
        vi.fn<typeof fetch>(async () => {
          throw new TypeError("Failed to fetch");
        }),
    ],
    ["bad_response", () => vi.fn<typeof fetch>(async () => new Response("oops", { status: 200 }))],
  ])("%s → 時間をおいての文言", async (_name, makeFetch) => {
    render(<InputLogDownload session={SESSION} available fetchFn={makeFetch()} />);

    fireEvent.click(screen.getByRole("button", { name: LABEL }));

    expect(
      await screen.findByText(
        "入力ログを取り出せませんでした。時間をおいてもう一度お試しください。",
      ),
    ).toBeDefined();
    expect((screen.getByRole("button", { name: LABEL }) as HTMLButtonElement).disabled).toBe(false);
  });

  it("createObjectURL が無い環境 → 失敗の文言", async () => {
    Reflect.deleteProperty(URL, "createObjectURL");
    const fetchFn = vi.fn<typeof fetch>(async () => okResponse());
    render(<InputLogDownload session={SESSION} available fetchFn={fetchFn} />);

    fireEvent.click(screen.getByRole("button", { name: LABEL }));

    expect(
      await screen.findByText(
        "入力ログを取り出せませんでした。時間をおいてもう一度お試しください。",
      ),
    ).toBeDefined();
    expect(clicked).toEqual([]);
  });
});
