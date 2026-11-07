# REVIEW.md — PR をレビューするときの観点

PR をレビューする人と AI エージェント（Codex・Claude Code の Reviewer）向けの観点をまとめる。規約そのものは [CLAUDE.md](CLAUDE.md)、レビューの流れと記録の書式は [docs/agents/workflow.md](docs/agents/workflow.md#レビュー記録の書式) を正とする。
レビューは必ず日本語で行ってください

## 進め方

1. PR の本文の `Closes #N` から Issue を読み、**スコープ・実装仕様・変更予定ファイル・変更禁止範囲・Acceptance Criteria** を確かめる
2. 差分が Issue のスコープに収まっているかを見る。スコープの外の変更は、それだけで指摘にする（`spec`）
3. 下の「必ず確かめること」を、差分が触れた領域について確かめる
4. 確かめられることは、テスト・リンターを実行して確かめる（`make test`・`make lint`）。推測だけで `critical`・`high` を付けない

## 重大度の目安

`critical`・`high` の `open` の指摘が 1 つでもあれば `request_changes`（`.agents/config.json` の `blocking_severities`）。

| 重大度     | 目安                                                                                                                                                                            |
| ---------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `critical` | 本番の費用・シークレット・ガードに直接響くもの。シークレットのコミット、署名の確認（G3）の無効化・迂回、保護されたパスでのガードの弱体化、`main` への直接の反映を可能にする変更 |
| `high`     | 「絶対に守ること」への違反、決定性を壊す変更、Raft の安全性・不変条件を壊しうるバグ、AC を満たしていない、コストガードの迂回、テストが通らない                                  |
| `medium`   | 仕様との食い違い（docs を更新していない）、境界値・エラー処理の漏れ、AC に対応するテストの不足、並列開発の約束（固定のポートなど）への違反                                      |
| `low`      | 保守性・読みやすさ・命名、小さな重複                                                                                                                                            |
| `info`     | 提案・質問。直さなくてよいもの                                                                                                                                                  |

## 必ず確かめること

### Raft（`server/internal/cluster`・`server/third_party/etcd-raft`）

- `server/third_party/etcd-raft` の変更は ADR-0005 の乱数のパッチだけ（`bash scripts/raft/verify-patch.sh --with-tests`）。それ以外は `high`
- ゲーム側で Leader や Commit の結果を決めていない。`Ready` を観察するだけ
- Raft のメッセージを書き換えていない。港キューでの合流・破棄（ネットワークでの欠落と同じ扱い）だけ
- Ready の処理順序が「Append → SetHardState → 送信 → 適用 → Advance」で、`cluster.Node.processReady` の外で Ready を扱っていない
- `Ready.Messages` を港に置く前に `proto.Clone` で複製している。`raftpb` のフィールドはゲッター（`m.GetTerm()`・`st.HardState.GetTerm()`）で読んでいる

### 決定性（`internal/sim`・`cluster`・`ferry`・`ledger`・`game`）

`forbidigo` は識別子しか見ないので、次の 4 つは **レビューでしか見つからない**（[DesignDoc §9.2](docs/03-design-doc.md#92-決定性を壊すもの実装上の禁止事項)）。

- goroutine・チャネル・`select`
- `map` の走査順への依存（走査するならキーをソートしてから）。結果の順番・イベントの登録順・ログの出力順に効いていないか
- 型を書かない浮動小数点（`x := 1.5`、整数の割り算の代わりの小数）をスコアや判定に使っていない
- `sort.Slice` など安定でない並べ替えで、同じ値の順番が結果に効いていないか

あわせて、乱数が `sim.Sub` か `rand.New(rand.NewPCG(…))` から来ていること、同じ時刻のイベントの順番が DesignDoc §4.2 の規則に従うことを確かめる。

### 共有台帳・ゲームの判定（`ledger`・`game`）

- 不変条件（DesignDoc §7.3）を壊さない。予約者は 0 件か 1 件、予約数は定員以下、残高は 0 以上、同じ index まで適用した台帳は一致する
- 依頼の成否は、依頼を出した島での適用で判定する（ADR-0015）
- ゲームの数値をコードに直接書かず、`data/stages/*.yaml` に置いている

### Cloudflare・コストガード（`web/worker`・`web/cloudflare.config.ts`）

- sessionId の署名を確かめる前に Durable Object を呼んでいない（G3）。管理用 API の切り替えの対象にもしていない
- Durable Object・コンテナを作る処理が `CostGuard` の予約を通る
- アラームの回数に上限があり、アラームの中で無条件にアラームを設定し直していない
- ラウンドが終わったらすぐにコンテナを止める（`destroy()`）
- ブラウザから届いた `internal.*` の電文を Durable Object で破棄している。`Origin` の照合、WS の受信サイズ・レートの上限を外していない
- コンテナが `enableInternet: false` のまま。Wrangler・Terraform を持ち込んでいない（ADR-0016）
- ログを増やす変更は、Workers Logs の課金に見合うか

### 保護されたパスとガード

`.agents/config.json` の `protected_paths`（`.github/**` のワークフローと Rulesets、`.agents/config.json`、`.claude/settings.json`・`hooks/`・`agents/`、`tools/agentctl/`、`scripts/agents/`）に触れる PR は、次を確かめて `protected_paths_ack` に入れる。

- 変更予定ファイルに含まれているか（含まれていなければ `high`）
- 拒否していた操作を通すようになっていないか（hooks の正規表現の緩み、`permissions.allow` の追加、Merge Gate の条件の削除・既定値の変更）
- `.github/workflows/` を直接変えていないか（正本は `.github/workflow-sources/`）
- 変更したガードに、拒否されることを確かめるテストがあるか（`bash .claude/hooks/tests/run.sh`・`bash scripts/agents/tests/run.sh`）

### 仕様・ドキュメント

- 仕様を変えたなら、同じ PR で `docs/` を更新している
- 新しい技術判断には ADR がある（`docs/adr/template.md`）
- PR の本文とコミットに、要件 ID（`FR-…`）かタスク ID（`T-…`）がある
- Markdown の相対リンクが切れていない（`python3 scripts/check-docs.py`）

### テスト

- AC の 1 つずつに、対応するテストがある
- 決定性に関わる変更には、同じシードで同じ結果になることを確かめるテストがある
- テストで固定のポート・固定のファイル名を使っていない（ポート `:0`・`t.TempDir()`。ADR-0019）
- `go test -race` で通る（`tools/agentctl` など、goroutine を使ってよいパッケージ）

### 公開リポジトリ

- シークレット・トークン・`--secrets-file` 用のファイルをコミットしていない
- 新しい依存の license が Apache-2.0 と両立し、必要なら `NOTICE` を更新している（ADR-0020）

## 指摘しなくてよいもの

- `gofmt`・ESLint・型チェック・`golangci-lint` が直すか検出する書式（CI に任せる）
- 生成物の中身（`make gen` の TS の型、`make sync-data` でコピーした `server/internal/stage/embedded/`、`pnpm-lock.yaml`）。元のファイルと生成の手順だけを見る
- Issue の対象外に書かれた事柄が実装されていないこと
- 好みの違いだけの書き換えの提案（`info` にとどめる）

保存したら、python3 scripts/check-docs.py で相対リンクを確かめてください。このファイルには、docs/ へのリンクと見出しへのアンカー（#レビュー記録の書式・#92-決定性を壊すもの…）があります。
