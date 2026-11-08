// インフラの構成の正本（ADR-0016・T-601）。dtn/main では DTN の環境だけを作る（DTN-12・docs/dtn/environment.md §1・
// ADR-D002）。ビルドの出力は web/scripts/check-build-output.ts で検査する。
//
// - dtn（--mode なしも）: DTN の環境。raft-dtn.kizuku-hackathon.work
// - development: make dev・vite・cf dev。Container（DtnSession・ActorHost）を含めない（Docker なしで動く）
// - local-container: cf dev --mode local-container。Container まで手元で動かす（Docker と make sync-data が要る）
//
// 失敗しても安全な側に倒す: production・demo のモードはない。どのモードでも Worker の名前は DTN 用
// （raft-game-dtn・raft-game-dtn-dev）なので、dtn/main から誤って cf deploy しても prod・demo を上書きしない。
import { bindings, defineConfig, defineContainer, exports } from "cf/config";
import * as entrypoint from "./worker/index.ts" with { type: "cf-worker" };

const DTN_HOST = "raft-dtn.kizuku-hackathon.work";
// DTN の環境のホスト名だけ（prod・demo のオリジンを許さない）
const ALLOWED_ORIGINS = `https://${DTN_HOST}`;
const COMPATIBILITY_DATE = "2026-11-04";
// GUARDS.cpuMs（表示用）と同じ値にする
const CPU_MS = 50;
// Container の大きさは、すべて lite から始める（environment.md §2。DTN-12 の測定で見直す）
const INSTANCE = "lite";
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

type Mode = "dtn" | "development" | "local-container";

function parseMode(mode: string | undefined): Mode {
  // --mode なしで評価する cf のコマンドは DTN の環境の構成を見る（デプロイのワークフローは必ず --mode dtn を付ける）
  if (mode === undefined) return "dtn";
  if (mode === "dtn" || mode === "development" || mode === "local-container") return mode;
  throw new Error(
    `cloudflare.config.ts: 知らないモード: ${mode}（dtn・development・local-container のどれか。dtn/main に production・demo はない）`,
  );
}

interface ModeValues {
  workerName: string;
  host: string | undefined; // domains。undefined（ローカル）なら ALLOWED_ORIGINS も書かない
  // Container のアプリケーションの名前（DO のクラスごとに 1 つ）。undefined なら Container も DtnSession・ActorHost もない
  containers: { coordinator: string; actor: string } | undefined;
  rateLimitNamespace: string; // アカウントの中で一意（ADR-0024）。prod 1001・demo 1002・dev 1003 と重ならない
}

const MODES: Record<Mode, ModeValues> = {
  dtn: {
    workerName: "raft-game-dtn",
    host: DTN_HOST,
    containers: { coordinator: "raft-dtn-coordinator", actor: "raft-dtn-actor" },
    rateLimitNamespace: "1004",
  },
  development: {
    workerName: "raft-game-dtn-dev",
    host: undefined,
    containers: undefined,
    rateLimitNamespace: "1005",
  },
  "local-container": {
    workerName: "raft-game-dtn-dev",
    host: undefined,
    containers: { coordinator: "raft-dtn-dev-coordinator", actor: "raft-dtn-dev-actor" },
    rateLimitNamespace: "1005",
  },
};

export default defineConfig((ctx) => {
  if (ctx.isPreview) throw new Error("cloudflare.config.ts: プレビューは使わない（ADR-0016）");
  const m = MODES[parseMode(ctx.mode)];
  const buildId = parseBuildId(buildEnv["BUILD_ID"]);

  // 2 つのアプリケーションは同じ Dockerfile（dtnactor。ROLE で役割を変える）。image の共有は Q-IMAGE で測る
  const dtnContainer = (name: string) =>
    defineContainer({
      name,
      schedulingPolicy: "durable-object",
      images: {
        // DtnSession・ActorHost の ctx.container.images.dtnactor が使う。cf build の前に make sync-data が要る
        dtnactor: {
          dockerfile: "../server/Dockerfile.dtnactor",
          buildContext: "../server",
          buildVars: { BUILD_ID: buildId },
        },
      },
      observability: { enabled: true, logs: { enabled: true } },
    });
  const coordinator =
    m.containers === undefined ? undefined : dtnContainer(m.containers.coordinator);
  const actor = m.containers === undefined ? undefined : dtnContainer(m.containers.actor);

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
        // 全ステージ（demo-goto・demo-izu も）を選べるようにする。デモの操作も誰でも使える（environment.md §1）
        DEMO_CONTROLS: bindings.text("1"),
        BUILD_ID: bindings.text(buildId),
        CONTAINER_INSTANCE: bindings.text(INSTANCE),
        DTN_SESSION:
          coordinator === undefined
            ? undefined
            : bindings.durableObject({ worker: m.workerName, exportName: "DtnSession" }),
        ACTOR_HOST:
          actor === undefined
            ? undefined
            : bindings.durableObject({ worker: m.workerName, exportName: "ActorHost" }),
        // DTN の環境の専用の CostGuard（prod・demo の CostGuard とは別の Worker の別のインスタンス）
        COST_GUARD: bindings.durableObject({ worker: m.workerName, exportName: "CostGuard" }),
        // signature のキーを書かない（書くと CostGuard が設定ごと不正にする。G3）
        GUARDS: bindings.json({
          budget: { enabled: true, monthlyUsd: 10, dailyUsd: 1 },
          gameTime: { maxMinutes: 15, maxAlarms: 20 },
          turnstile: { enabled: true },
          rateLimit: { enabled: true, perIpPerMinute: SESSION_RATE_LIMIT_PER_MINUTE },
          cpuMs: CPU_MS,
          acceptGames: true,
          // lite・standard-1 は今の {sessionId, instance} の予約（コンテナ 1 つ＋ DO 1 つ）。
          // container.*・do は内訳つきの予約（environment.md §3。ADR-0018 の単価）
          unitCostUsd: {
            lite: 0.0065,
            "standard-1": 0.04,
            "container.lite": 0.0036,
            "container.standard-1": 0.037,
            do: 0.0028,
          },
        }),
        // シークレットの値は cf deploy --secrets-file とローカルの web/.dev.vars で渡す（prod・demo と別の値）
        SESSION_SIGNING_KEY: bindings.secret(),
        TURNSTILE_SECRET: bindings.secret(),
        ADMIN_TOKEN: bindings.secret(),
        DTN_CONTACT_KEY: bindings.secret(),
        SESSION_RATE_LIMIT: bindings.rateLimit({
          namespace: m.rateLimitNamespace,
          simple: { limit: SESSION_RATE_LIMIT_PER_MINUTE, period: 60 },
        }),
      },
      // キー（DtnSession・ActorHost・CostGuard）を変えない。Durable Object のクラスの改名・削除は前の版に戻せない（ADR-0016）
      // GameSession は置かない（index.ts は export するが、ここに宣言しないので Durable Object にならない）
      exports: {
        ...(coordinator === undefined
          ? {}
          : { DtnSession: exports.durableObject({ storage: "sqlite", container: coordinator }) }),
        ...(actor === undefined
          ? {}
          : { ActorHost: exports.durableObject({ storage: "sqlite", container: actor }) }),
        CostGuard: exports.durableObject({ storage: "sqlite" }),
      },
      observability: {
        enabled: true,
        redactQueryString: true, // /ws/sessions/{id}?token=… のトークンをログに残さない
        logs: { enabled: true, headSamplingRate: 1 },
      },
    },
    containers: coordinator === undefined || actor === undefined ? [] : [coordinator, actor],
  };
});
