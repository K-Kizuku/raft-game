import { describe, expect, it, vi } from "vitest";

import { fetchStages, StagesLoadError } from "./stages.ts";

const stage1 = {
  id: "stage1",
  title: "はじめての郵便船",
  region: "瀬戸内",
  islands: 3,
  ferries: 1,
  durationMs: 180000,
};
const stage2 = {
  id: "stage2",
  title: "島から返事が来ない！",
  region: "伊豆諸島",
  islands: 5,
  ferries: 2,
  durationMs: 210000,
};

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function fakeFetch(respond: () => Response | Promise<Response>) {
  return vi.fn<typeof fetch>(async () => respond());
}

describe("fetchStages", () => {
  it("ST-1 正しい本文を順序どおりに返し、知らないフィールドを落とす", async () => {
    const fetchMock = fakeFetch(() =>
      jsonResponse({
        stages: [
          { ...stage2, unlock: { after: "stage1" }, extra: 1 },
          { ...stage1, unlock: null },
        ],
        version: 1,
      }),
    );

    const stages = await fetchStages({ fetch: fetchMock });

    expect(stages).toEqual([stage2, stage1]);
    expect(stages[0]).not.toHaveProperty("unlock");
    expect(stages[0]).not.toHaveProperty("extra");
    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [input, init] = fetchMock.mock.calls[0] ?? [];
    expect(input).toBe("/api/stages");
    expect(new Headers(init?.headers).get("Accept")).toBe("application/json");
  });

  it("ST-1 0 件は空の配列", async () => {
    await expect(
      fetchStages({ fetch: fakeFetch(() => jsonResponse({ stages: [] })) }),
    ).resolves.toEqual([]);
  });

  // ST-2
  const bodies: ReadonlyArray<[string, unknown]> = [
    ["null", null],
    ["配列", []],
    ["空のオブジェクト", {}],
    ["stages がオブジェクト", { stages: {} }],
    ["要素が null", { stages: [null] }],
    ["id が大文字", { stages: [{ ...stage1, id: "Stage1" }] }],
    ["id が無い", { stages: [{ ...stage1, id: undefined }] }],
    ["title が空", { stages: [{ ...stage1, title: "" }] }],
    ["title が数値", { stages: [{ ...stage1, title: 1 }] }],
    ["region が空", { stages: [{ ...stage1, region: "" }] }],
    ["islands が 0", { stages: [{ ...stage1, islands: 0 }] }],
    ["islands が 1.5", { stages: [{ ...stage1, islands: 1.5 }] }],
    ["islands が文字列", { stages: [{ ...stage1, islands: "3" }] }],
    ["ferries が 0", { stages: [{ ...stage1, ferries: 0 }] }],
    ["durationMs が負", { stages: [{ ...stage1, durationMs: -1 }] }],
    ["1 件だけ壊れている", { stages: [stage1, { ...stage2, islands: 0 }] }],
    ["id の重複", { stages: [stage1, { ...stage2, id: "stage1" }] }],
  ];
  const failures: ReadonlyArray<[string, () => Response | Promise<Response>]> = [
    [
      "fetch が拒否",
      () => {
        throw new TypeError("Failed to fetch");
      },
    ],
    ["404", () => jsonResponse({ error: "not_found" }, 404)],
    ["500", () => jsonResponse({ stages: [stage1] }, 500)],
    ["壊れた JSON", () => new Response("{stages:", { status: 200 })],
    ...bodies.map(([label, body]): [string, () => Response] => [label, () => jsonResponse(body)]),
  ];
  it.each(failures)("ST-2 %s → StagesLoadError", async (_label, respond) => {
    await expect(fetchStages({ fetch: fakeFetch(respond) })).rejects.toBeInstanceOf(
      StagesLoadError,
    );
  });

  it("ST-3 signal を中断すると AbortError で拒否する", async () => {
    const controller = new AbortController();
    const fetchMock = vi.fn<typeof fetch>(
      (_input, init) =>
        new Promise<Response>((_resolve, reject) => {
          init?.signal?.addEventListener("abort", () => {
            reject(new DOMException("aborted", "AbortError"));
          });
        }),
    );

    const pending = fetchStages({ fetch: fetchMock, signal: controller.signal });
    controller.abort();

    const error: unknown = await pending.catch((e: unknown) => e);
    expect(error).toBeInstanceOf(DOMException);
    expect(error).not.toBeInstanceOf(StagesLoadError);
    expect((error as DOMException).name).toBe("AbortError");
  });

  it("ST-3 本文を読んだ後に中断されたときも AbortError で拒否する", async () => {
    const controller = new AbortController();
    const fetchMock = fakeFetch(() => {
      controller.abort();
      return jsonResponse({ stages: [stage1] });
    });

    const error: unknown = await fetchStages({ fetch: fetchMock, signal: controller.signal }).catch(
      (e: unknown) => e,
    );
    expect(error).toBeInstanceOf(DOMException);
    expect((error as DOMException).name).toBe("AbortError");
  });
});
