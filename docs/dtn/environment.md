# DTN の環境（Cloudflare）・費用・人がする作業

DTN-12（#169）・DTN-13（#170）の契約。判断は [ADR-D002](./adr/D002-environment-and-branch.md)。

## 1. 構成

| 項目 | prod・demo（`main`、変えない） | DTN の環境（`dtn/main`） |
|---|---|---|
| ホスト名 | `raft.kizuku-hackathon.work`・`raft-demo.kizuku-hackathon.work` | `raft-dtn.kizuku-hackathon.work` |
| Worker | 今のもの | 別の Worker（名前の例: `raft-game-dtn`） |
| Durable Object | `GameSession`・`CostGuard` | `DtnSession`・`ActorHost`・`CostGuard`（別の名前空間。prod の `CostGuard` とは別のインスタンス） |
| Container の image | `server` | `dtnactor`（`ROLE=coordinator|island|ferry`） |
| 費用の上限 | 1 日 1 USD・1 か月 30 USD（prod・demo で共有） | **1 日 1 USD・1 か月 10 USD**（DTN の専用） |
| 起動できる人 | 誰でも（Turnstile・レート制限） | 誰でも（Turnstile・レート制限）。全ステージ |
| シークレット | `SESSION_SIGNING_KEY`・`ADMIN_TOKEN`・`TURNSTILE_SECRET` | 同じ名前で**別の値**＋ `DTN_CONTACT_KEY` |
| デプロイ | CI（`main` の push） | CI（`dtn/main` の push）だけ |

- `dtn/main` の `web/cloudflare.config.ts` では、**どのモードでも Worker の名前を DTN 用にし、production・demo のモードを消す**。`dtn/main` から誤って `cf deploy` しても prod・demo を上書きしないようにする（失敗しても安全な側に倒す）
- コストガードの G1〜G6 は、DTN の環境でもすべて有効にする。G3（署名の確認）は無効にしない
- Container の外向きの通信は禁止（`enableInternet: false`）
- **`DEMO_CONTROLS=1` にする**。全ステージ（demo-goto・demo-izu も）を選べるようにするため。そのため、DTN の環境では誰でもデモの操作（「次の出来事まで」・速度 8 倍・嵐を起こす）を使える。費用は時間の上限で決まり、これらの操作で増えないので受け入れる
- ステージの一覧（カタログ）に、船の ID の列と定義したイベントの種類を足す（Actor の名前と、対象外のステージの判定に使う。DTN-12）。形は各ステージの `dtn: {actors: ["island:1", …, "ferry:f1", …], eventKinds: [...], unsupportedEvents: [...]}`（`server/cmd/stagecatalog`。`demo.storm` はデモの嵐。対応するのは `storm`・`demo.storm` だけ）。Worker は `dtn` をブラウザに返さない

### 1.1 `dtn/main` の `web/cloudflare.config.ts`（DTN-12 で実装）

| モード | Worker | ホスト名 | Container（DO のクラスごと） | `SESSION_RATE_LIMIT` の namespace |
|---|---|---|---|---|
| `dtn`（`--mode` なしも） | `raft-game-dtn` | `raft-dtn.kizuku-hackathon.work`（`ALLOWED_ORIGINS` もこれだけ） | `raft-dtn-coordinator`（`DtnSession`）・`raft-dtn-actor`（`ActorHost`） | `1004` |
| `development` | `raft-game-dtn-dev` | なし | なし（`DtnSession`・`ActorHost` も置かない） | `1005` |
| `local-container` | `raft-game-dtn-dev` | なし | `raft-dtn-dev-coordinator`・`raft-dtn-dev-actor` | `1005` |

- `production`・`demo` は「知らないモード」で失敗する。どのモードでも `GameSession`・R2（`raft-game-replays`）・prod の `CostGuard` を参照しない。`web/scripts/check-build-output.ts` が、出力に prod・demo の Worker の名前・ホスト名・`GameSession` があれば失敗にする
- 2 つの Container のアプリケーションは、どちらも `../server/Dockerfile.dtnactor`（`linux/amd64`・image の名前 `dtnactor`・`schedulingPolicy: "durable-object"`）。起動は `ctx.container.start({image, instance: "lite", enableInternet: false, env: {ROLE, PORT: "8080"}})`。計測の入口（§7）の Coordinator には `BOT`・`SPEED` も渡す
- `GUARDS`: `budget {monthlyUsd: 10, dailyUsd: 1}`・`gameTime {maxMinutes: 15, maxAlarms: 20}`・Turnstile とレート制限は有効・`unitCostUsd`（§3）。`DEMO_CONTROLS=1`・`CONTAINER_INSTANCE=lite`
- シークレット: `SESSION_SIGNING_KEY`・`TURNSTILE_SECRET`・`ADMIN_TOKEN`・`DTN_CONTACT_KEY`（§5.2）
- 全体の起動の上限は `DTN_START_TIMEOUT_MS`（任意の text。既定 90 秒、10 秒〜5 分。Q-COLD で見直す）。各 `ActorHost` の Container の準備を待つ上限は 60 秒

## 2. 1 ゲームの費用の見積もり

ADR-0018 の単価（30 分の最悪の費用: `lite` の Container 約 0.0036 USD、Durable Object 約 0.0028 USD）で計算する。Container の数は「島＋船＋Coordinator」、Durable Object の数は「島＋船（ActorHost）＋ DtnSession」。

| ステージ | 島・船 | Container | DO | 30 分の予約 | 15 分の予約 | 1 日 1 USD で遊べる数（15 分） |
|---|---|---|---|---|---|---|
| stage1 | 3・1 | 5 | 5 | 約 0.032 USD | 約 0.016 USD | 約 60 |
| stage2 | 3・2 | 6 | 6 | 約 0.038 USD | 約 0.019 USD | 約 50 |
| demo-goto | 3・3 | 7 | 7 | 約 0.045 USD | 約 0.022 USD | 約 45 |
| demo-izu | 6・6 | 13 | 13 | 約 0.083 USD | 約 0.042 USD | 約 24 |
| stage3 | 6・6 | 13 | 13 | 約 0.083 USD | 約 0.042 USD | 約 24（欠航に対応する #365 の後） |

- ラウンドは最長 4 分（stage3）なので、DTN の環境の 1 ゲームの時間の上限（G2）は **15 分**から始める（予約が半分になる）
- Container の大きさは、すべて `lite` から始める。足りないと分かったら、DTN-12 の測定の結果で決める
- RPC・保存・Workers Logs の費用は、この表に入っていない。DTN-12 で測って記録する

## 3. CostGuard の拡張

- `POST /reserve` に、使う資源の内訳を渡せるようにする: `{sessionId, units: [{kind: "container", instance: "lite", count: 5}, {kind: "do", count: 5}]}`。予約額は内訳の合計 × 時間の上限
- **すべての Actor を起動する前に**予約する。予約が通らなければ、1 つも起動しない
- 起動の途中で失敗したら、起動した分をすべて止め（`destroy()`）、実際に動いた時間で精算する。精算は冪等にする（同じセッションの 2 回目の精算は何もしない）
- 単価のキー（DTN-12 で決めた）: `GUARDS.unitCostUsd` の `container.lite`・`container.standard-1`・`do`（30 分あたりの USD。ADR-0018 の 0.0036・0.037・0.0028）。今の `lite`・`standard-1`（コンテナ 1 つ＋ DO 1 つの合計）も残し、今の `{sessionId, instance}` の予約に使う。内訳の単価が書いていない種類を予約すると `sessions_disabled`（安全側）
- 予約額 = ceil(Σ count × 単価 × `maxMinutes` / 30)（µUSD の整数）。帳簿の行は `instance = "units"`・`units` 列に内訳の JSON・`rate_micro` に単価の合計を持つ。精算は `rate_micro` × 動いた秒数（今と同じ）
- 予約の内訳は `createDtnSession`（`web/worker/dtn/create.ts`）が作る: Container = 島＋船＋1（Coordinator）、DO = 島＋船（`ActorHost`）＋1（`DtnSession`）。`instance` は `CONTAINER_INSTANCE`
- 動いた秒数は、`DtnSession` の `/init` から全停止を確かめるまで（ゲーム全体で 1 つ。安全側）

## 4. ゲームの一生

1. `POST /api/sessions`（DTN の入り口）: レート制限 → Turnstile → ステージの検査（対象外のイベント → `dtn_unsupported_stage`）→ セッションの発行（署名）→ 予約 → `DtnSession` の初期化
2. `DtnSession` が、アラームを設定してから（今の `GameSession` と同じ）、Coordinator と全 `ActorHost` を並行に起動する。全員が `init` に答えたら `ready`
3. ブラウザが `/connect` で WS をつなぎ、遊ぶ（外部の電文は今と同じ）
4. 終わったら（`round_ended`・`time_limit`・`idle`・`alarm_cap`・`start_timeout`・`dtn_actor_lost`・`dtn_restore_failed`・`dtn_limit`・`dtn_internal`）、`DtnSession` が全 `ActorHost` に `shutdown` を送り、Coordinator を止め、精算する。止められなかったものは、アラームで 30 秒ごとに再試行する（アラームの回数の上限の中で）
5. Coordinator が止まって作り直しても、片付けを最初からやり直せる（D-8）

DTN-12 で決めた細部:

- 全体の起動の上限（`DTN_START_TIMEOUT_MS`）までに Coordinator の `ready` がなければ、アラームで `start_timeout`。1 つの Actor の起動の失敗・Coordinator の起動の途中の停止も `start_timeout` で全停止する
- 片付けは `actors` の表の状態（`starting`・`started`・`stop_failed` …）から、まだ止まっていない `ActorHost` にだけ `/shutdown` を送り直す。`ActorHost` は自分の期限（ゲームの期限＋60 秒）のアラームでも Container を止めて保存を消す（`DtnSession` が止まっても残さない）
- `idle`（ブラウザの切断）は DTN-13 で WS をつなぐときに入れる。DTN-12 の計測の入口のセッションにはブラウザがないので、時間の上限で終わる

## 5. 人がする作業

AI エージェントには許されていない操作（ワークフロー・シークレット・Rulesets・ゾーンの設定）なので、オーナーが行う。手順は表の下（§5.1〜§5.3）。

| # | 作業 | いつまでに |
|---|---|---|
| ~~H1~~ | ~~`dtn/main` の Ruleset~~ → 設けない（[D002](./adr/D002-environment-and-branch.md)） | — |
| H2 | （任意）`dtn/main` の push でも CI が動くよう、`dtn/main` の `.github/workflows/ci.yml` の対象に `dtn/main` を足す（`dtn/main` の `workflow-sources` を直し、`install-workflows.sh` で取り込む） | 必要になったら |
| H3 | DTN の環境のデプロイのワークフロー（`dtn/main` の push → 検査（`make test`）→ `cf deploy --prebuilt --mode dtn`）を取り込む。PR を使わないので、検査に通らなければデプロイしない形にする | DTN-12 |
| H4 | DTN の環境のシークレット（`SESSION_SIGNING_KEY`・`ADMIN_TOKEN`・`TURNSTILE_SECRET`・`DTN_CONTACT_KEY` の新しい値）を GitHub に登録する | DTN-12 |
| H5 | `raft-dtn.kizuku-hackathon.work` の DNS・Turnstile のウィジェット・WAF のレート制限（**今の prod・demo のルールは変えず、DTN 用のルールを別に足す**）・予算アラート | DTN-12 |
| H6 | MVP の凍結のタグ（例: `mvp-final`）を `main` に付ける | MVP の完成の後 |

### 5.1 H3: デプロイのワークフロー

`dtn/main` の `.github/workflow-sources/` に、今の `deploy.yml` を元にした `deploy-dtn.yml` を書き、H2 と同じ取り込みのスクリプトで `.github/workflows/` に取り込む（`main` の `deploy.yml` は `main` だけを対象にしたまま。変えない）。

1. きっかけ: `push`（`branches: [dtn/main]`）と `workflow_dispatch`（`dtn/main` だけ）。`concurrency` を 1 つにする
2. 検査のジョブ: `make test`（Go と web）・`python3 scripts/check-docs.py`。失敗したらデプロイのジョブに進まない（`needs`）
3. デプロイのジョブ（GitHub の Environment を `dtn` にして Secrets を分ける）:
   - `make sync-data` → `BUILD_ID=git:<sha>` で `pnpm -C web exec cf build --mode dtn`
   - `node web/scripts/check-build-output.ts --mode dtn web/.cloudflare/output/v0`（prod・demo の名前があれば止まる）
   - `node web/scripts/deploy-secrets.ts --mode dtn --out "$RUNNER_TEMP/secrets.json"`（環境変数 `CF_SECRET_SESSION_SIGNING_KEY_DTN`・`CF_SECRET_TURNSTILE_SECRET_DTN`・`CF_SECRET_ADMIN_TOKEN_DTN`・`CF_SECRET_DTN_CONTACT_KEY`。prod・demo の署名の鍵と同じ値なら止まる）
   - `pnpm -C web exec cf deploy --prebuilt --mode dtn --secrets-file "$RUNNER_TEMP/secrets.json"`（`CLOUDFLARE_API_TOKEN`・`CLOUDFLARE_ACCOUNT_ID`）→ ファイルを消す
   - `node web/scripts/wait-healthz.ts` で `https://raft-dtn.kizuku-hackathon.work/api/healthz` の `build` を確かめる
4. 手元の確認（デプロイしない）: `DEV_SLOT=<n> bash web/scripts/check-cloudflare.sh`（Docker が要る。`dtn`・`local-container`・`development` を作って検査し、`dtn` の dry-run の表を確かめ、`production`・`demo` が「知らないモード」で失敗することを確かめる）

### 5.2 H4: シークレット

GitHub の Environment `dtn` の Secrets に、prod・demo と**別の新しい値**で登録する（例: `openssl rand -base64 48 | tr -d '\n'`。32〜256 文字の空白を含まない ASCII）。

| GitHub の Secrets | Worker のシークレット | 規則 |
|---|---|---|
| `DTN_SESSION_SIGNING_KEY` | `SESSION_SIGNING_KEY` | 32〜256 文字。prod・demo と同じ値は `deploy-secrets.ts` が断る |
| `DTN_TURNSTILE_SECRET` | `TURNSTILE_SECRET` | DTN 用の Turnstile のウィジェット（H5）のシークレットキー |
| `DTN_ADMIN_TOKEN` | `ADMIN_TOKEN` | 32〜256 文字。管理用 API・計測の入口（§7）に使う |
| `DTN_CONTACT_KEY` | `DTN_CONTACT_KEY` | 32〜256 文字。Contact の署名（[handoff-and-contact.md §2.1](./handoff-and-contact.md#21-形)） |
| `CLOUDFLARE_API_TOKEN`・`CLOUDFLARE_ACCOUNT_ID` | — | 今のものを使える（同じアカウントのとき。Q-ACCOUNT） |

Variables の `TURNSTILE_SITE_KEY` も、DTN 用のウィジェットの値を Environment `dtn` に置く。

### 5.3 H5: ゾーン・Turnstile・WAF・予算

1. DNS: `raft-dtn.kizuku-hackathon.work` は `cf deploy` の `domains` が Custom Domain として作る。手でレコードを作らない（ほかのホスト名・ワイルドカードのレコードを足さない）
2. Turnstile: DTN 用のウィジェットを新しく作り、ホスト名に `raft-dtn.kizuku-hackathon.work` だけを入れる（prod・demo のウィジェットを変えない）
3. WAF のレート制限: 今の prod・demo のルールは変えず、`http.host eq "raft-dtn.kizuku-hackathon.work"` かつ `/api/sessions`・`/api/admin/dtn/probe` に DTN 用のルールを別に足す
4. 予算アラート: Cloudflare の課金の通知を、DTN の分が分かるように足す（CostGuard の上限は 1 日 1 USD・1 か月 10 USD。帳簿は `GET /api/admin/guards`）
5. 発表の前後は、`PATCH /api/admin/guards {"acceptGames": false, "reason": "…"}` で DTN の環境の受付を止める（Q-ACCOUNT）

## 6. 運用（DTN-13 で詳しく書く）

- 起動・遊ぶ・障害の注入（DTN-16 の管理者のボタン）・復元・全停止の手順
- 受付の一括停止（管理用 API）。発表の前後は、DTN の環境の受付を止められるようにする（[open-questions.md](./open-questions.md) の Q-ACCOUNT）
- ログ: session・actor・bundle・transfer・contact・eventSeq・epoch を上限付きで出す。シークレット・Contact の署名・MoQ のトークンを含む URL は出さない
- 単一プロセス版に戻す方法: DTN の環境では戻さない（MVP は prod・demo にある）。DTN の環境を止めるだけでよい

## 7. 計測の入口（管理者だけ。DTN-12）

`web/worker/routes/dtn-probe.ts`。`ADMIN_TOKEN` がなければ 404、`Authorization: Bearer <ADMIN_TOKEN>` が違えば 401。

- `POST /api/admin/dtn/probe {stageId, seed?, bot: "greedy", speed?}`（`speed` は 1〜8。既定 1）→ `201 {sessionId, stageId, seed, probePath}`。`createDtnSession` で作る（予約を通る。対象外のステージは `400 dtn_unsupported_stage`）。Coordinator の Container に `BOT=greedy`・`SPEED` を渡し、ブラウザなしで遊ばせる（`server/cmd/dtnactor` の側は DTN-05）
- `GET /api/admin/dtn/probe/{sessionId}` → 署名を確かめてから `DtnSession` の `GET /probe` を呼ぶ。返す値: `phase`・`endReason`・`settled`・`startup {readyMs, coordinator, hosts}`（起動の時間。`coordinator`・`hosts` は `{count, p50Ms, p95Ms}`）・`dispatch`（上限の往復を含む dispatch 1 回の実時間の p50・p95）・`handoff`（`transfer.prepare` を含む dispatch の実時間）・`storage {coordJournalRows, coordJournalBytes, hosts: [{actor, state, startMs, status}]}`（`status` は各 `ActorHost` の `host.status` の `data`。`journalRows`・`storedBytes`・`restoreMs` を含む）・`restores`・`coordinatorStarts`・`bot`（Coordinator の `stats`）・`result`

測った結果は [open-questions.md](./open-questions.md) に書く。
