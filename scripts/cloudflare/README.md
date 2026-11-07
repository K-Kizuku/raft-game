# scripts/cloudflare — ゾーンのルールと Turnstile のウィジェット（T-606）

コストガード G4「入り口での bot の遮断」（[ADR-0018](../../docs/adr/0018-cost-guard.md)）のうち、ゾーンと Turnstile の設定を `cf` で扱うスクリプトを置く。構成は `cf` だけで管理し、ゾーンの設定は 2 つのホスト名だけに効かせる（[ADR-0016](../../docs/adr/0016-infrastructure-cloudflare-with-cf-cli.md)）。

| ファイル | 中身 |
|---|---|
| `zone_rules.json` | ゾーンに置くルールの宣言（AI クローラーの遮断・`/api/` と `/ws/` のレート制限） |
| `cf_zone.py` | 上のルールを確かめる（`--check`）・反映する（`--apply`）・外す（`--remove`）。既定は `--dry-run` |
| `cf_turnstile.py` | Turnstile のウィジェット（2 つのホスト名に 1 つ）を確かめる・作る（直す）。既定は `--dry-run` |

テストは `scripts/tests/test_cloudflare_zone.py`（偽の `cf` を使う。ネットワークも認証も使わない）。

```bash
python3 -m unittest discover -s scripts/tests -p 'test_cloudflare_zone.py'
```

## 何を置くか

| もの | 場所 | 中身 |
|---|---|---|
| `raft-game-zone-ai-crawlers` | ゾーンの WAF のカスタムルール（フェーズ `http_request_firewall_custom`） | `cf.verified_bot_category` が `AI Crawler` の要求を `block` |
| `raft-game-zone-ratelimit-api` | ゾーンのレート制限のルール（フェーズ `http_ratelimit`） | `/api/`・`/ws/` への要求を、IP ごと（`cf.colo.id`・`ip.src`）に 10 秒に 50 回まで。超えたら 10 秒 `block` |
| ウィジェット `raft-game` | アカウントの Turnstile | モード `managed`、ドメインは 2 つのホスト名。サイトキーとシークレットキーを 1 組にする（画面は T-413、Worker は T-310） |

### 2 つのホスト名に限る理由

ゾーン `kizuku-hackathon.work` には、このゲームのほかのサービスも載っている。ゾーンにかかる設定は、すべて次の条件で始め、`raft.kizuku-hackathon.work`（一般公開）と `raft-demo.kizuku-hackathon.work`（デモ用）だけに効かせる（ADR-0016・ADR-0018・[docs/09](../../docs/09-assumptions-and-open-questions.md)）。

```text
(http.host in {"raft.kizuku-hackathon.work" "raft-demo.kizuku-hackathon.work"}) and (…)
```

`cf_zone.py` は、どのモードでも最初に `zone_rules.json` を手元で検査し、この形でない式（ホスト名の条件がない・`*.kizuku-hackathon.work`・3 つ目のホスト名・`HOST_GUARD and (x) or (y)` のように条件の外に出る式）があれば、`cf` を呼ばずに終了コード 1 で止まる。

### `bot-management` を使わない理由

`cf bot-management update`（`--ai-bots-protection` など）は、ゾーン全体の設定（`PUT /zones/{zone_id}/bot_management`）で、ホスト名に限れない。同じゾーンのほかのサービスにも効いてしまうので使わない。AI クローラーの遮断は、ホスト名の条件を付けた WAF のカスタムルール（`cf.verified_bot_category`）で行う。Bot Fight Mode・`zones settings` などのゾーン全体の設定も変えない。

### `ref` の約束

- `cf_zone.py` は、`ref` が `raft-game-zone-` で始まるルールだけを扱う（作る・直す・消す）。それ以外のルール（ゾーンのほかのサービスのもの）は数えるだけで、決して変えない・消さない
- 緊急用のルール（T-607）は別の接頭辞（`raft-game-emergency-`）にする。`cf_zone.py --apply`・`--remove` はこれに触れない
- エントリーポイントのルールセット全体を置き換える `cf rulesets entrypoint update` は、そのフェーズのエントリーポイントがまだないとき（ほかのルールがなく、置き換えても失わないとき）だけ使う。あるときはルールを 1 つずつ足す・直す・消す

## 値の理由

レート制限の値は、Cloudflare のどのプランでも使える形にしている（Free プランの制約: `period` は 10 秒、`mitigation_timeout` は 10 秒、`characteristics` は `cf.colo.id` と `ip.src`）。

- 1 つの IP から 10 秒に 50 回（5 回／秒）を超えた `/api/`・`/ws/` の要求を 10 秒止める
- 画面の静的ファイルは数えない（パスの条件で `/api/`・`/ws/` に限る）
- 人が遊ぶときの要求（`GET /api/stages`・`POST /api/sessions`・WebSocket の接続 1 本。WebSocket の中の電文は数えない）と、T-603 のデプロイの後の `/api/healthz`（GitHub のランナーから 15 秒に 1 回）には十分に余裕がある
- 細かい制限は Worker の中の `POST /api/sessions` のレート制限（1 分に 10 回、ADR-0018）が受け持つ。このルールは、Worker の呼び出しの費用を抑える粗い制限
- `description` は ASCII にする（ダッシュボードや API での文字化けを避ける）

### 値を変えるとき

1. `zone_rules.json` を直す PR を出す（`validate_config` とテストが通ること）
2. マージの後、人が `python3 scripts/cloudflare/cf_zone.py --check` で差とサーバー側の検査（`--validate-only`）を確かめる
3. 人が `python3 scripts/cloudflare/cf_zone.py --apply` で反映する

Free プランの制約を外す値（`period` 60 など）は、ゾーンのプランを確かめてから（`--check` の最初の行にプランが出る）。手元の検査（`validate_config`）も合わせて直す。ホスト名の条件は外さない。

## 使い方

ルートで実行する。`cf` は `web/node_modules/.bin/cf`（`pnpm install` の後）を使う。環境変数 `CF` があれば、その値（`shlex.split` する）を使う。

| モード | 何をするか | 認証 | 書き込み |
|---|---|---|---|
| `--dry-run`（既定） | 手元の検査をし、書き込むはずのコマンドを `cf` の `--dry-run` で呼び、送る内容（`method`・`url`・`body`）を出す | 要らない | しない |
| `--check` | 今の状態を読み、何を足す・直す・消すかを出す。サーバー側の検査（`--validate-only`）も行う | 要る | しない |
| `--apply` | `--check` の後、差があれば反映し、反映の後にもう一度読んで差がないことを確かめる | 要る | する |
| `--remove`（`cf_zone.py` だけ） | `raft-game-zone-` で始まるルールだけを消す（ルールセット自体は残す） | 要る | する |

```bash
python3 scripts/cloudflare/cf_zone.py                 # 送る内容を見る（認証なし）
python3 scripts/cloudflare/cf_zone.py --check
python3 scripts/cloudflare/cf_zone.py --apply         # 標準入力に yes を求める（--yes で省く）
python3 scripts/cloudflare/cf_zone.py --remove

export CLOUDFLARE_ACCOUNT_ID=<アカウント ID>           # cf_turnstile.py の --check・--apply に要る
python3 scripts/cloudflare/cf_turnstile.py            # 送る内容を見る（認証なし）
python3 scripts/cloudflare/cf_turnstile.py --check
python3 scripts/cloudflare/cf_turnstile.py --apply --secret-out "$(mktemp -d)/turnstile-secret"
```

- 終了コード: 成功 0、検査の違反・`cf` の失敗・反映の後の差・確認の拒否 1、使い方の誤り（モードを 2 つ以上・`--apply` に `--secret-out` がない）2
- `--apply`・`--remove` は、`--yes` がなければ標準入力に `yes` を求める。標準入力が端末でなく `--yes` もなければ、何も書かずに 1
- `--apply` が途中で失敗したら、そこで止まる。済んだ分は残るので、もう一度 `--check` で状態を見てやり直す（同じ `ref` で比べるので、何度実行しても同じ結果になる）
- `--check` のサーバー側の検査で止まったとき（プランで使えない式・ルールの数の上限など）は、何も書いていない。`cf` の文を Issue に貼り、値を決め直す
- `cf_turnstile.py --apply` の `--secret-out` は、まだ存在せず、親のディレクトリがあり、リポジトリの外のパスにする（違えば何も書かずに 1）。シークレットキーはこのファイル（権限 0600・末尾に改行なし）にだけ書き、画面にもログにも出さない。ウィジェットが既に同じ内容であれば作らずに、シークレットキーだけを書き出す（失くしたときの取り直し）
- 同じ名前（`raft-game`）のウィジェットが複数あるときは 1 で止まる。`cf turnstile widgets list` で確かめ、要らないものを人が消す

## 外し方

1. ゾーンのルール: `python3 scripts/cloudflare/cf_zone.py --remove`（`raft-game-zone-` で始まるルールだけを消す。エントリーポイントのルールセットとほかのルールは残る）
2. Turnstile のウィジェット: 先に管理用 API で `turnstile.enabled` を `false` にする（[docs/06-protocol.md](../../docs/06-protocol.md) §2。ウィジェットを消すと、Worker は Turnstile の確認に失敗し、`POST /api/sessions` がすべて `403 turnstile_failed` になる）。そのあと `pnpm -C web exec cf turnstile widgets delete <サイトキー>`

## シークレットキーの入れ替え

1. `pnpm -C web exec cf turnstile widgets rotate-secret <サイトキー>`（出力にシークレットキーが出るので、画面の記録を残さない。`--invalidate-immediately` を付けなければ、前の値は 2 時間有効）
2. 新しい値を `gh secret set TURNSTILE_SECRET`（標準入力から渡す）
3. `production`・`demo` をデプロイし直す

## 人が行う手順

マージの後、オーナーが手元で行う（AI は `--check`・`--apply`・`--remove` を実行しない。認証の情報も持たない）。結果（`--check` の出力。シークレットキーは出ない）を Issue に貼る。

1. 準備: `pnpm install`、`export CLOUDFLARE_ACCOUNT_ID=<アカウント ID>`、`pnpm -C web exec cf auth login`（Turnstile とゾーンの WAF を編集できる権限。`pnpm -C web exec cf auth scopes` で確かめる）
2. Turnstile（最初のデプロイの前に）: `python3 scripts/cloudflare/cf_turnstile.py --check` → `python3 scripts/cloudflare/cf_turnstile.py --apply --secret-out "$(mktemp -d)/turnstile-secret"`
3. 出たファイルで `gh secret set TURNSTILE_SECRET < <ファイル>`、出たサイトキーで `gh variable set TURNSTILE_SITE_KEY --body <サイトキー>`（画面のビルドに渡す。T-603 で使う）。済んだらファイルを消す
4. ゾーン: `python3 scripts/cloudflare/cf_zone.py`（送る内容を見る）→ `--check`（サーバー側の検査が通ること。プランの制約で止まったら、出力を Issue に貼って止める）→ `--apply`
5. 確かめる: `curl -s https://raft.kizuku-hackathon.work/api/healthz` が 200（最初のデプロイの後）。同じゾーンのほかのサービスが今までどおり動く。もう一度 `--check` が「変更なし」
6. 外すとき: `python3 scripts/cloudflare/cf_zone.py --remove`
