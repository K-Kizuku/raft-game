# CLAUDE.md — 開発者・実装エージェント向けの規約

## まず読むもの

1. `docs/01-requirements.md`（要件 ID と優先度）
2. `docs/03-design-doc.md`（設計）
3. `docs/08-implementation-plan.md`（タスク一覧）
4. 迷ったら `docs/adr/` と `docs/09-assumptions-and-open-questions.md`（判断の記録）
5. AI エージェントとして動くときは `docs/agents/README.md`（役割と権限・Issue からマージまでの流れ・運用。ローカル環境の分け方は ADR-0019）
6. レビューをするときは `REVIEW.md`

## リポジトリの構成

| パス | 中身 |
|---|---|
| `server/` | Go のモジュール（`server/go.mod`）。`cmd/`（`islandsdata` などのコマンド）・`internal/`（シミュレーションのコアなど。役割は `docs/02-architecture.md` §3）・`third_party/etcd-raft`（同梱の etcd-raft。ADR-0005） |
| `web/` | ブラウザのクライアント（`src/`）・Worker と Durable Object（`worker/`）・インフラの正本 `cloudflare.config.ts` |
| `data/` | 提供データの原本（`raw/`）・人手の抜き出し（`curated/`）・ETL の設定（`islands-select.yaml`）・生成物（`islands.json`。コミットする）・ステージ定義（`stages/`） |
| `spike/` | T-007 のスパイク（本番と別の Worker `raft-game-spike`）。本番のコードから import しない。CI では検査しない（`spike/README.md`） |
| `scripts/` | 開発の補助（`dev-env.sh`・`dev-slot.sh`・`check-docs.py`）・`raft/`（パッチの検証）・`github/`（人が使うワークフローの取り込み・リポジトリの設定）・`agents/`（エージェントの運用）・`tests/`（Python のテスト） |
| `tools/agentctl` | Merge Gate と Issue・PR の書式の検査（Go の別モジュール） |
| `docs/` | 要件・設計・ADR・エージェントの運用 |
| `.github/workflow-sources/` | ワークフローの正本。`.github/workflows/` は人が取り込む |

全体の予定の構成は `docs/02-architecture.md` §6 にある。人向けの準備・作業の流れは [CONTRIBUTING.md](CONTRIBUTING.md) にある。

## 絶対に守ること

- **Raft のアルゴリズムを変更しない。** `server/third_party/etcd-raft` への変更は ADR-0005 の乱数のパッチだけ。それ以外の変更が必要に見えたら、ADR を書いて相談する
- **ゲーム側で Leader や Commit の結果を決めない。** Raft の出力（`Ready`）を観察するだけ
- **Raft のメッセージを書き換えない。** 港キューでの合流・破棄（ネットワークでの欠落と同じ扱い）だけが許される
- **`sim.Engine` の中で動くパッケージ（`server/internal/sim`・`cluster`・`ferry`・`ledger`・`game`）とボット（`internal/bot`）を決定的に保つ。** `time.Now`、goroutine、チャネル、グローバルな `math/rand`、map の走査順への依存、スコアや判定での浮動小数点を使わない。`forbidigo`（`server/.golangci.yml`）が検出するのは実時間・`math/rand`・共有の乱数・`float32`/`float64` の型だけで、goroutine・チャネル・`select`・map の走査順・型を書かない小数はレビューで見る（DesignDoc §9.2）
- **Ready の処理順序**は「Append → SetHardState → 送信 → 適用 → Advance」。`cluster.Node.processReady` 以外で Ready を扱わない
- ゲームの数値はコードに直接書かず、ステージ定義（`data/stages/*.yaml`）に置く

## 仕様の変更

- 仕様を変えるときは、先に `docs/` を更新し、同じ PR でコードを変える
- 新しい技術判断をしたら `docs/adr/` に ADR を追加する（`docs/adr/template.md`）
- PR やコミットには、関係する要件 ID（`FR-…`）やタスク ID（`T-…`）を書く

## コマンド

ルートで実行する。一覧は `make help`。

```bash
corepack enable && pnpm install   # pnpm は package.json の packageManager の版（ワークスペース: web・spike）
scripts/dev-slot.sh acquire       # 並列で動かすとき。ポートなどは make dev-env で確かめる
```

```bash
make test       # Go と web のテスト
make lint       # golangci-lint・ESLint・型チェック
make gen        # Go のプロトコルの型から TS の型を生成
make gen-check  # 生成した TS の型が Go の型と一致するか（CI の server ジョブ）
make sync-data  # data/ を server/internal/stage/embedded/ にコピー
make check-secrets  # gitleaks で git の履歴にシークレットがないか（CI の secrets ジョブ。初回は gitleaks をビルドする）
make check-license  # LICENSE・NOTICE が同梱の OSS・提供データの出典と合っているか（CI の docs ジョブ）
make dev        # Go（standalone モード）と Vite を DEV_SLOT のポートで起動（DRY_RUN=1 で表示だけ）
make dev-env    # この作業ツリーの DEV_SLOT・ポート・cf の状態・Docker の名前を出す
make clean-docker  # この作業ツリーの Docker のコンテナとイメージを消す（DRY_RUN=1 で表示だけ）
make demo-local # デモの保険（ノート PC で起動）
(cd web && pnpm exec cf build)                           # Build Output を作る
(cd web && pnpm exec cf deploy --prebuilt --dry-run)     # 反映せずに検査
python3 scripts/check-docs.py                            # Markdown の相対リンク切れを検査（CI の docs ジョブ）
python3 -m unittest discover -s scripts/tests -p 'test_*.py'  # scripts/ のテスト
bash scripts/github/tests/run.sh                         # install-workflows.sh のテスト（CI の shell ジョブ）
bash scripts/raft/verify-patch.sh --with-tests   # 同梱の etcd-raft と上流の差分がパッチだけか（CI の shell ジョブ）
```

- `make test` は Go（`tools/*`・`server`）・web・`scripts/tests` の Python・追跡されている `*tests/run.sh` を実行する。`-race` は付けないので、CI と同じにするときは `go -C server test -race -count=1 ./...`
- `make lint` は go vet・gofmt（`tools/*`・`server`）・golangci-lint（`server`。版は Makefile の `GOLANGCI_LINT_VERSION`）・web の lint と typecheck・shellcheck を実行する。ツールがないものは「対象なし」と出して飛ばす
- `make help` で「未実装」と出るターゲットは、まだ何もしない
- `make dev` は、今は web（Vite）だけを起動する。Go の standalone は、そのコマンド（server/cmd/server）ができてから（それまでは `scripts/dev-env.sh` が「対象なし」と出す）

よく使う個別のコマンド:

```bash
go -C server test -race -count=1 ./...          # CI の server ジョブと同じ
pnpm -C web lint && pnpm -C web typecheck && pnpm -C web test   # CI の web ジョブと同じ
pnpm -C web format                               # Prettier の検査（CI では動かさない）
go -C server run ./cmd/islandsdata --raw ../data/raw --curated ../data/curated \
  --select ../data/islands-select.yaml --out ../data/islands.json --check   # islands.json が最新か（--check を外すと生成し直す）
bash spike/scripts/check.sh                      # スパイクの検査（Docker が要る。CI では動かさない）
```

- ワークフローは `.github/workflow-sources/` に書き、`.github/workflows/` には書かない（人が `scripts/github/install-workflows.sh` で取り込む）

Merge Gate（`tools/agentctl`、#3）。`go -C` を使うとカレントディレクトリが `tools/agentctl` になるので、ファイルは `"$PWD/…"` のように絶対パスで渡す（`--config` は省略すると親のディレクトリへたどって `.agents/config.json` を探す）。終了コードは pass=0・fail=1・pending=2・内部エラー=3。

```bash
go -C tools/agentctl test -race ./...                                              # テスト
go -C tools/agentctl run . gate --repo K-Kizuku/raft-game --pr 12 --json           # GitHub の状態で判定（GITHUB_TOKEN か GH_TOKEN が必要）
go -C tools/agentctl run . gate --repo K-Kizuku/raft-game --pr 12 --json \
  --input "$PWD/tools/agentctl/internal/gate/testdata/pass.json"                   # スナップショットで判定（GitHub に接続しない）
go -C tools/agentctl run . review validate "$PWD/review.json"                     # レビュー記録を検証する
go -C tools/agentctl run . review render "$PWD/review.json"                       # PR のコメントの本文（目印＋表＋JSON）を出す
```

- `--set-status`（コミットステータス `merge-gate` を付ける）と `--comment`（Merge Gate のコメントを更新する）は、ワークフロー `merge-gate` だけが使う。ローカルでは付けない

## コストガード（ADR-0018）の注意点

- **署名の確認（G3）は無効にしない。** sessionId の署名を確かめる前に Durable Object を呼ばない。管理用 API の切り替えの対象にもしない
- Durable Object のアラームは、1 ゲームで設定できる回数に上限を設ける。アラームの中で無条件にアラームを設定し直さない
- Durable Object やコンテナを作る処理は、必ず `CostGuard` の予約を通す
- ラウンドが終わったら、すぐにコンテナを止める（`destroy()`）
- ログの量を増やす変更をするときは、Workers Logs の課金を意識する

## 並列開発（ADR-0019）の約束

- AI エージェントごとに `git worktree` で作業ディレクトリを分け、`DEV_SLOT` を割り当てる（`scripts/dev-slot.sh`）
- 固定のポート・固定のファイル名・固定の Docker の名前を使わない。ポートは `DEV_SLOT` から計算し、テストではポート `:0` と `t.TempDir()` を使う
  - 計算は `scripts/dev-env.sh` が正本: `DEV_SERVER_PORT = 18080 + DEV_SLOT×10`・`DEV_WEB_PORT = 15173 + DEV_SLOT×10`（`+1`〜`+9` は予約）。Docker は `DEV_DOCKER_IMAGE`（`raft-game-server:<名前>-<ハッシュ>`）・`DEV_DOCKER_CONTAINER`・ラベル `DEV_DOCKER_LABEL`（`raft-game.worktree=<パス>`）を使い、`make clean-docker` で消す
- `cf` のローカルの状態は、作業ディレクトリの中に置く。`make dev` は `web/vite.config.ts` の `persistState` で `web/.cloudflare/state` に置く（`cf dev` は `--persist-to` を受け付けない）。`--local` で動く `cf` のコマンドには `--persist-to "$DEV_CF_PERSIST_TO"` を付ける（値は `make dev-env`）
- 本番・デモ用の環境へのデプロイ、ゾーンの設定の変更、Cloudflare のシークレットの更新はしない（CI だけが行う）
- タスクは GitHub の Issue で管理されている。作業の前に、対応する Issue を確認する

## AI エージェントの役割と禁止事項（#5）

役割は環境変数 `AGENT_ROLE`（`orchestrator`・`worker`・`reviewer`・`human`）で決まる。未設定なら、リンクした Worktree の中は `worker`、それ以外は `orchestrator`。禁止事項は指示だけでなく、`.claude/settings.json` の権限と `.claude/hooks/guard-*.sh` で止める。詳しい運用は [docs/agents/](docs/agents/README.md) を参照。

- **全員**: `main` への push、`--force`・`-f` の push（`--force-with-lease` は `agent/issue-*` へだけ）、`gh pr merge`、`.github/workflows/` への書き込み（`human` を除く）、`gh secret`・`gh variable`・`gh ruleset`、ラベル `human-approved` の付与、`git worktree remove --force`、`cf deploy`（`--dry-run` を除く）、`wrangler` をしない
- **Orchestrator**: コード・テスト・ドキュメントを直接変更しない（書けるのは `.agents/`・`.claude/worktrees/` だけ）。マージは `scripts/agents/merge.sh`（Merge Gate が pass のときだけ）で行う
- **Worker**: 自分の Worktree の中と、自分のブランチ `agent/issue-<n>` だけを変更する。`merge.sh`・`review.sh`・`spawn-worker.sh`・`wt.sh` を実行しない。保護されたパス（`.agents/config.json` の `protected_paths`）は、Issue の変更予定ファイルに含まれるときだけ変更できる
- **Reviewer**: 書き込みにあたる操作をしない（指摘だけを返す）
- `guard-bash.sh` の判定は字句によるもの（`eval`・`git -c alias.*`・Reviewer のインタプリタは拒否する）で、`cd` してからの相対パスや変数で組み立てたパス、スクリプトのファイルの中での書き込みは見分けられない。最後の砦は Rulesets とレビュー
- エージェントの定義（`.claude/agents/*.md`）は、メインの作業ツリー（`main`）のものを正とする。`review.sh` は PR の中の定義を使わないよう `--agent` を使わず、本文とツールを明示して渡す。`settings.json` と hooks も PR の中のものを読まず（`--setting-sources user`）、main のものを `--settings` で渡す

```bash
scripts/agents/spawn-worker.sh <issue> [--mode implement|fix|conflict] [--resume] [--background] [--dry-run]
scripts/agents/review.sh <pr> [--no-post]   # まず Codex、使えないときだけ Claude Code
scripts/agents/merge.sh <pr> [--dry-run]
scripts/agents/wt.sh list|cleanup [--issue N] [--dry-run]
scripts/agents/status.sh
scripts/dev-slot.sh acquire|release|list|current
bash .claude/hooks/tests/run.sh && bash scripts/agents/tests/run.sh   # テスト（CI の shell ジョブ）
```

## Cloudflare（`cf`）の注意点

- インフラの構成は `web/cloudflare.config.ts` と `cf`（Cloudflare CLI、ベータ版）だけで管理する。Wrangler・Terraform・ダッシュボードでの手作業の変更は使わない（ADR-0016）
- `cf` は `web/` の devDependency として版を固定する。上げるときは専用の PR にする。`spike/package.json` も同じ版にする（`spike/README.md`）
- `cf deploy` を使う。Durable Object のクラスの作成・削除・改名は `cf workers versions create` ではできない
- リアルタイムのログ表示とシークレットの個別登録は未対応。ログはダッシュボード、シークレットは `cf deploy --secrets-file`
- 1 ゲーム＝1 Durable Object `GameSession`＝1 コンテナ。コンテナ（Go）は外部に直接公開せず、外部への通信も禁止する（`enableInternet: false`）
- コンテナは `durable_object` 方式（`default` 方式は非推奨）。マシンの大きさは `ctx.container.start()` で指定する。イメージは `linux/amd64`
- URL は `raft.kizuku-hackathon.work`（一般公開）と `raft-demo.kizuku-hackathon.work`（デモ用）。ゾーンの設定は、この 2 つのホスト名だけに効くようにする
- 公開リポジトリなので、シークレットをコミットしない（`--secrets-file` 用のファイルは `.gitignore` に入れる）
  - テストの偽のトークン・鍵は、その行に `gitleaks:allow` のコメントを付けるか、値を実行時に組み立てる（例: `"x".repeat(32)`）。`.gitleaksignore` に足してよいのは main にあるコミットの指紋だけ（`make check-secrets` が確かめる）
- ブラウザから届いた `internal.*` の電文は、Durable Object で必ず破棄する

## etcd-io/raft v3.7 の注意点

- `raftpb` は protobuf-go 製。フィールドはポインタなので `m.GetTerm()` などのゲッターで読む
- `Ready.Messages` は `[]*pb.Message`。港に置く前に `proto.Clone` で複製する
- `raft.Status` は `*pb.HardState` を埋め込んでいる。`st.Term` はポインタなので `st.HardState.GetTerm()` を使う
- Web 上のサンプル（v3.5 以前）とは API の書き方が違う
