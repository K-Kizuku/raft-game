#!/usr/bin/env bash
# Worker（Claude Code）を Issue 専用の Worktree で起動する（Issue #5）。
#
# 使い方: scripts/agents/spawn-worker.sh <issue> [--mode implement|fix|conflict] [--resume]
#                                        [--background] [--dry-run] [--note FILE]
#   --mode        implement（既定）・fix（レビューの指摘・CI の失敗への対応）・conflict（Conflict の解消）
#   --resume      中断した Worker を再開する（.agents/run/issue-<n>.json のセッションを使う）
#   --background  起動したらすぐに戻る（ログは .agents/logs/）
#   --dry-run     何も変えずに、組み立てたコマンドを出す
#   --note FILE   プロンプトの {{NOTE}} に入れる文章（conflict の解消の方針・fix の指示など）
#
# - 役割が orchestrator でなければ拒否する
# - 動いている Worker（.agents/run/issue-*.json の pid が生きているもの）が max_workers 以上なら拒否する
# - git fetch origin main → DEV_SLOT を確保 → agentctl claim（implement と --resume のとき）→
#   AGENT_ROLE=worker AGENT_ISSUE=<n> DEV_SLOT=<slot> claude -p -w issue-<n> --agent worker
#   --permission-mode dontAsk --allowedTools <worker.md の tools> --session-id <uuid>
#   --verbose --output-format stream-json "<prompt>"
# - Issue の変更予定ファイルが protected_paths と重なるときは AGENT_ALLOW_PROTECTED=1 を付ける
# - 状態は .agents/run/issue-<n>.json（issue・session・slot・worktree・branch・pid・mode・started_at・log）
# - agentctl claim が拒否したら（同じ Issue の 2 回目など）、何もしないでその終了コードで終わる
set -euo pipefail

# shellcheck disable=SC2034 # lib.sh の die などが使う
PROG="spawn-worker"
# shellcheck source=lib.sh disable=SC1091
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

issue=""
mode="implement"
resume=0
background=0
dry_run=0
note_file=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --mode)
      [ "$#" -ge 2 ] || die "--mode には値が必要"
      mode="$2"
      shift
      ;;
    --mode=*) mode="${1#--mode=}" ;;
    --resume) resume=1 ;;
    --background) background=1 ;;
    --dry-run) dry_run=1 ;;
    --note)
      [ "$#" -ge 2 ] || die "--note には値が必要"
      note_file="$2"
      shift
      ;;
    -h | --help)
      usage_from_header "$0"
      exit 0
      ;;
    -*) die "不明な引数: $1" ;;
    *)
      [ -z "$issue" ] || die "Issue の番号は 1 つだけ"
      issue="$1"
      ;;
  esac
  shift
done
case "$issue" in
  '' | *[!0-9]*) die "Issue の番号を指定する（--help）" ;;
esac
case "$mode" in
  implement | fix | conflict) ;;
  *) die "--mode は implement・fix・conflict のどれか（$mode）" ;;
esac

need git jq
require_orchestrator
need claude gh
root="$(main_root)"
cd "$root"

prompt_file="$AGENTS_DIR/prompts/worker-$mode.md"
[ -f "$prompt_file" ] || die "$prompt_file がない"
note=""
if [ -n "$note_file" ]; then
  [ -r "$note_file" ] || die "$note_file を読めない"
  note="$(cat "$note_file")"
fi

run_dir="$root/.agents/run"
log_dir="$root/.agents/logs"
run_file="$run_dir/issue-$issue.json"
wt="$root/.claude/worktrees/issue-$issue"
branch="$(cfg '.branch_prefix' 'agent/issue-')$issue"
max_workers="$(cfg '.max_workers' 2)"

# ---- 動いている Worker の数 ----
running=0
for f in "$run_dir"/issue-*.json; do
  [ -f "$f" ] || continue
  p="$(jq -r '.pid // empty' "$f" 2>/dev/null || true)"
  if pid_alive "$p"; then
    if [ "$f" = "$run_file" ]; then
      die "Issue #$issue の Worker はすでに動いている（pid $p）"
    fi
    running=$((running + 1))
  fi
done
if [ "$running" -ge "$max_workers" ]; then
  die "動いている Worker が max_workers（$max_workers）に達している（$running 件）。終わるのを待つ"
fi

# ---- Issue の本文と、保護されたパスとの重なり ----
issue_json="$(gh issue view "$issue" --json title,body)" || die "Issue #$issue を読めない（gh issue view）"
issue_title="$(printf '%s' "$issue_json" | jq -r '.title // ""')"
issue_body="$(printf '%s' "$issue_json" | jq -r '.body // ""')"

# planned_files: 本文の「## 変更予定ファイル」の項目（バッククォートの中、なければ箇条書きの文）。
planned_files() {
  printf '%s\n' "$issue_body" | tr -d '\r' | awk '
    /^##[ \t]/ { insec = ($0 ~ /^##[ \t]+変更予定ファイル[ \t]*$/); next }
    insec && /^[ \t]*[-*][ \t]+/ {
      line = $0
      n = 0
      while (match(line, /`[^`]+`/)) {
        print substr(line, RSTART + 1, RLENGTH - 2)
        line = substr(line, RSTART + RLENGTH)
        n++
      }
      if (n == 0) {
        sub(/^[ \t]*[-*][ \t]+/, "", line)
        sub(/[ \t].*$/, "", line)
        if (line != "") print line
      }
    }'
}

# glob_prefix <glob>: 最初のワイルドカードより前の部分。
glob_prefix() {
  local g="$1"
  g="${g%%[*?[{]*}"
  printf '%s\n' "$g"
}

allow_protected=0
protected="$(cfg '.protected_paths | join("\n")' '')"
while IFS= read -r pf; do
  [ -n "$pf" ] || continue
  a="$(glob_prefix "$pf")"
  while IFS= read -r pp; do
    [ -n "$pp" ] || continue
    b="$(glob_prefix "$pp")"
    case "$a" in "$b"*) allow_protected=1 ;; esac
    case "$b" in "$a"*) allow_protected=1 ;; esac
  done <<EOF
$protected
EOF
done <<EOF
$(planned_files)
EOF

# ---- セッションと起動の方法 ----
old_session=""
if [ -f "$run_file" ]; then
  old_session="$(jq -r '.session // empty' "$run_file" 2>/dev/null || true)"
fi

session_exists() {
  local dir="${CLAUDE_CONFIG_DIR:-$HOME/.claude}/projects"
  [ -n "$1" ] && [ -d "$dir" ] && [ -n "$(find "$dir" -name "$1.jsonl" -print -quit 2>/dev/null)" ]
}

resume_note=""
use_resume_flag=0
use_w=1
session=""
if [ "$resume" = 1 ]; then
  resume_note="これは中断した作業の再開です。最初に Git の状態（git status・git log・origin/$branch）と PR の有無を確かめ、どこまで進んだかを判断してから続けてください。"
  if [ -d "$wt" ]; then
    use_w=0
    if session_exists "$old_session"; then
      session="$old_session"
      use_resume_flag=1
    fi
  fi
elif [ "$mode" != "implement" ] && [ -d "$wt" ]; then
  use_w=0
fi
[ -n "$session" ] || session="$(new_uuid)"

# ---- DEV_SLOT ----
slot_new=0
if [ "$dry_run" = 1 ]; then
  slot="$("$AGENTS_DIR/../dev-slot.sh" current --path "$wt" 2>/dev/null || echo '<acquire>')"
else
  git fetch --quiet origin main || die "git fetch origin main に失敗した"
  if ! "$AGENTS_DIR/../dev-slot.sh" current --path "$wt" >/dev/null 2>&1; then
    slot_new=1
  fi
  slot="$("$AGENTS_DIR/../dev-slot.sh" acquire --path "$wt" --pid "$$")" || die "空いている DEV_SLOT がないので起動しない"
fi

release_slot() {
  if [ "$slot_new" = 1 ]; then
    "$AGENTS_DIR/../dev-slot.sh" release "$slot" >/dev/null 2>&1 || true
  fi
}

# ---- 着手の宣言 ----
claim_args=()
if [ "$mode" = "implement" ] || [ "$resume" = 1 ]; then
  claim_args=(claim "$issue" --repo "$(repo_slug)" --worker "issue-$issue" --session "$session")
  [ "$resume" = 1 ] && claim_args+=(--resume)
fi

# ---- プロンプトとコマンド ----
prompt="$(render_template "$prompt_file" \
  ISSUE "$issue" BRANCH "$branch" MODE "$mode" DEV_SLOT "$slot" \
  ISSUE_TITLE "$issue_title" ISSUE_BODY "$issue_body" NOTE "${note:-（なし）}")"
if [ -n "$resume_note" ]; then
  prompt="$resume_note

$prompt"
fi

envs=(AGENT_ROLE=worker "AGENT_ISSUE=$issue" "DEV_SLOT=$slot")
[ "$allow_protected" = 1 ] && envs+=(AGENT_ALLOW_PROTECTED=1)

cmd=(claude -p)
run_cwd="$root"
if [ "$use_resume_flag" = 1 ]; then
  cmd+=(-r "$session")
  run_cwd="$wt"
elif [ "$use_w" = 1 ]; then
  cmd+=(-w "issue-$issue")
else
  run_cwd="$wt"
fi
# dontAsk では許可の一覧にないツールが拒否されるので、worker.md の tools を --allowedTools で明示する。
worker_tools="$(agent_tools "$(agent_file worker)")"
cmd+=(--agent worker --permission-mode dontAsk --allowedTools "$worker_tools")
[ "$use_resume_flag" = 1 ] || cmd+=(--session-id "$session")
# プロンプトは、値を 1 つだけとるオプション（--output-format）の直後に置く
# （--allowedTools は値を複数とるので、後ろに置くとプロンプトがツール名として解釈される）。
cmd+=(--verbose --output-format stream-json "$prompt")

if [ "$dry_run" = 1 ]; then
  echo "# spawn-worker --dry-run（何も変えていない）"
  echo "issue: $issue"
  echo "mode: $mode"
  echo "cwd: $run_cwd"
  echo "slot: $slot"
  echo "session: $session"
  echo "allow_protected: $allow_protected"
  if [ "${#claim_args[@]}" -gt 0 ]; then
    printf 'claim:'
    printf ' %q' agentctl "${claim_args[@]}"
    echo
  fi
  printf 'command:'
  printf ' %q' env "${envs[@]}" "${cmd[@]}"
  echo
  exit 0
fi

if [ "${#claim_args[@]}" -gt 0 ]; then
  set +e
  agentctl "${claim_args[@]}"
  rc=$?
  set -e
  if [ "$rc" -ne 0 ]; then
    release_slot
    die_code "$rc" "agentctl claim が拒否した（終了コード $rc）。Worker は起動しない"
  fi
fi

mkdir -p "$run_dir" "$log_dir"
log="$log_dir/issue-$issue-$(date -u +%Y%m%dT%H%M%SZ).jsonl"
(cd "$run_cwd" && exec nohup env "${envs[@]}" "${cmd[@]}") </dev/null >"$log" 2>&1 &
pid=$!

jq -n --argjson issue "$issue" --arg session "$session" --arg slot "$slot" --arg worktree "$wt" \
  --arg branch "$branch" --argjson pid "$pid" --arg mode "$mode" --arg started_at "$(now_utc)" --arg log "$log" \
  '{issue:$issue, session:$session, slot:($slot|tonumber? // $slot), worktree:$worktree, branch:$branch,
    pid:$pid, mode:$mode, started_at:$started_at, log:$log}' >"$run_file.tmp"
mv "$run_file.tmp" "$run_file"
"$AGENTS_DIR/../dev-slot.sh" acquire --path "$wt" --pid "$pid" >/dev/null

info "Issue #$issue の Worker を起動した（pid $pid・DEV_SLOT $slot・ログ $log）"
if [ "$background" = 1 ]; then
  exit 0
fi
set +e
wait "$pid"
rc=$?
set -e
info "Worker が終了した（終了コード $rc）"
exit "$rc"
