import { cloudflare } from "@cloudflare/vite-plugin";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

// @cloudflare/vite-plugin は cloudflare.config.ts を読み、Worker と静的ファイルを一緒にビルドする。
// ローカルの状態は作業ディレクトリの中（.cloudflare/state）に置く（ADR-0019）。
export default defineConfig({
  plugins: [react(), cloudflare({ persistState: { path: ".cloudflare/state" } })],
});
