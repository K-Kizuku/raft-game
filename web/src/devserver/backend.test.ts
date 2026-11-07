import { describe, expect, it } from "vitest";
import { resolveDevBackend } from "./backend.ts";

const API_KEY = "^/api(?:[/?]|$)";
const WS_KEY = "^/ws(?:[/?]|$)";
const dev = { preview: false };

function standalone(port: string) {
  const backend = resolveDevBackend({ DEV_BACKEND: "standalone", DEV_SERVER_PORT: port }, dev);
  if (backend.kind !== "standalone")
    throw new Error(`standalone ではない: ${JSON.stringify(backend)}`);
  return backend;
}

describe("resolveDevBackend", () => {
  it("DB-1: 未設定・空・worker は worker（DEV_SERVER_PORT は見ない）", () => {
    expect(resolveDevBackend({}, dev)).toEqual({ kind: "worker" });
    expect(resolveDevBackend({ DEV_BACKEND: "" }, dev)).toEqual({ kind: "worker" });
    expect(resolveDevBackend({ DEV_BACKEND: "worker", DEV_SERVER_PORT: "abc" }, dev)).toEqual({
      kind: "worker",
    });
  });

  it("DB-2: standalone は Go の 127.0.0.1:<DEV_SERVER_PORT> に /api・/ws を中継する", () => {
    const backend = standalone("18110");
    expect(backend.target).toBe("http://127.0.0.1:18110");
    expect(Object.keys(backend.proxy).sort()).toEqual([API_KEY, WS_KEY].sort());
    expect(backend.proxy[API_KEY]).toEqual({
      target: "http://127.0.0.1:18110",
      ws: false,
      changeOrigin: false,
    });
    expect(backend.proxy[WS_KEY]).toEqual({
      target: "http://127.0.0.1:18110",
      ws: true,
      changeOrigin: false,
    });
  });

  it("DB-3: キーの正規表現は /api・/ws とその下だけに当たる", () => {
    const keys = Object.keys(standalone("18110").proxy);
    const matches = (path: string) => keys.some((k) => new RegExp(k).test(path));
    for (const path of ["/api/healthz", "/api?x=1", "/api", "/ws/sessions/s_1?token=t", "/ws"]) {
      expect(matches(path), path).toBe(true);
    }
    for (const path of ["/apix", "/wsx", "/", "/assets/ws.js"]) {
      expect(matches(path), path).toBe(false);
    }
    expect(new RegExp(API_KEY).test("/ws")).toBe(false);
    expect(new RegExp(WS_KEY).test("/api")).toBe(false);
  });

  it("DB-4: DEV_SERVER_PORT の誤りは Error、1 と 65535 は通る", () => {
    expect(() => resolveDevBackend({ DEV_BACKEND: "standalone" }, dev)).toThrow(/DEV_SERVER_PORT/);
    for (const port of ["", "0", "080", "65536", "1.5", "abc"]) {
      expect(() => standalone(port), port).toThrow(/DEV_SERVER_PORT/);
    }
    expect(standalone("1").target).toBe("http://127.0.0.1:1");
    expect(standalone("65535").target).toBe("http://127.0.0.1:65535");
  });

  it("DB-5: DEV_BACKEND の不正な値は Error（大文字・小文字を区別する）", () => {
    for (const value of ["Standalone", "go", " standalone"]) {
      expect(
        () => resolveDevBackend({ DEV_BACKEND: value, DEV_SERVER_PORT: "18110" }, dev),
        value,
      ).toThrow(/DEV_BACKEND/);
    }
  });

  it("DB-6: vite preview では standalone は Error、worker は通る", () => {
    expect(() =>
      resolveDevBackend({ DEV_BACKEND: "standalone", DEV_SERVER_PORT: "18110" }, { preview: true }),
    ).toThrow(Error);
    expect(resolveDevBackend({ DEV_BACKEND: "worker" }, { preview: true })).toEqual({
      kind: "worker",
    });
  });

  it("DB-7: 渡した env を変えない", () => {
    const env: Record<string, string | undefined> = {
      DEV_BACKEND: "standalone",
      DEV_SERVER_PORT: "18110",
    };
    const before = { ...env };
    resolveDevBackend(env, dev);
    expect(env).toEqual(before);
    const bad: Record<string, string | undefined> = {
      DEV_BACKEND: "standalone",
      DEV_SERVER_PORT: "0",
    };
    expect(() => resolveDevBackend(bad, dev)).toThrow();
    expect(bad).toEqual({ DEV_BACKEND: "standalone", DEV_SERVER_PORT: "0" });
  });
});
