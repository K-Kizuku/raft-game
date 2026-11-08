// E2E テスト（T-506）の環境の決め方。playwright.config.ts とテストの両方から読む。
// - ブラウザ: PLAYWRIGHT_CHROMIUM_EXECUTABLE → Playwright が入れた Chromium → macOS の Google Chrome の順に探す。
//   見つからなければテストを飛ばす（失敗にしない）
// - ポート: scripts/dev-env.sh env（ポートの計算の正本。ADR-0019）が DEV_SLOT から出す値を使う。固定のポートは使わない
import { execFileSync } from "node:child_process";
import { existsSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

import { chromium } from "@playwright/test";

export const E2E_DIR = dirname(fileURLToPath(import.meta.url));
export const WEB_DIR = resolve(E2E_DIR, "..");
export const REPO_ROOT = resolve(WEB_DIR, "..");
export const ARTIFACTS_DIR = resolve(E2E_DIR, "artifacts");

const MAC_CHROME = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome";

export type BrowserChoice =
  | Readonly<{ found: true; executablePath: string; source: string }>
  | Readonly<{ found: false; reason: string }>;

// bundledChromium: Playwright が入れる Chromium の場所（入っていなくても場所は返る）。分からなければ空文字。
function bundledChromium(): string {
  try {
    return chromium.executablePath();
  } catch {
    return "";
  }
}

// findBrowser: 使う Chrome・Chromium の実行ファイルを決める。
export function findBrowser(
  env: NodeJS.ProcessEnv = process.env,
  platform: NodeJS.Platform = process.platform,
): BrowserChoice {
  const fromEnv = env.PLAYWRIGHT_CHROMIUM_EXECUTABLE ?? "";
  if (fromEnv !== "") {
    if (existsSync(fromEnv)) {
      return { found: true, executablePath: fromEnv, source: "PLAYWRIGHT_CHROMIUM_EXECUTABLE" };
    }
    return {
      found: false,
      reason: `PLAYWRIGHT_CHROMIUM_EXECUTABLE のファイルがない: ${fromEnv}`,
    };
  }
  const bundled = bundledChromium();
  if (bundled !== "" && existsSync(bundled)) {
    return { found: true, executablePath: bundled, source: "Playwright の Chromium" };
  }
  if (platform === "darwin" && existsSync(MAC_CHROME)) {
    return { found: true, executablePath: MAC_CHROME, source: "Google Chrome" };
  }
  return {
    found: false,
    reason:
      "Chrome・Chromium が見つからない（PLAYWRIGHT_CHROMIUM_EXECUTABLE を設定するか、" +
      "`pnpm -C web exec playwright install chromium` で入れる）",
  };
}

export type DevEnv = Readonly<{ slot: string; webPort: number; serverPort: number }>;

// devSlot: DEV_SLOT を決める。未設定なら scripts/dev-slot.sh current（この作業ツリーの枠）を使う。
function devSlot(env: NodeJS.ProcessEnv): string {
  const slot = env.DEV_SLOT ?? "";
  if (slot !== "") return slot;
  try {
    return execFileSync("bash", [resolve(REPO_ROOT, "scripts/dev-slot.sh"), "current"], {
      cwd: REPO_ROOT,
      encoding: "utf8",
      stdio: ["ignore", "pipe", "ignore"],
    }).trim();
  } catch {
    throw new Error(
      'DEV_SLOT が未設定で、この作業ツリーの枠もない。`export DEV_SLOT="$(scripts/dev-slot.sh acquire)"` で割り当てる',
    );
  }
}

// devEnv: scripts/dev-env.sh env の出力（KEY=VALUE）から、DEV_SLOT とポートを読む。
export function devEnv(env: NodeJS.ProcessEnv = process.env): DevEnv {
  const slot = devSlot(env);
  const out = execFileSync("bash", [resolve(REPO_ROOT, "scripts/dev-env.sh"), "env"], {
    cwd: REPO_ROOT,
    encoding: "utf8",
    env: { ...env, DEV_SLOT: slot },
  });
  const values = new Map<string, string>();
  for (const line of out.split("\n")) {
    const eq = line.indexOf("=");
    if (eq > 0) values.set(line.slice(0, eq), line.slice(eq + 1));
  }
  const webPort = Number(values.get("DEV_WEB_PORT"));
  const serverPort = Number(values.get("DEV_SERVER_PORT"));
  if (!Number.isInteger(webPort) || !Number.isInteger(serverPort)) {
    throw new Error(`scripts/dev-env.sh env の出力からポートを読めない:\n${out}`);
  }
  return { slot, webPort, serverPort };
}
