import { describe, expect, it, vi } from "vitest";

import { fetchStages, StagesLoadError } from "./stages.ts";

const derivedIntro = {
  text: "温暖で雨の少ない瀬戸内の海。",
  provenance: "derived",
  source: {
    dataset: "mlit-ritou-2026",
    region: "小豆島地域",
    field: "NATURAL_CLIMATE_FEATURES",
    quote: "温暖、小雨の典型的な瀬戸内式気候",
  },
};
const creativeIntro = { text: "島から返事が来ない海。", provenance: "creative", source: null };
const stage1 = {
  id: "stage1",
  title: "はじめての郵便船",
  region: "瀬戸内",
  islands: 3,
  ferries: 1,
  durationMs: 180000,
  demo: false,
  intro: derivedIntro,
  learning: ["log_replication", "commit"],
  unlock: null,
};
const stage2 = {
  id: "stage2",
  title: "島から返事が来ない！",
  region: "伊豆諸島",
  islands: 5,
  ferries: 2,
  durationMs: 210000,
  demo: false,
  intro: creativeIntro,
  learning: [],
  unlock: "stage1",
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
          { ...stage2, extra: 1 },
          { ...stage1, extra: { after: "stage1" } },
        ],
        version: 1,
      }),
    );

    const stages = await fetchStages({ fetch: fetchMock });

    expect(stages).toEqual([stage2, stage1]);
    expect(stages[0]).not.toHaveProperty("extra");
    expect(stages[1]).not.toHaveProperty("extra");
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

  it("L-1 demo が true・false の要素を読む", async () => {
    const demoStage = { ...stage2, id: "demo-izu", demo: true };
    const stages = await fetchStages({
      fetch: fakeFetch(() => jsonResponse({ stages: [stage1, demoStage] })),
    });
    expect(stages).toEqual([stage1, demoStage]);
    expect(stages[0]?.demo).toBe(false);
    expect(stages[1]?.demo).toBe(true);
  });

  it.each<[string, unknown]>([
    ["demo が無い", { ...stage2, demo: undefined }],
    ["demo が文字列の true", { ...stage2, demo: "true" }],
    ["demo が 1", { ...stage2, demo: 1 }],
  ])("L-1 %s 要素が 1 つでもあれば失敗", async (_label, broken) => {
    await expect(
      fetchStages({ fetch: fakeFetch(() => jsonResponse({ stages: [stage1, broken] })) }),
    ).rejects.toBeInstanceOf(StagesLoadError);
  });

  // T-416 の intro・learning。
  async function loadOne(stage: unknown) {
    const stages = await fetchStages({
      fetch: fakeFetch(() => jsonResponse({ stages: [stage] })),
    });
    expect(stages).toHaveLength(1);
    return stages[0]!;
  }

  it("ST-1 intro（derived・引用つき）と learning を読み、intro の知らないキーを落とす", async () => {
    const stage = await loadOne({
      ...stage1,
      intro: { ...derivedIntro, extra: 1, source: { ...derivedIntro.source, extra: 2 } },
      learning: ["log_replication", "commit"],
    });

    expect(stage.intro).toEqual(derivedIntro);
    expect(stage.intro).not.toHaveProperty("extra");
    expect(stage.intro.source).not.toHaveProperty("extra");
    expect(stage.learning).toEqual(["log_replication", "commit"]);
  });

  it("ST-1 text・quote は前後の空白も変えない", async () => {
    const intro = {
      ...derivedIntro,
      text: "  前後に空白 ",
      source: { ...derivedIntro.source, quote: " 引用 " },
    };
    const stage = await loadOne({ ...stage1, intro });

    expect(stage.intro.text).toBe("  前後に空白 ");
    expect(stage.intro.source?.quote).toBe(" 引用 ");
  });

  it.each<[string, unknown]>([
    ["source が null", { ...creativeIntro, source: null }],
    ["quote が null", { ...derivedIntro, source: { ...derivedIntro.source, quote: null } }],
    ["fact", { ...derivedIntro, provenance: "fact" }],
  ])("ST-2 %s はそのまま読む", async (_label, intro) => {
    const stage = await loadOne({ ...stage1, intro });

    expect(stage.intro).toEqual(intro);
  });

  it("ST-3 learning の重複は最初の 1 つだけ残す", async () => {
    const stage = await loadOne({ ...stage1, learning: ["quorum", "quorum", "commit"] });

    expect(stage.learning).toEqual(["quorum", "commit"]);
  });

  it.each<[string, unknown]>([
    ["intro が無い", { ...stage1, intro: undefined }],
    ["intro が文字列", { ...stage1, intro: "紹介文" }],
    ["intro が null", { ...stage1, intro: null }],
    ["text が空白だけ", { ...stage1, intro: { ...derivedIntro, text: "  " } }],
    ["text が数", { ...stage1, intro: { ...derivedIntro, text: 1 } }],
    ["provenance が知らない値", { ...stage1, intro: { ...derivedIntro, provenance: "x" } }],
    ["source が無い", { ...stage1, intro: { text: "x", provenance: "creative" } }],
    [
      "source に field が無い",
      {
        ...stage1,
        intro: { ...derivedIntro, source: { ...derivedIntro.source, field: undefined } },
      },
    ],
    [
      "source.dataset が空",
      { ...stage1, intro: { ...derivedIntro, source: { ...derivedIntro.source, dataset: "" } } },
    ],
    [
      "quote が数",
      { ...stage1, intro: { ...derivedIntro, source: { ...derivedIntro.source, quote: 1 } } },
    ],
    ["learning が無い", { ...stage1, learning: undefined }],
    ["learning が文字列", { ...stage1, learning: "commit" }],
    ["learning に Bad-Key", { ...stage1, learning: ["commit", "Bad-Key"] }],
    ["learning に数", { ...stage1, learning: ["commit", 1] }],
  ])("ST-4 %s → 一覧全体が StagesLoadError", async (_label, broken) => {
    await expect(
      fetchStages({ fetch: fakeFetch(() => jsonResponse({ stages: [stage2, broken] })) }),
    ).rejects.toBeInstanceOf(StagesLoadError);
  });

  // T-703 の unlock。
  it.each<[string, string | null]>([
    ["null", null],
    ["ほかのステージの id", "stage1"],
    ["一覧に無いステージの id", "demo-izu"],
  ])("ST-U1 unlock が %s なら読める", async (_label, unlock) => {
    const stage = await loadOne({ ...stage2, unlock });

    expect(stage.unlock).toBe(unlock);
  });

  it.each<[string, unknown]>([
    ["キーが無い", Object.fromEntries(Object.entries(stage2).filter(([k]) => k !== "unlock"))],
    ["undefined", { ...stage2, unlock: undefined }],
    ["数", { ...stage2, unlock: 1 }],
    ["オブジェクト", { ...stage2, unlock: { after: "stage1" } }],
    ["形の合わない文字列", { ...stage2, unlock: "Bad" }],
    ["空の文字列", { ...stage2, unlock: "" }],
    ["自分の id", { ...stage2, unlock: "stage2" }],
  ])("ST-U1 unlock が %s → 一覧全体が StagesLoadError", async (_label, broken) => {
    await expect(
      fetchStages({ fetch: fakeFetch(() => jsonResponse({ stages: [stage1, broken] })) }),
    ).rejects.toBeInstanceOf(StagesLoadError);
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
