# spike/ — cf と Worker＋Durable Object＋コンテナの確認・測定（T-007）

1 ゲーム＝1 Durable Object＝1 コンテナ（`durable_object` 方式）という設計の前提を、最小の構成で確かめて測るための、一時的なプロジェクト。

- AI の部分（このディレクトリのコード・スクリプト）: [#134（T-007a）](https://github.com/K-Kizuku/raft-game/issues/134)
- 人の部分（デプロイ・測定・後片付け・記録）: [#15（T-007）](https://github.com/K-Kizuku/raft-game/issues/15)
- 結果は人が [ADR-0016](../docs/adr/0016-infrastructure-cloudflare-with-cf-cli.md) に「T-007 の測定結果」の節として書く（下の雛形）

本番のコード（`web/`・`server/`）から import しない・されない。本番の `CostGuard`（[ADR-0018](../docs/adr/0018-cost-guard.md)）も使わない（本番の帳簿に測定の費用を入れない）。代わりに下の上限で守る。

## 構成

| パス | 内容 |
|---|---|
| `cloudflare.config.ts` | Worker `raft-game-spike`（`workers.dev` だけ。`domains`・`routes` を書かない）、Durable Object `SpikeSession`（SQLite・コンテナ付き・継承しない）・`PlainStore`（SQLite・継承しない）・`ControlStore`（SQLite・`extends DurableObject`。比べるための対照）、コンテナ `raft-game-spike-server`（`schedulingPolicy: "durable-object"`）。`limits.cpuMs` はビルドの時の環境変数 `SPIKE_CPU_MS`（既定 50、10〜30000） |
| `worker/` | 入口（`router.ts`。トークン・番号・引数を確かめた後でだけ Durable Object を呼ぶ）・`SpikeSession`（`session.ts`）・WS の中継（`relay.ts`）・`PlainStore`（`stores.ts`）・`ControlStore`（`control.ts`）・上限の値（`limits.ts`） |
| `container/` | Go のコンテナ（`/healthz`・`/info`・`/ws`・`/bench/raft`・`/bench/loop`・`/egress`・`/crash`）と Dockerfile（`linux/amd64`・`scratch`）。負荷の模型は上流の `go.etcd.io/raft/v3 v3.7.0` で作る（`loadmodel/`。ゲームのエンジンの代わりではない） |
| `scripts/check.sh` | AI の部分の検査を一度に行う |
| `scripts/check-config.ts` | `cf build` の出力が本番と分かれていることの検査 |
| `scripts/local-smoke.sh` | `cf dev` での確認（M-12） |
| `scripts/measure.ts` | 測定（人が本番の `workers.dev` に対して実行する。ローカルにも使う） |

## 本番との分離と費用の上限

| 要件 | 守り方 |
|---|---|
| 本番と混ざらない | Worker 名 `raft-game-spike`・コンテナ `raft-game-spike-server`。Durable Object のバインディングは自分の Worker のクラスだけ。`check-config.ts` がビルドの出力で確かめる |
| ゾーンに触れない | `domains`・`routes` を書かない。公開は `https://raft-game-spike.<サブドメイン>.workers.dev` だけ。`previewUrls: false` |
| 外から好きな ID で Durable Object を作れない | `/spike/healthz` 以外はトークン `SPIKE_TOKEN`（32 文字以上・空白なし）を確かめてから Durable Object を呼ぶ。名前は `s-1`〜`s-<SPIKE_MAX_SESSIONS>`（既定 30）・`p-1`〜`p-5` だけ。トークンが設定されていなければ `/spike/healthz` 以外はすべて 404 |
| コンテナが止まらずに動き続けない | 起動ごとに `maxRunMs`（既定・上限 5 分）後のアラーム。`alarm()` は `destroy()` するだけで設定し直さない。1 つの Durable Object の起動は 3 回まで。`setInactivityTimeout`（既定 2 分）。`measure.ts` は終わり（失敗・Ctrl-C を含む）に必ず `stop` を呼ぶ |
| 大きいマシンを使わない | `instance` は `lite`・`standard-1` だけ |
| CPU 時間 | `limits.cpuMs`（既定 50）。反復回数・負荷の模型の引数に上限 |
| 最悪の費用 | `standard-1` で 30 個 × 3 回 × 5 分 ＝ 450 分 ≈ 0.56 USD ＋ Durable Object。1 日 1 USD（ADR-0018）を超えない |
| ログの量 | `console.log` は起動・準備完了・停止・アラーム・終了・落ちたことの 1 行ずつだけ（WS の 1 通ごとに出さない） |
| 外部への通信 | コンテナは既定で `enableInternet: false`。`internet=1` は M-9 の対照だけ。宛先はコードに固定した 3 つ（`example.com` の名前解決・`1.1.1.1:443`・`https://example.com/`） |

上限の値（`worker/limits.ts`。Worker と Durable Object の両方で検査する。範囲外・整数でない・重複・知らない引数は 400）:

| 名前 | 既定 | 範囲 |
|---|---|---|
| `SPIKE_MAX_SESSIONS`（バインディング） | 30 | 1〜50。不正ならすべての `/spike/s/*` が 404 |
| `instance` | `lite` | `lite`・`standard-1` |
| `internet` | `0` | `0`・`1` |
| `maxRunMs` | 300000 | 10000〜300000 |
| `inactivityMs` | 120000 | 10000〜300000 |
| `timeoutMs` | 30000 | 1000〜60000 |
| 1 つの Durable Object の起動の回数 | — | 3 まで（4 回目は 429 `start_cap`） |
| `ping` の `count` | 50 | 1〜200 |
| `sql` の `rows` | 100 | 1〜10000 |
| `cpu` の `iters` | — | 1〜1000000000（必須） |
| WS の 1 通 | — | 16384 バイトまで（超えた電文とバイナリは Durable Object が捨てて数える） |

### Worker の API

トークンは `Authorization: Bearer <SPIKE_TOKEN>`。WS の Upgrade だけは `?token=`（それ以外の経路で `?token=` を付けると 401）。応答はすべて JSON（`cache-control: no-store`）。エラーは `{"error":"<code>","message":"…"}`。

| メソッド | パス | 内容 |
|---|---|---|
| GET | `/spike/healthz` | `{"ok":true}`（認証なし。Durable Object を呼ばない） |
| POST | `/spike/cpu?iters=N` | Worker の中で決まった回数の計算（M-4） |
| POST | `/spike/s/{n}/start?instance=&internet=&maxRunMs=&inactivityMs=&timeoutMs=` | コンテナを起動し、`/healthz` が 200 を返すまで待つ（M-1・M-14） |
| POST | `/spike/s/{n}/stop` | `destroy()`（M-10）。動いていなければ `{running:false}` |
| GET | `/spike/s/{n}/status` | `running`・起動とアラームの回数・記録（`runs`）・SQLite の行数・WS の中継の状態 |
| POST | `/spike/s/{n}/ping?count=` | Durable Object からコンテナの `/healthz` を順に呼ぶ（M-6） |
| POST | `/spike/s/{n}/bench/raft?…`・`/spike/s/{n}/bench/loop?…` | コンテナに転送（M-2・M-3） |
| POST | `/spike/s/{n}/egress` | コンテナの外部への通信の確認（M-9） |
| POST | `/spike/s/{n}/crash?restart=0\|1` | コンテナを落とし、`monitor()` が終わるまでの時間と起動し直しの時間（M-11） |
| POST | `/spike/s/{n}/sql?rows=` | 入力ログの形の表に追記（M-7） |
| POST | `/spike/s/{n}/cpu?iters=` | Durable Object の中で決まった回数の計算（M-4） |
| GET | `/spike/s/{n}/ws?token=` | WS の中継（M-5）。コンテナが動いていなければ、Upgrade の前に 409 `not_running` を返す |
| POST | `/spike/plain/{n}`・`/spike/control/{n}` | `PlainStore`・`ControlStore` の数を 1 増やす（M-8） |

## ローカル

リポジトリのルートで実行する。

```bash
pnpm install
bash spike/scripts/check.sh                       # 型検査・テスト・Go・cf build・出力の検査・linux/amd64・cf deploy --prebuilt --dry-run
DEV_SLOT=<n> bash spike/scripts/local-smoke.sh    # cf dev での確認（M-12）。結果は spike/.cloudflare/local-smoke.json
go -C spike/container run . bench raft --nodes 7 --minutes 30           # M-2 の手元の基準値
go -C spike/container run . bench loop --nodes 7 --speed 8 --seconds 20 # M-3 の手元の基準値
```

- `check.sh` と `local-smoke.sh` は Docker が要る（`cf build`・`cf dev` がローカルの Docker でイメージを作る）
- `local-smoke.sh` のポートは `DEV_WEB_PORT + 1`（`scripts/dev-env.sh`。ADR-0019 の予約の範囲）。`cf dev` は `--port` を受け付けないので、`vite.config.ts` が環境変数 `SPIKE_DEV_PORT` を読む（未設定ならエラー）
- `local-smoke.sh` はトークンを毎回作って `spike/.dev.vars`（`spike/.gitignore` で無視）に書き、終わったら消す。ローカルの Durable Object の状態（`spike/.cloudflare/state`）は毎回消してから始める
- `cf dev`（workerd）は Docker の context を見ず、`DOCKER_HOST` がなければ `/var/run/docker.sock` を使う。Docker Desktop（macOS）ではそこにソケットがなく、コンテナの付いた Durable Object への要求がすべて `internal error` になる。`local-smoke.sh` は `DOCKER_HOST` が未設定なら `docker context inspect` の宛先を渡す。手で `cf dev` を動かすときは `DOCKER_HOST="$(docker context inspect --format '{{.Endpoints.docker.Host}}')"` を付ける
- `cloudflare.config.ts` から `worker/` の中のファイルを（entrypoint 以外で）import しない。`cf dev` がそのファイルを読めなくなる（`cf@1.0.0-beta.28` で確認）

## 本番での測定（人の手順。#15 の H-1〜H-8）

AI の部分の PR がマージされた後に、人（`AGENT_ROLE=human`）が行う。AI のエージェントは実行しない。

- [ ] H-1: 準備。Workers の有料プランであること、`cf auth login` 済みで `cf auth whoami` が正しいアカウントを出すこと、アカウントの `workers.dev` のサブドメインがあること、Docker Desktop が動いていることを確かめる。main を最新にして `pnpm install`、`bash spike/scripts/check.sh` が通ることを確かめる
- [ ] H-2: トークンを作る。`printf '{"SPIKE_TOKEN":"%s"}' "$(openssl rand -hex 32)" > spike/spike.secrets.json`（`*.secrets*` で無視される。`git check-ignore spike/spike.secrets.json` で確かめる）。測定のスクリプト用に `export SPIKE_TOKEN=…` も用意する
- [ ] H-3: デプロイ（M-13）。`cd spike && SPIKE_CPU_MS=50 pnpm exec cf build && time pnpm exec cf deploy --prebuilt --secrets-file spike.secrets.json`。所要時間・出力された `workers.dev` の URL・イメージの大きさを記録する。2 回目以降のデプロイ（M-4 で `cpuMs` を変えるとき）は `--containers-rollout immediate` を付ける。**本番の `web/` では実行しない**
- [ ] H-4: 測定。`node spike/scripts/measure.ts --base https://raft-game-spike.<サブドメイン>.workers.dev --instance lite --runs 10 --out spike/.cloudflare/lite.json` と、同じことを `--instance standard-1 --first 16 --out spike/.cloudflare/standard-1.json` で行う（番号が重ならないよう `--first` をずらす）。`--only egress --internet-control` で M-9 の対照も 1 回行う。失敗した項目は、エラーの本文を残す
- [ ] H-5: `limits.cpuMs`（M-4）。H-4 の `cpu` の結果と、ダッシュボードの Workers Logs（Worker と Durable Object の要求ごとの CPU 時間）・Workers のメトリクスを見る。必要なら `SPIKE_CPU_MS` を変えて H-3（`--containers-rollout immediate`）と `--only cpu,ws` を繰り返す。本番の値の目安（ふつうの要求の CPU 時間の p99 の数倍で、WS の中継が切れない値）を決める
- [ ] H-6: 後片付け（費用を止める）。`measure.ts` は終わりに `stop` を呼ぶが、念のため使った番号すべてに `POST /spike/s/{n}/stop` を送り、`GET /spike/s/{n}/status` で `running: false` を確かめる。その後 `cf workers delete raft-game-spike`（ID は `cf workers list` で確かめる）、`cf containers applications list` で `raft-game-spike-server` を探して `cf containers applications delete <id>`、`cf containers images list` で `raft-game-spike-server` のイメージを `cf containers images delete …`。ダッシュボードで Worker・Durable Object・コンテナが残っていないこと、翌日に請求の明細で費用を確かめる。ローカルの `spike/spike.secrets.json` を消し、`docker images` で `cloudflare-build/…/raft-game-spike-server-server` を名前を指定して `docker rmi` する
- [ ] H-7: 結果を書く。`spike/README.md` の雛形で ADR-0016 に「T-007 の測定結果」の節を足し、判断（一般公開の `CONTAINER_INSTANCE` を `lite` のままにするか `standard-1` にするか、`limits.cpuMs` の値、起動の待ち時間の上限、`fetch` の形の Durable Object で問題がないか、`cf dev` でできること・できないこと）を書く。判断が変わるなら、同じ PR で R-02（docs/09-assumptions-and-open-questions.md）・ADR-0018 の見積もり・ADR-0016 の設定例と「見直すきっかけ」を直す（`python3 scripts/check-docs.py`）。この PR は人が作るか、Orchestrator に新しい Issue として頼む
- [ ] H-8: 後続へ伝える。T-601（#65）に `limits.cpuMs` と `CONTAINER_INSTANCE`、T-308（#44）に起動時間・WS の中継の注意点・`monitor()` と `destroy()` の挙動、T-309（#45）と ADR-0023 に M-8 の結果（継承しないクラスで `ctx.storage.sql` が使えたか）、T-605 に本番のスモークテストで見るべき点を、各 Issue のコメントで伝える。M-8 で継承しないクラスが動かなかったときは、ADR-0023 の見直しと、スパイクの `SpikeSession` を `extends DurableObject` に直す小さな Issue を Orchestrator に頼む

### measure.ts の使い方の注意

```text
node spike/scripts/measure.ts --base <URL> [--token-file <path>] [--instance lite|standard-1]
     [--only start,ws,ping,bench,loop,sql,plain,egress,crash,cpu,stop,alarm,inactivity]
     [--runs 10] [--concurrency 3] [--first <n>] [--internet-control] [--idle 120] [--out <path>]
```

- トークンは環境変数 `SPIKE_TOKEN` か `--token-file`（中身はトークンだけ。末尾の改行 1 つは取り除く）。出力・ログに出さない。`--secrets-file` の JSON の値の末尾に改行を入れない（空白・改行を含むトークンは、Worker が「設定されていない」とみなして 404 を返す）
- `--base` は `https://` か `http://localhost`・`http://127.0.0.1` だけ（トークンを平文で外に送らない）
- **Durable Object の番号**: 同じ番号は 3 回までしか起動できない（4 回目は 429）。`measure.ts` は `--first` から順に、この実行の中で残りのある一番小さい番号を使う。すべての項目（`--runs 10`・`--concurrency 3`）で 9 個（`s-<first>`〜`s-<first+8>`）を使い、使った番号は結果の `sessions` に書く。`plain` は `p-1` を使う
  - H-4 の 2 回目（`--first 16`）は `s-16`〜`s-24`。M-9 の対照（`--only egress --internet-control`）は、まだ使っていない番号から始める（例: `--first 10`。`--first` を省くと 1 回目の `s-1` から始まり、起動の回数の上限に当たる）
  - 番号が足りなくなったら（同じ番号で何度も測り直したとき）、`--first` をまだ使っていない番号にずらす（`SPIKE_MAX_SESSIONS` まで）
- 時間: `alarm` は 75 秒、`inactivity` は 60 秒、`ws` は無通信の確認で `--idle` 秒（既定 120、0 で飛ばす）、`loop` は 20 秒 × 3（1・4・8 倍速）待つ。すべてで 10 分ほど
- `inactivity` の結果の `status` の要求は、それ自体が Durable Object を起こす（コンテナへの要求はしない）
- 結果は JSON（`--out`、既定は標準出力）。ADR-0016 に貼る表は JSON の `table` と、`--out` のときは標準出力（なければ標準エラー）に出る。測った列（`lite`・`standard-1`・ローカル）だけを埋めるので、2 回分の表の列を合わせて貼る
- 終了コード: 成功 0、どれかの項目の失敗・`stop` の失敗 1、引数・トークンの誤り 2

### 手で止める

`measure.ts` が `stop` に失敗した番号を出したとき・`kill -9` などで `measure.ts` が `stop` を呼べずに終わったときは、手で止める（トークンを画面に出さない。Ctrl-C なら `measure.ts` が起動したものを止めてから終える）。

```bash
BASE=https://raft-game-spike.<サブドメイン>.workers.dev
for n in 1 2 3; do   # 結果の sessions の番号
  curl -fsS -X POST -H "Authorization: Bearer $SPIKE_TOKEN" "$BASE/spike/s/$n/stop"; echo
  curl -fsS -H "Authorization: Bearer $SPIKE_TOKEN" "$BASE/spike/s/$n/status" | jq '{running, starts, alarms}'
done
```

止まらないときも、起動ごとのアラーム（最長 5 分）と `setInactivityTimeout`（既定 2 分）で止まる。それでも止まらなければ、H-6 の `cf workers delete`・`cf containers applications delete` で消す。

## 後片付け

- Cloudflare: H-6（Worker `raft-game-spike`・コンテナのアプリケーション `raft-game-spike-server`・レジストリの `raft-game-spike-server` のイメージ）
- シークレットのファイル: `spike/spike.secrets.json` を消す（`spike/.dev.vars` は `local-smoke.sh` が消す）
- ローカルの Docker（`prune` しない。名前を指定して消す）:
  - `cf build` のイメージ: `docker images --filter reference='cloudflare-build/*/raft-game-spike-server-server'` で探して `docker rmi cloudflare-build/<ハッシュ>/raft-game-spike-server-server:<タグ>`
  - `cf dev` のイメージ: `docker images --filter reference='cloudflare-dev/spikesession-server-*'` で探して `docker rmi cloudflare-dev/spikesession-server-<ハッシュ>:<タグ>`（`cf dev` を起動するたびに新しいタグができる）
  - `cf dev` が起動ごとに作る中継のコンテナ（イメージ `cloudflare/proxy-everything`、名前 `workerd-raft-game-spike-SpikeSession-<ハッシュ>-proxy`）は、`destroy()` の後も `cf dev` を止めた後も残る。`local-smoke.sh` は動いているものを `docker stop` するので、止まったものを `docker ps -a --filter name=workerd-raft-game-spike --filter status=exited -q` で探して `docker rm <ID>` で消す。イメージ `cloudflare/proxy-everything` は、ほかの作業ツリーの `cf dev` も使うので消さなくてよい
- これらの名前は `cf` が決めるので、ADR-0019 の名前の規則（`make clean-docker`）の外になる

## cf の版・スパイクを消すとき

- `cf` の版を上げる専用の PR では、`spike/package.json` の `cf`・`@cloudflare/vite-plugin` なども `web/package.json` と同じ版にする（同じ版なので `pnpm-lock.yaml` の `packages` は増えない）
- スパイクを消すとき（T-605 の後）は、`spike/` と `pnpm-workspace.yaml` の `- spike` の行を消し、`pnpm install` で `pnpm-lock.yaml` を更新する

## ADR-0016 に貼る表の雛形

```markdown
## T-007 の測定結果（YYYY-MM-DD、cf@1.0.0-beta.28）

| ID | 項目 | lite | standard-1 | ローカル（cf dev） | 判断 |
|---|---|---|---|---|---|
| M-1 | コンテナの起動時間（p50 / p95 / 最大、初回、同時 3 つ） | | | | |
| M-2 | 復元の速さ（7 島・30 分の再計算。手元の PC との比） | | | | |
| M-3 | 8 倍速（1 ステップの p99・遅れの p99・CPU の使用率） | | | | |
| M-4 | limits.cpuMs（止まる反復回数・DO に効くか・ふつうの要求の CPU 時間） | | | | |
| M-5 | WS の中継（往復の p50 / p99、16 KiB、500 件、無通信 120 秒、切断） | | | | |
| M-6 | DO とコンテナの HTTP の往復（p50 / p99） | | | | |
| M-7 | DO の SQLite（100 行・1,000 行、再起動の後） | | | | |
| M-8 | DurableObject を継承しないクラス（sql・container・alarm） | | | | |
| M-9 | enableInternet: false（DNS・TCP・HTTPS。対照） | | | | |
| M-10 | 停止（destroy・アラーム・setInactivityTimeout） | | | | |
| M-11 | 落ちたときの検知と再起動 | | | | |
| M-12 | cf dev での動作 | — | — | | |
| M-13 | デプロイ（時間・イメージの大きさ・ログ） | | | — | |
| M-14 | コンテナから見た資源（CPU・メモリ・env・colo） | | | | |
```
