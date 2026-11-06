# ADR-0016: インフラは Cloudflare（Workers ＋ Durable Objects ＋ Containers）、構成は Cloudflare CLI（`cf`）だけで管理する

- 状態: Accepted
- 日付: 2026-11-06
- 決めた人: プロダクトオーナー（Cloudflare と `cf` の採用、1 ゲーム＝1 コンテナ）、Claude（調査と具体化）
- 関連: C-03、NFR-06・15、ADR-0009、ADR-0017、[アーキテクチャ §5](../02-architecture.md)、[仮決め A-02・A-03・A-22](../09-assumptions-and-open-questions.md)
- 置き換える ADR: [ADR-0011](./0011-infrastructure-aws-ecs-fargate.md)（AWS ＋ Terraform）

## 背景

ADR-0011 では AWS（CloudFront ＋ S3 ＋ ALB ＋ ECS Fargate）と Terraform を仮決めしていた。オーナーのレビューで、次のように決まった。

- インフラは **Cloudflare** にする
- 操作には **Cloudflare CLI（`cf`）** を使い、構成は `cf` **だけ**で管理する
- 1 ゲーム＝1 Durable Object＝1 コンテナ（A-03 補足）

`cf` は、2026-09-28 にベータ版になった Cloudflare の次世代 CLI。Cloudflare の公開 API 全体（2,900 以上のコマンド）と、Workers のプロジェクトを 1 つのツールで扱える。

## 調査した `cf` の仕様（2026-11-06 時点、`cf@1.0.0-beta.28`）

npm パッケージ・型定義（`@cloudflare/config` 0.30.2）・公式ドキュメントで確認した。

| 項目 | 内容 |
|---|---|
| 入手 | npm パッケージ `cf`（公開者は Cloudflare）。コマンド名は `cf`。Node.js 22.18 以上（パッケージの指定は `^22.23.0 \|\| ^24.22.0 \|\| ^26.12.0`）。Bun では設定ファイルを読めない |
| 設定ファイル | `cloudflare.config.ts`。`import { defineConfig, defineContainer, bindings, exports } from "cf/config"`。`package.json` に `"type": "module"` が必要 |
| 構成要素 | `defineConfig({ accountId?, worker, containers })` |
| Worker | `name`・`compatibilityDate`・`entrypoint`（`import * as entrypoint from "./worker/index.ts" with { type: "cf-worker" }`）・`env`（バインディング）・`exports`（Durable Object のクラス）・`assets`・`domains`・`observability` |
| 静的ファイル | `assets.notFoundHandling: "single-page-application"`、`assets.runWorkerFirst: ["/api/*", "/ws/*"]`。配信するディレクトリの指定はなく、Vite のクライアントビルドの出力がそのまま静的ファイルになる（`@cloudflare/vite-plugin`） |
| Durable Object | `worker.exports.GameSession = exports.durableObject({ storage: "sqlite", container })`。Wrangler のマイグレーション履歴の代わりに、クラスの状態（`created` / `deleted` / `renamed` / `transferred`）を宣言する。クラスの作成・削除・改名は `cf deploy` で行い、それ以前の版へのロールバックはできない |
| Containers | `defineContainer({ name, image: { dockerfile, buildContext? }, instanceType, maxInstances, constraints? })` を最上位の `containers` に入れ、Durable Object の `container` から参照する。1 つのコンテナは 1 つの Durable Object にだけ紐付けられる。`cf deploy` が適用する（`--containers-rollout immediate \| gradual \| none`） |
| インスタンスの大きさ | `lite`（1/16 vCPU・256 MiB）/ `basic`（1/4 vCPU・1 GiB）/ `standard-1`〜`standard-4` |
| R2 | `bindings.r2({ name })`。`cf deploy` は既定でバインディングに必要な資源を自動で作る（`--provision`） |
| コマンド | `cf init` / `cf dev` / `cf build`（出力は `.cloudflare/output/v0/`）/ `cf deploy`（`--prebuilt`・`--dry-run`・`--mode`・`--secrets-file`）/ `cf workers versions` / `cf workers deployments` / `cf workers types` / `cf containers build・push・images・ssh` / `cf r2 …` / `cf dns …` / `cf auth login・whoami` |
| 認証 | 手元: `cf auth login`（OAuth）。CI: `CLOUDFLARE_API_TOKEN` と `CLOUDFLARE_ACCOUNT_ID` |
| モード | `--mode <名前>` で設定ファイルを切り替える（Wrangler の `--env` の代わり） |

### ベータ版の制約

| 制約 | 対応 |
|---|---|
| コマンド・設定・ビルド結果の形式は、正式版の前に変わり得る | `cf` を devDependency として**版を固定**する。上げるときは専用の PR で確認する |
| 実行中のログをリアルタイムに流し見る機能（`wrangler tail` 相当）がない | `observability.logs` を有効にして、ダッシュボードで見る |
| シークレットを 1 つずつ登録する機能がない | `cf deploy --secrets-file` でデプロイ時にまとめて渡す |
| Durable Object に紐付けたコンテナはプレビュー環境に対応していない | PR では `cf deploy --prebuilt --dry-run` で検査するだけにする |

### 未確認の事項（実装計画 T-007 で確認する）

- 手元でコンテナを動かすのに Docker が必要か、`cf dev` でコンテナまで動くか
- コンテナの CPU アーキテクチャ（`linux/amd64` を想定）
- 長時間通信がないときに、コンテナがいつ止まるか（止まっても ADR-0017 の仕組みで復元できる）

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
// web/cloudflare.config.ts（概略。正確な書き方は実装時に v1.0.0-beta.28 の型に合わせる）
import { bindings, defineConfig, defineContainer, exports } from "cf/config";
import * as entrypoint from "./worker/index.ts" with { type: "cf-worker" };

const gameServer = defineContainer({
  name: "raft-game-server",
  image: { dockerfile: "../server/Dockerfile", buildContext: "../server" },
  instanceType: "basic",       // 1/4 vCPU・1 GiB。負荷テストの結果で lite も検討
  maxInstances: 50,            // 同時に遊べるゲームの上限
  constraints: { regions: ["APAC"] }, // 日本からの遅延を抑える
});

export default defineConfig(({ mode }) => ({
  worker: {
    name: mode === "demo" ? "raft-game-demo" : "raft-game",
    compatibilityDate: "2026-11-04",
    entrypoint,
    assets: { notFoundHandling: "single-page-application", runWorkerFirst: ["/api/*", "/ws/*"] },
    env: {
      DEMO_CONTROLS: bindings.text(mode === "demo" ? "1" : "0"),
      ALLOWED_ORIGINS: bindings.text("…"),
      REPLAYS: bindings.r2({ name: "raft-game-replays" }), // P1
    },
    exports: {
      GameSession: exports.durableObject({ storage: "sqlite", container: gameServer }),
    },
    observability: { enabled: true, logs: { enabled: true } },
  },
  containers: [gameServer],
}));
```

- モードを 2 つ使う: `production`（一般公開）と `demo`（デモ操作を有効にした別の Worker）
- デプロイ: GitHub Actions で `pnpm install` → `cf build` → PR では `cf deploy --prebuilt --dry-run`、`main` への push で `cf deploy --prebuilt --mode production`
- 費用: Durable Objects と Containers には有料のプランが必要になる想定。料金は構築時に Cloudflare の料金ページで確認し、[要件 NFR-15](../01-requirements.md) を更新する

## 結果

- 良いこと: インフラの定義が `web/cloudflare.config.ts` の 1 ファイルにまとまる。1 ゲーム＝1 コンテナで、ゲームどうしが完全に分かれる
- 悪いこと・受け入れるリスク:
  - ベータ版の変更に追従する必要がある
  - ゲームを始めるたびにコンテナの起動待ちが発生する（ステージ紹介と開会式の間に隠す）
  - 同時に遊べるゲームの数は `maxInstances` で頭打ちになる

## 差し替えるときの影響範囲

`web/cloudflare.config.ts`・`web/worker/`（Worker と Durable Object）・`.github/workflows/deploy.yml`・アーキテクチャ §5・DesignDoc §12。Go のシミュレーション（`server/internal/sim` 以下）と画面（`web/src/`）は変わらない。

## 見直すきっかけ

- `cf` の正式版が出たとき（版を上げ、制約の表を見直す）
- コンテナの起動待ちがデモで目立つとき（共有コンテナの案を再検討する）
- 同時に遊ぶ人数が `maxInstances` を超えそうなとき
