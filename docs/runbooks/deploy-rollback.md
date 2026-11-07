# 前の版に戻す（デプロイのロールバック）

デプロイの後に不具合が出たときに、本番（`production`）かデモ用（`demo`）を前の版に戻す手順。T-604（Issue #68）。

- **実行するのは人（オーナー）。** AI エージェントは `cf workers deployments create`・`cf deploy`（`--dry-run` を除く）を実行しない（[README.md](./README.md)）
- 共通の準備（`cf` の認証など）は [README.md の「共通の準備」](./README.md#共通の準備)
- 関連: [ADR-0016](../adr/0016-infrastructure-cloudflare-with-cf-cli.md)（Durable Object のクラスの作成・削除・改名は戻せない）、[ADR-0018](../adr/0018-cost-guard.md)（コストガード）、T-603（Issue #67、`deploy.yml`）

## 1. 前提

| モード | Worker | ホスト名 | デプロイのきっかけ |
|---|---|---|---|
| `production` | `raft-game` | `raft.kizuku-hackathon.work` | `main` への push（`web/**`・`server/**`・`data/**`・`pnpm-lock.yaml` などの変更） |
| `demo` | `raft-game-demo` | `raft-demo.kizuku-hackathon.work` | ワークフロー `deploy` の手動の実行 |

- `deploy.yml`（T-603）は、`main` への push で `production` に、手動の実行で `demo`（既定）か `production` にデプロイする。`demo` は `production` が動いていることを確かめてから進む（`production` を先に）
- `deploy.yml` は失敗しても自動では前の版に戻さない。戻すのはこの手順で人が行う
- 版の見分け方:
  - `cf deploy --message "$BUILD_ID run $GITHUB_RUN_ID"` で、Worker の版の `annotations["workers/message"]` に `git:<SHA の先頭 7 桁> run <実行 ID>` が入る
  - `/api/healthz` は `{"status":"ok","build":"git:<7 桁>"}` を返す（[プロトコル仕様 §2](../06-protocol.md#get-apihealthz)）。今どの commit の Worker が配信されているかが分かる
  - GitHub の Environments `production`・`demo` のデプロイの履歴に、commit と実行が残る
- `demo` の Worker は `production` の Worker（`raft-game`）の `CostGuard` を使う。`production` を戻すと、`CostGuard` のコードの変更は `demo` にも効く

## 2. 何が戻り、何が戻らないか

| 対象 | 方法 A（Worker の版） | 方法 B（前の実行の Re-run） | 方法 C（revert） |
|---|---|---|---|
| Worker のコード・静的ファイル（画面）・テキストの設定（`BUILD_ID`・`GUARDS` の既定値など） | 戻る | 戻る | 戻る |
| シークレット | その版を作ったときの値に戻る（変えた後なら下の注意） | 今の GitHub の Secrets の値 | 今の値 |
| コンテナ（Go のイメージ） | **戻らない**（Containers のアプリケーションは Worker の版と別） | 戻る（前の commit からビルドし直す） | 戻る |
| Durable Object のクラスの作成・削除・改名 | **戻せない**（ADR-0016） | 前の構成の宣言で上書きされる（下の注意） | 前進の修正として扱う |
| Durable Object の保存データ（`CostGuard` の帳簿・管理用 API の上書き・`GameSession` の記録）、R2、ゾーンの設定 | 戻らない | 戻らない | 戻らない |
| かかる時間の目安 | 1 分以内 | 10〜15 分（ビルドとコンテナのイメージ） | PR とレビューの時間＋デプロイ |

- 「コンテナは戻らない」は、`cf@1.0.0-beta.28` の API の形（Worker の版は `cf workers versions`、コンテナの版は `cf containers applications versions` と別に管理される）から判断したもの。まだ確かめていない。最初の練習（下の「8. 練習の記録」）で確かめ、違えばこの表を直す
- 方法 B の「前の構成の宣言で上書きされる」: `cf deploy` は `web/cloudflare.config.ts` の `exports` を宣言として反映する。前の commit の宣言に、後から足した Durable Object のクラスがないと、削除として扱われ得る（データを失い、戻せない）。下の「3. 方法の選び方」で先に確かめる

## 3. 方法の選び方

上から順に当てはめる。

1. 戻す範囲（良い版の SHA から悪い版の SHA まで）に **Durable Object のクラスの変更**（`web/cloudflare.config.ts` の `exports`）がある → **方法 C だけ**（必要なら前進の修正）

   ```bash
   git diff <良い SHA>..<悪い SHA> -- web/cloudflare.config.ts
   ```

2. 戻す範囲に **シークレットの変更**（漏れたための入れ替えを含む）がある → **方法 A は使わない**（古い値に戻り、漏れた鍵が復活する・発行済みのセッションが通らなくなる）。方法 B か C
3. 不具合が Worker か画面（`web/worker/**`・`web/src/**`）だけ → **方法 A** ですぐ止血し、続けて方法 C
4. コンテナ（`server/**`・`data/**`）が関係する → **方法 B** で止血し、続けて方法 C

- どの方法でも、止血の後に**必ず方法 C**を行う。方法 A・B の後でも、次に `main` へ push（マージ）すると最新の `main` がデプロイされ、不具合が戻るため
- 方法 C が済むまで、ほかの PR のマージを止める（Orchestrator に伝える）

## 4. 方法 A: Worker の版を戻す

`production` の例。`demo` は `raft-game` を `raft-game-demo`、ホスト名を `raft-demo.kizuku-hackathon.work` に読み替える。

```bash
cd web
export CF_SEND_TELEMETRY=false
export CLOUDFLARE_ACCOUNT_ID=<アカウント ID>
pnpm exec cf auth whoami
# 1. 今の配信と版の一覧を見る（先頭が今の配信）
pnpm exec cf workers deployments list --worker raft-game
pnpm exec cf workers versions list --worker-id raft-game
# 2. 戻す先の版（annotations の workers/message が "git:<良い 7 桁> run …"）の ID を決め、送る内容を確かめる
pnpm exec cf workers deployments create --worker raft-game --dry-run --body '{"strategy":"percentage","versions":[{"version_id":"<版の ID>","percentage":100}],"annotations":{"workers/message":"rollback to git:<良い 7 桁>: <理由>"}}'
# 3. --dry-run を外して実行する
# 4. 確かめる
curl -s https://raft.kizuku-hackathon.work/api/healthz          # build が git:<良い 7 桁>
pnpm exec cf workers deployments list --worker raft-game        # 先頭が戻した版で 100%
```

- 版の一覧に `workers/message` が見えないときは、版ごとに詳細を見る

  ```bash
  pnpm exec cf workers versions get <版の ID> --worker-id raft-game
  ```

- 版の一覧（`--versions '[…]'` などの配列の値）はフラグで渡さず、`--body` で JSON をまとめて渡す（配列をフラグで渡すと、文字列のまま 1 要素の配列に包まれることがある）
- 「シークレットが変わった」などの理由で拒否されたら、上の「3. 方法の選び方」に戻る。`--bypass-deployment-checks` は、シークレットを変えた理由が漏れでないと確かめたときだけ使い、使ったら理由を記録（下の「7. 戻した後の記録」）に残す
- 割合での分割（2 つの版が配信中）が残っていると、次の `cf deploy`（CI）が確認を求めて失敗する（#67 のエッジケース）。この手順は必ず 1 つの版を 100% にする
- コンテナ（Go のイメージ）は戻らない（上の「2. 何が戻り、何が戻らないか」）。コンテナが関係する不具合なら方法 B

## 5. 方法 B: 前の成功した実行を Re-run する

1. 上の「3. 方法の選び方」の Durable Object のクラスの確認をする（前の構成の宣言で、後から足したクラスを消すような変更になっていないか）
2. GitHub の Actions のワークフロー `deploy` で、戻したい commit の成功した実行を開く（Environments `production`・`demo` の履歴から辿れる）
3. 「Re-run jobs」を押す。Re-run はその実行の commit とワークフローのまま、**今の Secrets** でビルドとデプロイをやり直す（GitHub の Re-run の仕様。Re-run できるのは 30 日以内の実行）
4. ジョブの中の `cf deploy --prebuilt --dry-run` の出力で、Durable Object のクラスの表に削除・改名がないことを確かめる（あれば、そのジョブを取り消して方法 C にする）
5. 終わったら `/api/healthz` の `build` を確かめる

```bash
curl -s https://raft.kizuku-hackathon.work/api/healthz          # build が git:<戻した 7 桁>
curl -s https://raft-demo.kizuku-hackathon.work/api/healthz     # demo を戻したとき
```

- `production` と `demo` は別の実行。`demo` も戻すときは、`demo` の実行を Re-run する

## 6. 方法 C: revert する

1. [docs/agents/operations.md の「マージ後の revert・rollback」](../agents/operations.md#マージ後の-revertrollback) の手順で、原因の PR を revert する
2. revert のマージで `production` に自動でデプロイされる。`demo` は、ワークフロー `deploy` の手動の実行でデプロイし直す
3. `/api/healthz` の `build` が revert の commit（`git:<revert の 7 桁>`）になったことを確かめる

## 7. 戻した後の記録

- Issue に、いつ・どの方法で・どの版（`git:<7 桁>`）に戻したか・理由・方法 C の PR を書く。`--bypass-deployment-checks` を使ったら、その理由も書く
- 止血の間、管理用 API で受付を止めた（[cost-emergency.md](./cost-emergency.md) の手順。T-607）なら、再開を忘れない
- 方法 C が済んだら、止めていた PR のマージを再開してよいと Orchestrator に伝える

## 8. 練習の記録

`demo` への最初のデプロイの後に、方法 A を `demo`（`raft-game-demo`）で 1 回練習する。1 つ前の版に戻して `/api/healthz` の `build` が変わることを確かめ、元の版に戻す。そのとき、コンテナの版が変わらないこと（「コンテナは戻らない」）を確かめる。

```bash
cd web
export CF_SEND_TELEMETRY=false
export CLOUDFLARE_ACCOUNT_ID=<アカウント ID>
pnpm exec cf containers applications list
#   → デモ用のコンテナ（名前は web/cloudflare.config.ts の containerName を元にしたもの）のアプリの ID
pnpm exec cf containers applications versions list --application-id <アプリの ID>
#   → 方法 A の前と後で、版の一覧と最新の版が変わらないこと
```

| 日付 | 試したこと | 結果（コンテナが戻らないことの確認を含む） | 実施した人 |
|---|---|---|---|
| 未実施 | 方法 A を `demo` で（1 つ前の版に戻し、元に戻す） | — | — |
| 未実施 | 古い `deploy` の実行に「Re-run jobs」が出ること（方法 B の前提） | — | — |
