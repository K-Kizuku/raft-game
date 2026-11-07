import { describe, expect, it } from "vitest";

import { ISSUED_TOKEN, ISSUED_TOKEN_HASH, sequentialRandom } from "./test-helpers.ts";
import { createSessionToken, hashSessionToken, SESSION_TOKEN_PATTERN } from "./token.ts";

describe("T-1 hashSessionToken", () => {
  it("SHA-256 の 16 進の小文字（64 文字）", async () => {
    const hash = await hashSessionToken(ISSUED_TOKEN);
    expect(hash).toBe(ISSUED_TOKEN_HASH);
    expect(hash).toMatch(/^[0-9a-f]{64}$/);
  });

  it("トークンは乱数 16 バイトの base64url（22 文字）", () => {
    const random = sequentialRandom();
    random.randomBytes(16); // 1 回目（0x00〜0x0f）を捨てる
    expect(createSessionToken(random.randomBytes)).toBe(ISSUED_TOKEN);
    expect(random.lengths).toEqual([16, 16]);
  });
});

describe("T-2 SESSION_TOKEN_PATTERN", () => {
  it.each([
    [ISSUED_TOKEN, true],
    ["AAAAAAAAAAAAAAAAAAAA-_", true],
    ["A".repeat(21), false],
    ["A".repeat(23), false],
    [`${"A".repeat(21)}+`, false],
    [`${"A".repeat(21)}=`, false],
    ["", false],
  ])("%j → %s", (token, ok) => {
    expect(SESSION_TOKEN_PATTERN.test(token)).toBe(ok);
  });
});
