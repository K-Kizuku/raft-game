// インフラの構成の正本（ADR-0016）。T-004 では Worker と静的ファイルだけの最小の構成。
// Durable Object・コンテナ・ドメイン・シークレットは T-307・T-308・T-601 で足す。
import { defineConfig } from "cf/config";
import * as entrypoint from "./worker/index.ts" with { type: "cf-worker" };

export default defineConfig({
  worker: {
    name: "raft-game",
    compatibilityDate: "2026-11-04",
    entrypoint,
    workersDev: false, // workers.dev の URL は使わない
    previewUrls: false, // Durable Object に紐付けたコンテナはプレビューに非対応（ADR-0016）
    assets: {
      notFoundHandling: "single-page-application",
      runWorkerFirst: ["/api/*", "/ws/*"],
    },
  },
});
