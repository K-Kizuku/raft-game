// web/scripts/deploy-secrets.ts のテスト（T-603 / #67、D-1〜D-10）。
// 値はすべて作り物（"p".repeat(32) など。Turnstile は Cloudflare のテスト用のキー）。
import { existsSync, mkdtempSync, readFileSync, rmSync, statSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { buildSecrets, main, type DeployMode } from "./deploy-secrets.ts";

type Env = Record<string, string | undefined>;

const PROD_KEY = "p".repeat(32);
const DEMO_KEY = "d".repeat(40);
const ADMIN = "a".repeat(64);
const TURNSTILE = "1x0000000000000000000000000000000AA";

function fullEnv(): Env {
  return {
    CF_SECRET_SESSION_SIGNING_KEY_PRODUCTION: PROD_KEY,
    CF_SECRET_SESSION_SIGNING_KEY_DEMO: DEMO_KEY,
    CF_SECRET_ADMIN_TOKEN: ADMIN,
    CF_SECRET_TURNSTILE_SECRET: TURNSTILE,
  };
}

function errorsOf(mode: DeployMode, env: Env): string[] {
  const r = buildSecrets(mode, env);
  return r.ok ? [] : r.errors;
}

const REQUIRED: Record<DeployMode, string[]> = {
  production: [
    "CF_SECRET_SESSION_SIGNING_KEY_PRODUCTION",
    "CF_SECRET_TURNSTILE_SECRET",
    "CF_SECRET_ADMIN_TOKEN",
  ],
  demo: ["CF_SECRET_SESSION_SIGNING_KEY_DEMO", "CF_SECRET_TURNSTILE_SECRET"],
};

/** process.stdout.write・process.stderr.write を差し替えて集める */
function capture(): { out: () => string; err: () => string; restore: () => void } {
  let out = "";
  let err = "";
  const o = vi.spyOn(process.stdout, "write").mockImplementation((s) => {
    out += String(s);
    return true;
  });
  const e = vi.spyOn(process.stderr, "write").mockImplementation((s) => {
    err += String(s);
    return true;
  });
  return {
    out: () => out,
    err: () => err,
    restore: () => {
      o.mockRestore();
      e.mockRestore();
    },
  };
}

describe("buildSecrets", () => {
  it("D-1: production は 3 つのキーを持ち、署名の鍵は _PRODUCTION の値", () => {
    const r = buildSecrets("production", fullEnv());
    expect(r.ok).toBe(true);
    if (!r.ok) return;
    expect(Object.keys(r.secrets)).toEqual([
      "SESSION_SIGNING_KEY",
      "TURNSTILE_SECRET",
      "ADMIN_TOKEN",
    ]);
    expect(r.secrets).toEqual({
      SESSION_SIGNING_KEY: PROD_KEY,
      TURNSTILE_SECRET: TURNSTILE,
      ADMIN_TOKEN: ADMIN,
    });
  });

  it("D-2: demo は 2 つのキーを持ち、ADMIN_TOKEN を出さない", () => {
    const r = buildSecrets("demo", fullEnv());
    expect(r.ok).toBe(true);
    if (!r.ok) return;
    expect(Object.keys(r.secrets)).toEqual(["SESSION_SIGNING_KEY", "TURNSTILE_SECRET"]);
    expect(r.secrets["SESSION_SIGNING_KEY"]).toBe(DEMO_KEY);
    expect(JSON.stringify(r.secrets)).not.toContain(ADMIN);
  });

  it("D-2: demo では CF_SECRET_ADMIN_TOKEN がなくても誤りにしない", () => {
    const env = fullEnv();
    delete env["CF_SECRET_ADMIN_TOKEN"];
    expect(buildSecrets("demo", env).ok).toBe(true);
  });

  describe("D-3: 未設定・空", () => {
    const cases = (["production", "demo"] as const).flatMap((mode) =>
      REQUIRED[mode].flatMap((name) => [
        [mode, name, "未設定"] as const,
        [mode, name, "空"] as const,
      ]),
    );
    it.each(cases)("%s で %s が%s → 誤りが 1 つ", (mode, name, kind) => {
      const env = fullEnv();
      if (kind === "未設定") delete env[name];
      else env[name] = "";
      const errors = errorsOf(mode, env);
      expect(errors).toHaveLength(1);
      expect(errors[0]).toContain(name);
      expect(errors[0]).toContain("設定されていない");
      for (const v of [PROD_KEY, DEMO_KEY, ADMIN, TURNSTILE]) {
        expect(errors[0]).not.toContain(v);
      }
    });

    it("誤りの文に GitHub の Secret の名前が出る", () => {
      const env = fullEnv();
      delete env["CF_SECRET_SESSION_SIGNING_KEY_DEMO"];
      expect(errorsOf("demo", env)[0]).toContain("DEMO_SESSION_SIGNING_KEY");
    });
  });

  describe("D-4: 長さと文字", () => {
    const keyVars = ["CF_SECRET_SESSION_SIGNING_KEY_PRODUCTION", "CF_SECRET_ADMIN_TOKEN"];
    const bad: [string, string][] = [
      ["31 文字", "k".repeat(31)],
      ["257 文字", "k".repeat(257)],
      ["空白を含む", `${"k".repeat(20)} ${"k".repeat(20)}`],
      ["非 ASCII を含む", `${"k".repeat(32)}あ`],
    ];
    it.each(keyVars.flatMap((v) => bad.map(([n, value]) => [v, n, value] as const)))(
      "%s が %s → 誤り",
      (name, _n, value) => {
        const errors = errorsOf("production", { ...fullEnv(), [name]: value });
        expect(errors).toHaveLength(1);
        expect(errors[0]?.startsWith(`${name}: `)).toBe(true);
        expect(errors[0]).toContain("に合わない");
        expect(errors[0]).not.toContain(value);
      },
    );
    it.each(keyVars.flatMap((v) => [[v, 32] as const, [v, 256] as const]))(
      "%s が %d 文字 → 通る",
      (name, n) => {
        expect(errorsOf("production", { ...fullEnv(), [name]: "k".repeat(n) })).toEqual([]);
      },
    );
    it("TURNSTILE_SECRET は 1 文字で通り、257 文字で誤り", () => {
      expect(errorsOf("production", { ...fullEnv(), CF_SECRET_TURNSTILE_SECRET: "t" })).toEqual([]);
      const errors = errorsOf("production", {
        ...fullEnv(),
        CF_SECRET_TURNSTILE_SECRET: "t".repeat(257),
      });
      expect(errors).toHaveLength(1);
      expect(errors[0]?.startsWith("CF_SECRET_TURNSTILE_SECRET: ")).toBe(true);
    });
    it("demo の署名の鍵も同じ規則", () => {
      const errors = errorsOf("demo", {
        ...fullEnv(),
        CF_SECRET_SESSION_SIGNING_KEY_DEMO: "k".repeat(31),
      });
      expect(errors).toHaveLength(1);
      expect(errors[0]?.startsWith("CF_SECRET_SESSION_SIGNING_KEY_DEMO: ")).toBe(true);
    });
  });

  it("D-5: 末尾の改行 → 「末尾に改行がある」", () => {
    for (const name of REQUIRED.production) {
      const errors = errorsOf("production", { ...fullEnv(), [name]: `${fullEnv()[name]}\n` });
      expect(errors).toEqual([`${name}: 末尾に改行がある`]);
    }
  });

  describe("D-6: 値が漏れない", () => {
    const sentinel = (s: string): string => `SENTINEL-${s}-${"x".repeat(40)}`;
    const badEnv = (): Env => ({
      CF_SECRET_SESSION_SIGNING_KEY_PRODUCTION: `${sentinel("prod")} `,
      CF_SECRET_SESSION_SIGNING_KEY_DEMO: `${sentinel("demo")}\n`,
      CF_SECRET_ADMIN_TOKEN: "SENTINEL-short",
      CF_SECRET_TURNSTILE_SECRET: `SENTINEL-${"t".repeat(300)}`,
    });

    it.each(["production", "demo"] as const)("%s: errors に目印が含まれない", (mode) => {
      const errors = errorsOf(mode, badEnv());
      expect(errors.length).toBeGreaterThan(0);
      for (const e of errors) expect(e).not.toContain("SENTINEL");
    });

    it("demo で一般公開と同じ値 → 誤りの文に目印が含まれない", () => {
      const v = sentinel("same");
      const errors = errorsOf("demo", {
        ...fullEnv(),
        CF_SECRET_SESSION_SIGNING_KEY_PRODUCTION: v,
        CF_SECRET_SESSION_SIGNING_KEY_DEMO: v,
      });
      expect(errors).toHaveLength(1);
      expect(errors[0]).not.toContain("SENTINEL");
    });

    describe("main", () => {
      let dir: string;
      beforeEach(() => {
        dir = mkdtempSync(join(tmpdir(), "deploy-secrets-"));
      });
      afterEach(() => {
        rmSync(dir, { recursive: true, force: true });
      });

      it.each(["production", "demo"] as const)(
        "%s: 標準出力・標準エラーに目印が含まれない",
        (mode) => {
          const c = capture();
          let code: number;
          try {
            code = main(["--mode", mode, "--out", join(dir, "s.json")], badEnv());
          } finally {
            c.restore();
          }
          expect(code).toBe(1);
          expect(c.err()).not.toBe("");
          expect(c.out()).not.toContain("SENTINEL");
          expect(c.err()).not.toContain("SENTINEL");
        },
      );

      it("成功しても標準出力・標準エラーに値が含まれない", () => {
        const env = {
          ...fullEnv(),
          CF_SECRET_SESSION_SIGNING_KEY_PRODUCTION: sentinel("ok"),
          CF_SECRET_ADMIN_TOKEN: sentinel("admin"),
        };
        const c = capture();
        let code: number;
        try {
          code = main(["--mode", "production", "--out", join(dir, "s.json")], env);
        } finally {
          c.restore();
        }
        expect(code).toBe(0);
        expect(c.out()).not.toContain("SENTINEL");
        expect(c.err()).not.toContain("SENTINEL");
      });
    });
  });

  describe("D-7: demo の署名の鍵と一般公開の鍵", () => {
    it("demo で _DEMO と _PRODUCTION が同じ → 誤り", () => {
      const errors = errorsOf("demo", {
        ...fullEnv(),
        CF_SECRET_SESSION_SIGNING_KEY_DEMO: PROD_KEY,
      });
      expect(errors).toEqual(["CF_SECRET_SESSION_SIGNING_KEY_DEMO: 一般公開と同じ値は使えない"]);
    });
    it("_PRODUCTION がなければ比べない", () => {
      const env = fullEnv();
      delete env["CF_SECRET_SESSION_SIGNING_KEY_PRODUCTION"];
      expect(errorsOf("demo", env)).toEqual([]);
    });
    it("production では _DEMO を読まない", () => {
      expect(
        errorsOf("production", { ...fullEnv(), CF_SECRET_SESSION_SIGNING_KEY_DEMO: PROD_KEY }),
      ).toEqual([]);
      const env = fullEnv();
      env["CF_SECRET_SESSION_SIGNING_KEY_DEMO"] = "bad value";
      expect(errorsOf("production", env)).toEqual([]);
    });
  });
});

describe("main", () => {
  let dir: string;
  let c: ReturnType<typeof capture>;
  beforeEach(() => {
    dir = mkdtempSync(join(tmpdir(), "deploy-secrets-"));
    c = capture();
  });
  afterEach(() => {
    c.restore();
    rmSync(dir, { recursive: true, force: true });
  });

  it.each(["production", "demo"] as const)("D-8: %s の成功", (mode) => {
    const out = join(dir, "secrets.json");
    expect(main(["--mode", mode, "--out", out], fullEnv())).toBe(0);
    const text = readFileSync(out, "utf8");
    expect(text.endsWith("\n")).toBe(false);
    const expected = buildSecrets(mode, fullEnv());
    expect(expected.ok).toBe(true);
    if (!expected.ok) return;
    expect(JSON.parse(text)).toEqual(expected.secrets);
    expect(statSync(out).mode & 0o777).toBe(0o600);
    const stdout = JSON.parse(c.out()) as unknown;
    expect(stdout).toEqual({ ok: true, mode, keys: Object.keys(expected.secrets) });
    for (const v of [PROD_KEY, DEMO_KEY, ADMIN, TURNSTILE]) expect(c.out()).not.toContain(v);
  });

  describe("D-9: 引数の誤り → 2", () => {
    it("モードなし", () => {
      expect(main(["--out", join(dir, "s.json")], fullEnv())).toBe(2);
    });
    it("staging", () => {
      expect(main(["--mode", "staging", "--out", join(dir, "s.json")], fullEnv())).toBe(2);
      expect(existsSync(join(dir, "s.json"))).toBe(false);
    });
    it("--out なし", () => {
      expect(main(["--mode", "production"], fullEnv())).toBe(2);
    });
    it("相対パス", () => {
      expect(main(["--mode", "production", "--out", "secrets.json"], fullEnv())).toBe(2);
      expect(existsSync("secrets.json")).toBe(false);
    });
    it("既にあるファイル（中身が変わらない）", () => {
      const out = join(dir, "s.json");
      writeFileSync(out, "before");
      expect(main(["--mode", "production", "--out", out], fullEnv())).toBe(2);
      expect(readFileSync(out, "utf8")).toBe("before");
    });
    it("親のディレクトリがない", () => {
      const out = join(dir, "missing", "s.json");
      expect(main(["--mode", "production", "--out", out], fullEnv())).toBe(2);
      expect(existsSync(out)).toBe(false);
    });
  });

  it.each(["production", "demo"] as const)(
    "D-10: %s の検査の誤り → 1 でファイルを作らない",
    (mode) => {
      const out = join(dir, "s.json");
      const env = fullEnv();
      delete env["CF_SECRET_TURNSTILE_SECRET"];
      expect(main(["--mode", mode, "--out", out], env)).toBe(1);
      expect(existsSync(out)).toBe(false);
      expect(c.err()).toContain("CF_SECRET_TURNSTILE_SECRET: 設定されていない");
    },
  );
});
