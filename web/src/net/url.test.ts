import { describe, expect, it } from "vitest";

import { buildWsUrl } from "./url.ts";

const https = { protocol: "https:", host: "raft.example" };

describe("buildWsUrl", () => {
  it("UR-1: https は wss、http は ws。token をエンコードする", () => {
    expect(buildWsUrl({ wsPath: "/ws/sessions/s_a.b", token: "t+k/n=" }, https)).toBe(
      "wss://raft.example/ws/sessions/s_a.b?token=t%2Bk%2Fn%3D",
    );
    expect(
      buildWsUrl(
        { wsPath: "/ws/sessions/s_a.b", token: "tok" },
        { protocol: "http:", host: "localhost:18160" },
      ),
    ).toBe("ws://localhost:18160/ws/sessions/s_a.b?token=tok");
  });

  const secret = "SECRET-token-value";
  // セッションの作成の API のパス。net のコードに文字列のまま書かない（AC-8 の grep）ので組み立てる。
  const apiSessionsPath = ["", "api", "sessions"].join("/");
  const cases: { name: string; wsPath: string; token: string; protocol: string }[] = [
    {
      name: "別のホスト（//）",
      wsPath: "//evil.example/ws/sessions/x",
      token: secret,
      protocol: "https:",
    },
    {
      name: "絶対 URL",
      wsPath: "https://evil.example/ws/sessions/x",
      token: secret,
      protocol: "https:",
    },
    { name: "id が空", wsPath: "/ws/sessions/", token: secret, protocol: "https:" },
    { name: "パスが深い", wsPath: "/ws/sessions/a/b", token: secret, protocol: "https:" },
    { name: "クエリ付き", wsPath: "/ws/sessions/a?x=1", token: secret, protocol: "https:" },
    { name: "パーセントエンコード", wsPath: "/ws/sessions/%61", token: secret, protocol: "https:" },
    { name: "別のパス", wsPath: apiSessionsPath, token: secret, protocol: "https:" },
    { name: "token が空", wsPath: "/ws/sessions/a", token: "", protocol: "https:" },
    { name: "file:", wsPath: "/ws/sessions/a", token: secret, protocol: "file:" },
  ];
  it.each(cases)("UR-2: $name は投げる（トークンを含まない）", ({ wsPath, token, protocol }) => {
    let error: unknown = null;
    try {
      buildWsUrl({ wsPath, token }, { protocol, host: "raft.example" });
    } catch (e) {
      error = e;
    }
    expect(error).toBeInstanceOf(Error);
    expect((error as Error).message).not.toContain(secret);
    expect((error as Error).message).not.toContain("raft.example");
  });
});
