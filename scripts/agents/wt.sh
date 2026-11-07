#!/usr/bin/env bash
# Worker の Worktree（.claude/worktrees/issue-*）の一覧と片付け（Issue #5）。
#
# 使い方: scripts/agents/wt.sh list|cleanup [--issue N] [--dry-run] [--pr-state-file FILE]
#   list               Worktree ごとに、ブランチ・未コミットの変更・未 push のコミット・PR の状態を出す
#   cleanup            安全なものだけを消す
#   --issue N          Issue N の Worktree だけを対象にする
#   --dry-run          消さずに、消す予定のものを出す
#   --pr-state-file F  PR の状態を gh の代わりに F（gh pr list --json headRefName,state,number,headRefOid
#                      と同じ形の配列）から読む
#
# 消すのは、PR がマージ済み（または閉じた）で、未コミットの変更がなく、push 済みで、
# Worker が動いていないものだけ。消す前に .agents/archive/issue-<n>.json（ブランチ・最後の SHA・
# PR・ログのパス・消した時刻）を書き、git worktree unlock（ロックされているときだけ）→
# git worktree remove（--force は使わない）→ ローカルのブランチの削除 → dev-slot.sh release →
# .agents/run/issue-<n>.json の削除 の順に行う。条件を満たさないものは理由を出して残す。
set -euo pipefail

# shellcheck disable=SC2034 # lib.sh の die などが使う
PROG="wt"
# shellcheck source=lib.sh disable=SC1091
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

sub="${1:-}"
[ "$#" -gt 0 ] && shift
only_issue=""
dry_run=0
pr_state_file=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --issue)
      [ "$#" -ge 2 ] || die "--issue には値が必要"
      only_issue="$2"
      shift
      ;;
    --dry-run) dry_run=1 ;;
    --pr-state-file)
      [ "$#" -ge 2 ] || die "--pr-state-file には値が必要"
      pr_state_file="$2"
      shift
      ;;
    -h | --help)
      usage_from_header "$0"
      exit 0
      ;;
    *) die "不明な引数: $1" ;;
  esac
  shift
done
case "$sub" in
  list | cleanup) ;;
  -h | --help | help)
    usage_from_header "$0"
    exit 0
    ;;
  *) die "list か cleanup を指定する（--help）" ;;
esac
case "$only_issue" in
  '' | [0-9]*) ;;
  *) die "--issue は番号" ;;
esac
if [ -n "$pr_state_file" ]; then
  case "$pr_state_file" in
    /*) ;;
    *) pr_state_file="$PWD/$pr_state_file" ;;
  esac
  [ -f "$pr_state_file" ] || die "$pr_state_file がない"
fi

need git jq
if [ "$sub" = "cleanup" ]; then
  require_orchestrator
fi
root="$(main_root)"
cd "$root"

# pr_states <branch>: PR の配列（JSON）。
pr_states() {
  if [ -n "$pr_state_file" ]; then
    jq -c --arg b "$1" '[.[] | select(.headRefName == $b)]' "$pr_state_file"
  else
    need gh
    gh pr list --head "$1" --state all --json headRefName,state,number,headRefOid
  fi
}

# is_locked <path>
is_locked() {
  local gd
  gd="$(git -C "$1" rev-parse --absolute-git-dir 2>/dev/null)" || return 1
  [ -f "$gd/locked" ]
}

status=0
found=0
for wt in "$root"/.claude/worktrees/issue-*; do
  [ -d "$wt" ] || continue
  n="${wt##*/issue-}"
  case "$n" in
    '' | *[!0-9]*) continue ;;
  esac
  if [ -n "$only_issue" ] && [ "$n" != "$only_issue" ]; then
    continue
  fi
  found=1
  wt="$(abs_dir "$wt")"
  branch="$(git -C "$wt" symbolic-ref --quiet --short HEAD 2>/dev/null || echo "")"
  sha="$(git -C "$wt" rev-parse HEAD 2>/dev/null || echo "")"
  dirty="$(git -C "$wt" status --porcelain 2>/dev/null | head -n 1 || true)"

  prs="[]"
  [ -z "$branch" ] || prs="$(pr_states "$branch")" || prs="error"
  if [ "$prs" = "error" ]; then
    pr_state="unknown"
    pr_no=""
  else
    pr_state="$(printf '%s' "$prs" | jq -r '
      if length == 0 then "none"
      elif any(.[]; .state == "OPEN") then "OPEN"
      elif any(.[]; .state == "MERGED") then "MERGED"
      else "CLOSED" end')"
    pr_no="$(printf '%s' "$prs" | jq -r '(map(select(.state == "MERGED")) + .)[0].number // empty')"
  fi

  # push 済み: PR の HEAD と同じか、リモートのブランチに含まれる
  pushed=0
  if [ -n "$sha" ]; then
    if [ "$prs" != "error" ] && printf '%s' "$prs" | jq -e --arg s "$sha" 'any(.[]; .headRefOid == $s)' >/dev/null; then
      pushed=1
    elif [ -n "$(git -C "$wt" branch -r --contains "$sha" 2>/dev/null | head -n 1)" ]; then
      pushed=1
    fi
  fi

  run_file="$root/.agents/run/issue-$n.json"
  running=0
  if [ -f "$run_file" ] && pid_alive "$(jq -r '.pid // empty' "$run_file" 2>/dev/null || true)"; then
    running=1
  fi

  if [ "$sub" = "list" ]; then
    printf 'issue-%s\tbranch=%s\tdirty=%s\tpushed=%s\tpr=%s%s\trunning=%s\n' "$n" "${branch:-（detached）}" \
      "$([ -n "$dirty" ] && echo yes || echo no)" "$([ "$pushed" = 1 ] && echo yes || echo no)" \
      "$pr_state" "${pr_no:+ #$pr_no}" "$([ "$running" = 1 ] && echo yes || echo no)"
    continue
  fi

  reason=""
  if [ "$running" = 1 ]; then
    reason="Worker が動いている"
  elif [ -n "$dirty" ]; then
    reason="未コミットの変更がある"
  elif [ "$pr_state" = "OPEN" ]; then
    reason="PR が開いている"
  elif [ "$pr_state" = "none" ] || [ "$pr_state" = "unknown" ]; then
    reason="PR がない（または状態が分からない）"
  elif [ "$pushed" != 1 ]; then
    reason="push していないコミットがある"
  fi
  if [ -n "$reason" ]; then
    echo "残す: issue-${n}（${reason}）"
    continue
  fi

  if [ "$dry_run" = 1 ]; then
    echo "消す予定: issue-${n}（branch=${branch}・PR #$pr_no ${pr_state}）"
    continue
  fi

  archive_dir="$root/.agents/archive"
  mkdir -p "$archive_dir"
  logs="$(find "$root/.agents/logs" -maxdepth 1 -name "issue-$n-*.jsonl" 2>/dev/null | sort || true)"
  jq -n --argjson issue "$n" --arg branch "$branch" --arg sha "$sha" --arg pr "$pr_no" --arg state "$pr_state" \
    --arg wt "$wt" --arg logs "$logs" --arg at "$(now_utc)" '
    {issue: $issue, branch: $branch, last_sha: $sha, pr: ($pr | tonumber? // null), pr_state: $state,
     worktree: $wt, logs: ($logs | split("\n") | map(select(. != ""))), removed_at: $at}' \
    >"$archive_dir/issue-$n.json"

  if is_locked "$wt"; then
    git worktree unlock "$wt"
  fi
  if ! git worktree remove "$wt"; then
    echo "残す: issue-${n}（git worktree remove に失敗した。--force は使わない）" >&2
    status=1
    continue
  fi
  if [ -n "$branch" ]; then
    git branch -D "$branch" >/dev/null || info "ローカルのブランチ $branch を消せない"
  fi
  "$AGENTS_DIR/../dev-slot.sh" release --path "$wt" >/dev/null 2>&1 || true
  rm -f "$run_file"
  echo "消した: issue-${n}（記録: .agents/archive/issue-$n.json）"
done

if [ "$found" = 0 ] && [ -n "$only_issue" ]; then
  echo "issue-$only_issue の Worktree はない"
fi
exit "$status"
