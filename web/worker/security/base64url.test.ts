import { describe, expect, it } from "vitest";

import { decodeBase64Url, encodeBase64Url } from "./base64url.ts";

const VECTORS: readonly (readonly [readonly number[], string])[] = [
  [[], ""],
  [[0x66], "Zg"],
  [[0x66, 0x6f], "Zm8"],
  [[0x66, 0x6f, 0x6f], "Zm9v"],
  [[0x66, 0x6f, 0x6f, 0x62], "Zm9vYg"],
  [[0xfb, 0xff], "-_8"],
  [[0x00, 0x01, 0x02], "AAEC"],
];

describe("B-1 符号化", () => {
  it.each(VECTORS)("%j → %s", (bytes, text) => {
    const encoded = encodeBase64Url(Uint8Array.from(bytes));
    expect(encoded).toBe(text);
    expect(encoded).not.toContain("=");
  });

  it("0xfb 0xff は - と _ を含む", () => {
    expect(encodeBase64Url(Uint8Array.of(0xfb, 0xff))).toMatch(/^[-_]+8$/);
  });
});

describe("B-2 厳密な復号", () => {
  it.each(VECTORS)("%j ↔ %s は往復できる", (bytes, text) => {
    const decoded = decodeBase64Url(text);
    expect(decoded).not.toBeNull();
    expect([...(decoded ?? [])]).toEqual(bytes);
    expect(encodeBase64Url(decoded ?? new Uint8Array())).toBe(text);
  });

  it.each([
    ["+ を含む", "+_8"],
    ["/ を含む", "-/8"],
    ["= を含む", "Zg=="],
    ["= を 1 つ", "Zm8="],
    ["空白を含む", "Zm 9v"],
    ["改行を含む", "Zm9v\n"],
    ["長さが 4 で割って 1 余る", "Zm9vY"],
    ["1 文字", "A"],
    ["余りのビットが 0 でない（AR）", "AR"],
    ["余りのビットが 0 でない（Zm9）", "Zm9"],
    ["base64url 以外の文字", "Zm9*"],
  ])("%s → null", (_name, text) => {
    expect(decodeBase64Url(text)).toBeNull();
  });

  it("AQ は 0x01 になる", () => {
    expect([...(decodeBase64Url("AQ") ?? [])]).toEqual([0x01]);
  });
});
