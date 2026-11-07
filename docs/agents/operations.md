# 並列開発の運用（セットアップ・コマンド・障害の対応）

全体像と役割は [README.md](./README.md)、Issue からマージまでの流れは [workflow.md](./workflow.md) にある。ローカル環境（ポート・`DEV_SLOT`・`cf` の状態）の分け方は [ADR-0019](../adr/0019-parallel-local-development.md)。

---

## 初期セットアップ

### オーナーが手で行う設定

AI の認証には `workflow` 権限がなく、Rulesets とシークレットの操作は hooks が拒否する。次の設定は、オーナーが `AGENT_ROLE` を設定しない（または `human` の）シェルで、この順番で行う。

| # | 設定 | 内容 |
|---|---|---|
| 1 | デフォルトブランチを `main` にする | Merge Gate の C1 と Rulesets の `~DEFAULT_BRANCH` が前提にする |
| 2 | ワークフローを取り込む | [install-workflows.sh](../../scripts/github/install-workflows.sh) が `.github/workflow-sources/` の `ci.yml`・`merge-gate.yml` を `.github/workflows/` にコピーする PR を作る。オーナーがその PR をマージする |
| 3 | Rulesets とマージの方式を設定する | [apply-repo-settings.sh](../../scripts/github/apply-repo-settings.sh) が、デフォルトブランチ `main`・squash だけ・マージ後のブランチの削除と、[Ruleset `main`](../../.github/rulesets/main.json)（削除と force push の禁止、直線の履歴、PR 必須、必須のステータス `ci-ok`・`merge-gate`）を反映する。冪等 |
| 4 | Codex CLI を入れ、`codex login` する | ChatGPT のアカウントでログインする。API キーは使わない（[ADR-0021](../adr/0021-hootl-agent-development.md)） |
| 5 | `gh auth login` | エージェントとオーナーが同じ認証情報を使う（`workflow` 権限は付けない） |
| 6 | Claude Code を入れる | `claude` コマンド。Orchestrator はメインの作業ツリーで起動する |

2・3 は `gh` を使うので、`gh` に未ログインなら 5 を先に行う。2 と 3 は、先に `--dry-run` で内容を確かめる。

```bash
scripts/github/install-workflows.sh --dry-run
```

```bash
scripts/github/install-workflows.sh
```

```bash
scripts/github/apply-repo-settings.sh --dry-run
```

```bash
scripts/github/apply-repo-settings.sh
```

```bash
codex login
```

```bash
gh auth login
```

ほかに、`git`・`jq`・`go`・`timeout`（macOS は `gtimeout`）が要る。

### bash の版

hooks（`.claude/hooks/*.sh`）と `scripts/` の shell のスクリプトは、`#!/usr/bin/env bash` で PATH の先頭の bash を使い、macOS 標準の `/bin/bash`（3.2）でも動く書き方にしている（#107）。Homebrew などの bash 5 でもよい。

- bash 3.2 と UTF-8 のロケール（`LANG=ja_JP.UTF-8` など）では、`"$t）"` のように変数の直後に ASCII 以外の文字があると、そのバイトまで変数名として読まれ、`set -u` で `t�: unbound variable` になる。変数の直後に ASCII 以外の文字が続くときは `${t}` と書く。[test_shell_scripts.py](../../scripts/tests/test_shell_scripts.py) が、Git が追跡する `*.sh` を検査する
- hooks は fail-closed にしている。想定外のエラーで止まっても、終了コード 2 と `[guard] 拒否（内部エラー）: …` を出す（Claude Code は終了コード 2 だけを拒否として扱い、1 などでは操作を通してしまう）。拒否・許可は `deny`・`allow` で終える。bash 3.2 では、`set -u` などで止まったときの EXIT の trap の `$?` が 0 になることがあるので、終了コードではなく `deny`・`allow` を通ったかで見分ける
  - 判定に使うデータを作るコマンド（`tr`・`jq`・`sed`・`grep`・`git` など）の失敗は `set -e` に頼らず、親のシェルで明示的に検査して `internal_error` で拒否する。プロセス置換 `< <(…)` の失敗は親に伝わらず、コマンド置換の中や `if` の中の関数では `set -e` が効かないので、いったん変数に受けて（`x="$(…)" || internal_error …`）から、`split_lines`・`split_fields`（`.claude/hooks/lib.sh`）で配列に分けて読む。here-string（`<<<`）とヒアドキュメント（`<<`）は使わない（一時ファイルを作れない環境ではリダイレクトが失敗し、検査を飛ばしたまま許可に着く）。一致の判定は `printf … | grep -q` ではなく `re_match`（`.claude/hooks/lib.sh`）を使い、grep のエラーを「一致しない」と区別する
  - `git` の終了コード 128 は、Git の外のときだけでなく、git の設定の読み込みの失敗（`GIT_CONFIG_COUNT` が不正など）でも返る。終了コードだけで「Git の外」と扱わず、`git_not_repo`（`.claude/hooks/lib.sh`）で見分ける。それ以外の失敗は内部エラーで拒否する
- 手元で bash 3.2 のときの動きを確かめるには、`/bin/bash` へのシンボリックリンクを置いたディレクトリを PATH の先頭に入れ、`LC_ALL=ja_JP.UTF-8` でテスト（下の「テスト」）を実行する

### GitHub のトークン

`agentctl` は `GITHUB_TOKEN` か `GH_TOKEN` からトークンを読む。どちらもないときは、[agentctl.sh](../../scripts/agents/agentctl.sh) と [lib.sh](../../scripts/agents/lib.sh) の `agentctl` が `gh auth token` の値を `GH_TOKEN` に入れて渡す（#107）。トークンは標準出力・ログに出さない。`gh` がない・未ログインなら何もせず、`agentctl` が「トークンがない」で止まる（先に `gh auth login` する）。

### ブートストラップ（ワークフローを取り込む前）

ワークフローを取り込むまでは、GitHub で CI と Merge Gate が動かず、ステータス `ci-ok`・`merge-gate` が付かない。その間の PR は次のように扱う（#8・#73 が実例）。

- Orchestrator が GitHub から PR の状態を取得してスナップショット（JSON）にし、`agentctl gate --input` で判定する
- C4（CI）は、CI と同じコマンドをローカルで実行して代わりにする
- 判定の結果とローカルで実行したコマンドを、PR のコメントに記録する

```bash
go -C tools/agentctl run . gate --repo K-Kizuku/raft-game --pr 12 --json --input "$PWD/.agents/run/pr-12-snapshot.json"
```

## 運用のコマンド集

コマンドは 1 つずつ実行する。`agentctl` は [agentctl.sh](../../scripts/agents/agentctl.sh)（メインの作業ツリーの `.agents/bin/agentctl` にビルドして実行する）で呼ぶ。書き込みの API（`state`・`claim`・`attempt`）は `--repo` が必要で、`--dry-run` で行う予定の操作だけを出せる。

### Orchestrator の 1 周

[/orchestrate](../../.claude/skills/orchestrate/SKILL.md) の手順で、復旧 → 計画 → 起動 → レビュー → マージ → 片付けを回す。

```bash
scripts/agents/agentctl.sh recover --repo K-Kizuku/raft-game --json
```

```bash
scripts/agents/agentctl.sh plan --repo K-Kizuku/raft-game --json
```

```bash
scripts/agents/agentctl.sh plan --repo K-Kizuku/raft-game --mermaid
```

```bash
scripts/agents/agentctl.sh state 12 ready --repo K-Kizuku/raft-game --reason "Issue を補った"
```

```bash
scripts/agents/spawn-worker.sh 12 --background
```

```bash
scripts/agents/agentctl.sh state 12 in-review --repo K-Kizuku/raft-game --pr 34
```

```bash
scripts/agents/review.sh 34
```

```bash
scripts/agents/agentctl.sh state 12 ready-to-merge --repo K-Kizuku/raft-game
```

```bash
scripts/agents/merge.sh 34 --dry-run
```

```bash
scripts/agents/merge.sh 34
```

### 指摘・CI の失敗・Conflict への対応

```bash
scripts/agents/agentctl.sh state 12 changes-requested --repo K-Kizuku/raft-game
```

```bash
scripts/agents/agentctl.sh attempt 12 --cause review:F1 --repo K-Kizuku/raft-game
```

```bash
scripts/agents/spawn-worker.sh 12 --mode fix --note .agents/run/fix-12.md
```

```bash
scripts/agents/spawn-worker.sh 12 --mode conflict --note .agents/run/conflict-12.md
```

### 様子を見る

```bash
scripts/agents/status.sh
```

```bash
scripts/agents/wt.sh list
```

```bash
scripts/dev-slot.sh list
```

```bash
scripts/agents/spawn-worker.sh 12 --dry-run
```

### スクリプトの終了コード

| コマンド | 0 | 1 | 2 | 3 | 4 | 5 |
|---|---|---|---|---|---|---|
| `agentctl gate` | pass | fail | pending | 内部エラー | — | — |
| `agentctl state`・`attempt` | 成功 | 遷移や状態の確認で拒否 | — | 内部エラー | — | `attempt` の上限に達した |
| `agentctl claim` | 成功（2 回目は何もしない） | — | — | 内部エラー | 前提を満たさない | — |
| `merge.sh` | マージした（`--dry-run` は pass） | gate が fail・そのほかのエラー | gate が pending | 判定できない | — | — |
| `review.sh` | 記録を載せた | エラー | — | — | — | Codex も Claude Code もレビューできない |

### テスト

```bash
go -C tools/agentctl test -race ./...
```

```bash
bash .claude/hooks/tests/run.sh
```

```bash
bash scripts/agents/tests/run.sh
```

```bash
python3 scripts/check-docs.py
```

## Worktree の管理と片付け

| 項目 | 内容 |
|---|---|
| 場所 | `.claude/worktrees/issue-<n>`（`spawn-worker.sh` が `claude -w issue-<n>` で作る）。メインの作業ツリーの中だが、Git には入らない |
| ブランチ | `agent/issue-<n>`（Worker が最初に `git branch -m` で付け替え、`origin/main` に rebase する） |
| 環境変数 | `AGENT_ROLE=worker`・`AGENT_ISSUE=<n>`・`DEV_SLOT=<番号>`（[dev-slot.sh](../../scripts/dev-slot.sh) が割り当てる）、必要なら `AGENT_ALLOW_PROTECTED=1` |
| Worker の記録 | `.agents/run/issue-<n>.json`（issue・session・slot・worktree・branch・pid・mode・started_at・log）。ログは `.agents/logs/issue-<n>-<時刻>.jsonl` |
| 生存の確認 | `.agents/run/issue-<n>.json` の pid で確かめる（`pgrep` は、Claude Code がプロセスの表示名を書き換えるので当てにならない） |
| Worktree の外のファイル | Worker が読むには `--add-dir` が要る |

片付けは [wt.sh](../../scripts/agents/wt.sh) で行う（`merge.sh` も、マージの後に `wt.sh cleanup --issue <n>` を呼ぶ）。

```bash
scripts/agents/wt.sh cleanup --dry-run
```

```bash
scripts/agents/wt.sh cleanup
```

```bash
scripts/agents/wt.sh cleanup --issue 12
```

消すのは、次をすべて満たすものだけ。満たさないものは理由を出して残す。

| 条件 | 満たさないときに出る理由 |
|---|---|
| Worker が動いていない | Worker が動いている |
| 未コミットの変更がない | 未コミットの変更がある |
| PR がマージ済みか閉じている | PR が開いている・PR がない（または状態が分からない） |
| push 済み（PR の HEAD と同じか、リモートのブランチに含まれる） | push していないコミットがある |

消す順番: `.agents/archive/issue-<n>.json`（ブランチ・最後の SHA・PR・ログのパス・時刻）を書く → ロックされていれば `git worktree unlock` → `git worktree remove`（`--force` は使わない）→ ローカルのブランチの削除 → `DEV_SLOT` の解放 → `.agents/run/issue-<n>.json` の削除。

- テストで作られた `__pycache__` などの追跡されないファイルが残ると、`git worktree remove` が失敗する。`--force` は使わず、中身を確かめてから人が消す
- Docker のイメージ・コンテナの掃除は [ADR-0019](../adr/0019-parallel-local-development.md)（`make clean-docker`）

## 障害からの復旧・再試行・ロールバック

### 障害からの復旧（`agentctl recover`）

状態は GitHub にあるので、PC やセッションが止まっても、`recover` で次の行動が分かる。作業中の状態の Issue ごとに、GitHub（ラベル・記録・ブランチ・PR）とローカルの Worktree（`git worktree list` の `.claude/worktrees/issue-<n>`）から行動を 1 つ出す（[recover.go](../../tools/agentctl/internal/plan/recover.go)）。

```bash
scripts/agents/agentctl.sh recover --repo K-Kizuku/raft-game --json
```

| 行動 | 出るとき | 対応 |
|---|---|---|
| `escalate` | 1 つの Issue に開いた PR が 2 つ以上ある。または PR がマージ済みなのに、状態が `ready-to-merge` 以外で `merged` に遷移できない | `needs-human` を付けて人に任せる |
| `fix-labels` | `status:*` がちょうど 1 つではない（付け替えの途中で失敗したなど） | ラベルを直す。自動で直せなければ人に任せる |
| `set-state merged` | PR がマージ済みで、状態が `ready-to-merge` | `agentctl state <n> merged --pr <PR>` |
| `set-state in-review` | `in-progress` で、開いた PR がある | `agentctl state <n> in-review --pr <PR>` |
| `resume-worker` | `in-progress` で、ブランチがあり PR がない。またはブランチも PR もないが、ローカルの Worktree に未コミットの変更か push していないコミットがある。または `changes-requested` | `spawn-worker.sh <n> --resume`（`changes-requested` なら `--mode fix`）。出力に、状態の記録にある最新の `session` と Worktree のパスが入る |
| `reset-to-ready` | `in-progress` で、ブランチも PR もローカルの作業もない（Worker が push する前に止まった） | `agentctl state <n> ready --reason …` |
| `request-review` | `in-review` で、HEAD の SHA に対するレビュー記録がない | `review.sh <PR>` |
| `merge` | `ready-to-merge` で、HEAD のステータス `merge-gate` が success | `merge.sh <PR>` |

中断した Worker の再開:

- `spawn-worker.sh <n> --resume` は、Worktree があり、`.agents/run/issue-<n>.json` のセッションが残っていれば `claude -r <session>` で再開する。なければ新しいセッションで Worktree の中から始め、最初に Git の状態と PR の有無を確かめさせる
- `--resume` のときは `agentctl claim --resume` が、attempt を 1 増やした記録を付ける（`in-progress` で、ブランチがあり、PR がないときだけ）
- `.agents/run/` が失われても、Issue の状態のコメントの `session` で `claude -r` により再開できる（#4 が実例）
- 応答が遅いだけの Worker を「中断」と誤って判断しないように、pid が生きているかを先に確かめる（`status.sh`）

### 再試行の上限

同じ原因の再試行は `retry_limit`（3 回）までにする。原因は `--cause` のキーで数える。

| 原因 | キーの例 |
|---|---|
| レビューの指摘 | `review:F3` |
| CI の失敗 | `ci:ci-ok` |
| Conflict | `conflict` |

```bash
scripts/agents/agentctl.sh attempt 12 --cause ci:ci-ok --repo K-Kizuku/raft-game
```

- 同じ cause の記録が `retry_limit` に達していたら、`attempt` は `status:blocked` と `needs-human` を付けて終了コード 5 で終わる。まだなら記録を 1 つ足す
- 人が `blocked` から戻した（`agentctl state <n> ready` など）後は、その記録より後だけを数える
- Worker も、同じ原因で 3 回直してもテストが通らなければ止めて報告する。`review.sh` も、Codex と Claude Code をそれぞれ `retry_limit` 回まで試す

### マージ後の revert・rollback

`main` は Rulesets で守られ、直接の push・force push はできない。マージを取り消すときも、PR と Merge Gate を通す。

1. 取り消す PR（squash merge なので `main` の 1 コミット）と、理由を Issue に書く。変更予定ファイルには、元の PR が変えたファイルを入れる
2. Orchestrator が、その Issue を通常どおり Worker に任せる。Worker は `agent/issue-<n>` で `git revert <squash の SHA>` を行い、テストする
3. レビュー・CI・Merge Gate を通してマージする
4. 元の Issue は `merged` のまま（`merged` から出る遷移はない）。必要なら、やり直しの Issue を新しく作る

```bash
git revert --no-edit <squash の SHA>
```

- 急ぐときは、オーナーが GitHub の「Revert」で PR を作ってもよい。ただし Merge Gate は C2（`Closes #N` と AC）と C11（変更予定ファイル）を求めるので、Issue を用意する
- 本番・デモの環境へのデプロイと、その rollback は CI だけが行う（[ADR-0019](../adr/0019-parallel-local-development.md)）。エージェントは直接行わない
- データの破壊など、revert で元に戻せない変更は、人の判断を求める（下の条件）

## 人間が介入する条件

次のどれかに当たったら、エージェントはその Issue の作業を止め、Issue に `needs-human` を付けて（必要なら `status:blocked` にして）、状況と選択肢を報告する。

- 要件や仕様に、解決できない矛盾がある
- データの破壊など、元に戻せない操作が必要
- 本番のインフラや機密情報への権限の変更が必要（Rulesets・シークレット・Cloudflare の設定を含む）
- セキュリティ上の重大なリスクが見つかった
- 同じ原因の再試行が上限（3 回）を超えても解決しない（`status:blocked` と `needs-human`）
- トークン・費用の上限や実行の制限を超えた

Merge Gate は、ラベル `status:blocked` か `needs-human` が付いた PR を C1 で fail にする。人は判断の後、ラベルを外し、`agentctl state <n> ready`（など）で戻す。`/orchestrate` の「止めて報告する条件」は、これを Orchestrator の手順に落としたもの。

## 運用で分かったこと

| 事柄 | 対応 |
|---|---|
| AI の認証に `workflow` 権限がない | ワークフローの正本は `.github/workflow-sources/`。人が `install-workflows.sh` で取り込む |
| Claude Code は、`dontAsk` のエージェントのセッションから `.claude/` への書き込みを拒否する | 下の「`.claude/` を変える Issue」 |
| 信頼していないワークスペースでは、`settings.json` の `permissions.allow` が無視される（deny と hooks は効く） | スクリプトは `--allowedTools` を明示する |
| `claude -p --agent X --json-schema` では、構造化された出力が null になる | Reviewer の定義は `--append-system-prompt` で渡す |
| PR の中の `.claude/` は、その PR が書き換えられる | Reviewer は main の settings・hooks・定義で動かす |
| `pgrep` では Worker の生存を確かめられない | pid で確かめる |
| Worker は Worktree の外のファイルを読めない | `--add-dir` を付ける |
| 変更予定ファイル・変更禁止範囲の glob の末尾の `/` | Merge Gate の C11 が fail にする。`dir/**` と書く |
| PR の本文に例として `Closes #N` を書く | C2 が数えて fail にする。GitHub が別の Issue を閉じる危険もある |
| Epic に `status:*` を付ける | `agentctl plan` が作業中として数え、容量を減らす。付けない |

### `.claude/` を変える Issue

`.claude/**` を変える Issue では、Worker は変更したファイルを `.agents/run/dotclaude/` に（同じ相対パスで）用意する。Orchestrator が内容を変えずに Worker のブランチにコミットし、そのことを PR に記録する。保護されたパスなので、Merge Gate の C12 でレビューの確認が要る。

### クラウドの環境（Claude Code on the web）で動かすときの違い

正はローカル PC での運用。クラウドの環境では次が違う。

- Worker は `gh` と GitHub の API を使わない。PR の作成・コメント・マージは、Orchestrator が GitHub の MCP で行う（MCP のマージは `guard-mcp.sh` が Orchestrator 以外を拒否する）
- PR の本文は、Worker が Worktree の中の `.agents/run/pr-body.md` に書き、Orchestrator がそれで PR を作る
- ネットワークの制限で Codex が使えないことが多い。`review.sh` はタイムアウトで `service_unavailable` としてフォールバックする

## 検証の結果

並列開発の仕組みを、#2〜#5 の開発そのもので確かめた結果（2026-11-06 時点）。「未検証」は、まだ確かめていないもの。

| シナリオ | 結果 | 証拠 |
|---|---|---|
| 独立した 2 Issue の並列実装 | 検証済み | 第 1 波 #2∥#3、第 2 波 #4∥#5（変更予定ファイルが重ならないことを確かめて同時に起動）。`agentctl plan` は実データのスナップショットで、#5 と `.gitignore` が重なる #9 を選ばなかった |
| Worker 間の Worktree の分離 | 検証済み | `claude -w issue-<n>` で `.claude/worktrees/issue-<n>`・ブランチ `agent/issue-<n>`。Worker に Worktree の外への Write を試させると `guard-write.sh` が拒否した |
| CI 失敗時のマージのブロック | 一部検証。**GitHub Actions の CI と `merge-gate` のステータスは未検証** | PR #8 で shellcheck が失敗 → マージせず修正。PR #76 で `review.sh` が動かない（2 回）→ マージせず修正。PR #77 は必ず失敗するテスト（開いたまま）。GitHub の CI と `merge-gate` は、ワークフローを取り込んだ後に確かめる |
| Codex の指摘の後の修正・再レビュー | 検証済み（レビュー役はフォールバック）。**Codex 自体のレビューは未検証** | PR #76: round 1 で high の指摘 F1 → 修正 → round 2 で resolved・approve。PR #75: F1（medium）〜F4 → 修正 → round 2 で approve |
| Codex が使えないときのフォールバック | 検証済み | Codex CLI 0.167.0 がネットワークの遮断で終わらない → `review.sh` がタイムアウト（終了コード 124）で `service_unavailable` と分類し、別セッションの Claude Code に切り替えた。すべての PR の記録に `fallback_reason` がある |
| Conflict の発生と、委任による解消 | 検証済み | #8 のマージの後、#3 のブランチが `CLAUDE.md` で競合 → Orchestrator が方針（両方を残す・順番）を決め、Worker が rebase で解消した（PR #73 に記録） |
| Orchestrator 以外からのマージの拒否 | 一部検証。**GitHub の Rulesets は未検証**（オーナーの適用待ち） | Worker の役割での `merge.sh` を hooks が拒否した（実際のセッション）。`gh pr merge`・`main` への push・`gh api …/merge`・MCP のマージは、hooks に入力を直接渡して拒否を確かめた |
| セッションの中断からの復旧 | 検証済み | #4 の Worker を実装の途中で止め、Issue の状態のコメントのセッション ID で `claude -r` により再開して完成した。`recover` にローカルの Worktree の確認を足した。誤って「中断」と判断した例（応答が遅いだけ）では、再開したセッションが二重の書き込みを避けて止まった |
| マージの後の Worktree の片付け | 検証済み | `wt.sh cleanup` が、マージ済みの issue-5 を記録（`.agents/archive/issue-5.json`）を残して消し、PR が開いている issue-7 は残した。`__pycache__` が残ると `git worktree remove` が失敗することも確かめた |
| マージ後の revert | 未検証 | — |
| `agentctl attempt` の上限での `blocked`・`needs-human` | 未検証（実際の Issue では。単体テストはある） | — |
