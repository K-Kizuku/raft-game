# ADR-0016: インフラは Cloudflare（Workers ＋ Durable Objects ＋ Containers）、構成は Cloudflare CLI（`cf`）だけで管理する

- 状態: Accepted
- 日付: 2026-11-06（同日、Containers の公式ドキュメントの確認結果で改訂）
- 決めた人: プロダクトオーナー（Cloudflare と `cf` の採用、1 ゲーム＝1 コンテナ、マシンの大きさ、ドメイン）、Claude（調査と具体化）
- 関連: C-03、NFR-06・15、ADR-0009、ADR-0017、ADR-0018、ADR-0019、[アーキテクチャ §5](../02-architecture.md)、[判断の記録 A-02・A-03・A-22、R-01〜R-04・R-07](../09-assumptions-and-open-questions.md)
- 置き換える ADR: [ADR-0011](./0011-infrastructure-aws-ecs-fargate.md)（AWS ＋ Terraform）

## 背景

ADR-0011 では AWS（CloudFront ＋ S3 ＋ ALB ＋ ECS Fargate）と Terraform を仮決めしていた。オーナーのレビューで、次のように決まった。

- インフラは **Cloudflare** にする
- 操作には **Cloudflare CLI（`cf`）** を使い、構成は `cf` **だけ**で管理する
- 1 ゲーム＝1 Durable Object＝1 コンテナ（A-03 補足）
- ドメインは、Cloudflare で管理している `kizuku-hackathon.work` のサブドメインを使う（Q-03）

`cf` は、2026-09-28 にベータ版になった Cloudflare の次世代 CLI。Cloudflare の公開 API 全体（2,900 以上のコマンド）と、Workers のプロジェクトを 1 つのツールで扱える。

## 調べた仕様

### `cf`（2026-11-06 時点、`cf@1.0.0-beta.28`）

npm パッケージ・型定義（`@cloudflare/config` 0.30.2）・公式ドキュメントで確認した。

| 項目 | 内容 |
|---|---|
| 入手 | npm パッケージ `cf`（公開者は Cloudflare）。コマンド名は `cf`。Node.js 22.18 以上（パッケージの指定は `^22.23.0 \|\| ^24.22.0 \|\| ^26.12.0`）。Bun では設定ファイルを読めない |
| 設定ファイル | `cloudflare.config.ts`。`import { defineConfig, defineContainer, bindings, exports } from "cf/config"`。`package.json` に `"type": "module"` が必要 |
| 構成要素 | `defineConfig({ accountId?, worker, containers })` |
| Worker | `name`・`compatibilityDate`・`entrypoint`（`import * as entrypoint from "./worker/index.ts" with { type: "cf-worker" }`）・`env`（バインディング）・`exports`（Durable Object のクラス）・`assets`・`domains`・`workersDev`・`previewUrls`・`limits`・`observability` |
| 静的ファイル | `assets.notFoundHandling: "single-page-application"`、`assets.runWorkerFirst: ["/api/*", "/ws/*"]`。配信するディレクトリの指定はなく、Vite のクライアントビルドの出力がそのまま静的ファイルになる（`@cloudflare/vite-plugin`） |
| Durable Object | `worker.exports.GameSession = exports.durableObject({ storage: "sqlite", container })`。Wrangler のマイグレーション履歴の代わりに、クラスの状態（`created` / `deleted` / `renamed` / `transferred`）を宣言する。クラスの作成・削除・改名は `cf deploy` で行い、それ以前の版へのロールバックはできない |
| Containers | `defineContainer({ name, schedulingPolicy: "durable-object", images: { server: { dockerfile, buildContext? } } })` を最上位の `containers` に入れ、Durable Object の `container` から参照する |
| R2 | `bindings.r2({ name })`。`cf deploy` は既定でバインディングに必要な資源を自動で作る（`--provision`） |
| コマンド | `cf init` / `cf dev` / `cf build`（出力は `.cloudflare/output/v0/`）/ `cf deploy`（`--prebuilt`・`--dry-run`・`--mode`・`--secrets-file`）/ `cf workers versions` / `cf workers deployments` / `cf workers types` / `cf containers build・push・images・ssh` / `cf r2 …` / `cf dns …` / `cf zones …` / `cf auth login・whoami` |
| 認証 | 手元: `cf auth login`（OAuth）。CI: `CLOUDFLARE_API_TOKEN` と `CLOUDFLARE_ACCOUNT_ID` |
| モード | `--mode <名前>` で設定ファイルを切り替える（Wrangler の `--env` の代わり） |

### Containers（公式ドキュメント、2026-11 時点）

| 項目 | 内容 | 設計への反映 |
|---|---|---|
| スケジューリングの方式 | `default` 方式は**非推奨**。推奨は `durable_object` 方式（公開ベータ）。方式は後から変えられない | **`durable_object` 方式を使う**（当初の設定例は `default` 方式だったので改めた） |
| マシンの大きさ | `durable_object` 方式では、Durable Object のコードで `ctx.container.start({ image, instance, enableInternet })` に指定する。`instance` は `lite`（1/16 vCPU・256 MiB・2 GB）/ `standard-1`（1/2 vCPU・4 GiB・8 GB）/ `standard-2`〜`4` / 独自の値 | 一般公開は `lite` で始め、測ってから決める。デモは `standard-1`（R-02） |
| 同時に動かす数の上限 | `durable_object` 方式は `max_instances` に非対応（アカウントの上限に数えられる） | 同時に遊べる数の上限は設けない（R-01）。費用はコストガード（ADR-0018）で抑える |
| 起動 | `start()` は準備完了の前に戻る。準備ができたかはアプリで確かめる。失敗は `monitor()` で検知する | コンテナの `/healthz` に応答があるまで待つ |
| 停止 | 固定の最長時間はない。ホストの再起動などで止まることがある（`SIGTERM`）。ディスクは揮発する。`setInactivityTimeout(ms)` で、Durable Object が動いていない間にいつ止めるかを決められる。`destroy()` で即座に止める | ラウンドの終了で `destroy()`。止まったら ADR-0017 の仕組みで復元する |
| 通信 | `ctx.container.getTcpPort(8080).fetch(…)` | Go サーバーは 8080 番で待つ |
| 外部への通信 | `start()` の `enableInternet` で許可・禁止を選ぶ | **禁止する**（`enableInternet: false`）。Go サーバーは外部と通信しない |
| 起動にかかる時間 | 多くは 1〜3 秒（イメージの大きさなどによる） | ステージ紹介と開会式の間に隠す |
| イメージ | `linux/amd64` でビルドする。`durable_object` 方式は Docker Hub などから取得せず、Dockerfile からビルドして Cloudflare のレジストリに登録する | Dockerfile で `--platform=linux/amd64` を指定する |
| ローカルでの実行 | Docker 互換のツール（Docker Desktop・Colima）が必要。Cloudflare の Vite プラグイン（`cf dev` が使う）でコンテナもローカルで動く。デプロイ時のイメージのビルドにも Docker を使う | 開発者は Docker Desktop を使う（R-03）。並列開発は ADR-0019 |
| 料金 | Workers の有料プラン（月 5 USD）に、メモリ 25 GiB 時間・CPU 375 vCPU 分・ディスク 200 GB 時間が含まれる。超えた分は従量（メモリとディスクは割り当てた量、CPU は使った分） | コストガード（ADR-0018）の見積もりに使う |

### ベータ版の制約と対応

| 制約 | 対応 |
|---|---|
| `cf` のコマンド・設定・ビルド結果の形式は、正式版の前に変わり得る | `cf` を devDependency として**版を固定**する。上げるときは専用の PR で確認する |
| `durable_object` 方式のコンテナは公開ベータ | 実装計画の最初の確認作業（T-007）で、最小の構成を実際にデプロイして確かめる |
| 実行中のログをリアルタイムに流し見る機能（`wrangler tail` 相当）がない | `observability.logs` を有効にして、ダッシュボードで見る |
| シークレットを 1 つずつ登録する機能がない | `cf deploy --secrets-file` でデプロイ時にまとめて渡す（CI では GitHub の Secrets からファイルを作る） |
| Durable Object に紐付けたコンテナはプレビュー環境に対応していない | `previewUrls: false`。PR では `cf deploy --prebuilt --dry-run` で検査するだけにする |

## 検討した選択肢

### 案 A: AWS（ADR-0011）

- 長所: 常駐するコンテナで WebSocket を素直に扱える。構成が枯れている
- 短所: オーナーの判断で不採用

### 案 B: Cloudflare ＋ Wrangler

- 長所: 情報が多い
- 短所: オーナーの指定は `cf`。Wrangler は `cf` の正式版の後、保守のみになる予定

### 案 C: Cloudflare ＋ `cf`（Terraform などとは併用しない）

- 長所: 設定ファイル 1 つと `cf deploy` だけで、Worker・Durable Object・コンテナ・R2・ドメインまで管理できる。TypeScript で型が付くので、設定の誤りに早く気づける
- 短所: ベータ版である（上の制約）

## 決定

案 C。構成は次のとおり。

```ts
// web/cloudflare.config.ts（概略。正確な書き方は実装時に cf の型に合わせる）
import { bindings, defineConfig, defineContainer, exports } from "cf/config";
import * as entrypoint from "./worker/index.ts" with { type: "cf-worker" };

const gameServer = defineContainer({
  name: "raft-game-server",
  schedulingPolicy: "durable-object",          // 推奨の方式。マシンの大きさは start() で指定する
  images: { server: { dockerfile: "../server/Dockerfile", buildContext: "../server" } },
  observability: { enabled: true, logs: { enabled: true } },
});

export default defineConfig(({ mode }) => {
  const isDemo = mode === "demo";
  return {
    worker: {
      name: isDemo ? "raft-game-demo" : "raft-game",
      compatibilityDate: "2026-11-04",
      entrypoint,
      domains: [isDemo ? "raft-demo.kizuku-hackathon.work" : "raft.kizuku-hackathon.work"],
      workersDev: false,                         // workers.dev の URL は使わない
      previewUrls: false,
      assets: { notFoundHandling: "single-page-application", runWorkerFirst: ["/api/*", "/ws/*"] },
      limits: { cpuMs: 50 },                     // コストガード（ADR-0018）。値は T-007 で測って決める
      env: {
        DEMO_CONTROLS: bindings.text(isDemo ? "1" : "0"),
        CONTAINER_INSTANCE: bindings.text(isDemo ? "standard-1" : "lite"),
        ALLOWED_ORIGINS: bindings.text(isDemo ? "https://raft-demo.kizuku-hackathon.work" : "https://raft.kizuku-hackathon.work"),
        GUARDS: bindings.json({ /* コストガードの既定値（ADR-0018） */ }),
        SESSION_SIGNING_KEY: bindings.secret(),  // --secrets-file で渡す
        ADMIN_TOKEN: bindings.secret(),
        TURNSTILE_SECRET: bindings.secret(),
        REPLAYS: bindings.r2({ name: "raft-game-replays" }), // P1
        // デモ用の Worker は、一般公開の Worker の CostGuard を共有する（ADR-0018）
        COST_GUARD: bindings.durableObject({ worker: "raft-game", exportName: "CostGuard" }),
      },
      exports: {
        GameSession: exports.durableObject({ storage: "sqlite", container: gameServer }),
        ...(isDemo ? {} : { CostGuard: exports.durableObject({ storage: "sqlite" }) }),
      },
      observability: { enabled: true, logs: { enabled: true, headSamplingRate: 1 } },
    },
    containers: [gameServer],
  };
});
```

```ts
// web/worker/game-session.ts（コンテナの起動の部分）
this.ctx.container.start({
  image: this.ctx.container.images.server,
  instance: this.env.CONTAINER_INSTANCE,   // 一般公開: "lite"、デモ: "standard-1"
  enableInternet: false,
  env: { MODE: "single", PORT: "8080" },
});
// その後、getTcpPort(8080).fetch("http://container/healthz") が成功するまで待つ
```

| 項目 | 一般公開（`production`） | デモ用（`demo`） |
|---|---|---|
| Worker | `raft-game` | `raft-game-demo` |
| URL | `https://raft.kizuku-hackathon.work` | `https://raft-demo.kizuku-hackathon.work` |
| コンテナの大きさ | `lite`（T-007 と負荷テストで測って見直す） | `standard-1` |
| デモ操作 | 無効 | 有効 |

- **ドメインの設定**: `kizuku-hackathon.work` のゾーンに対する設定（WAF のレート制限、AI クローラーの遮断など。ADR-0018）は、**この 2 つのホスト名だけに効く条件を付けて**作り、同じゾーンのほかのサービスに影響させない。これらの設定も `cf`（`cf zones …` などの API コマンド）で行い、手順をスクリプトにしてリポジトリに置く
- **デプロイ**: GitHub Actions で `pnpm install` → `cf build` → PR では `cf deploy --prebuilt --dry-run`、`main` への push で `cf deploy --prebuilt --mode production`。デモ用は手動の実行で `--mode demo`。シークレットは GitHub の Secrets から一時ファイルを作り、`--secrets-file` で渡す
- **費用**: Workers の有料プラン（月 5 USD）が必要。追加料金はコストガード（ADR-0018）で月 30 USD までに抑える

## 結果

- 良いこと: インフラの定義が `web/cloudflare.config.ts` の 1 ファイルにまとまる。1 ゲーム＝1 コンテナで、ゲームどうしが完全に分かれる。マシンの大きさを環境ごとに変えられる
- 悪いこと・受け入れるリスク:
  - ベータ版（`cf` と `durable_object` 方式）の変更に追従する必要がある
  - ゲームを始めるたびにコンテナの起動待ちが発生する（多くは 1〜3 秒。ステージ紹介と開会式の間に隠す）
  - 開発者の PC と CI に Docker が必要

## 差し替えるときの影響範囲

`web/cloudflare.config.ts`・`web/worker/`（Worker と Durable Object）・`scripts/cloudflare/`（ゾーンの設定）・`.github/workflows/deploy.yml`・アーキテクチャ §5・DesignDoc §12。Go のシミュレーション（`server/internal/sim` 以下）と画面（`web/src/`）は変わらない。

## 見直すきっかけ

- `cf` の正式版が出たとき、`durable_object` 方式が正式版になったとき（版を上げ、制約の表を見直す）
- 測定の結果、`lite` で 8 倍速や復元が遅いと分かったとき（`standard-1` に上げ、ADR-0018 の見積もりを直す）
- コンテナの起動待ちがデモで目立つとき
