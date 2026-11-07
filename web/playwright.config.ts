// E2E テスト（T-506）の Playwright の設定。実行は `pnpm -C web test:e2e`（`make e2e`）。
// 開発のサーバー（make dev と同じ scripts/dev-env.sh dev。Go の standalone・VITE_GAME_MOCK=off・Turnstile なし）を
// DEV_SLOT のポートで起動する（ADR-0019。固定のポートは使わない）。Chrome・Chromium がなければテストを飛ばす。
import { resolve } from "node:path";

import { defineConfig, devices } from "@playwright/test";

import { ARTIFACTS_DIR, devEnv, findBrowser } from "./e2e/env.ts";

const browser = findBrowser();
// ブラウザがないときはサーバーも起動しない（テストは e2e/*.spec.ts の中で理由を付けて飛ばす）。
const dev = browser.found ? devEnv() : null;

// 設定はワーカーでも読まれる。メッセージは最初のプロセスでだけ出す。
if (process.env.TEST_WORKER_INDEX === undefined) {
  if (browser.found) {
    console.log(
      `e2e: ブラウザ=${browser.executablePath}（${browser.source}）DEV_SLOT=${dev?.slot ?? ""} ` +
        `Vite=${String(dev?.webPort ?? "")} Go=${String(dev?.serverPort ?? "")}`,
    );
  } else {
    console.log(`e2e: テストを飛ばす: ${browser.reason}`);
  }
}

export default defineConfig({
  testDir: "./e2e",
  outputDir: resolve(ARTIFACTS_DIR, "test-results"),
  // 1 つの開発のサーバーに対して、画面を順にたどる。
  fullyParallel: false,
  workers: 1,
  retries: 0,
  forbidOnly: process.env.CI !== undefined,
  timeout: 60_000,
  expect: { timeout: 15_000 },
  reporter: [["list"]],
  use: {
    ...devices["Desktop Chrome"],
    viewport: { width: 1600, height: 900 },
    baseURL: dev === null ? undefined : `http://127.0.0.1:${String(dev.webPort)}`,
    launchOptions: browser.found ? { executablePath: browser.executablePath } : {},
    locale: "ja-JP",
    trace: "off",
    screenshot: "only-on-failure",
  },
  webServer:
    dev === null
      ? undefined
      : {
          command: `bash e2e/dev-server.sh "${resolve(ARTIFACTS_DIR, "dev-server.log")}" ${String(dev.webPort)}`,
          url: `http://127.0.0.1:${String(dev.webPort)}/`,
          env: { DEV_SLOT: dev.slot },
          // 同じポートで動いている make dev（モックありかもしれない）は使わない。
          reuseExistingServer: false,
          // 初回は Go のビルドがあるので長めに待つ。
          timeout: 240_000,
          stdout: "ignore",
          stderr: "pipe",
          gracefulShutdown: { signal: "SIGTERM", timeout: 10_000 },
        },
});
