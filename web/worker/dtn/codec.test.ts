// 正準の JSON・digest のテスト（DTN-12）。DTN-02 の fixture（server/internal/dtn/protocol/testdata）を
// リポジトリから読み、Go の dtnproto と同じ結果になることを確かめる（actor-protocol.md §7.4）。

import { describe, expect, it } from "vitest";

import { canonicalJson, decodeBase64, digestOf, encodeBase64, parseStrictJson } from "./codec.ts";
import { parseEnvelope, requestDigestBody } from "./protocol.ts";

interface NodeFs {
  readdirSync(path: string): string[];
  readFileSync(path: string, encoding: "utf8"): string;
}

async function load<T>(sub: string): Promise<[string, T][]> {
  const spec: string = "node:fs";
  const fs = (await import(/* @vite-ignore */ spec)) as NodeFs;
  const dir = new URL(`../../../server/internal/dtn/protocol/testdata/${sub}/`, import.meta.url);
  return fs
    .readdirSync(dir.pathname)
    .filter((name) => name.endsWith(".json"))
    .sort()
    .map((name) => [name, JSON.parse(fs.readFileSync(new URL(name, dir).pathname, "utf8")) as T]);
}

interface CanonicalCase {
  readonly input: string;
  readonly canonical?: string;
  readonly digest?: string;
  readonly error?: string;
}

interface RequestCase {
  readonly request?: unknown;
  readonly requestText?: string;
  readonly expect: string;
  readonly digest?: string;
}

const CANONICAL = await load<CanonicalCase>("canonical");
const REQUESTS = (await load<RequestCase>("requests")).filter(([, c]) => c.digest !== undefined);

describe("正準の JSON（DTN-02 の fixture）", () => {
  it("fixture を読める", () => {
    expect(CANONICAL.length).toBeGreaterThan(10);
  });

  it.each(CANONICAL)("%s", async (_name, c) => {
    if (c.error !== undefined) {
      expect(() => canonicalJson(parseStrictJson(c.input))).toThrow();
      return;
    }
    const value = parseStrictJson(c.input);
    expect(canonicalJson(value)).toBe(c.canonical);
    expect(await digestOf(value)).toBe(c.digest);
  });
});

describe("要求の digest（DTN-02 の fixture の ok の要求）", () => {
  it("fixture を読める", () => {
    expect(REQUESTS.length).toBeGreaterThan(5);
  });

  it.each(REQUESTS)("%s", async (_name, c) => {
    const raw = c.requestText === undefined ? c.request : parseStrictJson(c.requestText);
    expect(await digestOf(requestDigestBody(parseEnvelope(raw)))).toBe(c.digest);
  });
});

describe("base64", () => {
  it("パディングありの形だけを受ける", () => {
    expect(encodeBase64(new Uint8Array([1, 2, 3, 4]))).toBe("AQIDBA==");
    expect(decodeBase64("AQIDBA==")).toEqual(new Uint8Array([1, 2, 3, 4]));
    expect(decodeBase64("AQIDBA")).toBeNull();
    expect(decodeBase64("AQID\nBA==")).toBeNull();
    expect(decodeBase64("AQIDBB==")).toBeNull(); // 余りのビットが 0 でない
  });
});
