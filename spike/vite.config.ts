import { cloudflare } from "@cloudflare/vite-plugin";
import { defineConfig } from "vite";

// @cloudflare/vite-plugin は cloudflare.config.ts を読み、Worker をビルドする。
// ローカルの状態は作業ディレクトリの中（spike/.cloudflare/state）に置く（ADR-0019）。
// cf dev は --port を渡せないので、ポートは環境変数 SPIKE_DEV_PORT から読む（固定のポートを使わない）。
export default defineConfig(({ command }) => {
  if (command !== "serve") {
    return { plugins: [cloudflare({ persistState: { path: ".cloudflare/state" } })] };
  }
  const raw = process.env.SPIKE_DEV_PORT;
  if (raw === undefined || !/^[1-9][0-9]*$/.test(raw) || Number(raw) > 65535) {
    throw new Error(
      "SPIKE_DEV_PORT が未設定か不正（1〜65535 の整数）。spike/scripts/local-smoke.sh は DEV_SLOT から計算して渡す",
    );
  }
  return {
    plugins: [cloudflare({ persistState: { path: ".cloudflare/state" } })],
    server: { port: Number(raw), strictPort: true, host: "127.0.0.1" },
  };
});
