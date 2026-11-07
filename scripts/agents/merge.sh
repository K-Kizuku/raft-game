#!/usr/bin/env bash
# Merge Gate（agentctl gate）が pass の PR だけを squash merge する（Issue #5）。
#
# 使い方: scripts/agents/merge.sh <pr> [--dry-run]
#   --dry-run   判定だけをして、マージのコマンドを出す（マージしない）
#
# - 役割が orchestrator で、リンクした Worktree の外でなければ拒否する
# - agentctl gate --repo <repo> --pr <n> --json が pass のときだけ
#   gh pr merge <n> --squash --match-head-commit <gate が返した head_sha> --delete-branch
# - その後 agentctl state <issue> merged --pr <n> と wt.sh cleanup --issue <issue>
# - 終了コード: マージした（または --dry-run で pass）=0、gate が fail=1、pending=2、その他のエラー=1・3
# - GitHub の操作（gate・gh pr view・gh pr merge・state）は、AI 用の GitHub App のトークンで行う（#282。
#   lib.sh の agentctl・ghx）。使えなければ gh のログイン（オーナーのトークン）。Rulesets の必須のチェックはそのまま効く
set -euo pipefail

# shellcheck disable=SC2034 # lib.sh の die などが使う
PROG="merge"
# shellcheck source=lib.sh disable=SC1091
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

pr=""
dry_run=0
while [ "$#" -gt 0 ]; do
  case "$1" in
    --dry-run) dry_run=1 ;;
    -h | --help)
      usage_from_header "$0"
      exit 0
      ;;
    -*) die "不明な引数: $1" ;;
    *)
      [ -z "$pr" ] || die "PR の番号は 1 つだけ"
      pr="$1"
      ;;
  esac
  shift
done
case "$pr" in
  '' | *[!0-9]*) die "PR の番号を指定する（--help）" ;;
esac

need git jq
require_orchestrator
if in_linked_worktree; then
  die "リンクした Worktree の中では実行しない（メインの作業ツリーで実行する）"
fi
need gh
root="$(main_root)"
cd "$root"
repo="$(repo_slug)"

set +e
gate_out="$(agentctl gate --repo "$repo" --pr "$pr" --json)"
gate_rc=$?
set -e
verdict="$(printf '%s' "$gate_out" | jq -r '.verdict // empty' 2>/dev/null || true)"
head_sha="$(printf '%s' "$gate_out" | jq -r '.head_sha // empty' 2>/dev/null || true)"

case "$gate_rc:$verdict" in
  0:pass) ;;
  2:* | *:pending) die_code 2 "Merge Gate が pending（PR #${pr}）。マージしない" ;;
  1:* | *:fail)
    printf '%s' "$gate_out" | jq -r '.results[]? | select(.status != "pass") | "  \(.id) \(.status): \(.detail)"' >&2 || true
    die_code 1 "Merge Gate が fail（PR #${pr}）。マージしない"
    ;;
  *) die_code 3 "Merge Gate の判定ができない（終了コード ${gate_rc}・verdict=${verdict:-なし}）。マージしない" ;;
esac
case "$head_sha" in
  [0-9a-f]*) ;;
  *) die_code 3 "Merge Gate が head_sha を返していない" ;;
esac
[ "${#head_sha}" -eq 40 ] || die_code 3 "Merge Gate の head_sha が 40 桁ではない（${head_sha}）"

# Issue の番号: ブランチ名 agent/issue-<n>、なければ PR の本文の Closes #<n>
pr_info="$(ghx pr view "$pr" --json headRefName,body)" || die "PR #$pr を読めない"
head_ref="$(printf '%s' "$pr_info" | jq -r '.headRefName // ""')"
prefix="$(cfg '.branch_prefix' 'agent/issue-')"
issue=""
case "$head_ref" in
  "$prefix"*) issue="${head_ref#"$prefix"}" ;;
esac
case "$issue" in
  '' | *[!0-9]*)
    issue="$(printf '%s' "$pr_info" | jq -r '.body // ""' |
      grep -Eio '(close[sd]?|fix(e[sd])?|resolve[sd]?)[[:space:]]+#[0-9]+' | head -n 1 | grep -Eo '[0-9]+' || true)"
    ;;
esac

merge_cmd=(gh pr merge "$pr" --squash --match-head-commit "$head_sha" --delete-branch)
if [ "$dry_run" = 1 ]; then
  echo "# merge --dry-run（マージしない）: Merge Gate は pass"
  printf 'merge:'
  printf ' %q' "${merge_cmd[@]}"
  echo
  [ -z "$issue" ] || echo "after: agentctl state $issue merged --pr $pr; scripts/agents/wt.sh cleanup --issue $issue"
  exit 0
fi

# 表示は gh のまま、実行は ghx（App のトークン）で行う
ghx "${merge_cmd[@]:1}" || die "gh pr merge に失敗した（PR #${pr}）"
info "PR #$pr を squash merge した（${head_sha}）"

if [ -z "$issue" ]; then
  info "Issue の番号が分からないので、状態の更新と Worktree の片付けを飛ばす"
  exit 0
fi
agentctl state "$issue" merged --repo "$repo" --pr "$pr" || info "agentctl state $issue merged に失敗した（agentctl recover で直す）"
"$AGENTS_DIR/wt.sh" cleanup --issue "$issue" || info "Worktree の片付けで残ったものがある（scripts/agents/wt.sh list で確かめる）"
