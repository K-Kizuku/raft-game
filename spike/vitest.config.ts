import { defineConfig } from "vitest/config";

// テストでは @cloudflare/vite-plugin を使わない（workerd を起動しない）。
export default defineConfig({
  test: {
    environment: "node",
    include: ["worker/**/*.test.ts", "scripts/**/*.test.ts"],
  },
});
