# コストの緊急停止

費用が想定より増えたときに、新しいゲームの受付を止め、必要なら入り口の要求そのものを止めて、落ち着いてから再開するための手順です。あわせて、気づくための予算アラート（ADR-0018 G6）の設定を書きます。

- **実行するのは人（オーナー）です。AI エージェントはこの手順のコマンドを実行しません**（管理用 API の呼び出し、`cf` での設定の変更、`scripts/cloudflare/budget_alert.py` の `--check`・`--apply` を含む）
- 関連: [ADR-0018（コストガード）](../adr/0018-cost-guard.md)、[プロトコル仕様 §2 の管理用 API](../06-protocol.md#get-apiadminguardspatch-apiadminguards管理用)、[ADR-0016（`cf` だけで管理する）](../adr/0016-infrastructure-cloudflare-with-cf-cli.md)、T-607（Issue #71）
- コマンドの `<…>` は置き換える値です。本物の値（`ADMIN_TOKEN`・アカウント ID・メールのアドレス）をリポジトリに書かないでください（公開リポジトリ）
- `cf` は `web/` の devDependency の版（`pnpm exec cf …`）を使います。Wrangler やダッシュボードでの手作業の変更は使いません（ダッシュボードは見るだけ）

## 1. いつ使うか

次のどれかに当てはまったら、この手順を使います。

- 予算アラートのメールが届いた（下の「9. 予算アラート（G6）」）
- 管理用 API の `GET /api/admin/guards` の `usage.todayUsd`・`monthUsd` が想定より多い（目安: 1 日 1 USD・1 か月 30 USD が G1 の上限）
- Cloudflare のダッシュボードの利用量のグラフ（見るだけ）が急に増えた
- 知らないアクセスが多い（bot のクローリングなど）

コストガードの全体（G1〜G6 の表と、それぞれを無効にできるか）は [ADR-0018 の「決定」](../adr/0018-cost-guard.md#決定) にあります。

## 2. 平時の準備

- `ADMIN_TOKEN` は、**一般公開の Worker（`raft-game`）だけ**が持ちます。GitHub の Secrets と同じ値を、パスワードの管理の道具に置いておきます
- `ADMIN_TOKEN` をコマンドの引数に置かないでください（シェルの履歴とプロセスの一覧に残る）。次のように、画面に出さずに入力し、`curl` には標準入力から設定として渡します

```bash
read -rs ADMIN_TOKEN          # 画面に出さずに入力する
admin() { printf 'header = "Authorization: Bearer %s"\n' "$ADMIN_TOKEN" | curl --config - -sS "$@"; }
admin https://raft.kizuku-hackathon.work/api/admin/guards
```

- 最初のデプロイの後に一度 `GET` して、`200` と `guards` が返ることを確かめておきます
- デモ用のホスト名（`raft-demo.kizuku-hackathon.work`）の `/api/admin/guards` は `404` です（デモ用の Worker は `ADMIN_TOKEN` を持たず、管理用 API がない）。**どちらのモードも、一般公開のホスト名で操作します**
- `ADMIN_TOKEN` の値の末尾に改行や空白が入っていると、Worker は管理用 API がないものとして `404` を返します（`401` ではない）。`404` が返ったら、ホスト名と値を確かめます

## 3. 段階 1: 新しいゲームの受付を止める（数秒）

```bash
admin -X PATCH -H 'Content-Type: application/json' \
  --data '{"acceptGames":false,"reason":"緊急停止: <理由>"}' \
  https://raft.kizuku-hackathon.work/api/admin/guards
```

- 応答の `guards.acceptGames` が `false` になっていることを確かめます
- 効く範囲: **一般公開とデモの両方**です。デモ用の Worker（`raft-game-demo`）も、一般公開の Worker の `CostGuard` を共有して受付を判定するためです
- 止めた後は、`POST /api/sessions` が `503 sessions_disabled` を返し、画面には「ただいま受付を停止しています。」と出ます
- 遊んでいるゲームは続きます。`gameTime.maxMinutes`（既定 30 分）と、アラームの回数の上限で終わります
- `acceptGames` はすぐに効きます（使い回しの遅れはない。[プロトコル仕様 §2](../06-protocol.md#get-apiadminguardspatch-apiadminguards管理用)）
- `reason` は必須で、前後の空白を除いて 1〜200 文字です。ない・空だと `400 bad_request` です
- 変えられない項目（`signature`・`cpuMs`・`gameTime.enabled`・`gameTime.maxAlarms`）を送ると `400 locked` です。この手順では送りません

## 4. 段階 2: 遊んでいるゲームが終わるのを確かめる

- `admin https://raft.kizuku-hackathon.work/api/admin/guards` の `usage.activeGames`（目安の値）が減っていくことを確かめます
- コンテナの数は `cf` で見ます（`web/` で実行し、`CLOUDFLARE_ACCOUNT_ID` を設定しておく）

```bash
cd web && export CF_SEND_TELEMETRY=false CLOUDFLARE_ACCOUNT_ID=<アカウント ID>
pnpm exec cf containers applications list
#   → このアプリのアプリケーションの id
pnpm exec cf containers applications instances list --application-id <ID> --state active
```

- 署名の確認（G3）は外しません。管理用 API でも変えられません（ADR-0018 の注意）。費用を止めるために G3 を外すことはしません

## 5. 段階 3: 入り口の要求そのものを止める

Worker の呼び出しやログの費用が増えているときに使います（段階 1 の後も、Worker は要求を受けて動き続けます）。

ゾーンの WAF に、2 つのホスト名への要求をすべて遮断する緊急用のルールを足します。**Worker も静的ファイルも動かなくなります**。GitHub のデプロイの後の `/api/healthz` の確認も失敗します。

ルールの `ref` は `raft-game-emergency-block` にします。ゾーンのルールの約束は、T-606（ゾーンの設定のスクリプト）が `raft-game-zone-` で始まるもの、この緊急用が `raft-game-emergency-` で始まるものです。T-606 のスクリプトは `raft-game-zone-` で始まるものだけを扱うので、このルールを消しません。

```bash
cd web && export CF_SEND_TELEMETRY=false
pnpm exec cf rulesets list --zone kizuku-hackathon.work
#   → kind が "zone"、phase が "http_request_firewall_custom" のものの id
pnpm exec cf rulesets rules create <ルールセットの ID> --zone kizuku-hackathon.work --dry-run \
  --body '{"ref":"raft-game-emergency-block","description":"raft-game: emergency block (T-607)","expression":"(http.host in {\"raft.kizuku-hackathon.work\" \"raft-demo.kizuku-hackathon.work\"})","action":"block","enabled":true}'
#   → 送る内容を確かめてから --dry-run を外して実行する
```

- エントリーポイントのルールセットが**まだない（一覧にない）ときだけ**、次で作ります。**あるときにこれを使うと、ゾーンのほかのルールが消えます**

```bash
pnpm exec cf rulesets entrypoint update http_request_firewall_custom --zone kizuku-hackathon.work --dry-run \
  --body '{"rules":[{"ref":"raft-game-emergency-block","description":"raft-game: emergency block (T-607)","expression":"(http.host in {\"raft.kizuku-hackathon.work\" \"raft-demo.kizuku-hackathon.work\"})","action":"block","enabled":true}]}'
#   → 送る内容を確かめてから --dry-run を外して実行する
```

外し方:

```bash
pnpm exec cf rulesets get <ルールセットの ID> --zone kizuku-hackathon.work
#   → rules の中で ref が "raft-game-emergency-block" のものの id
pnpm exec cf rulesets rules delete <ルールの ID> --ruleset-id <ルールセットの ID> --zone kizuku-hackathon.work --force
```

## 6. 段階 3 でも止まらないとき

- Worker の削除（`cf workers delete`）は、`CostGuard` の帳簿などの Durable Object のデータを失い、戻せません。**オーナーが判断します**（AI エージェントは行いません）
- 費用が止まらない原因が Cloudflare の側にありそうなら、Cloudflare のサポートに連絡します

## 7. 再開

1. 段階 3 のルールを足していたら、上の「外し方」で外します
2. 受付を再開します。`acceptGames` に `null` を送ると上書きが消え、設定の既定値（`true`）に戻ります

```bash
admin -X PATCH -H 'Content-Type: application/json' \
  --data '{"acceptGames":null,"reason":"再開: <理由>"}' \
  https://raft.kizuku-hackathon.work/api/admin/guards
```

3. 応答の `guards.acceptGames` が `true` であることを確かめます
4. 入り口が動いていることを確かめます（`200` が返る）

```bash
curl -s -o /dev/null -w '%{http_code}\n' https://raft.kizuku-hackathon.work/api/healthz
```

5. ブラウザで 1 ラウンド遊べることを確かめます

## 8. 事後

- Issue に、いつ・何をきっかけに・どの段階まで止め・いつ再開したかを書きます。管理用 API での切り替えは `GET /api/admin/guards` の `recentChanges` に記録が残ります
- 翌日、Cloudflare の請求の画面（見るだけ）と予算アラートのメールを確かめます（集計は 1 日 1 回で、前日分が翌日に届く）
- `ADMIN_TOKEN` が漏れた疑いがあれば入れ替えます。値の末尾に改行を入れないでください

```bash
openssl rand -hex 32                  # 新しい値を作り、パスワードの管理の道具に保存する
gh secret set ADMIN_TOKEN             # 値は対話で貼り付ける（引数に置かない）
#   → ワークフロー deploy を手動の実行で production にデプロイし直す
```

## 9. 予算アラート（G6）

### 仕組み

- `cf billing spend-policies` の `action: notification` のポリシーを、製品 `workers` と `durable_objects` のそれぞれに 1 つずつ作ります（名前は `raft-game-budget-workers`・`raft-game-budget-durable-objects`）
- 今の請求期間の従量の金額が、製品ごとに 5 USD に達したらメールが届きます。**利用は止まりません**（気づくための仕組み）
- 集計は 1 日 1 回で、前日分が翌日に届きます。従量課金の分だけが対象です（月 5 USD の基本料金は含まない）
- コンテナの費用がどちらの製品に数えられるかは、最初の請求で確かめて、この節に書きます（未確認）
- `usage_block`（しきい値で利用を止めるポリシー）は使いません（ADR-0018 の改訂が要る）。`budget_alert.py` も送りません

### 設定と確認

`scripts/cloudflare/budget_alert.py` を使います（リポジトリの根で実行。先に `pnpm install`）。アドレスは引数か環境変数 `BUDGET_ALERT_EMAIL` で渡し、リポジトリに書きません。

```bash
python3 scripts/cloudflare/budget_alert.py                                 # --dry-run（既定）: 送る内容を出す。認証なし
export CLOUDFLARE_ACCOUNT_ID=<アカウント ID>
pnpm -C web exec cf auth login                                             # 請求の設定を編集できる権限
pnpm -C web exec cf auth scopes billing                                    # 権限を確かめる
python3 scripts/cloudflare/budget_alert.py --check --email <アドレス>       # 何を作る・直すかを出す。書き込まない
python3 scripts/cloudflare/budget_alert.py --apply --email <アドレス>       # 確認の後に作る・直し、もう一度読んで確かめる
python3 scripts/cloudflare/budget_alert.py --check --email <アドレス>       # 「変更なし」になることを確かめる
```

- 終了コード: 成功 0、違反・`cf` の失敗・反映の後の差・確認の拒否 1、使い方の誤り 2
- `spend-policies` の API が使えない（アカウントで有効でない・権限がない）と、`cf` の文を出して 1 で終わります。その出力を Issue に貼って止めます（ダッシュボードで設定するかは、Orchestrator とオーナーが決める）
- `cf billing spend-policies list` が `[1003] spend policy not found`（404）で失敗したときは、ポリシーが 0 件とみなして続けます（下の「2026-11-08 の例外」）。404 でも、文が `spend policy not found` でない失敗（権限がないなど）は、今までどおり 1 で終わります
- 同じ名前のポリシーが 2 つ以上あると 1 で終わります。どれを残すか人が決め、要らないものを消します（スクリプトには消すモードがない）

```bash
cd web && export CF_SEND_TELEMETRY=false
pnpm exec cf billing spend-policies list
pnpm exec cf billing spend-policies delete <ポリシーの ID> --if-match <version> --dry-run
#   → 確かめてから --dry-run を外し、--force を付けて実行する
```

### 2026-11-08 の例外: ダッシュボードで設定した

- **2026-11-08 時点で、予算アラートは Cloudflare のダッシュボードで設定しています。** D-22 の例外で、オーナーが判断しました（ADR-0016 はダッシュボードでの手作業の変更を使わない方針。ADR-0016 の「例外」に記録）
- 経緯: `budget_alert.py --check` の最初の `cf billing spend-policies list` が `APIError [1003] spend policy not found`（404）で失敗し、スクリプトで作れませんでした
- 考えられる原因（切り分けていない）: ポリシーが 0 件のとき、API が空の一覧ではなく 404 を返す（ベータ版の API の癖）可能性が高いと見ています。ほかに、アカウントで `spend-policies` がまだ有効でない、`cf` の版と API の食い違いも考えられます
- 対応: `budget_alert.py` は、`list` のこの失敗を 0 件として扱うようにしました（Issue #223）。ダッシュボードで作ったアラートは、変更・削除しません
- 次に確かめるときの手順（人が実行する）: `cf billing spend-policies list` を手で実行し、ダッシュボードで作ったポリシーが一覧に出るかを見ます。出れば、ダッシュボードのポリシーと `cf` の API は同じものを見ていて、0 件のときだけ 404 になると分かります。出なければ（同じ 404 なら）、ダッシュボードのアラートは `spend-policies` の API の外にあるので、`budget_alert.py --apply` で作ると二重になります。結果をこの節に書きます

```bash
cd web && export CF_SEND_TELEMETRY=false
export CLOUDFLARE_ACCOUNT_ID=<アカウント ID>
pnpm exec cf billing spend-policies list
#   → ダッシュボードで作ったアラートが出るか。出ないときは失敗の文（[1003] など）をそのまま記録する
```

### しきい値の変え方

`--threshold-usd <整数>`（1〜30。既定 5）を付けて `--check` → `--apply` します。30 を超える値は受け付けません（月の上限 30 USD、ADR-0018 G1）。

```bash
python3 scripts/cloudflare/budget_alert.py --check --email <アドレス> --threshold-usd <USD>
python3 scripts/cloudflare/budget_alert.py --apply --email <アドレス> --threshold-usd <USD>
```

### 触らないもの

- Cloudflare が既定で作るアラート（ADR-0018 の「既定で 10 USD」）とほかのポリシーは、`--check` の表に出すだけで触りません
- ほかのポリシーに、`usage_block` で有効なものがあると `--check` が注意を出します。しきい値に達すると、その製品が請求期間の終わりまで止まるので、残すかはオーナーが決めます

### メールが届いたら

`GET /api/admin/guards` の `usage` と、ダッシュボードの利用量のグラフ（見るだけ）を確かめ、想定より多ければ「3. 段階 1」を検討します。

## 10. 練習の記録

最初のデプロイの後に、段階 1 と再開を一度試し、ここに書きます。

| 日付 | 試したこと | 結果 | 実施した人 |
|---|---|---|---|
| 未実施 | 段階 1（受付の停止）と再開 | — | — |
