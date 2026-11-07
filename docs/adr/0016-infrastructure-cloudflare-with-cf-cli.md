# ADR-0016: インフラは Cloudflare（Workers ＋ Durable Objects ＋ Containers）、構成は Cloudflare CLI（`cf`）だけで管理する

- 状態: Accepted
- 日付: 2026-11-06 (2026-11-07、T-007 のクラウド実測を追記)
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
      limits: { cpuMs: 500 },                    // T-007 の暫定値。本体の CPU と WS は T-605 で再確認
      env: {
        DEMO_CONTROLS: bindings.text(isDemo ? "1" : "0"),
        CONTAINER_INSTANCE: bindings.text(isDemo ? "standard-1" : "lite"),
        CONTAINER_START_TIMEOUT_MS: bindings.text("10000"), // T-007。/healthz の待機期限
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
- T-605 の本体測定で CPU 500 ms・起動待ち 10 秒に余裕がない、または過大と分かったとき
- inactivity による停止や monitor の寿命の扱いが変わったとき

## T-007 の測定結果 (2026-11-07、cf@1.0.0-beta.28)

人がデプロイと測定を実行した。詳細とコマンド履歴は [実行記録](../verification/t007-cloudflare-spike.md)。
測定コードは main `d862bb78cbfea3fe0e476605b3b6d4996153f818` の `spike/`。
本番の `web/`・`server/` を使わない最小の構成と Raft の負荷模型であり、本体の性能や全 NFR の達成を証明する測定ではない。
公開先は独立した workers.dev のみで、ゾーンの設定は変更していない。

| ID | 項目 | lite | standard-1 | ローカル (cf dev / native Go) | 判断 |
|---|---|---|---|---|---|
| M-1 | 起動時間 (DO 内 p50 / p95 / 最大) | 247 / 1112 / 1112 ms。初回 1112 ms。同時 3 件は全成功、最大 1706 ms | 230 / 396 / 396 ms。初回 359 ms。同時 3 件は全成功、最大 303 ms | cf dev: 166 / 593 / 593 ms。同時最大 853 ms | 各 10 回。同じ DO の再起動を含む。毎回新規のコールド起動 10 回ではない |
| M-2 | 7 ノード・30 分の再計算 | wall 561 ms、CPU 481 ms、53.4 仮想分/秒 | wall 330 ms、CPU 282 ms、90.9 仮想分/秒 | Apple M2 / Go 1.26.9: wall 79.343 ms | 負荷模型では現行 NFR の 3 秒を下回る。本体の復元は T-605 |
| M-3 | 8 倍速 | step p99 1.2 ms、遅れ p99 1.8 ms、cpuUtil 0.008 | step p99 0.275 ms、遅れ p99 1.1 ms、cpuUtil 0.004 | native: step p99 0.289 ms、遅れ p99 3.639 ms、cpuUtil 0.002 | lite の 1 倍速では遅れ p99 202 ms も観測。cpuUtil は測定コードの値 |
| M-4 | CPU 制限 | 50 ms で Worker は 1e9 まで成功、DO は 1e9 で CPU 超過リセット。500 ms の再測定は Worker が 1e9 で 503、DO は 1e9 まで成功 | 50 ms: Worker は 1e9 で 503、DO は 1e9 で CPU 超過リセット | 50 ms 指定でも Worker / DO とも 1e9 まで成功 | 通常処理の CPU と設定値は下表。反復回数や clientMs は CPU 時間ではない |
| M-5 | WS 中継 | 1 KiB RTT p50 / p99 140 / 171 ms | 130 / 335 ms | 0.6 / 1.7 ms | 各 200 回。クラウド両サイズで 16 KiB 20/20、超過は破棄、500/500 順序維持、120 秒無通信後も往復成功 |
| M-6 | DO→コンテナ HTTP 往復 | p50 / p99 3 / 9 ms | 2 / 5 ms | 0 / 1 ms | 各 200/200 回成功。クライアントの RTT と区別 |
| M-7 | SQLite | 100 行 193 ms / 1000 行 227 ms | 204 / 219 ms | 12.9 / 38.4 ms | クライアント時間。コンテナの停止・再起動後に 1100 行を確認。DO の退避後の永続性を検証した実験ではない |
| M-8 | 継承しない DO | PlainStore の SQL、SpikeSession の SQL・container・alarm が成功。継承する ControlStore も成功 | 同左 | SQL・container 成功。ローカル smoke の alarm は対象外 | fetch の形を使う設計の前提を確認。CostGuard / GameSession 本体の試験は残る |
| M-9 | enableInternet: false | DNS / TCP / HTTPS がすべて失敗。true の対照ではすべて成功 | false はすべて失敗 | false でも TCP が成功。DNS / HTTPS は失敗 | 禁止の確認はクラウドの結果を使う。宛先は固定の 3 種類のみ |
| M-10 | 停止 | destroy 38 ms、60 秒アラームで停止。30 秒 inactivity は 60 秒後も稼働 | destroy 48 ms。alarm / inactivity は lite と同じ | destroy 30 ms | 明示的な destroy と上限付きアラームを主な停止手段にする |
| M-11 | crash 検知 / 再起動 | 127 / 1066 ms | 124 / 216 ms | 123 / 297 ms | monitor による検知と再起動を確認。本体の復元処理は別 |
| M-12 | cf dev | — | — | 必須 8 項目すべて成功。起動準備 6 秒 | 機能の smoke に使える。CPU 制限・外部通信禁止・クラウド性能の代用にはしない |
| M-13 | デプロイ | 初回 33.77 秒、CPU 500 ms への更新 7.07 秒 | 同じ Worker・同じイメージ | Docker 上のイメージ 10.5 MB、linux/amd64 | 初回と更新でキャッシュ条件が異なる。サイズはローカルの値 |
| M-14 | 見える資源 | Go 1.26.4、numCPU / GOMAXPROCS=1、cgroup 上限は null、入口 colo=SEA | 同じ Go / CPU 表示、入口 colo=SJC | cf dev: CPU / GOMAXPROCS=8 | colo は入口 Worker の情報。DO / コンテナの配置や、両サイズの割当資源が等しい証拠ではない |

### 通常処理の CPU と暫定値

50 ms のデプロイで、CPU 時間が存在するログから、`iters` を持つ反復負荷試験を除外して集計した。
Worker は `entrypoint` を含むグループ化では現れず、実行モデルとイベントの種類だけに分けて取得できた。
経路別には DO の `$workers.event.path` を使用した。
件数の少ないグループの p99 は参考値であり、本番の分布や余裕を保証しない。

| 対象 | 件数 | CPU p99 / 最大 |
|---|---|---|
| Worker fetch | 73 | 3 / 3 ms |
| DO fetch 全体 | 115 | 112 / 116 ms |
| DO alarm | 10 | 1 / 1 ms |
| SpikeSession /ws | 2 | 116 / 116 ms |
| SpikeSession /ping | 2 | 64 / 64 ms |
| SpikeSession /sql | 4 | 25 / 25 ms |
| SpikeSession /start | 49 | 10 / 10 ms |

`/ws` は 1 電文ごとの CPU の統計ではなく、fetch ログの集計。`/ping` は 1 要求で HTTP 往復を 200 回行い、`/sql` は 100 / 1000 行をまとめて書く測定。
[DO の CPU 制限](https://developers.cloudflare.com/durable-objects/platform/limits/) は HTTP 要求や WS メッセージの受信で予算が更新される。
[Workers の制限](https://developers.cloudflare.com/workers/platform/limits/) には設定値を時々超えることを許容する仕組みもあるため、設定値と累計や反復回数を単純に比較しない。

本番向けの暫定候補は **limits.cpuMs=500**。上の DO fetch p99 の約 4.46 倍にした。
実際に 500 ms をデプロイ済みバージョンのメタデータで確認し、lite / s-25 で CPU と WS を再測定した。
WS は 1 KiB p50 / p99 137 / 146 ms、16 KiB 20/20、超過の破棄、500/500 の順序維持、120 秒無通信後の往復が成功。
Worker の 1e9 反復は 503、DO は今回の範囲で失敗なし。
500 ms は必要な最小値でも、厳密な請求上限でもない。本体の入力・復元・長時間の中継の CPU を T-605 で再測定する。

### 設計への反映と次の検証

| 項目 | T-007 後の判断 |
|---|---|
| CONTAINER_INSTANCE | 一般公開 lite / デモ standard-1 を維持。CostGuard の予約と実際に起動するサイズを一致させる |
| 起動待ち | CONTAINER_START_TIMEOUT_MS=10000 を設定候補にする。同時起動の最大 1706 ms に余裕を持たせた値。start() の戻りを準備完了とせず /healthz を待つ。本体の T-605 で再確認 |
| 停止 | ラウンド終了の destroy と回数上限付きアラームを使う。inactivity のみを終了・費用停止の保証にしない |
| monitor / inactivity | 今回は monitor の Promise が生きたまま。DO の退避を妨げ得るため、60 秒後も動いた理由の候補。本実験は monitor を外した対照ではない |
| DO の形 | 継承しない fetch ハンドラーを維持できる。ADR-0023 の承認状態をこの実験だけで変更しない |
| WS 切断 | ブラウザ側からの切断は確認成功。コンテナ停止時は 1011 を観測。本体の終了理由・再接続・正常終了の close code は T-605 で別途確認 |

[コンテナ API](https://developers.cloudflare.com/containers/api/durable-object-container/) の monitor と inactivity の仕様を踏まえ、DO が作り直された際の monitor / inactivity 設定の付け直しも確認する。
スパイクの 8 倍速 step p99 <=25 ms、復元 <=5 秒、起動 p95 <=5 秒は Issue #15 の提案値。
現行 NFR-03 の復元 <=3 秒などを、この実験で緩和しない。
WS のクライアント RTT と国内向けの p95 要件も別物である。

### 後片付けと後続への引き継ぎ

2026-11-07 19:48:58.765 JST の s-16 の再確認で、使った全 20 セッションの running=false を確認できた。
19:49:07.962 JST の一覧で、対象 Worker・コンテナアプリ・レジストリタグがすべて残っていないことを確認し、削除の終了コードは 0。
アプリ削除には停止の遅延があり得る応答があるため、ダッシュボードでの残存確認、ローカルの後片付け、翌日 11-08 の費用確認は引き続き必要。
完了記録は [実行記録](../verification/t007-cloudflare-spike.md) で更新する。

- T-601 (#65): cpuMs=500、一般公開 lite / デモ standard-1、CONTAINER_START_TIMEOUT_MS=10000 を設定する候補。GUARDS の表示値も CPU 制限に合わせる。
- T-308 (#44) / 復元の後続: 起動時の /healthz、destroy / alarm、monitor の再設定、長時間の WS・終了理由を確認する。
- T-309 (#45) / ADR-0023: 継承しない DO の SQL がクラウドで使えた。実際の CostGuard の予約・精算は別途確認する。
- T-605 (#69): 本体の 8 倍速、30 分の復元、長時間 WS と CPU、停止・再起動、外部通信禁止、費用の帳簿を確認する。

上記は PR で提示する引き継ぎ案。各 Issue へのコメント投稿は未実施。
