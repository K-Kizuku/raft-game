# CLAUDE.md — 開発者・実装エージェント向けの規約

## まず読むもの

1. `docs/01-requirements.md`（要件 ID と優先度）
2. `docs/03-design-doc.md`（設計）
3. `docs/08-implementation-plan.md`（タスク一覧）
4. 迷ったら `docs/adr/` と `docs/09-assumptions-and-open-questions.md`（判断の記録）

## 絶対に守ること

- **Raft のアルゴリズムを変更しない。** `server/third_party/etcd-raft` への変更は ADR-0005 の乱数のパッチだけ。それ以外の変更が必要に見えたら、ADR を書いて相談する
- **ゲーム側で Leader や Commit の結果を決めない。** Raft の出力（`Ready`）を観察するだけ
- **Raft のメッセージを書き換えない。** 港キューでの合流・破棄（ネットワークでの欠落と同じ扱い）だけが許される
- **`server/internal/sim` 以下を決定的に保つ。** `time.Now`、goroutine、チャネル、グローバルな `math/rand`、map の走査順への依存、スコアや判定での浮動小数点を使わない（`forbidigo` で検出）
- **Ready の処理順序**は「Append → SetHardState → 送信 → 適用 → Advance」。`cluster.Node.processReady` 以外で Ready を扱わない
- ゲームの数値はコードに直接書かず、ステージ定義（`data/stages/*.yaml`）に置く

## 仕様の変更

- 仕様を変えるときは、先に `docs/` を更新し、同じ PR でコードを変える
- 新しい技術判断をしたら `docs/adr/` に ADR を追加する（`docs/adr/template.md`）
- PR やコミットには、関係する要件 ID（`FR-…`）やタスク ID（`T-…`）を書く

## コマンド（M0 で整備する予定）

```bash
make test       # Go と web のテスト
make lint       # golangci-lint・ESLint・型チェック
make gen        # Go のプロトコルの型から TS の型を生成
make sync-data  # data/ を server/internal/stage/embedded/ にコピー
make dev        # Go（standalone モード）と Vite を起動
make demo-local # デモの保険（ノート PC で起動）
(cd web && pnpm exec cf build)                           # Build Output を作る
(cd web && pnpm exec cf deploy --prebuilt --dry-run)     # 反映せずに検査
python3 scripts/check-docs.py                            # Markdown の相対リンク切れを検査（CI の docs ジョブ）
python3 -m unittest discover -s scripts/tests -p 'test_*.py'  # scripts/ のテスト
bash scripts/github/tests/run.sh                         # install-workflows.sh のテスト（CI の shell ジョブ）
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
- `cf` のローカルの状態は、作業ディレクトリの中（`--persist-to ./.cloudflare/state`）に置く
- 本番・デモ用の環境へのデプロイ、ゾーンの設定の変更、Cloudflare のシークレットの更新はしない（CI だけが行う）
- タスクは GitHub の Issue で管理されている。作業の前に、対応する Issue を確認する

## AI エージェントの役割と禁止事項（#5）

役割は環境変数 `AGENT_ROLE`（`orchestrator`・`worker`・`reviewer`・`human`）で決まる。未設定なら、リンクした Worktree の中は `worker`、それ以外は `orchestrator`。禁止事項は指示だけでなく、`.claude/settings.json` の権限と `.claude/hooks/guard-*.sh` で止める。詳しい運用は #6 のドキュメントを参照。

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
- `cf` は `web/` の devDependency として版を固定する。上げるときは専用の PR にする
- `cf deploy` を使う。Durable Object のクラスの作成・削除・改名は `cf workers versions create` ではできない
- リアルタイムのログ表示とシークレットの個別登録は未対応。ログはダッシュボード、シークレットは `cf deploy --secrets-file`
- 1 ゲーム＝1 Durable Object `GameSession`＝1 コンテナ。コンテナ（Go）は外部に直接公開せず、外部への通信も禁止する（`enableInternet: false`）
- コンテナは `durable_object` 方式（`default` 方式は非推奨）。マシンの大きさは `ctx.container.start()` で指定する。イメージは `linux/amd64`
- URL は `raft.kizuku-hackathon.work`（一般公開）と `raft-demo.kizuku-hackathon.work`（デモ用）。ゾーンの設定は、この 2 つのホスト名だけに効くようにする
- 公開リポジトリなので、シークレットをコミットしない（`--secrets-file` 用のファイルは `.gitignore` に入れる）
- ブラウザから届いた `internal.*` の電文は、Durable Object で必ず破棄する

## etcd-io/raft v3.7 の注意点

- `raftpb` は protobuf-go 製。フィールドはポインタなので `m.GetTerm()` などのゲッターで読む
- `Ready.Messages` は `[]*pb.Message`。港に置く前に `proto.Clone` で複製する
- `raft.Status` は `*pb.HardState` を埋め込んでいる。`st.Term` はポインタなので `st.HardState.GetTerm()` を使う
- Web 上のサンプル（v3.5 以前）とは API の書き方が違う
