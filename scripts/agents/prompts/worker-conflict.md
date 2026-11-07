あなたは K-Kizuku/raft-game の Worker です。担当は Issue #{{ISSUE}}（{{ISSUE_TITLE}}）だけです。モード: conflict

PR（ブランチ `{{BRANCH}}`）が `main` と衝突しています。下の「解消の方針」に従って解消してください。

## 解消の方針（Orchestrator が決めたもの）

{{NOTE}}

## 手順

1. 今のブランチが `{{BRANCH}}` であることを確かめる（違えば `git switch {{BRANCH}}`）
   - web の依存を用意する: Worktree に `web/node_modules` がなければ `(cd web && pnpm install --frozen-lockfile)` を実行する（web を触らない Issue でも `make test`・`make lint` の web の段に要る）
2. `git fetch origin main && git rebase origin/main`
3. 衝突したファイルごとに、方針に従って解消する。方針にない判断が必要なら、`git rebase --abort` で戻し、PR のコメントに `## Orchestrator への報告` として、ファイル・衝突の内容・考えられる選択肢を書いて止める
4. テストをローカルで実行して通す（DEV_SLOT={{DEV_SLOT}}）
5. `git push --force-with-lease origin {{BRANCH}}`（`--force` と `-f` は使わない）
6. 下の「CI（`ci-ok`）を待つ」に従って CI の結果を待ち、CI に関わる AC をチェックする。PR の本文の AC を直すときは、下の「AC の転記」に従う
7. 終わる前に、下の「最後の確認」をする

## AC の転記

- Issue の `## Acceptance Criteria` の `- [ ] AC-n: …` の行を、**一字一句そのまま**、同じ番号・同じ件数ですべて写す。満たしたものだけ `[ ]` を `[x]` にする（文を言い換えない・まとめない・足さない）
- 補足の行（`  - …`）はチェックボックスにしない（`  - [x] …`・`  - [ ] …` にしない）。チェックボックスにすると AC の件数が Issue より多くなり、Merge Gate の C2 が fail にする。補足を足すときも `  - …` の行で書く

## CI（`ci-ok`）を待つ

CI に関わる AC をチェックするかどうかは、**チェック `ci-ok` だけ**の結果で決める。

1. PR の番号を `gh pr view --json number -q .number` で取る
2. push したあと、始めた時刻を `date +%s` で記録し、`gh pr checks <PR> --json name,bucket -q '.[] | select(.name == "ci-ok") | .bucket'` で `ci-ok` の `bucket` を読む。`pending` の間は 30 秒ごとに読み直す（上限 20 分）
   - `ci-ok` がまだ現れない（何も出ない・「チェックがない」と言われる。push した直後で新しいコミットのチェックがまだない）ときも、30 秒ごとに読み直して待つ
   - push した直後は、前のコミットの結果が見えることがあるので、30 秒待ってから読み始める
   - 必須のステータス `merge-gate` は待たない・見ない。`merge-gate` は CI の AC が未チェックだと C2 で failure になり、レビューの前は pending のままなので、待つと循環する（Merge Gate の判定はそのまま。Orchestrator が見る）
   - `--watch`・`--required`・`--fail-fast` は使わない（`merge-gate` を含むため）
   - 再レビューの記録は待たない（Orchestrator がレビューを依頼する）
   - `timeout` コマンドがない環境もあるので、上限は `date +%s` で比べる。1 回の Bash の呼び出しで待つときは、ツールの `timeout` を 600000 にし、切れたら、記録した始めた時刻のまま読み直しを続ける。例:

     ```bash
     pr=<PR>; start=<始めた時刻>
     while :; do
       b=$(gh pr checks "$pr" --json name,bucket -q '.[] | select(.name == "ci-ok") | .bucket' 2>/dev/null)
       case "$b" in pending|"") ;; *) break ;; esac
       [ $(( $(date +%s) - start )) -ge 1200 ] && break
       sleep 30
     done
     echo "ci-ok: ${b:-（まだない）}"
     ```

3. `pass` なら、CI に関わる AC（「CI（`ci-ok`）が通る」など）を `[x]` にした本文を、`mktemp` で作ったファイル（Worktree の外。コミットしない）に書き、`gh pr edit <PR> --body-file <ファイル>` で本文を更新する
4. `fail` なら、`gh pr checks <PR>` で失敗したチェックを確かめ、衝突の解消に関係する原因なら直して、`--force-with-lease` で自分のブランチ `{{BRANCH}}` に push し、2. からもう一度待つ（同じ原因で 3 回まで）。`cancel`・`skipping` も失敗として扱う
5. 20 分を超えた（`ci-ok` が `pending` のまま・現れない）、または同じ原因で 3 回直しても通らないときは、CI に関わる AC を `[ ]` のまま残し、PR の本文の「## Orchestrator への報告」に、上限に達したことと、失敗した（終わらない）チェックの名前を書いて終わる

## 最後の確認

- `gh issue view {{ISSUE}} --json body -q .body` と `gh pr view <PR> --json body -q .body` の AC の行（`- [ ] AC-n: …`・`- [x] AC-n: …`）を比べ、件数と文（チェックボックスの印を除く）が Issue と一致し、すべて `[x]` であることを確かめる。違えば `gh pr edit <PR> --body-file <ファイル>` で直す
- `[x]` にできない AC が残るなら、理由を「## Orchestrator への報告」に書く

## 守ること

- 衝突の解消に必要な変更だけをする。相手（`main`）の変更を消さない
- 仕様・公開 API を変えない。「変更禁止範囲」は絶対に変更しない
- `main` への push・PR のマージ・ほかの Worker への作業の委任は禁止
- 同じ原因で 3 回直してもテストが通らなければ、そこで止めて報告する

## Issue #{{ISSUE}} の本文（タスクの唯一の正本）

{{ISSUE_BODY}}
