// スパイク（T-007a / #134）の構成。本番の web/cloudflare.config.ts とは別の Worker・コンテナにする。
// - Worker 名は raft-game-spike。公開は workers.dev だけ（domains・routes を書かない＝ゾーンに触れない）
// - Durable Object のバインディングは自分の Worker のクラスだけを指す
// - limits.cpuMs はビルドの時に環境変数 SPIKE_CPU_MS で変える（既定 50。M-4）
import { bindings, defineConfig, defineContainer, exports } from "cf/config";
import * as entrypoint from "./worker/index.ts" with { type: "cf-worker" };

/** SPIKE_CPU_MS（limits.cpuMs）を解釈する。10〜30000 の整数でなければ throw（cf build を失敗させる） */
function parseCpuMs(raw: string): number {
  if (!/^(0|[1-9][0-9]*)$/.test(raw)) throw new Error(`SPIKE_CPU_MS must be an integer, got ${JSON.stringify(raw)}`);
  const n = Number(raw);
  if (n < 10 || n > 30000) throw new Error(`SPIKE_CPU_MS must be in 10..30000, got ${n}`);
  return n;
}

// worker/ の中のファイルを（cf-worker の entrypoint 以外で）import しない。cf dev が worker/limits.ts を
// 読めなくなる（cf@1.0.0-beta.28 で確認）。
// process を直接書かない（生成した型 .cloudflare/types/index.d.ts からこのファイルが読まれ、
// Node の型のない tsconfig.workerd.json の検査にも入るため）
const buildEnv = (globalThis as { process?: { env?: Record<string, string | undefined> } }).process?.env ?? {};
const cpuMs = parseCpuMs(buildEnv["SPIKE_CPU_MS"] ?? "50");

const server = defineContainer({
  name: "raft-game-spike-server",
  schedulingPolicy: "durable-object",
  images: { server: { dockerfile: "./container/Dockerfile", buildContext: "./container" } },
  observability: { enabled: true, logs: { enabled: true } },
});

export default defineConfig({
  worker: {
    name: "raft-game-spike",
    compatibilityDate: "2026-11-04", // web と同じ
    entrypoint,
    workersDev: true, // domains・routes は書かない（ゾーンに触れない）
    previewUrls: false,
    limits: { cpuMs },
    env: {
      SESSIONS: bindings.durableObject({ worker: "raft-game-spike", exportName: "SpikeSession" }),
      PLAIN: bindings.durableObject({ worker: "raft-game-spike", exportName: "PlainStore" }),
      CONTROL: bindings.durableObject({ worker: "raft-game-spike", exportName: "ControlStore" }),
      SPIKE_MAX_SESSIONS: bindings.text("30"),
      SPIKE_TOKEN: bindings.secret(), // 人が cf deploy --secrets-file で渡す
    },
    exports: {
      SpikeSession: exports.durableObject({ storage: "sqlite", container: server }),
      PlainStore: exports.durableObject({ storage: "sqlite" }),
      ControlStore: exports.durableObject({ storage: "sqlite" }),
    },
    observability: { enabled: true, logs: { enabled: true, headSamplingRate: 1 } },
  },
  containers: [server],
});
