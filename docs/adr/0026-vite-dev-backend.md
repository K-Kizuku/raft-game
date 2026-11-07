# ADR-0026: standalone の開発サーバーでは `@cloudflare/vite-plugin` を読み込まず、Vite の `server.proxy` で `/api`・`/ws` を Go に中継する

- 状態: Proposed
- 日付: 2026-11-07
- 決めた人: オーナーの判断待ち
- 関連: T-504a（#141）・T-302（#38）・T-504（#64）、[ADR-0016](./0016-infrastructure-cloudflare-with-cf-cli.md)、[ADR-0019](./0019-parallel-local-development.md)

## 背景

`make dev` は、Go の `standalone` モードと Vite を `DEV_SLOT` のポートで起動する（ADR-0019。ポートの計算の正本は `scripts/dev-env.sh`）。ところが `web/vite.config.ts` は `@cloudflare/vite-plugin` を必ず読み込み、`web/cloudflare.config.ts` の `assets.runWorkerFirst: ["/api/*", "/ws/*"]` のとおり、ブラウザの `/api/*` と `/ws/*` を Worker（Miniflare）に渡していた。T-302（#38）が作る Go の受け口（`/api/*`・`/ws/sessions/{id}`）には届かないので、ブラウザから standalone で 1 ラウンド遊べなかった。

Vite の `server.proxy` を足すだけでは足りない。vite 8.3.6・@cloudflare/vite-plugin 2.0.0-beta.v.12 の実物で、次の 2 点を確かめた。

- プラグインは `configureServer` の中で直接 `middlewares.use(cloudflarePreMiddleware)` を登録する。Vite は `configureServer` を呼んだ後に `proxyMiddleware` を登録するので、`/api/*` はプロキシより先に Worker に渡る
- プラグインは `httpServer.on("upgrade")` で、Vite の HMR 以外の WebSocket をすべて Miniflare に渡す。Vite のプロキシの `upgrade` の処理と同じソケットを取り合う

## 検討した選択肢

### 案 a: standalone のときはプラグインを外し、`server.proxy` で中継する

- 長所: Vite の標準の機能だけで済み、プラグインの内部の順序に依存しない。WebSocket の取り合いが起きない。本番のコード（Worker）を変えない
- 短所: standalone の開発サーバーでは Worker を通らないので、Worker の処理（署名の確認・レート制限など）は確かめられない（`DEV_BACKEND=worker` で別に確かめる）。設定が 2 通りになる

### 案 b: プラグインを残し、`enforce: "pre"` の自作のプラグインで中継の middleware と `upgrade` の処理を先に登録する

- 長所: Worker と Go の両方を 1 つの開発サーバーで動かせる
- 短所: プラグインの内部の登録の順序に依存し、版を上げると壊れうる。`upgrade` のソケットの取り合いが残る

### 案 c: Worker（`cf dev`・Miniflare）から Go に中継する

- 長所: 本番と同じ経路（Worker → Go）を通せる
- 短所: Worker に開発用の経路が要り、本番のコードが増える。Miniflare からローカルの Go への接続の設定も要る

## 決定

案 a。`DEV_BACKEND=standalone` の Vite の開発サーバーでは `@cloudflare/vite-plugin` を読み込まず、`server.proxy` で `^/api(?:[/?]|$)` と `^/ws(?:[/?]|$)`（WebSocket を含む）を `http://127.0.0.1:<DEV_SERVER_PORT>` に中継する。

- 切り替えは環境変数 `DEV_BACKEND`（`standalone`・`worker`。未設定は `worker`）。決めるのは `web/src/devserver/backend.ts` の純粋な関数 `resolveDevBackend`。誤りは Vite の起動を止める
- `changeOrigin: false` にして、`Host` と `Origin` をブラウザの値のまま Go に渡す。Go は `ALLOWED_ORIGINS` で照合する
- ビルド（`vite build`・`cf build`）では `DEV_BACKEND` を読まない（手元の環境変数で本番のビルドを変えない）。`vite preview` で `standalone` は誤り
- `make dev`（`scripts/dev-env.sh`）は、`DEV_BACKEND` が未設定なら Go（`server/cmd/server/main.go`）があれば `standalone`、なければ `worker` を Vite に渡す。`worker` のときは Go を起動しない
- `worker` のときの設定（`persistState` を含む）は、この判断の前と同じ

## 結果（この判断によって起きること）

- 良いこと: `make dev` で、ブラウザから Go の standalone に `/api`・`/ws` が届き、1 ラウンド遊べる。`make demo-local`（T-504）も `DEV_BACKEND=standalone DEV_SERVER_PORT=<Go のポート>` で同じ中継を使える
- 悪いこと・受け入れるリスク: standalone の開発サーバーでは Worker の処理を通らない。Go がまだ待ち受けていない・落ちたときは、Vite のプロキシが `502` を返し、WebSocket は閉じる（`make dev` は止めない）

## 差し替えるときの影響範囲

- `web/vite.config.ts`
- `web/src/devserver/`（`backend.ts`・`backend.test.ts`）
- `scripts/dev-env.sh`（`cmd_dev`）と `scripts/tests/test_dev_env.py`
- `make demo-local`（T-504）

## 見直すきっかけ

- `@cloudflare/vite-plugin` が中継の設定（Worker を通さずに別のサーバーへ渡す設定）を持つようになったとき
- `@cloudflare/vite-plugin` や vite の版を上げたとき（middleware と `upgrade` の登録の順序が変わっていないか確かめる）
