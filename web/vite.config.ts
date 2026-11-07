import { cloudflare } from "@cloudflare/vite-plugin";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";
import { resolveDevBackend } from "./src/devserver/backend.ts";

export default defineConfig(({ command, isPreview }) => {
  // ビルド（vite build・cf build）では DEV_BACKEND を読まない（手元の環境変数で本番のビルドを変えない）。
  const backend =
    command === "build"
      ? { kind: "worker" as const }
      : resolveDevBackend(process.env, { preview: isPreview === true });
  if (backend.kind === "standalone") {
    // standalone: Worker を起動せず、/api・/ws を Go に中継する（ADR-0026）。
    // @cloudflare/vite-plugin を読み込むと、その middleware と upgrade の処理が server.proxy より先に要求を取る。
    return { plugins: [react()], server: { proxy: { ...backend.proxy } } };
  }
  // @cloudflare/vite-plugin は cloudflare.config.ts を読み、Worker と静的ファイルを一緒にビルドする。
  // ローカルの状態は作業ディレクトリの中（.cloudflare/state）に置く（ADR-0019）。
  return { plugins: [react(), cloudflare({ persistState: { path: ".cloudflare/state" } })] };
});
