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

## 2. 1 ゲームの費用の見積もり

ADR-0018 の単価（30 分の最悪の費用: `lite` の Container 約 0.0036 USD、Durable Object 約 0.0028 USD）で計算する。Container の数は「島＋船＋Coordinator」、Durable Object の数は「島＋船（ActorHost）＋ DtnSession」。

| ステージ | 島・船 | Container | DO | 30 分の予約 | 15 分の予約 | 1 日 1 USD で遊べる数（15 分） |
|---|---|---|---|---|---|---|
| stage1 | 3・1 | 5 | 5 | 約 0.032 USD | 約 0.016 USD | 約 60 |
| stage2 | 3・2 | 6 | 6 | 約 0.038 USD | 約 0.019 USD | 約 50 |
| demo-goto | 3・3 | 7 | 7 | 約 0.045 USD | 約 0.022 USD | 約 45 |
| demo-izu | 6・6 | 13 | 13 | 約 0.083 USD | 約 0.042 USD | 約 24 |

- ラウンドは最長 3.5 分なので、DTN の環境の 1 ゲームの時間の上限（G2）は **15 分**から始める（予約が半分になる）
- Container の大きさは、すべて `lite` から始める。足りないと分かったら、DTN-12 の測定の結果で決める
- RPC・保存・Workers Logs の費用は、この表に入っていない。DTN-12 で測って記録する

## 3. CostGuard の拡張

- `POST /reserve` に、使う資源の内訳を渡せるようにする: `{sessionId, units: [{kind: "container", instance: "lite", count: 5}, {kind: "do", count: 5}]}`。予約額は内訳の合計 × 時間の上限
- **すべての Actor を起動する前に**予約する。予約が通らなければ、1 つも起動しない
- 起動の途中で失敗したら、起動した分をすべて止め（`destroy()`）、実際に動いた時間で精算する。精算は冪等にする（同じセッションの 2 回目の精算は何もしない）

## 4. ゲームの一生

1. `POST /api/sessions`（DTN の入り口）: レート制限 → Turnstile → ステージの検査（対象外のイベント → `dtn_unsupported_stage`）→ セッションの発行（署名）→ 予約 → `DtnSession` の初期化
2. `DtnSession` が、アラームを設定してから（今の `GameSession` と同じ）、Coordinator と全 `ActorHost` を並行に起動する。全員が `init` に答えたら `ready`
3. ブラウザが `/connect` で WS をつなぎ、遊ぶ（外部の電文は今と同じ）
4. 終わったら（`round_ended`・`time_limit`・`idle`・`alarm_cap`・`start_timeout`・`dtn_actor_lost`・`dtn_restore_failed`・`dtn_limit`・`dtn_internal`）、`DtnSession` が全 `ActorHost` に `shutdown` を送り、Coordinator を止め、精算する。止められなかったものは、アラームで 30 秒ごとに再試行する（アラームの回数の上限の中で）
5. Coordinator が止まって作り直しても、片付けを最初からやり直せる（D-8）

## 5. 人がする作業

AI エージェントには許されていない操作（ワークフロー・シークレット・Rulesets・ゾーンの設定）なので、オーナーが行う。手順の詳細は DTN-12 で `docs/dtn/` に書く。

| # | 作業 | いつまでに |
|---|---|---|
| H1 | `dtn/main` の Ruleset を決めて設定する（例: PR 必須・`ci-ok` 必須） | DTN-00 の前 |
| H2 | `dtn/main` の `.github/workflows/ci.yml` の対象に `dtn/main` を足す（`dtn/main` の `workflow-sources` を直し、`install-workflows.sh` で取り込む） | DTN-00 の前 |
| H3 | DTN の環境のデプロイのワークフロー（`dtn/main` の push → `cf deploy --prebuilt --mode dtn`、PR は `--dry-run`）を取り込む | DTN-12 |
| H4 | DTN の環境のシークレット（`SESSION_SIGNING_KEY`・`ADMIN_TOKEN`・`TURNSTILE_SECRET`・`DTN_CONTACT_KEY` の新しい値）を GitHub に登録する | DTN-12 |
| H5 | `raft-dtn.kizuku-hackathon.work` の DNS・Turnstile のウィジェット・WAF のレート制限（**今の prod・demo のルールは変えず、DTN 用のルールを別に足す**）・予算アラート | DTN-12 |
| H6 | MVP の凍結のタグ（例: `mvp-final`）を `main` に付ける | MVP の完成の後 |

## 6. 運用（DTN-13 で詳しく書く）

- 起動・遊ぶ・障害の注入（DTN-16 の管理者のボタン）・復元・全停止の手順
- 受付の一括停止（管理用 API）。発表の前後は、DTN の環境の受付を止められるようにする（[open-questions.md](./open-questions.md) の Q-ACCOUNT）
- ログ: session・actor・bundle・transfer・contact・eventSeq・epoch を上限付きで出す。シークレット・Contact の署名・MoQ のトークンを含む URL は出さない
- 単一プロセス版に戻す方法: DTN の環境では戻さない（MVP は prod・demo にある）。DTN の環境を止めるだけでよい
