import { describe, expect, it } from "vitest";

import { parseDtnCatalog, parseStageCatalog, STAGE_CATALOG } from "./stage-catalog.ts";
import catalogJson from "./stage-catalog.gen.json";

type Mutable = Record<string, unknown>;

// 正しい生成物の形（2 件）。呼ぶたびに新しく作る。
function validCatalog(): { format: unknown; stages: Mutable[] } & Mutable {
  return {
    format: 1,
    stages: [
      {
        id: "stage1",
        title: "はじめての郵便船",
        region: "瀬戸内（香川県）",
        islands: 3,
        ferries: 1,
        durationMs: 180000,
        unlock: null,
        intro: {
          text: "温暖で雨の少ない瀬戸内の海。",
          provenance: "derived",
          source: { dataset: "d", region: "r", field: "f", quote: "q" },
        },
        learning: ["log_replication", "commit"],
        demo: false,
      },
      {
        id: "demo-goto",
        title: "嵐の五島列島",
        region: "五島列島（長崎県）",
        islands: 3,
        ferries: 2,
        durationMs: 120000,
        unlock: "stage1",
        intro: { text: "紹介", provenance: "creative", source: null },
        learning: [],
        demo: true,
      },
    ],
  };
}

function stageAt(c: { stages: Mutable[] }, i: number): Mutable {
  const s = c.stages[i];
  if (s === undefined) throw new Error("no stage");
  return s;
}

function introOf(s: Mutable): Mutable {
  return s["intro"] as Mutable;
}

describe("CAT-1 コミットした生成物", () => {
  it("parseStageCatalog を通り、STAGE_CATALOG の件数・id の並びが生成物と同じ", () => {
    const parsed = parseStageCatalog(catalogJson);
    const ids = catalogJson.stages.map((s) => s.id);

    expect(parsed.map((s) => s.id)).toEqual(ids);
    expect(STAGE_CATALOG).toHaveLength(catalogJson.stages.length);
    expect(STAGE_CATALOG.map((s) => s.id)).toEqual(ids);
    // dtn（DTN-12）は StageSummary に入れない（ブラウザに返さない）
    const withoutDtn = catalogJson.stages.map((stage) => {
      const copy: Record<string, unknown> = { ...stage };
      delete copy["dtn"];
      return copy;
    });
    expect(STAGE_CATALOG).toEqual(withoutDtn);
  });

  it("正しい形の一覧は通る", () => {
    expect(parseStageCatalog(validCatalog())).toEqual(validCatalog().stages);
  });
});

describe("CAT-2 誤り", () => {
  const cases: readonly [string, () => unknown, string][] = [
    ["オブジェクトでない", () => [], "(最上位)"],
    ["format が 2", () => ({ ...validCatalog(), format: 2 }), "format"],
    ["stages が配列でない", () => ({ ...validCatalog(), stages: {} }), "stages"],
    ["最上位に知らないキー", () => ({ ...validCatalog(), extra: 1 }), "(最上位)"],
    [
      "要素に知らないキー",
      () => {
        const c = validCatalog();
        stageAt(c, 1)["extra"] = 1;
        return c;
      },
      "stages[1]",
    ],
    [
      "intro がない",
      () => {
        const c = validCatalog();
        delete stageAt(c, 0)["intro"];
        return c;
      },
      "stages[0]",
    ],
    [
      'provenance が "x"',
      () => {
        const c = validCatalog();
        introOf(stageAt(c, 1))["provenance"] = "x";
        return c;
      },
      "stages[1].intro.provenance",
    ],
    [
      "source のキーが足りない",
      () => {
        const c = validCatalog();
        introOf(stageAt(c, 0))["source"] = { dataset: "d", region: "r", field: "f" };
        return c;
      },
      "stages[0].intro.source",
    ],
    [
      "quote が数",
      () => {
        const c = validCatalog();
        (introOf(stageAt(c, 0))["source"] as Mutable)["quote"] = 1;
        return c;
      },
      "stages[0].intro.source.quote",
    ],
    [
      "learning が配列でない",
      () => {
        const c = validCatalog();
        stageAt(c, 0)["learning"] = "commit";
        return c;
      },
      "stages[0].learning",
    ],
    [
      "learning が数を含む",
      () => {
        const c = validCatalog();
        stageAt(c, 0)["learning"] = ["commit", 1];
        return c;
      },
      "stages[0].learning[1]",
    ],
    [
      'demo が "true"',
      () => {
        const c = validCatalog();
        stageAt(c, 1)["demo"] = "true";
        return c;
      },
      "stages[1].demo",
    ],
    [
      'id が "Stage1"',
      () => {
        const c = validCatalog();
        stageAt(c, 0)["id"] = "Stage1";
        return c;
      },
      "stages[0].id",
    ],
    [
      "id が 33 文字",
      () => {
        const c = validCatalog();
        stageAt(c, 0)["id"] = "a".repeat(33);
        return c;
      },
      "stages[0].id",
    ],
    [
      "id の重複",
      () => {
        const c = validCatalog();
        stageAt(c, 1)["id"] = "stage1";
        stageAt(c, 1)["unlock"] = null;
        return c;
      },
      "stages[1].id",
    ],
    [
      "islands が 0",
      () => {
        const c = validCatalog();
        stageAt(c, 0)["islands"] = 0;
        return c;
      },
      "stages[0].islands",
    ],
    [
      "durationMs が 1.5",
      () => {
        const c = validCatalog();
        stageAt(c, 0)["durationMs"] = 1.5;
        return c;
      },
      "stages[0].durationMs",
    ],
    [
      'unlock が ""',
      () => {
        const c = validCatalog();
        stageAt(c, 1)["unlock"] = "";
        return c;
      },
      "stages[1].unlock",
    ],
  ];

  it.each(cases)("%s → 場所を含む Error", (_name, make, where) => {
    expect(() => parseStageCatalog(make())).toThrow(where);
  });

  it("32 文字の id は通る", () => {
    const c = validCatalog();
    stageAt(c, 0)["id"] = "a".repeat(32);
    stageAt(c, 1)["unlock"] = null;
    expect(() => parseStageCatalog(c)).not.toThrow();
  });
});

describe("CAT-3 入力と記憶を共有しない", () => {
  it("入力を書き換えても結果が変わらない", () => {
    const input = validCatalog();
    const parsed = parseStageCatalog(input);
    const before = structuredClone(parsed);

    const s0 = stageAt(input, 0);
    expect(parsed[0]).not.toBe(s0);
    expect(parsed[0]?.intro).not.toBe(s0["intro"]);
    expect(parsed[0]?.learning).not.toBe(s0["learning"]);

    s0["title"] = "変えた";
    introOf(s0)["text"] = "変えた";
    (introOf(s0)["source"] as Mutable)["quote"] = "変えた";
    (s0["learning"] as string[]).push("変えた");
    input.stages.push({ ...s0, id: "stage9" });

    expect(parsed).toEqual(before);
  });
});

describe("CAT-DTN dtn の欄（DTN-12）", () => {
  it("コミットした生成物の dtn を読める。stage3 は欠航で対象外、ほかは対象", () => {
    const dtn = parseDtnCatalog(catalogJson);
    expect(dtn.get("stage1")?.actors).toEqual(["island:1", "island:2", "island:3", "ferry:f1"]);
    expect(dtn.get("stage3")?.unsupportedEvents).toEqual(["cancellation"]);
    expect(dtn.get("demo-izu")?.actors).toHaveLength(12);
    expect(dtn.get("demo-izu")?.unsupportedEvents).toEqual([]);
  });

  it("dtn がない・数が合わないなら誤り", () => {
    expect(() => parseDtnCatalog(validCatalog())).toThrow(/stages\[0\]\.dtn/);
    const c = validCatalog();
    for (const s of c.stages) {
      s["dtn"] = { actors: ["island:1"], eventKinds: [], unsupportedEvents: [] };
    }
    expect(() => parseDtnCatalog(c)).toThrow(/島・船の数と合わない/);
  });
});
