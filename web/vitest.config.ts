import react from "@vitejs/plugin-react";
import { defineConfig } from "vitest/config";

// テストでは @cloudflare/vite-plugin を使わない（workerd を起動しない）。
// 画面のテストはファイルの先頭の `@vitest-environment jsdom` で jsdom にする。
export default defineConfig({
  plugins: [react()],
  test: {
    environment: "node",
    include: ["src/**/*.test.{ts,tsx}", "worker/**/*.test.ts"],
  },
});
