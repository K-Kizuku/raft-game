import { describe, expect, it } from "vitest";

import { checkAdminAuth } from "./auth.ts";

const TOKEN_32 = "0123456789abcdef0123456789ABCDEF";
const TOKEN_256 = "x".repeat(255) + "!";

function requestWith(authorization?: string): Request {
  const headers: Record<string, string> = {};
  if (authorization !== undefined) headers["authorization"] = authorization;
  return new Request("https://raft.example/api/admin/guards", { headers });
}

describe("U-1 checkAdminAuth", () => {
  const disabledCases: readonly [string, unknown][] = [
    ["未設定", undefined],
    ['""', ""],
    ["31 文字", TOKEN_32.slice(1)],
    ["257 文字", TOKEN_256 + "x"],
    ["空白を含む", TOKEN_32.slice(0, 16) + " " + TOKEN_32.slice(16)],
    ["末尾に改行", TOKEN_32 + "\n"],
    ["文字列でない", 1234],
  ];

  it.each(disabledCases)("ADMIN_TOKEN が %s → disabled", async (_name, token) => {
    const env = token === undefined ? {} : { ADMIN_TOKEN: token };
    expect(await checkAdminAuth(requestWith(`Bearer ${String(token)}`), env)).toBe("disabled");
  });

  describe.each([
    ["32 文字", TOKEN_32],
    ["256 文字", TOKEN_256],
  ])("正しい %s のトークン", (_label, token) => {
    const env = { ADMIN_TOKEN: token };

    const unauthorizedCases: readonly [string, string | undefined][] = [
      ["Authorization がない", undefined],
      ["Basic", `Basic ${token}`],
      ["Bearer だけ", "Bearer"],
      ["Bearer と空白", "Bearer "],
      ["空白が 2 つ", `Bearer  ${token}`],
      ["違うトークン", `Bearer ${token.slice(0, -1)}#`],
      ["前方一致", `Bearer ${token.slice(0, -1)}`],
      ["後ろに 1 文字", `Bearer ${token}x`],
      ["タブ区切り", `Bearer\t${token}`],
    ];

    it.each(unauthorizedCases)("%s → unauthorized", async (_name, header) => {
      expect(await checkAdminAuth(requestWith(header), env)).toBe("unauthorized");
    });

    it.each(["Bearer", "bearer", "BEARER"])("%s <tok> → ok", async (scheme) => {
      expect(await checkAdminAuth(requestWith(`${scheme} ${token}`), env)).toBe("ok");
    });
  });
});
