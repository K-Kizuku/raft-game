#!/usr/bin/env bash
# PR をレビューし、レビュー記録（agent-review/v1、Issue #3）を PR のコメントに載せる（Issue #5）。
#
# 使い方: scripts/agents/review.sh <pr> [--no-post] [--out FILE] [--reviewer codex|claude]
#                                   [--base REF --head REF --issue-file FILE --previous FILE]
#   --no-post        PR にコメントせず、コメントの本文を標準出力に出す
#   --out FILE       レビュー記録の JSON を FILE に書く
#   --reviewer R     codex: Codex だけを使う（フォールバックしない）
#                    claude: Codex を使わずに Claude Code で行う（理由を AGENT_FALLBACK_REASON に入れる）
#                    省略: まず Codex、使えないとき（4 つの理由）だけ Claude Code
#   --base/--head    比べるコミット（省略時は gh pr view の baseRefOid・headRefOid）
#   --issue-file     Issue の本文（省略時は PR の本文の Closes #N から gh issue view）
#   --previous       前回のレビュー記録（JSON かコメントの本文。省略時は PR のコメントの最新のもの）
#
# - 役割が orchestrator でなければ拒否する
# - PR の HEAD を一時的な Worktree に --detach で取り出し、そこでレビューする（終了時に必ず消す）
# - Codex は timeout（既定 300 秒、AGENT_CODEX_TIMEOUT）付きで実行し、失敗の理由を標準エラーと
#   終了コードから分類する。標準エラーのうち、プロンプト（Issue の本文などを含む）を echo した行は除き、
#   具体的なエラーの書き方だけを探す（Issue の本文の「auth」「timeout」などの語に一致させない）:
#   コマンドがない → not_installed、
#   not logged in・HTTP 401/403・401 Unauthorized・authentication failed など → auth_error、
#   HTTP 429・429 Too Many Requests・rate limit exceeded・usage limit → rate_limited、
#   終了コード 124・HTTP 5xx・503 Service Unavailable・ECONNREFUSED などの ECONN・
#   Proxy connection failed・waiting for network・stream disconnected → service_unavailable、
#   それ以外（出力がスキーマに合わないときを含む）→ retry_limit 回まで Codex で再試行し、だめなら終了コード 5
# - 理由が上の 4 つのときだけ、別のセッションの Claude Code に切り替える。--agent reviewer は使わず、
#   メインの作業ツリーの .claude/agents/reviewer.md の本文（--append-system-prompt）とツール
#   （--tools・--allowedTools）を渡す（PR の中の定義に左右されないようにする）
# - 同じ理由で、PR の HEAD の .claude/settings.json と hooks も使わない。プロジェクトの設定を読まず
#   （--setting-sources user・--strict-mcp-config）、メインの作業ツリーの settings.json の hooks のコマンドの
#   $CLAUDE_PROJECT_DIR を main の絶対パスに置き換えたものを --settings で渡す
# - 記録は agentctl review validate で検証し、agentctl review render で本文にして gh pr comment で載せる
set -euo pipefail

# shellcheck disable=SC2034 # lib.sh の die などが使う
PROG="review"
# shellcheck source=lib.sh disable=SC1091
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

pr=""
no_post=0
out_file=""
reviewer_opt=""
base_ref=""
head_ref=""
issue_file=""
previous_file=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --no-post) no_post=1 ;;
    --out | --reviewer | --base | --head | --issue-file | --previous)
      [ "$#" -ge 2 ] || die "$1 には値が必要"
      case "$1" in
        --out) out_file="$2" ;;
        --reviewer) reviewer_opt="$2" ;;
        --base) base_ref="$2" ;;
        --head) head_ref="$2" ;;
        --issue-file) issue_file="$2" ;;
        --previous) previous_file="$2" ;;
      esac
      shift
      ;;
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
case "$reviewer_opt" in
  '' | codex | claude) ;;
  *) die "--reviewer は codex か claude（$reviewer_opt）" ;;
esac

need git jq
require_orchestrator
root="$(main_root)"
cd "$root"

abs_file() {
  case "$1" in
    /*) printf '%s\n' "$1" ;;
    *) printf '%s\n' "$OLDPWD/$1" ;;
  esac
}
[ -z "$out_file" ] || out_file="$(abs_file "$out_file")"
[ -z "$issue_file" ] || issue_file="$(abs_file "$issue_file")"
[ -z "$previous_file" ] || previous_file="$(abs_file "$previous_file")"
[ -z "$issue_file" ] || [ -f "$issue_file" ] || die "$issue_file がない"
[ -z "$previous_file" ] || [ -f "$previous_file" ] || die "$previous_file がない"

schema="$AGENTS_DIR/review-schema.json"
prompt_tpl="$AGENTS_DIR/prompts/review.md"
[ -f "$schema" ] || die "$schema がない"
[ -f "$prompt_tpl" ] || die "$prompt_tpl がない"
retry_limit="$(cfg '.retry_limit' 3)"
allow_fallback="$(cfg '.review.allow_fallback' true)"
fallback_reasons="$(cfg '.review.fallback_reasons | join(" ")' 'auth_error rate_limited service_unavailable not_installed')"
codex_timeout="${AGENT_CODEX_TIMEOUT:-300}"

tmp="$(mktemp -d)"
wtp="$tmp/wt"
ctx="$tmp/ctx"
mkdir -p "$ctx"
cleanup() {
  if [ -d "$wtp" ]; then
    git -C "$root" worktree remove "$wtp" >/dev/null 2>&1 || true
  fi
  rm -rf "$tmp"
  git -C "$root" worktree prune >/dev/null 2>&1 || true
}
trap cleanup EXIT

# ---- PR の情報 ----
pr_json="{}"
if [ -z "$head_ref" ] || [ -z "$base_ref" ] || [ -z "$issue_file" ] || [ -z "$previous_file" ]; then
  need gh
  pr_json="$(gh pr view "$pr" --json headRefOid,baseRefOid,baseRefName,body,comments)" || die "PR #$pr を読めない（gh pr view）"
fi

ensure_commit() {
  git cat-file -e "$1^{commit}" 2>/dev/null
}

if [ -n "$head_ref" ]; then
  head_sha="$(git rev-parse --verify --quiet "$head_ref^{commit}")" || die "--head $head_ref が見つからない"
else
  head_sha="$(printf '%s' "$pr_json" | jq -r '.headRefOid // empty')"
  [ -n "$head_sha" ] || die "PR #$pr の headRefOid がない"
  ensure_commit "$head_sha" || git fetch --quiet origin "pull/$pr/head" || true
  ensure_commit "$head_sha" || die "PR #$pr の HEAD（$head_sha）を取得できない"
fi
if [ -n "$base_ref" ]; then
  base_sha="$(git rev-parse --verify --quiet "$base_ref^{commit}")" || die "--base $base_ref が見つからない"
else
  base_sha="$(printf '%s' "$pr_json" | jq -r '.baseRefOid // empty')"
  [ -n "$base_sha" ] || die "PR #$pr の baseRefOid がない"
  if ! ensure_commit "$base_sha"; then
    git fetch --quiet origin "$(printf '%s' "$pr_json" | jq -r '.baseRefName // "main"')" || true
  fi
  ensure_commit "$base_sha" || die "PR #$pr の base（$base_sha）を取得できない"
fi

# ---- Issue の本文 ----
if [ -n "$issue_file" ]; then
  issue_body="$(cat "$issue_file")"
else
  issue_no="$(printf '%s' "$pr_json" | jq -r '.body // ""' |
    grep -Eio '(close[sd]?|fix(e[sd])?|resolve[sd]?)[[:space:]]+#[0-9]+' | head -n 1 | grep -Eo '[0-9]+' || true)"
  if [ -n "$issue_no" ]; then
    issue_body="$(gh issue view "$issue_no" --json body -q .body)" || die "Issue #$issue_no を読めない"
  else
    issue_body="（PR の本文に Closes #N がないため、Issue の本文は分からない）"
  fi
fi

# ---- 前回のレビュー記録 ----
# extract_record: 標準入力（JSON かコメントの本文）からレビュー記録の JSON を出す。
extract_record() {
  local text
  text="$(cat)"
  case "$text" in
    *'<!-- agent-review:v1 -->'*)
      printf '%s\n' "$text" | awk '
        /^[ ]*(```|~~~)[ \t]*json[ \t]*$/ { inblk = 1; next }
        inblk && /^[ ]*(```|~~~)[ \t]*$/ { inblk = 0; exit }
        inblk { print }'
      ;;
    *) printf '%s\n' "$text" ;;
  esac
}

previous=""
if [ -n "$previous_file" ]; then
  previous="$(extract_record <"$previous_file")"
else
  last_body="$(printf '%s' "$pr_json" |
    jq -r '[.comments[]? | select(.body | contains("<!-- agent-review:v1 -->"))] | last | .body // empty')"
  [ -z "$last_body" ] || previous="$(printf '%s\n' "$last_body" | extract_record)"
fi
round=1
previous_text="（前回のレビュー記録はない。初回のレビュー）"
if [ -n "$previous" ]; then
  printf '%s\n' "$previous" | jq -e . >/dev/null 2>&1 || die "前回のレビュー記録が JSON ではない"
  round="$(printf '%s\n' "$previous" | jq -r '(.round // 0) + 1')"
  previous_text="$(printf '%s\n' "$previous" | jq -r '
    "round \(.round)（\(.head_sha)）の判定: \(.verdict)\n" +
    ([.findings[]? | "- \(.id) [\(.severity)/\(.category)] \(.file):\(.line) \(.title)（\(.status)）"] | join("\n"))')"
  printf '%s\n' "$previous" >"$ctx/previous.json"
fi

# ---- 一時的な Worktree と文脈 ----
git -C "$root" worktree add --quiet --detach "$wtp" "$head_sha" || die "一時的な Worktree を作れない"
printf '%s\n' "$issue_body" >"$ctx/issue.md"
git -C "$root" diff "$base_sha...$head_sha" >"$ctx/diff.patch" 2>/dev/null ||
  git -C "$root" diff "$base_sha" "$head_sha" >"$ctx/diff.patch"
diff_stat="$(git -C "$root" diff --stat "$base_sha...$head_sha" 2>/dev/null ||
  git -C "$root" diff --stat "$base_sha" "$head_sha")"
protected="$(cfg '.protected_paths | join(", ")' '')"

prompt="$(render_template "$prompt_tpl" \
  PR "$pr" ROUND "$round" BASE_SHA "$base_sha" HEAD_SHA "$head_sha" \
  ISSUE_BODY "$issue_body" DIFF_STAT "$diff_stat" PREVIOUS "$previous_text" \
  CONTEXT_DIR "$ctx" PROTECTED_PATHS "$protected")"

record="$tmp/record.json"

# build_record <reviewer> <reviewer_version> <fallback_reason> <レビュー役の出力の JSON のファイル>
build_record() {
  local raw="$4"
  [ -s "$raw" ] || return 1
  jq -e 'type == "object"' "$raw" >/dev/null 2>&1 || return 1
  jq -n --slurpfile r "$raw" --argjson pr "$pr" --arg head "$head_sha" --arg base "$base_sha" \
    --argjson round "$round" --arg reviewer "$1" --arg ver "$2" --arg reason "$3" --arg at "$(now_utc)" '
    $r[0] as $o |
    {schema: "agent-review/v1", pr: $pr, head_sha: $head, base_sha: $base, round: $round,
     reviewer: $reviewer, reviewer_version: $ver, fallback_reason: $reason,
     verdict: $o.verdict, summary: $o.summary, protected_paths_ack: $o.protected_paths_ack,
     findings: $o.findings, created_at: $at}' >"$record" 2>/dev/null || return 1
  if ! agentctl review validate "$record" >"$tmp/validate.out" 2>&1; then
    info "レビュー記録が書式に合わない: $(tr '\n' ' ' <"$tmp/validate.out")"
    return 1
  fi
}

printf '%s\n' "$prompt" >"$tmp/prompt.txt"

# classify <終了コード> <標準エラーのファイル>
# プロンプトを echo した行（Issue の本文や前回の指摘を含む）は除いてから、具体的なエラーの書き方を探す。
classify() {
  local rc="$1" f="$2" g="$tmp/codex-stderr.filtered"
  if [ "$rc" = 124 ]; then
    echo service_unavailable
    return 0
  fi
  grep -vxF -f "$tmp/prompt.txt" "$f" >"$g" 2>/dev/null || true
  if grep -Eiq 'not logged in|please (re-?)?log ?in|(log ?in|sign ?in) again|(HTTP|status)[ :]*40[13]([^0-9]|$)|40[13] (Unauthorized|Forbidden)|authentication (error|failed|required)|invalid api[ _-]?key|(access|auth|refresh) token (has )?expired' "$g"; then
    echo auth_error
  elif grep -Eiq '(HTTP|status)[ :]*429([^0-9]|$)|429 Too Many Requests|too many requests|rate[ _-]?limit(ed| exceeded| reached)|usage limit' "$g"; then
    echo rate_limited
  elif grep -Eiq '(HTTP|status)[ :]*5[0-9][0-9]([^0-9]|$)|5[0-9][0-9] (Internal Server Error|Bad Gateway|Service Unavailable|Gateway Timeout)|Proxy connection failed|waiting for network|stream disconnected|ECONN(REFUSED|RESET|ABORTED)|ETIMEDOUT|connection (refused|reset|timed out)|request timed out' "$g"; then
    echo service_unavailable
  else
    echo other
  fi
}

is_fallback_reason() {
  case " $fallback_reasons " in
    *" $1 "*) return 0 ;;
  esac
  return 1
}

done_reviewer=""
fallback_reason=""

# ---- Codex ----
run_codex() {
  if ! command -v codex >/dev/null 2>&1; then
    fallback_reason="not_installed"
    info "codex が見つからない（not_installed）"
    return 1
  fi
  local ver to=() attempt rc c
  ver="$(codex --version 2>/dev/null | head -n 1 || true)"
  [ -n "$ver" ] || ver="codex"
  if command -v timeout >/dev/null 2>&1; then
    to=(timeout "$codex_timeout")
  elif command -v gtimeout >/dev/null 2>&1; then
    to=(gtimeout "$codex_timeout")
  fi
  for ((attempt = 1; attempt <= retry_limit; attempt++)); do
    rm -f "$tmp/codex-out.json"
    set +e
    ${to[@]+"${to[@]}"} codex exec --cd "$wtp" --sandbox read-only --skip-git-repo-check \
      --output-schema "$schema" --output-last-message "$tmp/codex-out.json" "$prompt" \
      </dev/null >"$tmp/codex.out" 2>"$tmp/codex.log"
    rc=$?
    set -e
    if [ "$rc" -eq 0 ]; then
      if build_record codex "$ver" "" "$tmp/codex-out.json"; then
        done_reviewer="codex"
        return 0
      fi
      info "Codex の出力がスキーマに合わない（$attempt/$retry_limit 回目）。再試行する"
      continue
    fi
    c="$(classify "$rc" "$tmp/codex.log")"
    if [ "$c" != "other" ]; then
      fallback_reason="$c"
      info "Codex が使えない（$c、終了コード $rc）: $(tail -n 3 "$tmp/codex.log" | tr '\n' ' ')"
      return 1
    fi
    info "Codex が失敗した（終了コード $rc、$attempt/$retry_limit 回目）。再試行する"
  done
  die_code 5 "Codex が retry_limit（$retry_limit）回失敗した。フォールバックの理由に当たらないので止める"
}

# ---- Claude Code（別のセッション） ----
# sq <文字列>: シェルの単一引用符で囲む。
sq() {
  printf "'%s'" "$(printf '%s' "$1" | sed "s/'/'\\\\''/g")"
}

# reviewer_settings: Reviewer に渡す設定のファイルを作り、そのパスを出す。
# 一時的な Worktree（PR の HEAD）の .claude/settings.json と hooks は PR が書き換えられるので使わない。
# メインの作業ツリーの settings.json の hooks のコマンドの $CLAUDE_PROJECT_DIR（Claude Code は
# カレントディレクトリ＝PR の HEAD にする）を、main の絶対パスに置き換える。
reviewer_settings() {
  local src="$root/.claude/settings.json" dst="$tmp/reviewer-settings.json"
  [ -f "$src" ] || die "$src がない（Reviewer のガードの hooks を main から読めない）"
  jq --arg q "$(sq "$root")" '
    def walk(f): . as $in
      | if type == "object" then reduce keys[] as $k ({}; . + {($k): ($in[$k] | walk(f))}) | f
        elif type == "array" then map(walk(f)) | f
        else f end;
    walk(if type == "object" and ((.command? // null) | type) == "string"
         then .command |= (gsub("\"\\$CLAUDE_PROJECT_DIR\""; $q) | gsub("\\$\\{?CLAUDE_PROJECT_DIR\\}?"; $q))
         else . end)' "$src" >"$dst" || die "$src を読めない"
  jq -e '[.hooks.PreToolUse[]? | select((.matcher // "") | test("Bash"))] | length > 0' "$dst" >/dev/null 2>&1 ||
    die "$src に Bash の hooks がない（Reviewer の書き込みを止められない）"
  ! grep -q 'CLAUDE_PROJECT_DIR' "$dst" || die "$dst に CLAUDE_PROJECT_DIR が残っている"
  printf '%s\n' "$dst"
}

run_claude() {
  need claude
  local ver attempt rc def tools sysprompt
  ver="$(claude --version 2>/dev/null | head -n 1 || true)"
  ver="claude-code ${ver:-unknown}"
  # --agent reviewer は一時的な Worktree（PR の HEAD）の .claude/agents/reviewer.md を読むので、
  # PR がその定義を書き換えられてしまう。メインの作業ツリーの定義の本文とツールを明示して渡す。
  def="$(agent_file reviewer)"
  tools="$(agent_tools "$def")"
  sysprompt="$(agent_body "$def")"
  settings="$(reviewer_settings)"
  for ((attempt = 1; attempt <= retry_limit; attempt++)); do
    set +e
    # プロンプトは、値を 1 つだけとるオプション（--output-format）の直後に置く
    # （--tools・--allowedTools・--add-dir は値を複数とるので、後ろに置くとプロンプトがその値として解釈される）。
    (cd "$wtp" && AGENT_ROLE=reviewer claude -p --permission-mode dontAsk \
      --setting-sources user --settings "$settings" --strict-mcp-config \
      --tools "$tools" --allowedTools "$tools" --append-system-prompt "$sysprompt" \
      --no-session-persistence --add-dir "$ctx" --json-schema "$(cat "$schema")" \
      --output-format json "$prompt") </dev/null >"$tmp/claude.json" 2>"$tmp/claude.log"
    rc=$?
    set -e
    if [ "$rc" -eq 0 ] &&
      jq -e '.structured_output | type == "object"' "$tmp/claude.json" >/dev/null 2>&1 &&
      jq '.structured_output' "$tmp/claude.json" >"$tmp/claude-out.json" &&
      build_record claude-fallback "$ver" "$fallback_reason" "$tmp/claude-out.json"; then
      done_reviewer="claude-fallback"
      return 0
    fi
    info "Claude Code のレビューが失敗した（終了コード $rc、$attempt/$retry_limit 回目）"
  done
  die_code 5 "Claude Code のレビューが retry_limit（$retry_limit）回失敗した"
}

case "$reviewer_opt" in
  codex)
    run_codex || die_code 5 "Codex が使えない（$fallback_reason）。--reviewer codex なので切り替えない"
    ;;
  claude)
    fallback_reason="${AGENT_FALLBACK_REASON:-}"
    is_fallback_reason "$fallback_reason" ||
      die "--reviewer claude では AGENT_FALLBACK_REASON に理由（$fallback_reasons）を入れる"
    run_claude
    ;;
  '')
    if ! run_codex; then
      [ "$allow_fallback" = "true" ] || die_code 5 "Codex が使えない（$fallback_reason）が、設定がフォールバックを許していない"
      is_fallback_reason "$fallback_reason" || die_code 5 "理由 $fallback_reason ではフォールバックしない"
      info "別のセッションの Claude Code に切り替える（fallback_reason=$fallback_reason）"
      run_claude
    fi
    ;;
esac

[ -z "$out_file" ] || cp "$record" "$out_file"
body="$tmp/body.md"
agentctl review render "$record" >"$body" || die "agentctl review render に失敗した"
verdict="$(jq -r .verdict "$record")"
if [ "$no_post" = 1 ]; then
  cat "$body"
else
  need gh
  gh pr comment "$pr" --body-file "$body" >/dev/null || die "PR #$pr にコメントできない"
fi
info "PR #$pr round $round: $verdict（$done_reviewer${fallback_reason:+、fallback_reason=$fallback_reason}）"
