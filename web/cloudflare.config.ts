// インフラの構成の正本（ADR-0016・T-601）。モード（cf build --mode …）で一般公開とデモ、ローカルを切り替える。
// 構成の説明は docs/03-design-doc.md §12.6。ビルドの出力は web/scripts/check-build-output.ts で検査する。
//
// - production（--mode なしも）: 一般公開。raft.kizuku-hackathon.work
// - demo: デモ。raft-demo.kizuku-hackathon.work。CostGuard・GUARDS・ADMIN_TOKEN を持たず、一般公開の CostGuard を使う
// - development: make dev・vite・cf dev。コンテナ（GameSession）を含めない（Docker なしで動く）
// - local-container: cf dev --mode local-container。コンテナまで手元で動かす（Docker と make sync-data が要る）
import { bindings, defineConfig, defineContainer, exports } from "cf/config";
import * as entrypoint from "./worker/index.ts" with { type: "cf-worker" };

const PRODUCTION_HOST = "raft.kizuku-hackathon.work";
const DEMO_HOST = "raft-demo.kizuku-hackathon.work";
// 一般公開・デモの両方の Worker で、両方のホスト名のオリジンを許す（#130 の決定・T-601b）
const ALLOWED_ORIGINS = `https://${PRODUCTION_HOST},https://${DEMO_HOST}`;
const COMPATIBILITY_DATE = "2026-11-04";
// T-007（#15）の測定で見直す。GUARDS.cpuMs（表示用）と同じ値にする
const CPU_MS = 50;
// T-007（#15）の測定で見直す（R-02）。"lite" か "standard-1"
const PRODUCTION_INSTANCE = "lite";
const DEMO_INSTANCE = "standard-1";
// SESSION_RATE_LIMIT の simple.limit と GUARDS.rateLimit.perIpPerMinute に使う（ADR-0024）
const SESSION_RATE_LIMIT_PER_MINUTE = 10;

// worker/ の中のファイルを（cf-worker の entrypoint 以外で）import しない（cf dev が読めなくなる。spike で確認）。
// process を直接書かない（生成した型から読まれ、Node の型のない検査にも入るため。spike と同じ）。
const buildEnv =
  (globalThis as { process?: { env?: Record<string, string | undefined> } }).process?.env ?? {};

/** ビルドの識別子（/api/healthz と /healthz の build）。未設定・空なら "dev"。T-306 の Dockerfile と同じ規則 */
function parseBuildId(raw: string | undefined): string {
  if (raw === undefined || raw === "") return "dev";
  if (!/^[A-Za-z0-9._:-]{1,64}$/.test(raw)) {
    throw new Error(
      `cloudflare.config.ts: BUILD_ID が不正: ${JSON.stringify(raw)}（英数字と . _ : - の 64 文字まで）`,
    );
  }
  return raw;
}

type Mode = "production" | "demo" | "development" | "local-container";

function parseMode(mode: string | undefined): Mode {
  // --mode なしで評価する cf のコマンドは一般公開の構成を見る（デプロイのワークフローは必ず --mode を付ける）
  if (mode === undefined) return "production";
  if (
    mode === "production" ||
    mode === "demo" ||
    mode === "development" ||
    mode === "local-container"
  )
    return mode;
  throw new Error(
    `cloudflare.config.ts: 知らないモード: ${mode}（production・demo・development・local-container のどれか）`,
  );
}

interface ModeValues {
  workerName: string;
  host: string | undefined; // domains。undefined（ローカル）なら ALLOWED_ORIGINS も書かない
  containerName: string | undefined; // undefined ならコンテナ（GameSession）なし
  ownCostGuard: boolean; // false なら一般公開の Worker の CostGuard を参照し、GUARDS・ADMIN_TOKEN を置かない
  demoControls: "0" | "1";
  instance: "lite" | "standard-1";
  rateLimitNamespace: string; // アカウントの中で一意（ADR-0024）。仮の値。#130 で決め直す
}

const MODES: Record<Mode, ModeValues> = {
  production: {
    workerName: "raft-game",
    host: PRODUCTION_HOST,
    containerName: "raft-game-server",
    ownCostGuard: true,
    demoControls: "0",
    instance: PRODUCTION_INSTANCE,
    rateLimitNamespace: "1001",
  },
  demo: {
    workerName: "raft-game-demo",
    host: DEMO_HOST,
    // Containers のアプリケーションは 1 つの Durable Object のクラスに結び付くので、一般公開と分ける
    containerName: "raft-game-demo-server",
    ownCostGuard: false,
    demoControls: "1",
    instance: DEMO_INSTANCE,
    rateLimitNamespace: "1002",
  },
  development: {
    workerName: "raft-game-dev",
    host: undefined,
    containerName: undefined,
    ownCostGuard: true,
    demoControls: "1",
    instance: "lite",
    rateLimitNamespace: "1003",
  },
  "local-container": {
    workerName: "raft-game-dev",
    host: undefined,
    containerName: "raft-game-dev-server",
    ownCostGuard: true,
    demoControls: "1",
    instance: "lite",
    rateLimitNamespace: "1003",
  },
};

export default defineConfig((ctx) => {
  if (ctx.isPreview) throw new Error("cloudflare.config.ts: プレビューは使わない（ADR-0016）");
  const m = MODES[parseMode(ctx.mode)];
  const buildId = parseBuildId(buildEnv["BUILD_ID"]);

  const container =
    m.containerName === undefined
      ? undefined
      : defineContainer({
          name: m.containerName,
          schedulingPolicy: "durable-object",
          images: {
            // GameSession の ctx.container.images.server が使う（T-308）。cf build の前に make sync-data が要る（T-306）
            server: {
              dockerfile: "../server/Dockerfile",
              buildContext: "../server",
              buildVars: { BUILD_ID: buildId },
            },
          },
          observability: { enabled: true, logs: { enabled: true } },
        });

  const costGuardWorker = m.ownCostGuard ? m.workerName : MODES.production.workerName;

  return {
    worker: {
      name: m.workerName,
      compatibilityDate: COMPATIBILITY_DATE,
      entrypoint,
      // ゾーンに対しては domains の 1 つのホスト名だけを使う。triggers（ルート）・ワイルドカードを書かない
      ...(m.host === undefined ? {} : { domains: [m.host] }),
      workersDev: false, // workers.dev の URL は使わない
      previewUrls: false, // Durable Object に紐付けたコンテナはプレビューに非対応（ADR-0016）
      assets: {
        notFoundHandling: "single-page-application",
        runWorkerFirst: ["/api/*", "/ws/*"],
      },
      limits: { cpuMs: CPU_MS },
      env: {
        ALLOWED_ORIGINS: m.host === undefined ? undefined : bindings.text(ALLOWED_ORIGINS),
        DEMO_CONTROLS: bindings.text(m.demoControls),
        BUILD_ID: bindings.text(buildId),
        CONTAINER_INSTANCE: bindings.text(m.instance),
        GAME_SESSION:
          container === undefined
            ? undefined
            : bindings.durableObject({ worker: m.workerName, exportName: "GameSession" }),
        COST_GUARD: bindings.durableObject({ worker: costGuardWorker, exportName: "CostGuard" }),
        // CostGuard は自分の Worker の env から GUARDS を読むので、CostGuard を持たない Worker には置かない。
        // signature のキーを書かない（書くと CostGuard が設定ごと不正にする。G3）
        GUARDS: m.ownCostGuard
          ? bindings.json({
              budget: { enabled: true, monthlyUsd: 30, dailyUsd: 1 },
              gameTime: { maxMinutes: 30, maxAlarms: 20 },
              turnstile: { enabled: true },
              rateLimit: { enabled: true, perIpPerMinute: SESSION_RATE_LIMIT_PER_MINUTE },
              cpuMs: CPU_MS,
              acceptGames: true,
              unitCostUsd: { lite: 0.0065, "standard-1": 0.04 },
            })
          : undefined,
        // シークレットの値は cf deploy --secrets-file（T-603）とローカルの web/.dev.vars で渡す
        SESSION_SIGNING_KEY: bindings.secret(),
        TURNSTILE_SECRET: bindings.secret(),
        // 共有の CostGuard を切り替える入り口を一般公開の Worker の 1 つに絞る（デモの管理用 API は 404）
        ADMIN_TOKEN: m.ownCostGuard ? bindings.secret() : undefined,
        SESSION_RATE_LIMIT: bindings.rateLimit({
          namespace: m.rateLimitNamespace,
          simple: { limit: SESSION_RATE_LIMIT_PER_MINUTE, period: 60 },
        }),
        REPLAYS: bindings.r2({ name: "raft-game-replays" }), // P1。まだ誰も読まない
      },
      // キー（GameSession・CostGuard）を変えない。Durable Object のクラスの改名・削除は前の版に戻せない（ADR-0016）
      exports: {
        ...(container === undefined
          ? {}
          : { GameSession: exports.durableObject({ storage: "sqlite", container }) }),
        // デモ用の Worker の index.ts も CostGuard を export するが、ここに宣言しないので Durable Object にならない
        ...(m.ownCostGuard ? { CostGuard: exports.durableObject({ storage: "sqlite" }) } : {}),
      },
      observability: {
        enabled: true,
        redactQueryString: true, // /ws/sessions/{id}?token=… のトークンをログに残さない
        logs: { enabled: true, headSamplingRate: 1 },
      },
    },
    containers: container === undefined ? [] : [container],
  };
});
