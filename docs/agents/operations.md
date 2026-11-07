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

### GitHub の API の上限

GitHub の REST API は、認証済みで 1 時間に 5,000 回まで。オーナーの `gh` のトークンを Orchestrator・Worker・見張りのスクリプト・レビューで共有するので、並行して動かすとすぐに達する（2026-11-07 に 30 分で 2 回達した。#135）。上限に達している間は、`agentctl gate`・`review.sh`・Worker の `gh` が 403 で失敗する。残りの回数と戻る時刻（`reset`、UNIX 時刻）は `gh api rate_limit --jq .resources.core` で見られる（この呼び出しは回数に数えない）。

呼び出しの多い所の 1 つが、[review.sh](../../scripts/agents/review.sh) の Codex の GitHub 連携（`--via github`）の結果の待ち。`@codex review` を依頼したあと、次の間隔で PR のコメントを読み直す。間隔は環境変数で変えられる（秒、0 以上の整数）。

| 環境変数 | 既定 | 意味 |
|---|---|---|
| `AGENT_CODEX_GITHUB_FIRST_WAIT` | 60 | 依頼したあと、最初に読むまで待つ秒数（`AGENT_CODEX_GITHUB_TIMEOUT` を超えない） |
| `AGENT_CODEX_GITHUB_POLL` | 30 | その後に読み直す間隔。Codex の結果が出てから気づくまでの遅れは、最大でこの秒数 |
| `AGENT_CODEX_GITHUB_TIMEOUT` | 900 | 待ちの全体の上限。超えたら `service_unavailable` として、ローカルの Codex CLI → Claude Code の経路に切り替える |
| `AGENT_CODEX_GITHUB_RATE_LIMIT_WAIT` | 60 | API の上限に達したとき、戻る時刻が読めない場合に待つ秒数 |

- 待っている間に API の上限（`agentctl` のエラーの「（レート制限）」・`rate limit`）に達したら、`gh api rate_limit` の `reset` の時刻まで待ってから読み直す。失敗（`retry_limit` 回で切り替える連携の不調）としては数えない
- `gh api rate_limit` が読めない、または `remaining` が 0 でない（二次的な上限など、`reset` が当てにならない）ときは、`AGENT_CODEX_GITHUB_RATE_LIMIT_WAIT` 秒待つ
- 上限が戻るのが待ちの全体の上限（`AGENT_CODEX_GITHUB_TIMEOUT`）より後なら、待たずにタイムアウトと同じ理由（`service_unavailable`）で終わる
- 呼び出しを減らしたいときは、`AGENT_CODEX_GITHUB_POLL` を大きくする（例: `AGENT_CODEX_GITHUB_POLL=60 scripts/agents/review.sh 12`）

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

### マージの列と Worker の自動起動（#154）

上の 1 周のうち、Worker の起動と、レビューの依頼からマージまでを、2 つのスクリプトで自動で回せる。どちらも Orchestrator だけが、メインの作業ツリーで使う（Worker・Reviewer が実行すると hooks が拒否する）。終わらずに回り続けるので、バックグラウンドで動かす。

```bash
scripts/agents/merge-train.sh
```

```bash
scripts/agents/autospawn.sh
```

[merge-train.sh](../../scripts/agents/merge-train.sh)（マージの列。GitHub の merge queue は個人のアカウントのリポジトリで使えないための代わり）:

- 引数がなければ、周ごとに開いた PR（ブランチ `agent/issue-<n>`）を PR 番号の小さい順に見つける。`PR:Issue` を並べて渡すと、その PR だけを前から順に扱う。`.agents/run/train/skip` に書いた PR 番号は飛ばす。PR の Issue は一度だけ `in-review` にする（遷移に成功したか、すでに `in-review` 以降の状態のときだけ `.agents/run/train/inreview-<PR>` の印を作る。一時的に失敗したら次の周で試す）
- 1 周で、前から順に Merge Gate を判定し、pass の最初の 1 つを、Issue を `ready-to-merge` にしてから `merge.sh` でマージする。マージしたら、メインの作業ツリーで `git pull --ff-only` を実行する（`.agents/config.json` などの変更を、`autospawn.sh` などの判定にすぐ反映するため）
- 残りの PR は 1 歩ずつ進める

| Merge Gate の結果 | 行うこと |
|---|---|
| C10 が fail | main の取り込みを依頼する（`PUT pulls/<PR>/update-branch`）。HTTP 422 の merge conflict は「止まった」。それ以外の失敗（通信・HTTP 5xx・API の上限など）は次の周で試す。成功したら次の周で判定し直す |
| C9 が fail | 「止まった」（コンフリクト） |
| 最新のレビュー記録の round が `review.max_rounds` 以上で、C6 が pass でないか C12 が fail | 強制マージ待ち（下の「レビューは 2 往復まで」）。`review.sh` を起動せず、「止まった」にもしない |
| C6 が fail、または C6 が pass で C7 が fail | 「止まった」（`request_changes`・重大な指摘） |
| C6 が pass でなく、C10 が pass | `review.sh` をバックグラウンドで起動する（PR ごとに 1 つまで。`.agents/run/train/review-<PR>.pid` で判定し、終わっていれば起動し直す） |
| C6 が pass で C12 が fail、C10 が pass | 保護されたパスの確認の記録（`protected_paths_ack`）が足りない（最新の記録が Codex の GitHub 連携など）。`review.sh` を HEAD ごとに 1 回だけバックグラウンドで起動する（`.agents/run/train/review-c12-<PR>` に HEAD を記録する）。そのレビューのあとも C12 が fail なら「止まった」 |
| C2 が fail で、`ci-ok` が pass | 本文の `` - [ ] AC-n: CI（`ci-ok`）が通る `` の行だけを `[x]` にする（行の末尾の「（…結果待ち）」は外す） |

- 止まった PR は、ログに理由を出して、新しい PR か修正を待つ（終わらない）。Orchestrator が「指摘・CI の失敗・Conflict への対応」で直す
- 判定の使い回し: PR の HEAD・更新時刻・コメントの数・チェックの状態と、Issue の更新時刻と main の HEAD が前の周と同じなら、前の判定を使う（`agentctl gate` は引き継ぎの判定で REST の API を 10 回以上呼ぶため。Issue の更新時刻は、Issue の側だけで AC・変更予定ファイルを直したときに判定し直すため。Issue を読めなければ、その周はその PR に何もしない）。ただし、目印に表れない一時的な pending（C9 の `mergeable` の未計算）を含む判定は 2 周まで使い、過ぎたら判定し直す
- `agentctl gate` の結果が空・不正（API の上限など）のときは、その周はその PR に何もしない。`gh` が失敗しても止まらず、次の周で試す。`gh api rate_limit` の残りが 300 未満なら、戻る時刻まで休む
- マージ済み・閉じた PR は列から外す（`merge.sh` がマージの後の処理で 0 以外を返したときも）
- **凍結（#240）**: 保護されたパスの PR（C12 が fail・pending か、C12 の detail が「保護されたパスの変更（」で始まる）の `review.sh` を起動したら、それが動いている間は、ほかの PR を（Merge Gate が pass でも）マージしない。保護されたパスの PR はレビューの引き継ぎ（#120）が使えないので、レビューの間に main が進むと「取り込み → 再レビュー」を繰り返し、いつまでもマージできないため（#209・#214）。印は `.agents/run/train/freeze-<PR>`（起動した時刻）。`review.sh` が終わるか、`review.sh` の待ちの上限（`AGENT_CODEX_GITHUB_TIMEOUT`＋`AGENT_CODEX_TIMEOUT`、既定 900＋300 秒）を超えたら解く。凍結した PR 自身は pass ならマージする
- **レビューは 2 往復まで（#240）**: オーナーの決まり（2026-11-08）。最新のレビュー記録（Merge Gate の `latest_review.round`）が `.agents/config.json` の `review.max_rounds`（書かなければ 2）以上で、C6 が pass でない（か C12 が fail の）PR には、もう `review.sh` を起動しない。`ci-ok` が pass なら「強制マージ待ち」として `.agents/run/train/force-merge-<PR>.json`（`pr`・`issue`・`head_sha`・`round`・`max_rounds`・`ci_ok`・`failed`＝pass でない条件・`recorded_at`）に記録し、ログに「強制マージ待ち」と出す（同じ HEAD の間は「強制マージ待ちのまま」）。Orchestrator はこれをオーナーに知らせ、オーナーが強制マージする。`ci-ok` が pass でない間は記録を作らず、ログに「ci-ok が pass になるのを待つ」と出す。記録は、列から外したとき（マージ済み・閉じた）と、上限に当たらなくなったときに消す
- **review.sh を子にしない（#240）**: `review.sh` は、`merge-train.sh` と別のセッション（`setsid` がなければ、ジョブ制御 `set -m` で別のプロセスグループ）で、二重の fork で起動する。`merge-train.sh` を止めても（TaskStop・Ctrl-C でプロセスグループごと止めても）、`review.sh` は止まらない

[autospawn.sh](../../scripts/agents/autospawn.sh)（Worker の自動起動）:

- 周ごとに `agentctl plan --json` の `ready` を出た順に、`capacity` の数まで `spawn-worker.sh <n> --background` で起動する。拒否された Issue（終了コード 4 など）は、その周は飛ばす
- `deferred`（実装中の Issue と変更予定ファイルが重なる）は起動しない。`conflicts`・`deferred` の Issue と理由は、前の周と変わったときだけログに出す
- **状態の自動の直し（#240）**: 周ごとに、`agentctl plan --json` の `active` のうち `status` が `in-progress` で、動いている Worker（`.agents/run/issue-<n>.json` の pid が生きているもの）がなく、開いた PR（ブランチ `agent/issue-<n>`）がある Issue を `in-review` に移す（`agentctl state <n> in-review --pr <PR>`）。Worker が終わったのに API の上限などで状態を移しそこねると、枠（`capacity`）を食い続けるため（#211・#213）。枠は次の周の `plan` で反映される。移せなければログに出して次の周で試す
- **Worker を子にしない（#240）**: `spawn-worker.sh --background` は、`autospawn.sh` と別のセッション（`setsid` がなければ、ジョブ制御 `set -m` で別のプロセスグループ）で実行する。Worker はそのセッション・プロセスグループに入り、`spawn-worker.sh` が戻ると init の子になるので、`autospawn.sh` を止めても（プロセスグループごとでも）Worker は止まらない（#218）
- 止めるには `.agents/run/autospawn.stop` を作る（次の周で終わる。始めるときと終わるときに消す）

| 環境変数 | 既定 | 意味 |
|---|---|---|
| `AGENT_TRAIN_INTERVAL` | 180 | `merge-train.sh` の周の間隔（秒） |
| `AGENT_TRAIN_CYCLES` | 0 | `merge-train.sh` がこの数の周で終わる（テスト用。0 は終わらない） |
| `AGENT_CODEX_GITHUB_TIMEOUT`・`AGENT_CODEX_TIMEOUT` | 900・300 | `review.sh` の待ちの上限（秒）。`merge-train.sh` は、この和を超えたら凍結を解く |
| `AGENT_AUTOSPAWN_INTERVAL` | 300 | `autospawn.sh` の周の間隔（秒） |

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
- 本番・デモの環境へのデプロイは CI だけが行い、前の版に戻すのは人が [docs/runbooks/deploy-rollback.md](../runbooks/deploy-rollback.md) の手順で行う。エージェントは直接行わない
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
