#!/usr/bin/env bash
# scripts/dev-slot.sh と scripts/agents/*.sh のテスト（Issue #5）。
# 一時的な Git リポジトリ（bare の origin と作業用の clone）と、PATH の先頭に置いた
# 偽の gh・codex・claude・agentctl を使う。偽のコマンドは引数を JSON の配列で記録する。
# 実行: bash scripts/agents/tests/run.sh
# （`[ … ] && pass || fail …` は pass が失敗しないので if の代わりに使う。jq の式の $ は jq の変数）
# shellcheck disable=SC2015,SC2016
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
src_root="$(cd "$here/../../.." && pwd)"

command -v jq >/dev/null 2>&1 || {
  echo "jq がない" >&2
  exit 1
}

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
tmp="$(cd "$tmp" && pwd -P)"

export GIT_CONFIG_NOSYSTEM=1
export GIT_CONFIG_GLOBAL="$tmp/gitconfig"
git config --file "$GIT_CONFIG_GLOBAL" user.name "test"
git config --file "$GIT_CONFIG_GLOBAL" user.email "test@example.invalid"
git config --file "$GIT_CONFIG_GLOBAL" init.defaultBranch main
unset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE AGENT_ROLE AGENT_ISSUE AGENT_ALLOW_PROTECTED AGENT_CONFIG \
  AGENT_REPO AGENTCTL AGENT_FALLBACK_REASON CLAUDE_PROJECT_DIR DEV_SLOT \
  GITHUB_TOKEN GH_TOKEN FAKE_GH_TOKEN AGENT_CODEX_GITHUB_TIMEOUT AGENT_CODEX_GITHUB_POLL \
  AGENT_CODEX_GITHUB_FIRST_WAIT AGENT_CODEX_GITHUB_RATE_LIMIT_WAIT AGENT_CODEX_TIMEOUT \
  AGENT_APP_ID AGENT_APP_KEY AGENT_GH_REAL AGENT_GH_SHIM_DIR AGENT_GH_APP_ACTIVE
export CLAUDE_CONFIG_DIR="$tmp/claude-config"

# ---- 本物の agentctl（review validate・render の書式の確認に使う。go がなければ jq で代わりに確かめる）----
real_agentctl=""
if command -v go >/dev/null 2>&1 && [ -f "$src_root/tools/agentctl/go.mod" ]; then
  if go -C "$src_root/tools/agentctl" build -o "$tmp/real-agentctl" . 2>"$tmp/go-build.log"; then
    real_agentctl="$tmp/real-agentctl"
  else
    echo "注意: agentctl をビルドできないので、偽の検証を使う" >&2
  fi
fi
export REAL_AGENTCTL="$real_agentctl"
# Issue #282: GitHub App のトークンのキャッシュ（~/.config/raft-game/）と鍵を本物の HOME で探さない
# （go のビルドのキャッシュは本物の HOME を使いたいので、ビルドの後で変える）
export HOME="$tmp/home"
mkdir -p "$HOME"

# ---- 偽のコマンド ----
fake="$tmp/fake"
mkdir -p "$fake/bin" "$fake/bin-nocodex" "$fake/log"
export FAKE_LOG="$fake/log"

cat >"$fake/bin/agentctl" <<'EOF'
#!/usr/bin/env bash
jq -cn --arg cwd "$PWD" '{cwd:$cwd, args:$ARGS.positional}' --args -- "$@" >>"$FAKE_LOG/agentctl.log"
case "$1" in
  gate)
    printf '%s\n' "${FAKE_GATE_JSON:-{\}}"
    exit "${FAKE_GATE_RC:-0}"
    ;;
  claim) exit "${FAKE_CLAIM_RC:-0}" ;;
  state) exit 0 ;;
  # Issue #82: Codex の GitHub 連携（FAKE_GH_MODE: unavailable・completed・pending・pending-then-completed・error・bad）
  comment)
    prev=""
    for a in "$@"; do
      [ "$prev" = "--body-file" ] && cp "$a" "$FAKE_LOG/comment.md"
      [ "$prev" = "--body" ] && [ "${FAKE_GH_MODE:-unavailable}" = unavailable ] && {
        echo "agentctl comment: github: POST /repos/x/issues/12/comments: 503" >&2
        exit 3
      }
      prev="$a"
    done
    echo '{"id":1}'
    ;;
  pr)
    sha=""
    prev=""
    for a in "$@"; do
      [ "$prev" = "--sha" ] && sha="$a"
      prev="$a"
    done
    n="$(jq -s '[.[] | select(.args[0:2] == ["pr","codex-record"])] | length' "$FAKE_LOG/agentctl.log")"
    mode="${FAKE_GH_MODE:-unavailable}"
    if [ "$2" = view ]; then
      # FAKE_PR_VIEW=fail で失敗。FAKE_PR_COMMENTS（JSON の配列）を comments に入れる
      [ "${FAKE_PR_VIEW:-}" != fail ] || { echo "agentctl pr view: github: 503" >&2; exit 3; }
      jq -cn --arg s "${FAKE_PR_HEAD_SHA:-}" --argjson c "${FAKE_PR_COMMENTS:-[]}" '{pull_request:{number:12, head_sha:$s}, comments:$c}'
      exit 0
    fi
    if [ "$2" = protected-files ]; then
      # FAKE_PROTECTED_FILES（改行区切り）を出す。FAKE_PROTECTED_RC で終了コード
      [ -z "${FAKE_PROTECTED_FILES:-}" ] || printf '%s\n' "$FAKE_PROTECTED_FILES"
      exit "${FAKE_PROTECTED_RC:-0}"
    fi
    [ "$mode" != pending-then-completed ] || { [ "$n" -gt 2 ] && mode=completed || mode=pending; }
    # Issue #135: API の上限（403 のレート制限）を 4 回（retry_limit より多く）返してから完了する
    [ "$mode" != ratelimit-then-completed ] || { [ "$n" -gt 4 ] && mode=completed || mode=ratelimit; }
    case "$mode" in
      unavailable | error) echo "agentctl pr codex-record: github: 502" >&2; exit 3 ;;
      ratelimit)
        echo "agentctl pr codex-record: github: GET /repos/x/issues/12/comments: 403 API rate limit exceeded for user ID 1.（レート制限）" >&2
        exit 3
        ;;
      pending) echo "agentctl pr codex-record: まだ" >&2; exit 2 ;;
      bad) echo '{"schema":"agent-review/v1"}' ;;
      completed)
        jq -n --arg h "$sha" --arg b "$FAKE_BASE_SHA" '{schema:"agent-review/v1", pr:12, head_sha:$h, base_sha:$b, round:2,
          reviewer:"codex", reviewer_version:"chatgpt-codex-connector (GitHub)", fallback_reason:"", verdict:"approve",
          summary:"Codex（GitHub 連携）", protected_paths_ack:{reviewed:false, weakens_guards:false},
          findings:[{id:"C4101", severity:"medium", category:"correctness", file:"a.txt", line:1, title:"P2: t",
            detail:"", status:"open", resolution:""}], created_at:"2026-11-06T00:00:00Z"}'
        ;;
    esac
    ;;
  review)
    if [ -n "${REAL_AGENTCTL:-}" ]; then
      exec "$REAL_AGENTCTL" "$@"
    fi
    f="$3"
    if [ "$2" = "validate" ]; then
      jq -e '.schema == "agent-review/v1" and (.head_sha | test("^[0-9a-f]{40}$"))
        and (.verdict == "approve" or .verdict == "request_changes")
        and ((.reviewer == "codex" and .fallback_reason == "") or (.reviewer == "claude-fallback" and .fallback_reason != ""))
        and (.findings | type == "array") and (.protected_paths_ack.reviewed | type == "boolean")' "$f" >/dev/null || {
        echo "fake agentctl: invalid" >&2
        exit 1
      }
      echo OK
    else
      printf '<!-- agent-review:v1 -->\n```json\n%s\n```\n' "$(cat "$f")"
    fi
    ;;
esac
EOF

cat >"$fake/bin/gh" <<'EOF'
#!/usr/bin/env bash
jq -cn --arg cwd "$PWD" '{cwd:$cwd, args:$ARGS.positional}' --args -- "$@" >>"$FAKE_LOG/gh.log"
# Issue #282: どのトークンで呼ばれたか（テストの中だけの記録）
printf '%s %s\n' "${GH_TOKEN:-none}" "$*" >>"$FAKE_LOG/gh-env.log"
case "$1 $2" in
  # Issue #282: GitHub App の JWT での呼び出し（FAKE_APP_MODE=noinstall で未インストール）
  "api -H" | "api -X")
    case "${@: -1}" in
      /repos/*/installation)
        if [ "${FAKE_APP_MODE:-ok}" = noinstall ]; then
          echo '{"message":"Not Found"}'
          echo "gh: Not Found (HTTP 404)" >&2
          exit 1
        fi
        echo '{"id":42}'
        ;;
      /app/installations/42/access_tokens)
        printf '{"token":"%s","expires_at":"2026-11-08T01:00:00Z"}\n' "${FAKE_APP_TOKEN:-ghs_test282}"
        ;;
      *) exit 1 ;;
    esac
    ;;
  "issue view")
    body="${FAKE_ISSUE_BODY:-本文}"
    case " $* " in
      *" -q "*) printf '%s\n' "$body" ;;
      *) jq -cn --arg b "$body" '{title:"テストの Issue", body:$b}' ;;
    esac
    ;;
  "pr view")
    jq -cn --arg ref "${FAKE_PR_HEAD_REF:-agent/issue-5}" '{headRefName:$ref, body:"Closes #5"}'
    ;;
  "pr list")
    b=""
    prev=""
    for a in "$@"; do
      [ "$prev" = "--head" ] && b="$a"
      prev="$a"
    done
    jq -c --arg b "$b" '[.[] | select(.headRefName == $b)]' "${FAKE_PR_LIST:-/dev/null}"
    ;;
  "pr comment")
    prev=""
    for a in "$@"; do
      [ "$prev" = "--body-file" ] && cp "$a" "$FAKE_LOG/comment.md"
      prev="$a"
    done
    ;;
  "pr merge") exit "${FAKE_MERGE_RC:-0}" ;;
  # Issue #135: --jq の結果（core の remaining と reset）。FAKE_RATE_RESET が未設定なら失敗する
  "api rate_limit")
    [ -n "${FAKE_RATE_RESET:-}" ] || { echo "HTTP 500" >&2; exit 1; }
    printf '%s %s\n' "${FAKE_RATE_REMAINING:-0}" "$FAKE_RATE_RESET"
    ;;
  # FAKE_GH_TOKEN が未設定なら未ログイン（本物の gh と同じく終了コード 1）。
  # 設定されていれば（空でも）その値を出す
  "auth token")
    if [ -n "${FAKE_GH_TOKEN+x}" ]; then
      printf '%s\n' "$FAKE_GH_TOKEN"
    else
      echo "no oauth token found for github.com" >&2
      exit 1
    fi
    ;;
esac
EOF

cat >"$fake/bin/codex" <<'EOF'
#!/usr/bin/env bash
if [ "${1:-}" = "--version" ]; then
  echo "codex-cli 0.99.0"
  exit 0
fi
jq -cn --arg cwd "$PWD" '{cwd:$cwd, args:$ARGS.positional}' --args -- "$@" >>"$FAKE_LOG/codex.log"
out=""
prev=""
for a in "$@"; do
  [ "$prev" = "--output-last-message" ] && out="$a"
  prev="$a"
done
n="$(wc -l <"$FAKE_LOG/codex.log" | tr -d ' ')"
good='{"verdict":"approve","summary":"問題なし","protected_paths_ack":{"reviewed":true,"weakens_guards":false},"findings":[]}'
case "${FAKE_CODEX_MODE:-good}" in
  good) printf '%s\n' "$good" >"$out" ;;
  auth) echo "Error: unexpected status 401 Unauthorized: please login again" >&2; exit 1 ;;
  rate) echo "ERROR: You've hit your usage limit (429)" >&2; exit 1 ;;
  net) echo "Reconnecting... 2/5 waiting for network" >&2; exit 1 ;;
  hang) sleep 5 ;;
  fail) echo "boom" >&2; exit 1 ;;
  # 本物の codex exec のように、プロンプト（Issue の本文などを含む）を標準エラーに echo してから、
  # 分類できない理由で失敗する。標準出力には紛らわしい語を出す。
  echo-fail)
    printf 'user\n%s\n' "${*: -1}" >&2
    echo "codex: auth login timeout HTTP 401 HTTP 503 429 Too Many Requests"
    echo "error: author field missing in the response" >&2
    exit 1
    ;;
  proxy) echo "ERROR: Proxy connection failed: 403" >&2; exit 1 ;;
  notlogged) echo "Error: Not logged in. Run codex login." >&2; exit 1 ;;
  toomany) echo "unexpected status 429 Too Many Requests" >&2; exit 1 ;;
  bad) printf '{"verdict":"maybe"}\n' >"$out" ;;
  bad-then-good)
    if [ "$n" -le 1 ]; then printf 'not json\n' >"$out"; else printf '%s\n' "$good" >"$out"; fi
    ;;
esac
EOF

cat >"$fake/bin/claude" <<'EOF'
#!/usr/bin/env bash
if [ "${1:-}" = "--version" ]; then
  echo "2.0.0 (Claude Code)"
  exit 0
fi
# Reviewer のときは、本物の Claude Code と同じように設定（--setting-sources に project があれば
# カレントディレクトリの .claude/settings.json、それと --settings）の PreToolUse の hooks を集め、
# CLAUDE_PROJECT_DIR をカレントディレクトリにして、書き込みの操作で試す（拒否されたら 2）。
settings="" sources="user,project,local" prev=""
for a in "$@"; do
  [ "$prev" = "--settings" ] && settings="$a"
  [ "$prev" = "--setting-sources" ] && sources="$a"
  prev="$a"
done
files=()
case ",$sources," in
  *,project,*) [ -f "$PWD/.claude/settings.json" ] && files+=("$PWD/.claude/settings.json") ;;
esac
[ -n "$settings" ] && files+=("$settings")
probe() { # probe <ツール名> <tool_input の JSON>
  local tool="$1" ti="$2" f cmd rc=0 r input
  input="$(jq -cn --arg cwd "$PWD" --arg t "$tool" --argjson ti "$ti" \
    '{session_id:"s", cwd:$cwd, hook_event_name:"PreToolUse", tool_name:$t, tool_input:$ti}')"
  for f in "${files[@]+"${files[@]}"}"; do
    while IFS= read -r cmd; do
      [ -n "$cmd" ] || continue
      r=0
      printf '%s' "$input" | CLAUDE_PROJECT_DIR="$PWD" bash -c "$cmd" >/dev/null 2>&1 || r=$?
      [ "$r" = 2 ] && rc=2
    done < <(jq -r --arg t "$tool" '.hooks.PreToolUse[]? | select(.matcher as $m | $t | test("^(" + $m + ")$")) | .hooks[]?.command' "$f" 2>/dev/null)
  done
  echo "$rc"
}
bash_rc="" write_rc="" hook_cmds="[]"
if [ "${AGENT_ROLE:-}" = "reviewer" ]; then
  hook_cmds="$(for f in "${files[@]+"${files[@]}"}"; do jq -c '[.hooks.PreToolUse[]?.hooks[]?.command]' "$f"; done | jq -cs 'add // []')"
  bash_rc="$(probe Bash '{"command":"gh pr comment 12 --body x"}')"
  write_rc="$(probe Write "$(jq -cn --arg p "$PWD/a.txt" '{file_path:$p, content:"x"}')")"
fi
# Issue #282: 引き継いだトークンの環境変数と、PATH の先頭（gh-app のラッパーを置いたか）も記録する
jq -cn --arg cwd "$PWD" --arg role "${AGENT_ROLE:-}" --arg settings "$settings" --argjson hooks "$hook_cmds" \
  --arg bash_rc "$bash_rc" --arg write_rc "$write_rc" --arg tok "${GH_TOKEN:-}${GITHUB_TOKEN:-}" \
  --arg path0 "${PATH%%:*}" --arg ghreal "${AGENT_GH_REAL:-}" \
  '{cwd:$cwd, role:$role, settings:$settings, hook_cmds:$hooks, bash_rc:$bash_rc, write_rc:$write_rc,
    token_env:$tok, path0:$path0, gh_real:$ghreal, args:$ARGS.positional}' \
  --args -- "$@" >>"$FAKE_LOG/claude.log"
case "${FAKE_CLAUDE_MODE:-good}" in
  good)
    echo '{"type":"result","is_error":false,"structured_output":{"verdict":"request_changes","summary":"直すところがある","protected_paths_ack":{"reviewed":true,"weakens_guards":false},"findings":[{"id":"F1","severity":"high","category":"correctness","file":"a.txt","line":1,"title":"t","detail":"d","status":"open","resolution":""}]}}'
    ;;
  fail) exit 1 ;;
esac
EOF
# Issue #282: 偽の openssl（JWT の署名。鍵は読まない。FAKE_OPENSSL_FAIL=1 で失敗する）
cat >"$fake/bin/openssl" <<'EOF'
#!/usr/bin/env bash
jq -cn '{args:$ARGS.positional}' --args -- "$@" >>"$FAKE_LOG/openssl.log"
cat >/dev/null
[ -z "${FAKE_OPENSSL_FAIL:-}" ] || { echo "unable to load key" >&2; exit 1; }
printf 'fake-signature'
EOF
chmod +x "$fake/bin/"*
for c in agentctl gh claude; do
  ln -s "$fake/bin/$c" "$fake/bin-nocodex/$c"
done
# Issue #135: 待たずに秒数を記録する偽の sleep（review.sh の待ちの間隔を確かめるときだけ PATH の先頭に入れる）
mkdir -p "$fake/bin-sleep"
cat >"$fake/bin-sleep/sleep" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$FAKE_LOG/sleep.log"
EOF
chmod +x "$fake/bin-sleep/sleep"

# 本物の gh・codex・claude・agentctl を隠す（Issue #81）。それらを含むディレクトリは、
# ディレクトリごと外すと同じ場所の git・rm なども消える（GitHub Actions では gh が /usr/bin にある）。
# そこで、隠す対象以外の実行ファイルへのシンボリックリンクを一時ディレクトリに作り、代わりに PATH に入れる。
sys_path=""
shadow_n=0
IFS=: read -r -a path_dirs <<<"$PATH"
for d in "${path_dirs[@]}"; do
  [ -n "$d" ] || continue
  if [ -x "$d/codex" ] || [ -x "$d/claude" ] || [ -x "$d/gh" ] || [ -x "$d/agentctl" ]; then
    shadow_n=$((shadow_n + 1))
    shadow="$tmp/sys-path/$shadow_n"
    mkdir -p "$shadow"
    for f in "$d"/*; do
      [ -f "$f" ] && [ -x "$f" ] || continue
      case "${f##*/}" in
        gh | codex | claude | agentctl) continue ;;
      esac
      ln -s "$f" "$shadow/${f##*/}"
    done
    d="$shadow"
  fi
  sys_path="${sys_path:+$sys_path:}$d"
done
export PATH="$fake/bin:$sys_path"
PATH_NOCODEX="$fake/bin-nocodex:$sys_path"

failures=0
total=0
pass() { total=$((total + 1)); }
fail() {
  total=$((total + 1))
  echo "FAIL - $1" >&2
  failures=$((failures + 1))
}
check() {
  local desc="$1"
  shift
  if "$@" >/dev/null; then
    pass
  else
    fail "$desc"
    if [ -s "$tmp/err" ]; then sed 's/^/       | /' "$tmp/err" | tail -n 5 >&2; fi
  fi
}
reset_logs() {
  rm -f "$FAKE_LOG"/*
  : >"$FAKE_LOG/gh.log"
  : >"$FAKE_LOG/agentctl.log"
  : >"$FAKE_LOG/codex.log"
  : >"$FAKE_LOG/claude.log"
}
# run_rc <コマンド…>: 終了コードを RC に、出力を $tmp/out・$tmp/err に。
run_rc() {
  set +e
  "$@" >"$tmp/out" 2>"$tmp/err"
  RC=$?
  set -e
}
log_has() { # log_has <コマンド> <jq の条件>
  jq -e -s "any(.[]; $2)" "$FAKE_LOG/$1.log" >/dev/null 2>&1
}

# ---- 一時的なリポジトリ ----
git init --quiet --bare "$tmp/origin.git"
repo="$tmp/repo"
git init --quiet "$repo"
mkdir -p "$repo/scripts" "$repo/.agents"
cp -R "$src_root/scripts/agents" "$repo/scripts/agents"
rm -rf "$repo/scripts/agents/tests"
cp "$src_root/scripts/dev-slot.sh" "$repo/scripts/dev-slot.sh"
# github_app（#282）は外す（App のトークンは、下のテストで AGENT_APP_ID・AGENT_APP_KEY を付けて確かめる）
jq 'del(.github_app)' "$src_root/.agents/config.json" >"$repo/.agents/config.json"
check "config: リポジトリの設定に github_app（app_id と key_path）がある" \
  jq -e '.github_app.app_id == 5659452 and .github_app.key_path == "~/.config/raft-game/agent-app.pem"' "$src_root/.agents/config.json"
mkdir -p "$repo/.claude"
cp -R "$src_root/.claude/agents" "$repo/.claude/agents"
cp "$src_root/.claude/settings.json" "$repo/.claude/settings.json"
cp -R "$src_root/.claude/hooks" "$repo/.claude/hooks"
rm -rf "$repo/.claude/hooks/tests"
# settings.json は hooks を（bash を通さず）直接実行するので、実行のビットがないと拒否が効かない
for g in guard-bash.sh guard-write.sh guard-mcp.sh; do
  check "hooks: .claude/hooks/$g に実行のビットがある（Git でも 100755）" \
    test -x "$src_root/.claude/hooks/$g" -a "$(git -C "$src_root" ls-files -s ".claude/hooks/$g" | awk '{ print $1 }')" = 100755
done
printf 'a\n' >"$repo/a.txt"
(
  cd "$repo"
  git add -A
  git commit --quiet -m init
  git remote add origin "$tmp/origin.git"
  git push --quiet origin main
)
base_sha="$(git -C "$repo" rev-parse HEAD)"
git -C "$repo" switch --quiet -c agent/issue-5
printf 'b\n' >>"$repo/a.txt"
git -C "$repo" commit --quiet -am change
head_sha="$(git -C "$repo" rev-parse HEAD)"
git -C "$repo" switch --quiet main
A="$repo/scripts/agents"
DS="$repo/scripts/dev-slot.sh"
cd "$repo"

# ================= lib.sh の agentctl =================
# PATH に agentctl の実行ファイルがないときは scripts/agents/agentctl.sh を使う
# （command -v は関数自身にも一致するので、それに頼ると「command not found」になる）。
reset_logs
lt="$tmp/libtest"
mkdir -p "$lt"
cp "$A/lib.sh" "$lt/lib.sh"
cat >"$lt/agentctl.sh" <<'EOF'
#!/usr/bin/env bash
printf 'wrapper:%s\n' "$*"
EOF
chmod +x "$lt/agentctl.sh"
run_rc env PATH="$sys_path" bash -c '. "$1"; shift; agentctl "$@"' _ "$lt/lib.sh" review validate x.json
check "lib agentctl: PATH になければ agentctl.sh を使う" test "$RC:$(cat "$tmp/out")" = "0:wrapper:review validate x.json"
check "lib agentctl: command not found を出さない" test ! -s "$tmp/err"
run_rc env PATH="$fake/bin:$sys_path" bash -c '. "$1"; shift; agentctl "$@"' _ "$lt/lib.sh" state 5 merged
check "lib agentctl: PATH にあればそれを使う" test "$RC" = 0 -a ! -s "$tmp/out"
check "lib agentctl: PATH の agentctl を呼ぶ" log_has agentctl '.args == ["state","5","merged"]'
run_rc env PATH="$sys_path" AGENTCTL="$fake/bin/agentctl" bash -c '. "$1"; shift; agentctl "$@"' _ "$lt/lib.sh" claim 9
check "lib agentctl: AGENTCTL を優先する" test "$RC" = 0 -a ! -s "$tmp/out"
check "lib agentctl: AGENTCTL の agentctl を呼ぶ" log_has agentctl '.args == ["claim","9"]'

# ---- トークン（#107）: GITHUB_TOKEN も GH_TOKEN もなければ gh auth token の値を GH_TOKEN に入れる ----
# 偽の agentctl（トークンの環境変数を出す）
cat >"$lt/tokcheck" <<'EOF'
#!/usr/bin/env bash
printf 'GH_TOKEN=%s GITHUB_TOKEN=%s args=%s\n' "${GH_TOKEN:-none}" "${GITHUB_TOKEN:-none}" "$*"
EOF
chmod +x "$lt/tokcheck"
reset_logs
run_rc env PATH="$fake/bin:$sys_path" FAKE_GH_TOKEN=tok-107 \
  bash -c '. "$1"; ensure_gh_token; bash -c "[ \"\$GH_TOKEN\" = tok-107 ]" && echo exported' _ "$lt/lib.sh"
check "lib ensure_gh_token: gh auth token の値を GH_TOKEN に入れて export する" test "$RC:$(cat "$tmp/out")" = "0:exported"
check "lib ensure_gh_token: トークンを標準エラーに出さない" test ! -s "$tmp/err"
check "lib ensure_gh_token: gh auth token を呼ぶ" log_has gh '.args == ["auth","token"]'
reset_logs
run_rc env PATH="$fake/bin:$sys_path" FAKE_GH_TOKEN=tok-107 AGENTCTL="$lt/tokcheck" \
  bash -c '. "$1"; shift; agentctl "$@"' _ "$lt/lib.sh" plan --repo o/r
check "lib agentctl: トークンがなければ gh auth token の値を GH_TOKEN で渡す" \
  test "$RC:$(cat "$tmp/out")" = "0:GH_TOKEN=tok-107 GITHUB_TOKEN=none args=plan --repo o/r"
check "lib agentctl: トークンを標準エラーに出さない" test ! -s "$tmp/err"
reset_logs
run_rc env PATH="$fake/bin:$sys_path" FAKE_GH_TOKEN=tok-107 GH_TOKEN=tok-own AGENTCTL="$lt/tokcheck" \
  bash -c '. "$1"; shift; agentctl "$@"' _ "$lt/lib.sh" plan
check "lib agentctl: GH_TOKEN があればそのまま使う" test "$RC:$(cat "$tmp/out")" = "0:GH_TOKEN=tok-own GITHUB_TOKEN=none args=plan"
check "lib agentctl: GH_TOKEN があれば gh を呼ばない" test ! -s "$FAKE_LOG/gh.log"
run_rc env PATH="$fake/bin:$sys_path" FAKE_GH_TOKEN=tok-107 GITHUB_TOKEN=tok-actions AGENTCTL="$lt/tokcheck" \
  bash -c '. "$1"; shift; agentctl "$@"' _ "$lt/lib.sh" plan
check "lib agentctl: GITHUB_TOKEN があれば GH_TOKEN を足さない" \
  test "$RC:$(cat "$tmp/out")" = "0:GH_TOKEN=none GITHUB_TOKEN=tok-actions args=plan"
check "lib agentctl: GITHUB_TOKEN があれば gh を呼ばない" test ! -s "$FAKE_LOG/gh.log"
# gh が未ログイン・gh がない: 何もしない（agentctl が「トークンがない」で止める）
run_rc env PATH="$fake/bin:$sys_path" AGENTCTL="$lt/tokcheck" bash -c '. "$1"; shift; agentctl "$@"' _ "$lt/lib.sh" plan
check "lib agentctl: gh が未ログインならトークンを入れない" test "$RC:$(cat "$tmp/out")" = "0:GH_TOKEN=none GITHUB_TOKEN=none args=plan"
check "lib agentctl: gh が未ログインでも gh のエラーを出さない" test ! -s "$tmp/err"
run_rc env PATH="$sys_path" AGENTCTL="$lt/tokcheck" bash -c '. "$1"; shift; agentctl "$@"' _ "$lt/lib.sh" plan
check "lib agentctl: gh がなければトークンを入れない" test "$RC:$(cat "$tmp/out")" = "0:GH_TOKEN=none GITHUB_TOKEN=none args=plan"

# agentctl.sh: ビルド済みの .agents/bin/agentctl に、gh auth token の値を GH_TOKEN で渡す
acw="$tmp/acwrap"
mkdir -p "$acw/tools/agentctl" "$acw/scripts/agents" "$acw/.agents/bin" "$tmp/fakego"
git init --quiet "$acw"
cp "$A/agentctl.sh" "$A/lib.sh" "$acw/scripts/agents/"
printf 'module example.invalid/agentctl\n' >"$acw/tools/agentctl/go.mod"
touch -t 202001010000 "$acw/tools/agentctl/go.mod"
cp "$lt/tokcheck" "$acw/.agents/bin/agentctl"
# ビルドし直さないはずなので、go が呼ばれたら失敗させる
printf '#!/usr/bin/env bash\necho "go を呼んだ" >&2\nexit 1\n' >"$tmp/fakego/go"
chmod +x "$tmp/fakego/go"
reset_logs
(cd "$acw" && run_rc env PATH="$tmp/fakego:$fake/bin:$sys_path" FAKE_GH_TOKEN=tok-107 bash scripts/agents/agentctl.sh plan --repo o/r &&
  printf '%s\n' "$RC" >"$tmp/rc")
check "agentctl.sh: トークンがなければ gh auth token の値を GH_TOKEN で渡す" \
  test "$(cat "$tmp/rc"):$(cat "$tmp/out")" = "0:GH_TOKEN=tok-107 GITHUB_TOKEN=none args=plan --repo o/r"
check "agentctl.sh: トークンを標準エラーに出さない" test ! -s "$tmp/err"
(cd "$acw" && run_rc env PATH="$tmp/fakego:$fake/bin:$sys_path" GH_TOKEN=tok-own FAKE_GH_TOKEN=tok-107 bash scripts/agents/agentctl.sh plan &&
  printf '%s\n' "$RC" >"$tmp/rc")
check "agentctl.sh: GH_TOKEN があればそのまま使う" test "$(cat "$tmp/rc"):$(cat "$tmp/out")" = "0:GH_TOKEN=tok-own GITHUB_TOKEN=none args=plan"
(cd "$acw" && run_rc env PATH="$tmp/fakego:$fake/bin:$sys_path" bash scripts/agents/agentctl.sh plan &&
  printf '%s\n' "$RC" >"$tmp/rc")
check "agentctl.sh: gh が未ログインならトークンを入れない" test "$(cat "$tmp/rc"):$(cat "$tmp/out")" = "0:GH_TOKEN=none GITHUB_TOKEN=none args=plan"
reset_logs
# ensure_gh_token（#282 で review.sh の ensure_github_token をまとめた）: トークンがなければ gh auth token を
# GH_TOKEN に入れる。あれば gh を呼ばない
run_rc env FAKE_GH_TOKEN=tok-from-gh bash -c '. "$1"; ensure_gh_token; printf "%s" "${GH_TOKEN:-}"' _ "$lt/lib.sh"
check "lib ensure_gh_token: gh auth token を使う" test "$RC:$(cat "$tmp/out")" = "0:tok-from-gh"
reset_logs
run_rc env GITHUB_TOKEN=x FAKE_GH_TOKEN=tok-from-gh bash -c '. "$1"; ensure_gh_token; printf "%s" "${GH_TOKEN:-}"' _ "$lt/lib.sh"
check "lib ensure_gh_token: GITHUB_TOKEN があれば何もしない" test "$RC:$(cat "$tmp/out")" = "0:" -a ! -s "$FAKE_LOG/gh.log"
run_rc env FAKE_GH_TOKEN= bash -c '. "$1"; ensure_gh_token; printf "%s" "${GH_TOKEN-unset}"' _ "$lt/lib.sh"
check "lib ensure_gh_token: gh がトークンを出さなければ GH_TOKEN を設定しない" test "$RC:$(cat "$tmp/out")" = "0:unset"
check "lib: ensure_github_token はもうない（ensure_gh_token にまとめた）" \
  bash -c '. "$1"; ! declare -F ensure_github_token >/dev/null' _ "$lt/lib.sh"
reset_logs

# ================= GitHub App のトークン（#282） =================
# 鍵は偽物（openssl は偽物なので中身は読まない）。鍵の中身・トークンが標準出力・標準エラー・ログに出ないことを確かめる。
app_dir="$tmp/app"
mkdir -p "$app_dir"
app_key="$app_dir/agent-app.pem"
key_secret="not-a-real-key-282"
printf '%s\n' "$key_secret" >"$app_key"
chmod 600 "$app_key"
app_tok="ghs_test282"
app_cache_dir="$HOME/.config/raft-game"
app_cache="$app_cache_dir/agent-app-token-5659452-o_r"
appenv=(AGENT_APP_ID=5659452 "AGENT_APP_KEY=$app_key" AGENT_REPO=o/r FAKE_GH_TOKEN=tok-owner)
# no_secret <ファイル…>: トークンと鍵の中身を含まない
no_secret() {
  ! grep -q -e "$app_tok" -e "$key_secret" -e "tok-app-2" "$@"
}
# jwt_payload: gh.log の Authorization: Bearer の JWT のペイロード（JSON）
jwt_payload() {
  local p
  p="$(jq -r '.args[] | select(startswith("Authorization: Bearer ")) | ltrimstr("Authorization: Bearer ")' "$FAKE_LOG/gh.log" |
    head -n 1 | cut -d. -f2 | tr '_-' '/+')"
  while [ $((${#p} % 4)) -ne 0 ]; do p="$p="; done
  printf '%s' "$p" | { base64 -d 2>/dev/null || base64 -D; }
}
rm -rf "$app_cache_dir"
reset_logs
now_s="$(date +%s)"
run_rc env "${appenv[@]}" AGENT_ROLE=orchestrator \
  bash -c '. "$1"; ensure_gh_token; bash -c "[ \"\$GH_TOKEN\" = ghs_test282 ]" && echo app' _ "$lt/lib.sh"
check "app: Orchestrator の ensure_gh_token は App のトークンを GH_TOKEN に入れる" test "$RC:$(cat "$tmp/out")" = "0:app"
check "app: 作れたら標準エラーに何も出さない" test ! -s "$tmp/err"
check "app: 標準出力・標準エラーにトークンと鍵の中身を出さない" no_secret "$tmp/out" "$tmp/err"
check "app: JWT（Bearer）で /repos/o/r/installation を読む" \
  log_has gh '.args[0] == "api" and (.args | last) == "/repos/o/r/installation" and any(.args[]; test("^Authorization: Bearer [A-Za-z0-9_-]+\\.[A-Za-z0-9_-]+\\.[A-Za-z0-9_-]+$"))'
check "app: インストールのトークンを POST で作る" \
  log_has gh '.args[0:3] == ["api","-X","POST"] and (.args | last) == "/app/installations/42/access_tokens"'
check "app: 鍵で RS256（SHA-256）の署名をする" log_has openssl '.args == ["dgst","-sha256","-sign","'"$app_key"'","-binary"]'
payload="$(jwt_payload)"
check "app: JWT の iss は App ID・iat は 60 秒前・exp は 9 分後" \
  jq -e --argjson now "$now_s" '.iss == "5659452" and (.exp - .iat) == 600 and
    ((.iat - ($now - 60)) | if . < 0 then -. else . end) <= 5' <<<"$payload"
check "app: gh auth token（オーナーのトークン）を使わない" test -z "$(jq -c 'select(.args == ["auth","token"])' "$FAKE_LOG/gh.log")"
check "app: キャッシュを ~/.config/raft-game/ に作る" test -f "$app_cache"
check "app: キャッシュはほかの人が読めない（600）" \
  test "$(stat -c %a "$app_cache" 2>/dev/null || stat -f %Lp "$app_cache")" = 600
check "app: キャッシュのディレクトリもほかの人が読めない（700）" \
  test "$(stat -c %a "$app_cache_dir" 2>/dev/null || stat -f %Lp "$app_cache_dir")" = 700
check "app: キャッシュに鍵の中身を書かない" bash -c '! grep -q "$1" "$2"' _ "$key_secret" "$app_cache"
# キャッシュが 50 分以内なら使い回す（gh も openssl も呼ばない）
reset_logs
printf '%s\n%s\n' "$((now_s - 2900))" "tok-app-cached" >"$app_cache"
run_rc env "${appenv[@]}" AGENT_ROLE=orchestrator bash -c '. "$1"; ensure_gh_token; printf "%s" "${GH_TOKEN:-}" >"$2"' _ "$lt/lib.sh" "$tmp/tokval"
check "app: キャッシュが 50 分以内（48 分前）なら使い回す" test "$RC:$(cat "$tmp/tokval")" = "0:tok-app-cached"
check "app: キャッシュを使うときは gh を呼ばない" test ! -s "$FAKE_LOG/gh.log"
check "app: キャッシュを使うときは openssl を呼ばない" test ! -s "$FAKE_LOG/openssl.log"
check "app: キャッシュを使うときも何も出さない" test ! -s "$tmp/out" -a ! -s "$tmp/err"
# 50 分を過ぎたら作り直す（1 時間を超えて動いても切れない）
reset_logs
printf '%s\n%s\n' "$((now_s - 3100))" "tok-app-cached" >"$app_cache"
run_rc env "${appenv[@]}" AGENT_ROLE=orchestrator FAKE_APP_TOKEN=tok-app-2 \
  bash -c '. "$1"; ensure_gh_token; printf "%s" "${GH_TOKEN:-}" >"$2"' _ "$lt/lib.sh" "$tmp/tokval"
check "app: キャッシュが 50 分を過ぎたら（52 分前）作り直す" test "$RC:$(cat "$tmp/tokval")" = "0:tok-app-2"
check "app: 作り直したらキャッシュを書き換える" test "$(sed -n 2p "$app_cache")" = tok-app-2
check "app: 作り直すときも標準出力・標準エラーにトークンを出さない" no_secret "$tmp/out" "$tmp/err"
# 時計が戻った（キャッシュの時刻が未来）ら作り直す
reset_logs
printf '%s\n%s\n' "$((now_s + 600))" "tok-app-cached" >"$app_cache"
run_rc env "${appenv[@]}" AGENT_ROLE=orchestrator bash -c '. "$1"; ensure_gh_token; printf "%s" "${GH_TOKEN:-}" >"$2"' _ "$lt/lib.sh" "$tmp/tokval"
check "app: キャッシュの時刻が未来なら作り直す" test "$RC:$(cat "$tmp/tokval")" = "0:$app_tok"
# Worker の役割でも App のトークン
rm -rf "$app_cache_dir"
reset_logs
run_rc env "${appenv[@]}" AGENT_ROLE=worker bash -c '. "$1"; ensure_gh_token; printf "%s" "${GH_TOKEN:-}" >"$2"' _ "$lt/lib.sh" "$tmp/tokval"
check "app: Worker の ensure_gh_token も App のトークン" test "$RC:$(cat "$tmp/tokval")" = "0:$app_tok"
# 設定は .agents/config.json の github_app からも読む（key_path の ~/ はホームディレクトリ）
rm -rf "$app_cache_dir"
mkdir -p "$HOME/.config/raft-game"
cp "$app_key" "$HOME/.config/raft-game/agent-app.pem"
chmod 600 "$HOME/.config/raft-game/agent-app.pem"
cfg_app="$tmp/config-app.json"
jq '.github_app = {app_id: 5659452, key_path: "~/.config/raft-game/agent-app.pem"}' "$repo/.agents/config.json" >"$cfg_app"
reset_logs
run_rc env AGENT_CONFIG="$cfg_app" AGENT_REPO=o/r AGENT_ROLE=orchestrator \
  bash -c '. "$1"; ensure_gh_token; printf "%s" "${GH_TOKEN:-}" >"$2"' _ "$lt/lib.sh" "$tmp/tokval"
check "app: .agents/config.json の github_app（~/ の鍵のパス）を読む" test "$RC:$(cat "$tmp/tokval")" = "0:$app_tok"
check "app: config の鍵のパスで署名する" \
  log_has openssl '.args == ["dgst","-sha256","-sign","'"$HOME/.config/raft-game/agent-app.pem"'","-binary"]'
rm -rf "$app_cache_dir"
# ---- 戻る場面: オーナーのトークン（gh auth token）に戻り、標準エラーに 1 行出す ----
# app_fallback_case <説明> <env の追加…>: Orchestrator の ensure_gh_token がオーナーのトークンに戻ることを確かめる
app_fallback_case() {
  local desc="$1"
  shift
  rm -rf "$app_cache_dir"
  reset_logs
  run_rc env "${appenv[@]}" AGENT_ROLE=orchestrator "$@" \
    bash -c '. "$1"; ensure_gh_token; printf "%s" "${GH_TOKEN:-}" >"$2"' _ "$lt/lib.sh" "$tmp/tokval"
  check "app（${desc}）: オーナーのトークンに戻る" test "$RC:$(cat "$tmp/tokval")" = "0:tok-owner"
  check "app（${desc}）: 標準エラーに 1 行だけ出す" test "$(wc -l <"$tmp/err" | tr -d ' ')" = 1
  check "app（${desc}）: オーナーのトークンに戻ることを出す" grep -q "GitHub App のトークンを使えない.*オーナーのトークンを使う" "$tmp/err"
  check "app（${desc}）: 標準出力に何も出さない" test ! -s "$tmp/out"
  check "app（${desc}）: トークンと鍵の中身を出さない" no_secret "$tmp/out" "$tmp/err"
  check "app（${desc}）: キャッシュを作らない" test ! -e "$app_cache"
}
app_fallback_case "鍵がない" AGENT_APP_KEY="$app_dir/missing.pem"
check "app（鍵がない）: 理由に鍵のパスを出す" grep -q "秘密鍵がない.*missing.pem" "$tmp/err"
check "app（鍵がない）: gh api を呼ばない" test -z "$(jq -c 'select(.args[0] == "api")' "$FAKE_LOG/gh.log")"
chmod 644 "$app_key"
app_fallback_case "鍵の権限が 644"
check "app（鍵の権限が 644）: 理由に権限を出す" grep -q "600 より広い.*644" "$tmp/err"
check "app（鍵の権限が 644）: 鍵を使わない（openssl を呼ばない）" test ! -s "$FAKE_LOG/openssl.log"
chmod 640 "$app_key"
app_fallback_case "鍵の権限が 640"
chmod 600 "$app_key"
app_fallback_case "App が未インストール" FAKE_APP_MODE=noinstall
check "app（App が未インストール）: 理由に HTTP の状態を出す" grep -q "インストールが見つからない.*HTTP 404" "$tmp/err"
check "app（App が未インストール）: 応答の本文を出さない" test -z "$(grep -o 'Not Found"' "$tmp/err" || true)"
app_fallback_case "JWT を作れない" FAKE_OPENSSL_FAIL=1
check "app（JWT を作れない）: 理由を出す" grep -q "JWT を作れない" "$tmp/err"
app_fallback_case "App ID が整数でない" AGENT_APP_ID=abc
# 鍵が使えないとき、オーナーのトークンもなければ何も入れない
rm -rf "$app_cache_dir"
reset_logs
run_rc env AGENT_APP_ID=5659452 AGENT_APP_KEY="$app_dir/missing.pem" AGENT_REPO=o/r AGENT_ROLE=orchestrator \
  bash -c '. "$1"; ensure_gh_token; printf "%s" "${GH_TOKEN-unset}" >"$2"' _ "$lt/lib.sh" "$tmp/tokval"
check "app: 鍵がなく gh も未ログインなら GH_TOKEN を入れない" test "$RC:$(cat "$tmp/tokval")" = "0:unset"
# ---- 役割: AI（Orchestrator・Worker・Reviewer）は App、human はオーナー（オーナーの判断。2026-11-08）----
rm -rf "$app_cache_dir"
reset_logs
run_rc env "${appenv[@]}" AGENT_ROLE=reviewer bash -c '. "$1"; ensure_gh_token; printf "%s" "${GH_TOKEN:-}" >"$2"' _ "$lt/lib.sh" "$tmp/tokval"
check "app: 役割が reviewer でも App のトークン" test "$RC:$(cat "$tmp/tokval")" = "0:$app_tok"
reset_logs
run_rc env "${appenv[@]}" AGENT_ROLE=human bash -c '. "$1"; ensure_gh_token; printf "%s" "${GH_TOKEN:-}" >"$2"' _ "$lt/lib.sh" "$tmp/tokval"
check "app: 役割が human ならオーナーのトークン" test "$RC:$(cat "$tmp/tokval")" = "0:tok-owner"
check "app: 役割が human なら gh api を呼ばない" test -z "$(jq -c 'select(.args[0] == "api")' "$FAKE_LOG/gh.log")"
check "app: 役割が human なら何も出さない" test ! -s "$tmp/err"
# merge.sh（PROG=merge）・review.sh（PROG=review）も App のトークン
for p in merge review; do
  reset_logs
  run_rc env "${appenv[@]}" AGENT_ROLE=orchestrator bash -c 'PROG="$3"; . "$1"; ensure_gh_token; printf "%s" "${GH_TOKEN:-}" >"$2"' _ "$lt/lib.sh" "$tmp/tokval" "$p"
  check "app: PROG=${p} でも App のトークン" test "$RC:$(cat "$tmp/tokval")" = "0:$app_tok"
done
# ---- ghx（lib）: merge.sh・review.sh が直接呼ぶ gh。呼ぶたびに App のトークンを gh にだけ渡す ----
rm -rf "$app_cache_dir"
reset_logs
run_rc env "${appenv[@]}" AGENT_ROLE=orchestrator \
  bash -c '. "$1"; ghx pr view 5 --json headRefName,body; printf "after=%s\n" "${GH_TOKEN-unset}"' _ "$lt/lib.sh"
check "ghx: gh の出力をそのまま返す" test "$RC:$(head -n 1 "$tmp/out" | jq -r .headRefName)" = "0:agent/issue-5"
check "ghx: App のトークンで gh を呼ぶ" grep -qx "$app_tok pr view 5 --json headRefName,body" "$FAKE_LOG/gh-env.log"
check "ghx: 呼び出し側のシェルに GH_TOKEN を残さない" test "$(tail -n 1 "$tmp/out")" = "after=unset"
check "ghx: 標準出力・標準エラーにトークンと鍵の中身を出さない" no_secret "$tmp/out" "$tmp/err"
reset_logs
run_rc env "${appenv[@]}" AGENT_ROLE=orchestrator FAKE_MERGE_RC=3 bash -c '. "$1"; ghx pr merge 1' _ "$lt/lib.sh"
check "ghx: gh の終了コードを返す" test "$RC" = 3
reset_logs
run_rc env "${appenv[@]}" AGENT_ROLE=orchestrator GH_TOKEN=tok-explicit bash -c '. "$1"; ghx issue view 9' _ "$lt/lib.sh"
check "ghx: GH_TOKEN があればそのまま使う" test "$RC:$(cat "$FAKE_LOG/gh-env.log")" = "0:tok-explicit issue view 9"
reset_logs
run_rc env "${appenv[@]}" AGENT_ROLE=human bash -c '. "$1"; ghx issue view 9' _ "$lt/lib.sh"
check "ghx: 役割が human なら gh のログインで実行する" test "$RC:$(cat "$FAKE_LOG/gh-env.log")" = "0:none issue view 9"
rm -rf "$app_cache_dir"
reset_logs
run_rc env "${appenv[@]}" AGENT_ROLE=orchestrator AGENT_APP_KEY="$app_dir/missing.pem" bash -c '. "$1"; ghx issue view 9' _ "$lt/lib.sh"
check "ghx: 鍵がなければ gh のログイン（オーナー）で実行する" test "$RC:$(cat "$FAKE_LOG/gh-env.log")" = "0:none issue view 9"
check "ghx: 鍵がなければ標準エラーに 1 行出す" \
  test "$(wc -l <"$tmp/err" | tr -d ' ')" = 1 -a -n "$(grep 'オーナーのトークンを使う' "$tmp/err" || true)"
# ---- gh（lib の関数）: lib.sh を読み込むスクリプト（merge-train.sh・autospawn.sh など）の直接の gh も ghx を通る（F7）----
rm -rf "$app_cache_dir"
reset_logs
run_rc env "${appenv[@]}" AGENT_ROLE=orchestrator \
  bash -c '. "$1"; gh pr list --head x; gh issue view 9; printf "after=%s\n" "${GH_TOKEN-unset}"' _ "$lt/lib.sh"
check "gh（lib）: 直接の gh も App のトークンで呼ぶ" \
  test "$RC:$(grep -v ' api ' "$FAKE_LOG/gh-env.log" | tr '\n' ',')" = "0:$app_tok pr list --head x,$app_tok issue view 9,"
check "gh（lib）: App の API はトークンなしで呼ぶ（入れ子にならない）" test "$(grep -c '^none api ' "$FAKE_LOG/gh-env.log")" = 2
check "gh（lib）: 呼び出し側のシェルに GH_TOKEN を残さない" test "$(tail -n 1 "$tmp/out")" = "after=unset"
check "gh（lib）: 標準出力・標準エラーにトークンと鍵の中身を出さない" no_secret "$tmp/out" "$tmp/err"
reset_logs
run_rc env "${appenv[@]}" AGENT_ROLE=orchestrator bash -c '. "$1"; gh auth token' _ "$lt/lib.sh"
check "gh（lib）: gh auth はそのまま（App のトークンを入れない）" \
  test "$RC:$(cat "$tmp/out"):$(cat "$FAKE_LOG/gh-env.log")" = "0:tok-owner:none auth token"
reset_logs
rm -rf "$app_cache_dir"
reset_logs
run_rc env "${appenv[@]}" AGENT_ROLE=orchestrator AGENT_APP_KEY="$app_dir/missing.pem" bash -c '. "$1"; gh issue view 9' _ "$lt/lib.sh"
check "gh（lib）: 鍵がなければ gh のログイン（オーナー）で実行する" test "$RC:$(cat "$FAKE_LOG/gh-env.log")" = "0:none issue view 9"
check "gh（lib）: 鍵がなければ標準エラーに 1 行出す" \
  test "$(wc -l <"$tmp/err" | tr -d ' ')" = 1 -a -n "$(grep 'オーナーのトークンを使う' "$tmp/err" || true)"
reset_logs
run_rc env "${appenv[@]}" AGENT_ROLE=human bash -c '. "$1"; gh issue view 9' _ "$lt/lib.sh"
check "gh（lib）: 役割が human なら gh のログインで実行する" test "$RC:$(cat "$FAKE_LOG/gh-env.log")" = "0:none issue view 9"
reset_logs
run_rc env "${appenv[@]}" AGENT_ROLE=orchestrator FAKE_MERGE_RC=5 bash -c '. "$1"; gh pr "$2" 1' _ "$lt/lib.sh" merge
check "gh（lib）: gh の終了コードを返す" test "$RC" = 5
# need gh: gh が関数でも、実行ファイルがなければ止める（PATH には dirname だけを置く）
mkdir -p "$tmp/nogh"
ln -sf "$(command -v dirname)" "$tmp/nogh/dirname"
run_rc env PATH="$tmp/nogh" "$BASH" -c '. "$1"; need gh' _ "$lt/lib.sh"
check "gh（lib）: need gh は実行ファイルがなければ止める" test "$RC" = 1 -a -n "$(grep 'gh が見つからない' "$tmp/err" || true)"
run_rc env "${appenv[@]}" AGENT_ROLE=orchestrator bash -c '. "$1"; need gh' _ "$lt/lib.sh"
check "gh（lib）: need gh は実行ファイルがあれば通る" test "$RC" = 0
# merge.sh: gate（agentctl）と gh pr view を App のトークンで呼ぶ（gh pr merge は下の merge.sh の節で確かめる）
cat >"$lt/gatecheck" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "${GH_TOKEN:-none}" >>"$FAKE_LOG/gate-token.log"
exec "$FAKE_BIN/agentctl" "$@"
EOF
chmod +x "$lt/gatecheck"
gate_pass_app="$(jq -cn --arg s "$head_sha" '{verdict:"pass", repo:"K-Kizuku/raft-game", pr:12, head_sha:$s, results:[]}')"
rm -rf "$app_cache_dir"
reset_logs
run_rc env "${appenv[@]}" AGENT_ROLE=orchestrator AGENTCTL="$lt/gatecheck" FAKE_BIN="$fake/bin" \
  FAKE_GATE_JSON="$gate_pass_app" bash "$A/merge.sh" 12 --dry-run
check "app: merge.sh --dry-run が成功する" test "$RC" = 0
check "app: merge.sh の gate に App のトークンを渡す" test "$(sort -u "$FAKE_LOG/gate-token.log")" = "$app_tok"
check "app: merge.sh の gh pr view は App のトークン" grep -qx "$app_tok pr view 12 --json headRefName,body" "$FAKE_LOG/gh-env.log"
check "app: merge.sh --dry-run の表示は gh pr merge のまま" grep -q "^merge: gh pr merge 12 --squash " "$tmp/out"
check "app: merge.sh はトークンと鍵の中身を出さない" no_secret "$tmp/out" "$tmp/err"
# ---- agentctl（lib の関数）: 呼ぶたびに子にだけ渡し、呼び出し側のシェルには export しない ----
rm -rf "$app_cache_dir"
reset_logs
run_rc env "${appenv[@]}" AGENT_ROLE=orchestrator AGENTCTL="$lt/tokcheck" \
  bash -c '. "$1"; agentctl plan; printf "after=%s\n" "${GH_TOKEN-unset}"' _ "$lt/lib.sh"
check "app: agentctl に App のトークンを GH_TOKEN で渡す" \
  test "$RC:$(head -n 1 "$tmp/out")" = "0:GH_TOKEN=$app_tok GITHUB_TOKEN=none args=plan"
check "app: agentctl は呼び出し側のシェルに GH_TOKEN を残さない" test "$(tail -n 1 "$tmp/out")" = "after=unset"
check "app: agentctl は標準エラーにトークンを出さない" no_secret "$tmp/err"
# 1 時間を超えて動くシェル: キャッシュが古くなったら次の呼び出しで作り直す
printf '%s\n%s\n' "$((now_s - 3700))" "$app_tok" >"$app_cache"
run_rc env "${appenv[@]}" AGENT_ROLE=orchestrator AGENTCTL="$lt/tokcheck" FAKE_APP_TOKEN=tok-app-2 \
  bash -c '. "$1"; agentctl plan' _ "$lt/lib.sh"
check "app: agentctl は古い（1 時間を超えた）トークンを使わず作り直す" \
  test "$RC:$(cat "$tmp/out")" = "0:GH_TOKEN=tok-app-2 GITHUB_TOKEN=none args=plan"

# ---- gh-app.sh: gh を呼ぶたびに App のトークンを GH_TOKEN に入れて本物の gh を実行する ----
GA="$A/gh-app.sh"
check "gh-app.sh: 実行のビットがある（Git でも 100755）" \
  test -x "$src_root/scripts/agents/gh-app.sh" -a "$(git -C "$src_root" ls-files -s scripts/agents/gh-app.sh | awk '{ print $1 }')" = 100755
rm -rf "$app_cache_dir"
reset_logs
run_rc env "${appenv[@]}" AGENT_ROLE=worker bash "$GA" pr view 5 --json headRefName,body
check "gh-app.sh: gh の出力をそのまま返す" test "$RC:$(jq -r .headRefName "$tmp/out")" = "0:agent/issue-5"
check "gh-app.sh: App のトークンで gh を呼ぶ" grep -qx "$app_tok pr view 5 --json headRefName,body" "$FAKE_LOG/gh-env.log"
check "gh-app.sh: 標準出力・標準エラーにトークンと鍵の中身を出さない" no_secret "$tmp/out" "$tmp/err"
check "gh-app.sh: 作れたら標準エラーに何も出さない" test ! -s "$tmp/err"
# 2 回目はキャッシュを使う（App の API を呼ばない）
reset_logs
run_rc env "${appenv[@]}" AGENT_ROLE=worker bash "$GA" issue view 9
check "gh-app.sh: 2 回目はキャッシュのトークンで呼ぶ" test "$RC:$(cat "$FAKE_LOG/gh-env.log")" = "0:$app_tok issue view 9"
# 1 時間を超えたら作り直す（Worker が長く動いても切れない）
reset_logs
printf '%s\n%s\n' "$((now_s - 3700))" "$app_tok" >"$app_cache"
run_rc env "${appenv[@]}" AGENT_ROLE=worker FAKE_APP_TOKEN=tok-app-2 bash "$GA" issue view 9
check "gh-app.sh: 古いトークンを使わず作り直して呼ぶ" grep -qx "tok-app-2 issue view 9" "$FAKE_LOG/gh-env.log"
check "gh-app.sh: 作り直すときも標準出力・標準エラーにトークンを出さない" no_secret "$tmp/out" "$tmp/err"
# gh auth と、GH_TOKEN がすでにあるときは、そのまま
reset_logs
run_rc env "${appenv[@]}" AGENT_ROLE=worker bash "$GA" auth token
check "gh-app.sh: gh auth はそのまま（App のトークンを入れない）" \
  test "$RC:$(cat "$tmp/out"):$(cat "$FAKE_LOG/gh-env.log")" = "0:tok-owner:none auth token"
reset_logs
run_rc env "${appenv[@]}" AGENT_ROLE=worker GH_TOKEN=tok-explicit bash "$GA" issue view 9
check "gh-app.sh: GH_TOKEN があればそのまま使う" test "$RC:$(cat "$FAKE_LOG/gh-env.log")" = "0:tok-explicit issue view 9"
# 鍵がなければ、gh のログイン（オーナーのトークン）でそのまま実行し、標準エラーに 1 行出す
rm -rf "$app_cache_dir"
reset_logs
run_rc env "${appenv[@]}" AGENT_ROLE=worker AGENT_APP_KEY="$app_dir/missing.pem" bash "$GA" issue view 9
check "gh-app.sh: 鍵がなければ gh のログインで実行する" test "$RC:$(cat "$FAKE_LOG/gh-env.log")" = "0:none issue view 9"
check "gh-app.sh: 鍵がなければ標準エラーに 1 行出す" \
  test "$(wc -l <"$tmp/err" | tr -d ' ')" = 1 -a -n "$(grep 'オーナーのトークンを使う' "$tmp/err" || true)"
run_rc env "${appenv[@]}" AGENT_ROLE=worker FAKE_APP_MODE=noinstall bash "$GA" issue view 9
check "gh-app.sh: App が未インストールでも gh は実行する" test "$RC" = 0 -a -n "$(grep 'HTTP 404' "$tmp/err" || true)"
# 役割が human ならそのまま（reviewer は AI の役割なので App のトークン）
reset_logs
run_rc env "${appenv[@]}" AGENT_ROLE=human bash "$GA" issue view 9
check "gh-app.sh: 役割が human なら App のトークンを使わない" test "$RC:$(cat "$FAKE_LOG/gh-env.log")" = "0:none issue view 9"
reset_logs
run_rc env "${appenv[@]}" AGENT_ROLE=reviewer bash "$GA" issue view 9
check "gh-app.sh: 役割が reviewer なら App のトークン" test "$RC:$(tail -n 1 "$FAKE_LOG/gh-env.log")" = "0:$app_tok issue view 9"
# 本物の gh の終了コードを返す
run_rc env "${appenv[@]}" AGENT_ROLE=worker FAKE_MERGE_RC=7 bash "$GA" pr merge 1
check "gh-app.sh: gh の終了コードを返す" test "$RC" = 7
# PATH の先頭の gh（spawn-worker.sh が作るのと同じ形）から呼んでも、自分を除いた本物の gh を探す
shim="$tmp/ghshim"
mkdir -p "$shim"
{
  echo '#!/usr/bin/env bash'
  printf 'export AGENT_GH_SHIM_DIR=%q\n' "$shim"
  printf 'exec %q "$@"\n' "$GA"
} >"$shim/gh"
chmod +x "$shim/gh"
rm -rf "$app_cache_dir"
reset_logs
run_rc env "${appenv[@]}" AGENT_ROLE=worker PATH="$shim:$PATH" "$shim/gh" issue view 9
check "gh-app.sh: PATH の先頭のラッパーの gh から、本物の gh を App のトークンで呼ぶ" \
  test "$RC:$(tail -n 1 "$FAKE_LOG/gh-env.log")" = "0:$app_tok issue view 9"
check "gh-app.sh: App の API はラッパーを通さずに 1 回ずつ呼ぶ" test "$(grep -c '^none api ' "$FAKE_LOG/gh-env.log")" = 2
# lib の ensure_gh_token も、ラッパーを通さずに本物の gh で App の API を呼ぶ（AGENT_GH_REAL）
rm -rf "$app_cache_dir"
reset_logs
run_rc env "${appenv[@]}" AGENT_ROLE=worker PATH="$shim:$PATH" AGENT_GH_REAL="$fake/bin/gh" \
  bash -c '. "$1"; ensure_gh_token; printf "%s" "${GH_TOKEN:-}" >"$2"' _ "$lt/lib.sh" "$tmp/tokval"
check "app: PATH にラッパーがあっても ensure_gh_token は App のトークン" test "$RC:$(cat "$tmp/tokval")" = "0:$app_tok"
check "app: AGENT_GH_REAL の gh で App の API を呼ぶ" test "$(grep -c ' api ' "$FAKE_LOG/gh-env.log")" = 2
rm -rf "$app_cache_dir"
reset_logs

# ================= dev-slot.sh =================
cfg2="$tmp/config-2slots.json"
jq '.dev_slots = 2' "$repo/.agents/config.json" >"$cfg2"
mkdir -p "$tmp/p1" "$tmp/p2" "$tmp/p3" "$tmp/p4"
export AGENT_CONFIG="$cfg2"
run_rc bash "$DS" acquire --path "$tmp/p1"
check "dev-slot: 確保する（1）" test "$RC:$(cat "$tmp/out")" = "0:1"
run_rc bash "$DS" acquire --path "$tmp/p1"
check "dev-slot: 同じパスは同じ番号" test "$RC:$(cat "$tmp/out")" = "0:1"
run_rc bash "$DS" acquire --path "$tmp/p2"
check "dev-slot: 次の番号（2）" test "$RC:$(cat "$tmp/out")" = "0:2"
check "dev-slot: 中に Worktree のパスを書く" test "$(cat "$repo/.git/agent-slots/2/path")" = "$tmp/p2"
run_rc bash "$DS" acquire --path "$tmp/p3"
check "dev-slot: 枠がなければ終了コード 1" test "$RC" = 1
check "dev-slot: 枠がないことを伝える" grep -q "空いている DEV_SLOT がない" "$tmp/err"
run_rc bash "$DS" current --path "$tmp/p2"
check "dev-slot: current" test "$RC:$(cat "$tmp/out")" = "0:2"
(cd "$tmp/p2" && git init --quiet . && bash "$DS" current >/dev/null 2>&1) && fail "dev-slot: 別のリポジトリの枠は見えない" || pass
run_rc bash "$DS" release 2
check "dev-slot: 解放する" test "$RC" = 0 -a ! -d "$repo/.git/agent-slots/2"
run_rc bash "$DS" acquire --path "$tmp/p3"
check "dev-slot: 解放した枠を使う" test "$RC:$(cat "$tmp/out")" = "0:2"
rm -rf "$tmp/p1"
run_rc bash "$DS" list
check "dev-slot: list で古い枠に印を付ける" grep -q "^1.*(stale)" "$tmp/out"
run_rc bash "$DS" acquire --path "$tmp/p4"
check "dev-slot: 古い枠を回収する" test "$RC:$(cat "$tmp/out")" = "0:1"
check "dev-slot: 回収した枠のパスを書き換える" test "$(cat "$repo/.git/agent-slots/1/path")" = "$tmp/p4"
# 持ち主の pid が生きていれば、パスがまだなくても回収しない
bash "$DS" release 1 >/dev/null
run_rc bash "$DS" acquire --path "$tmp/not-yet" --pid "$$"
check "dev-slot: pid 付きで確保する" test "$RC:$(cat "$tmp/out")" = "0:1"
run_rc bash "$DS" acquire --path "$tmp/p1x"
check "dev-slot: 持ち主が生きている枠は回収しない" test "$RC" = 1
run_rc bash "$DS" release --path "$tmp/p3"
check "dev-slot: パスで解放する" test "$RC" = 0 -a ! -d "$repo/.git/agent-slots/2"
rm -rf "$repo/.git/agent-slots"
unset AGENT_CONFIG

# リポジトリの設定（dev_slots=15）で 15 番まで割り当て、16 個目は枠がないと伝える（#205）
check "dev-slot: .agents/config.json の dev_slots は 15" test "$(jq -r '.dev_slots' "$src_root/.agents/config.json")" = 15
# ds_fill <説明>: q1〜q15 を確保して 1〜15 が出ること、q16 が dev_slots=15 で断られることを確かめる
ds_fill() {
  local i got="" want=""
  for i in $(seq 1 15); do
    mkdir -p "$tmp/q$i"
    got="$got $(bash "$DS" acquire --path "$tmp/q$i" 2>/dev/null)"
    want="$want $i"
  done
  check "dev-slot（$1）: 1〜15 番を割り当てる" test "$got" = "$want"
  mkdir -p "$tmp/q16"
  run_rc bash "$DS" acquire --path "$tmp/q16"
  check "dev-slot（$1）: 16 個目は終了コード 1" test "$RC" = 1
  check "dev-slot（$1）: 枠がないことを dev_slots=15 で伝える" grep -qF "空いている DEV_SLOT がない（dev_slots=15）" "$tmp/err"
  rm -rf "$repo/.git/agent-slots"
}
ds_fill "設定の値"
# 設定がないときの既定値も 15
export AGENT_CONFIG="$tmp/no-such-config.json"
ds_fill "既定値"
unset AGENT_CONFIG

# ================= Worker のプロンプト（#117）=================
# 文言があることだけを確かめる（web の依存の用意・ci-ok だけを待つ・補足の行をチェックボックスにしない）。
# merge-gate を含む --required・--watch・--fail-fast を gh pr checks に付けていないことも確かめる。
for m in implement fix conflict; do
  pf="$src_root/scripts/agents/prompts/worker-$m.md"
  check "prompt $m: pnpm install --frozen-lockfile で web の依存を用意する" grep -qF -- "pnpm install --frozen-lockfile" "$pf"
  check "prompt $m: web/node_modules がなければ" grep -qF -- "web/node_modules" "$pf"
  check "prompt $m: gh pr checks で ci-ok を名指しで読む" grep -qF -- "select(.name == \"ci-ok\") | .bucket" "$pf"
  check "prompt $m: ci-ok だけの結果で決める" grep -qF -- "**チェック \`ci-ok\` だけ**" "$pf"
  check "prompt $m: merge-gate は待たない" grep -qF -- "\`merge-gate\` は待たない・見ない" "$pf"
  check "prompt $m: 30 秒ごとに読み直す" grep -qF -- "30 秒ごとに読み直す" "$pf"
  check "prompt $m: --required・--watch・--fail-fast を使わないと書く" grep -qF -- "\`--watch\`・\`--required\`・\`--fail-fast\` は使わない" "$pf"
  check "prompt $m: gh pr checks に --required・--watch・--fail-fast を付けない" \
    sh -c '! grep -qE "gh pr checks[^\`]*--(required|watch|fail-fast)" "$1"' _ "$pf"
  check "prompt $m: 待つ上限は 20 分" grep -qF -- "20 分" "$pf"
  check "prompt $m: PR の番号を gh pr view で取る" grep -qF -- "gh pr view --json number -q .number" "$pf"
  check "prompt $m: gh pr edit --body-file で本文を更新する" grep -qF -- "gh pr edit <PR> --body-file" "$pf"
  check "prompt $m: AC を一字一句そのまま写す" grep -qF -- "一字一句そのまま" "$pf"
  check "prompt $m: 補足の行をチェックボックスにしない" grep -qF -- "補足の行（\`  - …\`）はチェックボックスにしない" "$pf"
  check "prompt $m: 最後に Issue と PR の AC を比べる" grep -qF -- "gh issue view {{ISSUE}} --json body" "$pf"
done

# ================= spawn-worker.sh =================
reset_logs
export AGENT_REPO="K-Kizuku/raft-game"
export FAKE_ISSUE_BODY="## 変更予定ファイル

- \`server/internal/foo/**\`
- \`docs/x.md\`

## 変更禁止範囲

- \`scripts/agents/**\`"
run_rc env AGENT_ROLE=worker bash "$A/spawn-worker.sh" 7 --dry-run
check "spawn: Worker の役割では拒否する" test "$RC" = 1
check "spawn: 拒否の理由を出す" grep -q "Orchestrator だけ" "$tmp/err"
run_rc env AGENT_ROLE=orchestrator bash "$A/spawn-worker.sh" 7 --dry-run
check "spawn --dry-run: 成功する" test "$RC" = 0
cmdline="$(grep '^command:' "$tmp/out" || true)"
check "spawn --dry-run: -w issue-7" grep -q -- " -w issue-7 " <<<"$cmdline"
check "spawn --dry-run: --agent worker" grep -q -- " --agent worker " <<<"$cmdline"
check "spawn --dry-run: --permission-mode dontAsk" grep -q -- " --permission-mode dontAsk " <<<"$cmdline"
check "spawn --dry-run: claude -p" grep -q -- " claude -p " <<<"$cmdline"
check "spawn --dry-run: --session-id" grep -Eq -- " --session-id [0-9a-f-]{36} " <<<"$cmdline"
check "spawn --dry-run: stream-json と --verbose" grep -q -- " --verbose --output-format stream-json " <<<"$cmdline"
check "spawn --dry-run: --allowedTools" grep -q -- " --allowedTools " <<<"$cmdline"
check "spawn --dry-run: AGENT_ROLE=worker AGENT_ISSUE=7" grep -q -- "AGENT_ROLE=worker AGENT_ISSUE=7 DEV_SLOT=" <<<"$cmdline"
check "spawn --dry-run: 保護されたパスに触れなければ AGENT_ALLOW_PROTECTED を付けない" \
  test -z "$(grep -o AGENT_ALLOW_PROTECTED <<<"$cmdline" || true)"
check "spawn --dry-run: claim の引数（#4 の形）" grep -Eq -- "^claim: agentctl claim 7 --repo K-Kizuku/raft-game --worker issue-7 --session [0-9a-f-]{36}$" "$tmp/out"
check "spawn --dry-run: agentctl claim を呼ばない" test ! -s "$FAKE_LOG/agentctl.log"
check "spawn --dry-run: claude を呼ばない" test ! -s "$FAKE_LOG/claude.log"
check "spawn --dry-run: DEV_SLOT を確保しない" test ! -d "$repo/.git/agent-slots"
# 保護されたパスを含む Issue
FAKE_ISSUE_BODY="## 変更予定ファイル

- \`.claude/hooks/**\`
- \`docs/x.md\`" run_rc env AGENT_ROLE=orchestrator bash "$A/spawn-worker.sh" 7 --dry-run
check "spawn --dry-run: 変更予定ファイルが protected_paths と重なれば AGENT_ALLOW_PROTECTED=1" \
  grep -q "AGENT_ALLOW_PROTECTED=1 claude" "$tmp/out"
# 先頭がワイルドカードの項目は、保護されたパスと重ならないものとして扱い、警告を出す
FAKE_ISSUE_BODY="## 変更予定ファイル

- \`**/*.go\`
- \`*.md\`
- \`docs/x.md\`" run_rc env AGENT_ROLE=orchestrator bash "$A/spawn-worker.sh" 7 --dry-run
check "spawn --dry-run: 先頭がワイルドカードの項目では AGENT_ALLOW_PROTECTED を付けない" \
  test "$RC" = 0 -a -z "$(grep -o AGENT_ALLOW_PROTECTED "$tmp/out" || true)"
check "spawn --dry-run: 先頭がワイルドカードの項目に警告を出す" grep -q "警告.*\*\*/\*\.go.*先頭がワイルドカード" "$tmp/err"
# プロンプトに Issue の本文と値が入る（プロンプトは --output-format の値の直後＝最後の引数）
run_rc env AGENT_ROLE=orchestrator bash "$A/spawn-worker.sh" 7 --dry-run --mode conflict --note /dev/null
check "spawn --dry-run --mode conflict: 成功する" test "$RC" = 0
check "spawn --dry-run --mode conflict: claim しない" test -z "$(grep '^claim:' "$tmp/out" || true)"
check "spawn --dry-run --mode conflict: mode を出す" grep -q "^mode: conflict$" "$tmp/out"
# conflict で、Worktree がまだなければ -w で作る（あれば中で起動する。下の起動のテストで確かめる）
check "spawn --dry-run --mode conflict: Worktree がなければ -w" grep -q -- " -w issue-7 " "$tmp/out"
run_rc env AGENT_ROLE=orchestrator bash "$A/spawn-worker.sh" 7 --dry-run --mode bogus
check "spawn: 不正な --mode は拒否する" test "$RC" = 1
# max_workers に達していると拒否する（実際の設定の値に依存しないよう、コピーした設定で 2 に固定する）
cfg_mw="$tmp/config-max-workers-2.json"
jq '.max_workers = 2' "$repo/.agents/config.json" >"$cfg_mw"
mkdir -p "$repo/.agents/run"
sleep 30 &
sleeper1=$!
sleep 30 &
sleeper2=$!
jq -n --argjson pid "$sleeper1" '{issue:1, pid:$pid}' >"$repo/.agents/run/issue-1.json"
jq -n --argjson pid "$sleeper2" '{issue:2, pid:$pid}' >"$repo/.agents/run/issue-2.json"
run_rc env AGENT_ROLE=orchestrator AGENT_CONFIG="$cfg_mw" bash "$A/spawn-worker.sh" 7 --dry-run
check "spawn: max_workers（2）に達していると拒否する" test "$RC" = 1
check "spawn: max_workers の理由を出す" grep -q "max_workers" "$tmp/err"
kill "$sleeper2" 2>/dev/null || true
wait "$sleeper2" 2>/dev/null || true
run_rc env AGENT_ROLE=orchestrator AGENT_CONFIG="$cfg_mw" bash "$A/spawn-worker.sh" 7 --dry-run
check "spawn: 止まった Worker は数えない" test "$RC" = 0
jq -n --argjson pid "$sleeper1" '{issue:7, pid:$pid}' >"$repo/.agents/run/issue-7.json"
run_rc env AGENT_ROLE=orchestrator AGENT_CONFIG="$cfg_mw" bash "$A/spawn-worker.sh" 7 --dry-run
check "spawn: 同じ Issue の Worker が動いていれば拒否する" test "$RC" = 1
kill "$sleeper1" 2>/dev/null || true
wait "$sleeper1" 2>/dev/null || true
rm -rf "$repo/.agents/run"
# agentctl claim が拒否したら何もしない（DEV_SLOT も返す）
reset_logs
FAKE_CLAIM_RC=4 run_rc env AGENT_ROLE=orchestrator bash "$A/spawn-worker.sh" 7
check "spawn: claim が拒否したら claim の終了コード（4）で終わる" test "$RC" = 4
check "spawn: claim が拒否したら claude を呼ばない" test ! -s "$FAKE_LOG/claude.log"
check "spawn: claim が拒否したら DEV_SLOT を返す" test -z "$(ls "$repo/.git/agent-slots" 2>/dev/null || true)"
check "spawn: claim を #4 の形で呼ぶ" log_has agentctl '.args[0:2] == ["claim","7"] and (.args | index("--worker")) != null'
# 起動する（偽の claude）
reset_logs
run_rc env AGENT_ROLE=orchestrator bash "$A/spawn-worker.sh" 7
check "spawn: 起動する" test "$RC" = 0
check "spawn: claude を -w issue-7 で呼ぶ" log_has claude '.args[0:3] == ["-p","-w","issue-7"]'
check "spawn: Worker の環境変数" log_has claude '.role == "worker"'
check "spawn: プロンプトは --output-format の値の直後" \
  log_has claude '(.args | length) as $n | .args[$n-3] == "--output-format" and .args[$n-2] == "stream-json" and (.args[$n-1] | contains("Issue #7"))'
check "spawn: worker.md の tools を --allowedTools で渡す（--agent worker も付ける）" \
  log_has claude '(.args | index("--allowedTools")) as $i | $i != null and .args[$i + 1] == "Read,Edit,Write,Glob,Grep,Bash" and .args[(.args | index("--agent")) + 1] == "worker"'
check "spawn: 状態のファイル" jq -e '.issue == 7 and .mode == "implement" and (.pid | type == "number") and .branch == "agent/issue-7" and (.session | length) == 36 and .slot == 1' "$repo/.agents/run/issue-7.json"
check "spawn: ログ" test -n "$(ls "$repo/.agents/logs/"issue-7-*.jsonl 2>/dev/null || true)"
check "spawn: DEV_SLOT を確保した" test "$(cat "$repo/.git/agent-slots/1/path" 2>/dev/null)" = "$repo/.claude/worktrees/issue-7"
# fix: Worktree があれば、その中で新しいセッションを起動する（-w は付けない）。--note をプロンプトに入れる
wt7="$repo/.claude/worktrees/issue-7"
git -C "$repo" worktree add --quiet -b agent/issue-7 "$wt7" main
printf 'F1 を直す方針: テスト用の補足\n' >"$tmp/note.md"
reset_logs
run_rc env AGENT_ROLE=orchestrator bash "$A/spawn-worker.sh" 7 --mode fix --note "$tmp/note.md"
check "spawn --mode fix: 起動する" test "$RC" = 0
check "spawn --mode fix: Worktree の中で -w なしで起動する" \
  log_has claude '.cwd == "'"$wt7"'" and (.args | index("-w")) == null and (.args | index("--session-id")) != null'
check "spawn --mode fix: fix のプロンプトと --note" log_has claude '(.args | last | contains("モード: fix")) and (.args | last | contains("F1 を直す方針"))'
check "spawn --mode fix: claim しない" test -z "$(jq -c 'select(.args[0] == "claim")' "$FAKE_LOG/agentctl.log")"
# --resume: 記録のセッションが見つかれば、Worktree の中で claude -p -r <session>
old_session="$(jq -r .session "$repo/.agents/run/issue-7.json")"
mkdir -p "$CLAUDE_CONFIG_DIR/projects/x"
: >"$CLAUDE_CONFIG_DIR/projects/x/$old_session.jsonl"
reset_logs
run_rc env AGENT_ROLE=orchestrator bash "$A/spawn-worker.sh" 7 --resume
check "spawn --resume: 起動する" test "$RC" = 0
check "spawn --resume: claude -p -r <session> を Worktree の中で" \
  log_has claude '.args[0:3] == ["-p","-r","'"$old_session"'"] and .cwd == "'"$wt7"'"'
check "spawn --resume: Git の状態から再開するプロンプト" log_has claude '.args | last | contains("中断した作業の再開")'
check "spawn --resume: agentctl claim --resume" \
  log_has agentctl '.args[0:2] == ["claim","7"] and (.args | index("--resume")) != null and (.args | index("'"$old_session"'")) != null'
# --resume: セッションが見つからなければ新しいセッション
rm -rf "$CLAUDE_CONFIG_DIR/projects"
reset_logs
run_rc env AGENT_ROLE=orchestrator bash "$A/spawn-worker.sh" 7 --resume
check "spawn --resume: セッションがなければ新しいセッションで再開する" \
  log_has claude '(.args | index("-r")) == null and (.args | index("--session-id")) != null and .args[(.args | index("--session-id")) + 1] != "'"$old_session"'" and (.args | last | contains("中断した作業の再開"))'
git -C "$repo" worktree remove "$wt7"
git -C "$repo" branch -D agent/issue-7 >/dev/null
rm -rf "$repo/.git/agent-slots" "$repo/.agents/run" "$repo/.agents/logs"
check "spawn --help" bash "$A/spawn-worker.sh" --help
# Issue #282: App の鍵が使えなければ（設定がない）、今までどおり PATH も GH_TOKEN も変えない
check "spawn: App の設定がなければ gh のラッパーを作らない" test ! -e "$repo/.agents/bin/gh-app/gh"
check "spawn: App の設定がなければ PATH を変えない" log_has claude '.path0 != "'"$repo/.agents/bin/gh-app"'"'
# App の鍵が使えれば、gh-app.sh を呼ぶ gh を Worker の PATH の先頭に置き、GH_TOKEN・GITHUB_TOKEN を外す
rm -rf "$app_cache_dir"
reset_logs
run_rc env "${appenv[@]}" GH_TOKEN=tok-inherited AGENT_ROLE=orchestrator bash "$A/spawn-worker.sh" 7 --dry-run
cmdline="$(grep '^command:' "$tmp/out" || true)"
check "spawn --dry-run（App）: 成功する" test "$RC" = 0
check "spawn --dry-run（App）: GH_TOKEN と GITHUB_TOKEN を外す" grep -q -- "^command: env -u GH_TOKEN -u GITHUB_TOKEN AGENT_ROLE=worker " <<<"$cmdline"
check "spawn --dry-run（App）: PATH の先頭に .agents/bin/gh-app" grep -q -- " PATH=$repo/.agents/bin/gh-app:" <<<"$cmdline"
check "spawn --dry-run（App）: 本物の gh を AGENT_GH_REAL で渡す" grep -q -- " AGENT_GH_REAL=$fake/bin/gh " <<<"$cmdline"
check "spawn --dry-run（App）: App ID と鍵のパスとリポジトリを渡す" \
  grep -q -- " AGENT_APP_ID=5659452 AGENT_APP_KEY=$app_key AGENT_REPO=o/r claude " <<<"$cmdline"
check "spawn --dry-run（App）: トークンと鍵の中身を出さない" no_secret "$tmp/out" "$tmp/err"
check "spawn --dry-run（App）: 引き継いだトークンをコマンドに書かない" test -z "$(grep -o tok-inherited "$tmp/out" || true)"
check "spawn --dry-run（App）: ラッパーを作らない" test ! -e "$repo/.agents/bin/gh-app/gh"
reset_logs
run_rc env "${appenv[@]}" GH_TOKEN=tok-inherited AGENT_ROLE=orchestrator bash "$A/spawn-worker.sh" 7
check "spawn（App）: 起動する" test "$RC" = 0
check "spawn（App）: Worker の PATH の先頭がラッパー" log_has claude '.path0 == "'"$repo/.agents/bin/gh-app"'" and .gh_real == "'"$fake/bin/gh"'"'
check "spawn（App）: Worker に GH_TOKEN・GITHUB_TOKEN を渡さない" log_has claude '.token_env == ""'
check "spawn（App）: ラッパーの gh を作る（実行できる）" test -x "$repo/.agents/bin/gh-app/gh"
check "spawn（App）: ラッパーにトークンと鍵の中身を書かない" no_secret "$repo/.agents/bin/gh-app/gh"
check "spawn（App）: ラッパーは gh-app.sh を呼ぶ" grep -q "gh-app.sh" "$repo/.agents/bin/gh-app/gh"
check "spawn（App）: ログ・状態のファイルにトークンと鍵の中身を書かない" \
  no_secret "$repo/.agents/run/issue-7.json" "$repo/.agents/logs/"issue-7-*.jsonl "$tmp/out" "$tmp/err"
# ラッパーの gh を、Worker と同じ環境で呼ぶと App のトークンで本物の gh を実行する
reset_logs
run_rc env "${appenv[@]}" AGENT_ROLE=worker AGENT_GH_REAL="$fake/bin/gh" PATH="$repo/.agents/bin/gh-app:$PATH" \
  "$repo/.agents/bin/gh-app/gh" pr list --head x
check "spawn（App）: ラッパーの gh は App のトークンで本物の gh を呼ぶ" \
  test "$RC:$(tail -n 1 "$FAKE_LOG/gh-env.log")" = "0:$app_tok pr list --head x"
rm -rf "$repo/.git/agent-slots" "$repo/.agents/run" "$repo/.agents/logs" "$repo/.agents/bin/gh-app" "$app_cache_dir"
# 鍵が使えなければ（権限が広い）、理由を出して今までどおり起動する
chmod 644 "$app_key"
reset_logs
run_rc env "${appenv[@]}" AGENT_ROLE=orchestrator bash "$A/spawn-worker.sh" 7 --dry-run
check "spawn --dry-run（鍵の権限が広い）: 成功する" test "$RC" = 0
check "spawn --dry-run（鍵の権限が広い）: PATH を変えない" test -z "$(grep -o 'gh-app' "$tmp/out" || true)"
check "spawn --dry-run（鍵の権限が広い）: オーナーのトークンに戻ることを出す" grep -q "600 より広い.*オーナーのトークンを使う" "$tmp/err"
chmod 600 "$app_key"

# ================= merge.sh =================
reset_logs
gate_pass="$(jq -cn --arg s "$head_sha" '{verdict:"pass", repo:"K-Kizuku/raft-game", pr:12, head_sha:$s, results:[]}')"
gate_fail="$(jq -cn --arg s "$head_sha" '{verdict:"fail", head_sha:$s, results:[{id:"C4", status:"fail", detail:"ci-ok が failure"}]}')"
gate_pending="$(jq -cn --arg s "$head_sha" '{verdict:"pending", head_sha:$s, results:[]}')"
run_rc env AGENT_ROLE=worker FAKE_GATE_JSON="$gate_pass" bash "$A/merge.sh" 12
check "merge: Worker の役割では拒否する" test "$RC" = 1
check "merge: Worker のとき gh pr merge を呼ばない" test ! -s "$FAKE_LOG/gh.log"
check "merge: Worker のとき gate も呼ばない" test ! -s "$FAKE_LOG/agentctl.log"
git -C "$repo" worktree add --quiet --detach "$tmp/linked" HEAD
(cd "$tmp/linked" && run_rc env FAKE_GATE_JSON="$gate_pass" bash "$A/merge.sh" 12 && echo "$RC" >"$tmp/rc")
check "merge: 役割が未設定で Worktree の中なら拒否する" test "$(cat "$tmp/rc")" = 1
(cd "$tmp/linked" && run_rc env AGENT_ROLE=orchestrator FAKE_GATE_JSON="$gate_pass" bash "$A/merge.sh" 12 && echo "$RC" >"$tmp/rc")
check "merge: orchestrator でも Worktree の中なら拒否する" test "$(cat "$tmp/rc")" = 1
check "merge: Worktree の中のとき gh pr merge を呼ばない" test ! -s "$FAKE_LOG/gh.log"
git -C "$repo" worktree remove "$tmp/linked"
run_rc env AGENT_ROLE=orchestrator FAKE_GATE_JSON="$gate_fail" FAKE_GATE_RC=1 bash "$A/merge.sh" 12
check "merge: gate が fail なら終了コード 1" test "$RC" = 1
check "merge: gate が fail なら gh pr merge を呼ばない" test -z "$(jq -c 'select(.args[0:2] == ["pr","merge"])' "$FAKE_LOG/gh.log")"
run_rc env AGENT_ROLE=orchestrator FAKE_GATE_JSON="$gate_pending" FAKE_GATE_RC=2 bash "$A/merge.sh" 12
check "merge: gate が pending なら終了コード 2" test "$RC" = 2
check "merge: gate が pending なら gh pr merge を呼ばない" test -z "$(jq -c 'select(.args[0:2] == ["pr","merge"])' "$FAKE_LOG/gh.log")"
run_rc env AGENT_ROLE=orchestrator FAKE_GATE_JSON='{"verdict":"pass"}' FAKE_GATE_RC=0 bash "$A/merge.sh" 12
check "merge: head_sha がなければマージしない" test "$RC" = 3 -a -z "$(jq -c 'select(.args[0:2] == ["pr","merge"])' "$FAKE_LOG/gh.log")"
check "merge: gate を --repo --pr --json で呼ぶ" log_has agentctl '.args == ["gate","--repo","K-Kizuku/raft-game","--pr","12","--json"]'
run_rc env AGENT_ROLE=orchestrator FAKE_GATE_JSON="$gate_pass" bash "$A/merge.sh" 12 --dry-run
check "merge --dry-run: 成功する" test "$RC" = 0
check "merge --dry-run: gh pr merge を呼ばない" test -z "$(jq -c 'select(.args[0:2] == ["pr","merge"])' "$FAKE_LOG/gh.log")"
check "merge --dry-run: コマンドを出す" grep -q -- "--match-head-commit $head_sha" "$tmp/out"
reset_logs
run_rc env AGENT_ROLE=orchestrator FAKE_GATE_JSON="$gate_pass" bash "$A/merge.sh" 12
check "merge: gate が pass ならマージする" test "$RC" = 0
check "merge: --squash --match-head-commit <gate の head_sha> --delete-branch" \
  log_has gh '.args == ["pr","merge","12","--squash","--match-head-commit","'"$head_sha"'","--delete-branch"]'
check "merge: その後 agentctl state 5 merged" log_has agentctl '.args[0:3] == ["state","5","merged"]'
reset_logs
run_rc env AGENT_ROLE=orchestrator FAKE_GATE_JSON="$gate_pass" FAKE_MERGE_RC=1 bash "$A/merge.sh" 12
check "merge: gh pr merge が失敗したら状態を変えない" test "$RC" = 1 -a -z "$(jq -c 'select(.args[0] == "state")' "$FAKE_LOG/agentctl.log")"
# Issue #282: App の鍵が使えれば、gate・gh pr view・gh pr merge・state を App のトークンで行う
rm -rf "$app_cache_dir"
reset_logs
run_rc env "${appenv[@]}" AGENT_ROLE=orchestrator AGENTCTL="$lt/gatecheck" FAKE_BIN="$fake/bin" \
  FAKE_GATE_JSON="$gate_pass" bash "$A/merge.sh" 12
check "merge（App）: マージする" test "$RC" = 0
check "merge（App）: gh pr merge を App のトークンで呼ぶ" \
  grep -qx "$app_tok pr merge 12 --squash --match-head-commit $head_sha --delete-branch" "$FAKE_LOG/gh-env.log"
check "merge（App）: gh pr view を App のトークンで呼ぶ" grep -qx "$app_tok pr view 12 --json headRefName,body" "$FAKE_LOG/gh-env.log"
check "merge（App）: gate と state に App のトークンを渡す" test "$(sort -u "$FAKE_LOG/gate-token.log")" = "$app_tok"
check "merge（App）: state 5 merged を呼ぶ" log_has agentctl '.args[0:3] == ["state","5","merged"]'
check "merge（App）: トークンと鍵の中身を出さない" no_secret "$tmp/out" "$tmp/err"
rm -rf "$app_cache_dir"

# ================= review.sh =================
# Codex の GitHub 連携の確認の間隔・最初の待ち・API の上限での待ちを 0 秒にする
# （偽の agentctl の FAKE_GH_MODE の既定は unavailable）
export AGENT_CODEX_GITHUB_POLL=0 AGENT_CODEX_GITHUB_FIRST_WAIT=0 AGENT_CODEX_GITHUB_RATE_LIMIT_WAIT=0
export FAKE_BASE_SHA="$base_sha"
# GH_TOKEN があれば review.sh は gh auth token を呼ばない（偽の agentctl は使わない）
export GH_TOKEN="test-token"
printf '# Issue 5\n\n本文\n' >"$tmp/issue.md"
review() { # review <追加の引数…>
  run_rc env AGENT_ROLE=orchestrator bash "$A/review.sh" 12 --base "$base_sha" --head "$head_sha" \
    --issue-file "$tmp/issue.md" --no-post --out "$tmp/record.json" "$@"
}
validate_record() {
  if [ -n "$real_agentctl" ]; then
    (cd "$repo" && "$real_agentctl" review validate "$tmp/record.json" >/dev/null)
  else
    jq -e . "$tmp/record.json" >/dev/null
  fi
}
no_worktree_left() {
  [ "$(git -C "$repo" worktree list | wc -l | tr -d ' ')" = 1 ]
}

reset_logs
rm -f "$tmp/record.json"
run_rc env AGENT_ROLE=worker bash "$A/review.sh" 12 --base "$base_sha" --head "$head_sha" --issue-file "$tmp/issue.md" --no-post
check "review: Worker の役割では拒否する" test "$RC" = 1 -a ! -s "$FAKE_LOG/codex.log"

reset_logs
FAKE_CODEX_MODE=good review
check "review: Codex で成功する" test "$RC" = 0
check "review: reviewer=codex・fallback_reason なし" jq -e '.reviewer == "codex" and .fallback_reason == "" and .reviewer_version == "codex-cli 0.99.0"' "$tmp/record.json"
check "review: pr・head_sha・base_sha・round・created_at" \
  jq -e --arg h "$head_sha" --arg b "$base_sha" '.pr == 12 and .head_sha == $h and .base_sha == $b and .round == 1 and (.created_at | test("Z$"))' "$tmp/record.json"
check "review: #3 の書式（agentctl review validate）" validate_record
check "review: --no-post はコメントの本文を出す" grep -q "<!-- agent-review:v1 -->" "$tmp/out"
check "review: --no-post は gh pr comment を呼ばない" test -z "$(jq -c 'select(.args[0:2] == ["pr","comment"])' "$FAKE_LOG/gh.log")"
check "review: Codex を read-only・--output-schema 付きで呼ぶ" \
  log_has codex '.args[0] == "exec" and (.args | index("--sandbox")) != null and .args[(.args | index("--sandbox")) + 1] == "read-only" and (.args | index("--output-schema")) != null and (.args | index("--skip-git-repo-check")) != null'
check "review: Codex を PR の HEAD の一時的な Worktree で動かす" \
  log_has codex '.args[(.args | index("--cd")) + 1] != "'"$repo"'"'
check "review: Codex を使えたら Claude Code を呼ばない" test ! -s "$FAKE_LOG/claude.log"
check "review: 一時的な Worktree を消す" no_worktree_left
check "review: プロンプトに Issue の本文と差分の概要が入る" log_has codex '(.args | last | contains("# Issue 5")) and (.args | last | contains("a.txt"))'

# Issue #282: GH_TOKEN がなく App の鍵が使えれば、review.sh の agentctl（記録の投稿を含む）は App のトークンを使う
rm -rf "$app_cache_dir"
reset_logs
run_rc env -u GH_TOKEN "${appenv[@]}" AGENT_ROLE=orchestrator AGENTCTL="$lt/gatecheck" FAKE_BIN="$fake/bin" \
  FAKE_CODEX_MODE=good bash "$A/review.sh" 12 --base "$base_sha" --head "$head_sha" \
  --issue-file "$tmp/issue.md" --out "$tmp/record.json"
check "review（App）: 成功する" test "$RC" = 0
check "review（App）: 記録を agentctl comment で投稿する" log_has agentctl '.args[0] == "comment" and (.args | index("--body-file")) != null'
check "review（App）: agentctl にはすべて App のトークンを渡す" test "$(sort -u "$FAKE_LOG/gate-token.log")" = "$app_tok"
check "review（App）: gh auth token（オーナーのトークン）を使わない" test -z "$(jq -c 'select(.args == ["auth","token"])' "$FAKE_LOG/gh.log")"
check "review（App）: トークンと鍵の中身を出さない" no_secret "$tmp/out" "$tmp/err" "$FAKE_LOG/comment.md"
rm -rf "$app_cache_dir"

# 認証エラー → Claude Code に切り替える
reset_logs
FAKE_CODEX_MODE=auth review
check "review: 認証エラーなら Claude Code に切り替える" test "$RC" = 0
check "review: reviewer=claude-fallback・fallback_reason=auth_error" \
  jq -e '.reviewer == "claude-fallback" and .fallback_reason == "auth_error" and (.reviewer_version | startswith("claude-code"))' "$tmp/record.json"
check "review: フォールバックの記録も #3 の書式" validate_record
check "review: 認証エラーでは Codex を再試行しない" test "$(wc -l <"$FAKE_LOG/codex.log" | tr -d ' ')" = 1
check "review: Claude Code を reviewer の役割・別のセッションで呼ぶ" \
  log_has claude '.role == "reviewer" and .args[0] == "-p" and (.args | index("--no-session-persistence")) != null and (.args | index("--json-schema")) != null and .args[(.args | index("--permission-mode")) + 1] == "dontAsk"'
check "review: --agent を使わない（PR の HEAD の reviewer.md を読まない）" \
  log_has claude '(.args | index("--agent")) == null'
check "review: reviewer.md の tools を --tools・--allowedTools で明示する" \
  log_has claude '.args[(.args | index("--tools")) + 1] == "Read,Glob,Grep,Bash" and .args[(.args | index("--allowedTools")) + 1] == "Read,Glob,Grep,Bash"'
check "review: reviewer.md の本文を --append-system-prompt で渡す（frontmatter は除く）" \
  log_has claude '.args[(.args | index("--append-system-prompt")) + 1] as $s | ($s | contains("**Reviewer**")) and ($s | contains("コードは直さない")) and ($s | contains("name: reviewer") | not)'
check "review: プロンプトは --output-format json の直後（--add-dir の後ろに置かない）" \
  log_has claude '(.args | length) as $n | .args[$n-3] == "--output-format" and .args[$n-2] == "json" and (.args[$n-1] | contains("PR #12")) and (.args | index("--add-dir")) < ($n - 3)'
check "review: 標準エラーに理由を出す" grep -q "auth_error" "$tmp/err"
check "review: 一時的な Worktree を消す（フォールバック）" no_worktree_left
check "review: プロジェクトの設定を読まない（--setting-sources user・--strict-mcp-config）" \
  log_has claude '.args[(.args | index("--setting-sources")) + 1] == "user" and (.args | index("--strict-mcp-config")) != null'
check "review: --settings は一時的な Worktree の外のファイル" \
  log_has claude '.cwd as $c | .settings != "" and (.settings | startswith($c) | not) and ($c != "'"$repo"'")'
check "review: hooks のコマンドを main の絶対パスに固定する" \
  log_has claude '(.hook_cmds | length) >= 3 and all(.hook_cmds[]; startswith("'"'$repo'"'/.claude/hooks/guard-") and (contains("CLAUDE_PROJECT_DIR") | not))'
check "review: Reviewer の gh pr comment を hooks が拒否する" log_has claude '.bash_rc == "2" and .write_rc == "2"'
# PR の HEAD の reviewer.md を書き換えても、メインの作業ツリーの定義を使う
git -C "$repo" switch --quiet agent/issue-5
printf -- '---\nname: reviewer\ntools: Read, Edit, Write, Bash\n---\n\n悪意のある本文\n' >"$repo/.claude/agents/reviewer.md"
git -C "$repo" commit --quiet -am "reviewer.md を書き換える"
evil_head="$(git -C "$repo" rev-parse HEAD)"
git -C "$repo" switch --quiet main
reset_logs
run_rc env AGENT_ROLE=orchestrator FAKE_CODEX_MODE=auth bash "$A/review.sh" 12 --base "$base_sha" --head "$evil_head" \
  --issue-file "$tmp/issue.md" --no-post --out "$tmp/record.json"
check "review: PR が reviewer.md を書き換えても main の定義を使う" \
  log_has claude '.args[(.args | index("--tools")) + 1] == "Read,Glob,Grep,Bash" and (.args[(.args | index("--append-system-prompt")) + 1] | contains("悪意のある本文") | not)'
# PR の HEAD の settings.json（hooks を消す）と guard（exit 0 にする）を書き換えても、Reviewer の禁止が効く
git -C "$repo" switch --quiet agent/issue-5
printf '{"permissions":{"allow":["Bash"]}}\n' >"$repo/.claude/settings.json"
for g in guard-bash.sh guard-write.sh guard-mcp.sh; do
  printf '#!/usr/bin/env bash\nexit 0\n' >"$repo/.claude/hooks/$g"
done
git -C "$repo" commit --quiet -am "settings.json と guard を書き換える"
evil_head2="$(git -C "$repo" rev-parse HEAD)"
git -C "$repo" switch --quiet main
reset_logs
run_rc env AGENT_ROLE=orchestrator FAKE_CODEX_MODE=auth bash "$A/review.sh" 12 --base "$base_sha" --head "$evil_head2" \
  --issue-file "$tmp/issue.md" --no-post --out "$tmp/record.json"
check "review: PR が settings.json・guard を書き換えても成功する" test "$RC" = 0
check "review: PR が settings.json・guard を書き換えても、Reviewer の gh pr comment・Write を main の hooks が拒否する" \
  log_has claude '.bash_rc == "2" and .write_rc == "2"'
check "review: PR が settings.json を書き換えても main の hooks のコマンドを使う" \
  log_has claude 'all(.hook_cmds[]; startswith("'"'$repo'"'/.claude/hooks/"))'
# main の settings.json に hooks がなければ Reviewer を起動しない
cp "$repo/.claude/settings.json" "$tmp/settings.bak"
printf '{"permissions":{}}\n' >"$repo/.claude/settings.json"
reset_logs
run_rc env AGENT_ROLE=orchestrator FAKE_CODEX_MODE=auth bash "$A/review.sh" 12 --base "$base_sha" --head "$head_sha" \
  --issue-file "$tmp/issue.md" --no-post --out "$tmp/record.json"
check "review: main の settings.json に Bash の hooks がなければ止める" test "$RC" != 0 -a ! -s "$FAKE_LOG/claude.log"
cp "$tmp/settings.bak" "$repo/.claude/settings.json"
git -C "$repo" branch --quiet -f agent/issue-5 "$head_sha"

for pair in rate:rate_limited net:service_unavailable; do
  reset_logs
  FAKE_CODEX_MODE="${pair%%:*}" review
  check "review: ${pair%%:*} → ${pair#*:}" jq -e --arg r "${pair#*:}" '.reviewer == "claude-fallback" and .fallback_reason == $r' "$tmp/record.json"
done
if command -v timeout >/dev/null 2>&1 || command -v gtimeout >/dev/null 2>&1; then
  reset_logs
  FAKE_CODEX_MODE=hang AGENT_CODEX_TIMEOUT=1 review
  check "review: Codex が終わらない（timeout）→ service_unavailable" jq -e '.fallback_reason == "service_unavailable"' "$tmp/record.json"
fi

# codex がない → not_installed
reset_logs
run_rc env PATH="$PATH_NOCODEX" AGENT_ROLE=orchestrator bash "$A/review.sh" 12 --base "$base_sha" --head "$head_sha" \
  --issue-file "$tmp/issue.md" --no-post --out "$tmp/record.json"
check "review: codex がない → 成功する（フォールバック）" test "$RC" = 0
check "review: codex がない → fallback_reason=not_installed" jq -e '.reviewer == "claude-fallback" and .fallback_reason == "not_installed"' "$tmp/record.json"
check "review: codex がないことを標準エラーに出す" grep -q "not_installed" "$tmp/err"

# スキーマ違反 → フォールバックせずに再試行
reset_logs
FAKE_CODEX_MODE=bad-then-good review
check "review: スキーマに合わない出力は再試行する" test "$RC" = 0 -a "$(wc -l <"$FAKE_LOG/codex.log" | tr -d ' ')" = 2
check "review: 再試行で成功したら reviewer=codex" jq -e '.reviewer == "codex"' "$tmp/record.json"
check "review: スキーマ違反ではフォールバックしない" test ! -s "$FAKE_LOG/claude.log"
reset_logs
rm -f "$tmp/record.json"
FAKE_CODEX_MODE=bad review
check "review: スキーマ違反が retry_limit（3）回続いたら終了コード 5" test "$RC" = 5 -a "$(wc -l <"$FAKE_LOG/codex.log" | tr -d ' ')" = 3
check "review: スキーマ違反が続いてもフォールバックしない" test ! -s "$FAKE_LOG/claude.log"
check "review: 失敗したら記録を書かない" test ! -f "$tmp/record.json"
reset_logs
FAKE_CODEX_MODE=fail review
check "review: 分類できない失敗は再試行して終了コード 5" test "$RC" = 5 -a "$(wc -l <"$FAKE_LOG/codex.log" | tr -d ' ')" = 3 -a ! -s "$FAKE_LOG/claude.log"
check "review: 失敗しても一時的な Worktree を消す" no_worktree_left
# プロンプト（Issue の本文）に auth・login・timeout などの語があり、codex がそれを標準エラーに echo しても、
# 標準出力に紛らわしい語があっても、フォールバックの理由にしない（再試行して終了コード 5）
cat >"$tmp/issue-auth.md" <<'EOF'
# Issue 5

401・403・auth・login → auth_error、429・rate limit・usage limit → rate_limited、5xx・timeout・ECONN。
not logged in・HTTP 401・429 Too Many Requests・Proxy connection failed・waiting for network・stream disconnected
author: someone
EOF
reset_logs
FAKE_CODEX_MODE=echo-fail run_rc env AGENT_ROLE=orchestrator bash "$A/review.sh" 12 --base "$base_sha" --head "$head_sha" \
  --issue-file "$tmp/issue-auth.md" --no-post --out "$tmp/record.json"
check "review: プロンプトに auth などの語があっても、分類できない失敗は再試行して終了コード 5" \
  test "$RC" = 5 -a "$(wc -l <"$FAKE_LOG/codex.log" | tr -d ' ')" = 3 -a ! -s "$FAKE_LOG/claude.log"
for pair in proxy:service_unavailable notlogged:auth_error toomany:rate_limited; do
  reset_logs
  FAKE_CODEX_MODE="${pair%%:*}" run_rc env AGENT_ROLE=orchestrator bash "$A/review.sh" 12 --base "$base_sha" --head "$head_sha" \
    --issue-file "$tmp/issue-auth.md" --no-post --out "$tmp/record.json"
  check "review: ${pair%%:*} → ${pair#*:}（プロンプトに auth などの語があっても）" \
    jq -e --arg r "${pair#*:}" '.reviewer == "claude-fallback" and .fallback_reason == $r' "$tmp/record.json"
done

# --reviewer codex はフォールバックしない
reset_logs
FAKE_CODEX_MODE=auth review --reviewer codex
check "review --reviewer codex: 切り替えずに終了コード 5" test "$RC" = 5 -a ! -s "$FAKE_LOG/claude.log"

# 前回の記録 → round を進め、プロンプトに前回の指摘を入れる
reset_logs
jq -n --arg h "$head_sha" --arg b "$base_sha" '{schema:"agent-review/v1", pr:12, head_sha:$h, base_sha:$b, round:2,
  reviewer:"codex", reviewer_version:"codex-cli 0.99.0", fallback_reason:"", verdict:"request_changes", summary:"前回",
  protected_paths_ack:{reviewed:true, weakens_guards:false},
  findings:[{id:"F3", severity:"high", category:"security", file:"a.txt", line:2, title:"前回の指摘", detail:"", status:"open", resolution:""}],
  created_at:"2026-11-01T00:00:00Z"}' >"$tmp/prev.json"
printf '<!-- agent-review:v1 -->\n\n| a | b |\n\n```json\n%s\n```\n' "$(cat "$tmp/prev.json")" >"$tmp/prev.md"
FAKE_CODEX_MODE=good review --previous "$tmp/prev.md"
check "review: 前回の記録（コメントの本文）があれば round を進める" jq -e '.round == 3' "$tmp/record.json"
check "review: 前回の指摘をプロンプトに入れる" log_has codex '.args | last | contains("F3")'
reset_logs
FAKE_CODEX_MODE=good review --previous "$tmp/prev.json"
check "review: 前回の記録（JSON）も読める" jq -e '.round == 3' "$tmp/record.json"

# コメントを載せる
reset_logs
FAKE_CODEX_MODE=good run_rc env AGENT_ROLE=orchestrator bash "$A/review.sh" 12 --base "$base_sha" --head "$head_sha" \
  --issue-file "$tmp/issue.md"
check "review: agentctl comment --body-file で載せる" \
  log_has agentctl '.args[0:5] == ["comment","--repo","K-Kizuku/raft-game","--pr","12"] and (.args | index("--body-file")) != null'
check "review: gh pr comment を使わない" test -z "$(jq -c 'select(.args[0:2] == ["pr","comment"])' "$FAKE_LOG/gh.log")"
check "review: 載せた本文に目印がある" grep -q "<!-- agent-review:v1 -->" "$FAKE_LOG/comment.md"

# ---- Codex の GitHub 連携（--via github、既定）----
codex_gh_calls() { jq -s '[.[] | select(.args[0:2] == ["pr","codex-record"])] | length' "$FAKE_LOG/agentctl.log"; }
reset_logs
FAKE_GH_MODE=completed review
check "review github: 成功する" test "$RC" = 0
check "review github: @codex review を依頼する" \
  log_has agentctl '.args == ["comment","--repo","K-Kizuku/raft-game","--pr","12","--body","@codex review"]'
check "review github: 対象の SHA（HEAD）の完了を agentctl pr codex-record で待つ" \
  log_has agentctl '.args[0:6] == ["pr","codex-record","--repo","K-Kizuku/raft-game","--pr","12"] and .args[(.args | index("--sha")) + 1] == "'"$head_sha"'" and (.args | index("--config")) != null'
check "review github: 記録は reviewer=codex・GitHub 連携の版" \
  jq -e --arg h "$head_sha" '.reviewer == "codex" and .reviewer_version == "chatgpt-codex-connector (GitHub)" and .head_sha == $h and .findings[0].id == "C4101"' "$tmp/record.json"
check "review github: #3 の書式（agentctl review validate）" validate_record
check "review github: ローカルの Codex・Claude Code を呼ばない" test ! -s "$FAKE_LOG/codex.log" -a ! -s "$FAKE_LOG/claude.log"
check "review github: 一時的な Worktree を作らない" no_worktree_left
check "review github: --no-post は記録を投稿しない" test -z "$(jq -c 'select(.args[0] == "comment" and (.args | index("--body-file")) != null)' "$FAKE_LOG/agentctl.log")"
check "review github: --no-post はコメントの本文を出す" grep -q "<!-- agent-review:v1 -->" "$tmp/out"
check "review github: gh を使わない" test ! -s "$FAKE_LOG/gh.log"

# 終わるまで待つ
reset_logs
FAKE_GH_MODE=pending-then-completed review
check "review github: 完了するまで繰り返し確かめる" test "$RC:$(codex_gh_calls)" = "0:3"
check "review github: 待った後の記録は GitHub 連携のもの" jq -e '.reviewer_version == "chatgpt-codex-connector (GitHub)"' "$tmp/record.json"

# 待ちの間隔（Issue #135）: 偽の sleep で秒数を記録する。既定は最初に 60 秒待ち、その後 30 秒ごと
sleeps() { [ ! -f "$FAKE_LOG/sleep.log" ] || tr '\n' ' ' <"$FAKE_LOG/sleep.log" | sed 's/ $//'; }
review_sleep() { # review_sleep <環境変数…> -- <review の引数…>
  local e=()
  while [ "$#" -gt 0 ] && [ "$1" != -- ]; do
    e+=("$1")
    shift
  done
  [ "$#" -eq 0 ] || shift
  run_rc env -u AGENT_CODEX_GITHUB_POLL -u AGENT_CODEX_GITHUB_FIRST_WAIT -u AGENT_CODEX_GITHUB_RATE_LIMIT_WAIT \
    PATH="$fake/bin-sleep:$PATH" AGENT_ROLE=orchestrator ${e[@]+"${e[@]}"} bash "$A/review.sh" 12 \
    --base "$base_sha" --head "$head_sha" --issue-file "$tmp/issue.md" --no-post --out "$tmp/record.json" "$@"
}
reset_logs
review_sleep FAKE_GH_MODE=pending-then-completed
check "review github 間隔: 既定は最初に 60 秒、その後 30 秒ごとに読み直す" test "$RC:$(codex_gh_calls):$(sleeps)" = "0:3:60 30 30"
check "review github 間隔: 読み直した後の記録は GitHub 連携のもの" jq -e '.reviewer_version == "chatgpt-codex-connector (GitHub)"' "$tmp/record.json"
reset_logs
review_sleep FAKE_GH_MODE=pending-then-completed AGENT_CODEX_GITHUB_FIRST_WAIT=7 AGENT_CODEX_GITHUB_POLL=4
check "review github 間隔: AGENT_CODEX_GITHUB_FIRST_WAIT・AGENT_CODEX_GITHUB_POLL で変えられる" test "$RC:$(codex_gh_calls):$(sleeps)" = "0:3:7 4 4"
reset_logs
review_sleep FAKE_GH_MODE=completed
check "review github 間隔: すぐ終わっていても最初の待ちの後に 1 回だけ読む" test "$RC:$(codex_gh_calls):$(sleeps)" = "0:1:60"
reset_logs
review_sleep FAKE_GH_MODE=completed AGENT_CODEX_GITHUB_TIMEOUT=20
check "review github 間隔: 最初の待ちは待ちの全体の上限を超えない" test "$RC:$(sleeps)" = "0:20"
reset_logs
review_sleep FAKE_GH_MODE=pending AGENT_CODEX_GITHUB_POLL=x FAKE_CODEX_MODE=good
check "review github 間隔: 整数でない間隔は拒否する" test "$RC" = 1 -a ! -s "$FAKE_LOG/agentctl.log"

# API の上限（403 のレート制限、Issue #135）: 上限が戻るまで待って続け、失敗として数えない
reset_logs
FAKE_GH_MODE=ratelimit-then-completed FAKE_RATE_RESET="$(($(date +%s) - 10))" review
check "review github 上限: retry_limit より多く上限に達しても続けて GitHub 連携の記録を作る" \
  test "$RC:$(codex_gh_calls)" = "0:5"
check "review github 上限: 記録は GitHub 連携のもの（ローカルに切り替えない）" \
  jq -e '.reviewer_version == "chatgpt-codex-connector (GitHub)"' "$tmp/record.json"
check "review github 上限: ローカルの Codex・Claude Code を呼ばない" test ! -s "$FAKE_LOG/codex.log" -a ! -s "$FAKE_LOG/claude.log"
check "review github 上限: gh api rate_limit で戻る時刻を読む" log_has gh '.args[0:2] == ["api","rate_limit"]'
check "review github 上限: 失敗として数えない（n/3 回目を出さない）" bash -c '! grep -q "回目" "$1"' _ "$tmp/err"
check "review github 上限: 待つことを標準エラーに出す" grep -q "API の上限に達した" "$tmp/err"
# 戻る時刻（reset）まで待つ
reset_logs
review_sleep FAKE_GH_MODE=ratelimit-then-completed FAKE_RATE_RESET="$(($(date +%s) + 120))" AGENT_CODEX_GITHUB_FIRST_WAIT=0 AGENT_CODEX_GITHUB_POLL=0
check "review github 上限: reset の時刻まで待つ" \
  test "$RC" = 0 -a "$(head -n 1 "$FAKE_LOG/sleep.log" 2>/dev/null || echo 0)" -ge 110 -a "$(head -n 1 "$FAKE_LOG/sleep.log" 2>/dev/null || echo 999)" -le 122
check "review github 上限: 上限の待ちの後に GitHub 連携の記録を作る" jq -e '.reviewer_version == "chatgpt-codex-connector (GitHub)"' "$tmp/record.json"
# 戻る時刻が読めない・remaining が 0 でない（二次的な上限）なら 60 秒（AGENT_CODEX_GITHUB_RATE_LIMIT_WAIT）
reset_logs
review_sleep FAKE_GH_MODE=ratelimit-then-completed AGENT_CODEX_GITHUB_FIRST_WAIT=0 AGENT_CODEX_GITHUB_POLL=0
check "review github 上限: 戻る時刻が読めなければ 60 秒ずつ待つ" test "$RC:$(codex_gh_calls):$(sleeps)" = "0:5:60 60 60 60"
reset_logs
review_sleep FAKE_GH_MODE=ratelimit-then-completed FAKE_RATE_REMAINING=12 FAKE_RATE_RESET="$(($(date +%s) + 3000))" \
  AGENT_CODEX_GITHUB_FIRST_WAIT=0 AGENT_CODEX_GITHUB_RATE_LIMIT_WAIT=9
check "review github 上限: remaining が 0 でなければ AGENT_CODEX_GITHUB_RATE_LIMIT_WAIT 秒待つ" \
  test "$RC:$(codex_gh_calls):$(sleeps)" = "0:5:9 9 9 9"
# 戻るのが待ちの全体の上限より後・上限に達したまま時間切れ → 今までどおり service_unavailable（タイムアウト）
reset_logs
review_sleep FAKE_GH_MODE=ratelimit FAKE_RATE_RESET="$(($(date +%s) + 2000))" AGENT_CODEX_GITHUB_FIRST_WAIT=0 FAKE_CODEX_MODE=good
check "review github 上限: 戻るのが待ちの上限（900 秒）より後なら待たずにローカルの Codex に切り替える" \
  test "$RC:$(codex_gh_calls):$(sleeps)" = "0:1:" -a "$(wc -l <"$FAKE_LOG/codex.log" | tr -d ' ')" = 1
check "review github 上限: そのときの理由はタイムアウトの service_unavailable" grep -q "service_unavailable.*AGENT_CODEX_GITHUB_TIMEOUT" "$tmp/err"
reset_logs
FAKE_GH_MODE=ratelimit AGENT_CODEX_GITHUB_TIMEOUT=0 FAKE_CODEX_MODE=good review
check "review github 上限: 上限に達したまま時間切れならタイムアウトの理由で切り替える" \
  test "$RC" = 0 -a "$(wc -l <"$FAKE_LOG/codex.log" | tr -d ' ')" = 1
check "review github 上限: 時間切れの理由は今までどおり" grep -q "service_unavailable.*AGENT_CODEX_GITHUB_TIMEOUT" "$tmp/err"
check "review github 上限: 時間切れの後の記録はローカルの Codex のもの" jq -e '.reviewer_version == "codex-cli 0.99.0"' "$tmp/record.json"

# タイムアウト → service_unavailable で、これまでの経路（ローカルの Codex CLI）へ
reset_logs
FAKE_GH_MODE=pending AGENT_CODEX_GITHUB_TIMEOUT=0 FAKE_CODEX_MODE=good review
check "review github: タイムアウトしたらローカルの Codex に切り替える" \
  test "$RC" = 0 -a "$(wc -l <"$FAKE_LOG/codex.log" | tr -d ' ')" = 1
check "review github: タイムアウトの後の記録はローカルの Codex のもの" jq -e '.reviewer == "codex" and .reviewer_version == "codex-cli 0.99.0" and .fallback_reason == ""' "$tmp/record.json"
check "review github: タイムアウトの理由を出す" grep -q "service_unavailable.*AGENT_CODEX_GITHUB_TIMEOUT" "$tmp/err"
reset_logs
FAKE_GH_MODE=pending AGENT_CODEX_GITHUB_TIMEOUT=0 FAKE_CODEX_MODE=auth review
check "review github: タイムアウト → ローカルの Codex も使えなければ Claude Code" \
  jq -e '.reviewer == "claude-fallback" and .fallback_reason == "auth_error"' "$tmp/record.json"
if command -v timeout >/dev/null 2>&1 || command -v gtimeout >/dev/null 2>&1; then
  reset_logs
  FAKE_GH_MODE=pending AGENT_CODEX_GITHUB_TIMEOUT=1 AGENT_CODEX_GITHUB_POLL=1 FAKE_CODEX_MODE=good review
  check "review github: AGENT_CODEX_GITHUB_TIMEOUT 秒まで待つ" test "$RC" = 0 -a "$(codex_gh_calls)" -ge 2
fi

# 連携の不調（エラーが retry_limit 回続く・依頼を投稿できない・記録が不正）→ これまでの経路へ
reset_logs
FAKE_GH_MODE=error FAKE_CODEX_MODE=good review
check "review github: エラーが retry_limit（3）回続いたら切り替える" test "$RC:$(codex_gh_calls)" = "0:3"
check "review github: エラーの後はローカルの Codex" jq -e '.reviewer_version == "codex-cli 0.99.0"' "$tmp/record.json"
reset_logs
FAKE_CODEX_MODE=good review
check "review github: 依頼を投稿できなければ待たずに切り替える" test "$RC:$(codex_gh_calls)" = "0:0"
check "review github: 依頼を投稿できない理由を出す" grep -q "service_unavailable.*agentctl comment" "$tmp/err"
reset_logs
FAKE_GH_MODE=bad FAKE_CODEX_MODE=good review
check "review github: 変換した記録が不正なら切り替える" jq -e '.reviewer_version == "codex-cli 0.99.0"' "$tmp/record.json"
# --reviewer codex: GitHub 連携 → ローカルの Codex。Claude Code には切り替えない
reset_logs
FAKE_GH_MODE=pending AGENT_CODEX_GITHUB_TIMEOUT=0 FAKE_CODEX_MODE=auth review --reviewer codex
check "review github --reviewer codex: Claude Code に切り替えずに終了コード 5" test "$RC" = 5 -a ! -s "$FAKE_LOG/claude.log"
reset_logs
FAKE_GH_MODE=completed review --reviewer codex
check "review github --reviewer codex: GitHub 連携を使う" jq -e '.reviewer_version == "chatgpt-codex-connector (GitHub)"' "$tmp/record.json"

# --via local・--reviewer claude は GitHub 連携を使わない
reset_logs
FAKE_GH_MODE=completed FAKE_CODEX_MODE=good review --via local
check "review --via local: GitHub 連携を使わない" \
  test "$RC" = 0 -a -z "$(jq -c 'select(.args[0] == "pr" or .args[0] == "comment")' "$FAKE_LOG/agentctl.log")"
check "review --via local: ローカルの Codex" jq -e '.reviewer_version == "codex-cli 0.99.0"' "$tmp/record.json"
reset_logs
FAKE_GH_MODE=completed AGENT_FALLBACK_REASON=rate_limited review --reviewer claude
check "review --reviewer claude: GitHub 連携を使わない" \
  test "$RC" = 0 -a -z "$(jq -c 'select(.args[0] == "pr" or .args[0] == "comment")' "$FAKE_LOG/agentctl.log")"
review --via bogus
check "review: 不正な --via は拒否する" test "$RC" = 1
review --via github --reviewer claude
check "review: --via github と --reviewer claude は一緒に使えない" test "$RC" = 1

# --head がなければ agentctl pr view の HEAD を使う。--previous を渡す。記録を agentctl comment で載せる
reset_logs
FAKE_GH_MODE=completed FAKE_PR_HEAD_SHA="$head_sha" run_rc env AGENT_ROLE=orchestrator bash "$A/review.sh" 12 \
  --previous "$tmp/prev.json" --out "$tmp/record.json"
check "review github: --head がなくても成功する" test "$RC" = 0
check "review github: agentctl pr view で HEAD を読む" log_has agentctl '.args[0:2] == ["pr","view"]'
check "review github: pr view の HEAD の完了を待つ" log_has agentctl '.args[0:2] == ["pr","codex-record"] and .args[(.args | index("--sha")) + 1] == "'"$head_sha"'"'
check "review github: --previous を codex-record に渡す" log_has agentctl '.args[0:2] == ["pr","codex-record"] and .args[(.args | index("--previous")) + 1] == "'"$tmp/prev.json"'"'
check "review github: 記録を agentctl comment --body-file で載せる" grep -q "<!-- agent-review:v1 -->" "$FAKE_LOG/comment.md"
check "review github: gh を使わない（--head なし）" test ! -s "$FAKE_LOG/gh.log"

# 依頼する前の要約のコメントの updated_at を覚え、それより新しい completed だけを待つ（--after）
reset_logs
FAKE_GH_MODE=completed FAKE_PR_COMMENTS='[
  {"id":1,"author":"chatgpt-codex-connector[bot]","body":"<!-- codex-review-state:v1 {} -->","created_at":"2026-11-01T00:00:00Z","updated_at":"2026-11-05T10:30:00Z"},
  {"id":2,"author":"chatgpt-codex-connector[bot]","body":"<!-- codex-review-state:v1 {} -->","created_at":"2026-11-04T00:00:00Z","updated_at":""},
  {"id":3,"author":"someone-else","body":"<!-- codex-review-state:v1 {} -->","created_at":"2026-11-09T00:00:00Z","updated_at":"2026-11-09T00:00:00Z"},
  {"id":4,"author":"chatgpt-codex-connector[bot]","body":"ふつうのコメント","created_at":"2026-11-08T00:00:00Z","updated_at":"2026-11-08T00:00:00Z"}
]' review
check "review github: 依頼の前に agentctl pr view で要約のコメントを読む" \
  test "$(jq -s -r '[.[] | select(.args[0] == "pr" or .args[0] == "comment") | .args[0:2] | join(" ")] | .[0:2] | join(",")' "$FAKE_LOG/agentctl.log")" = "pr view,comment --repo"
check "review github: 信頼する作者の要約のコメントの最新の updated_at を --after に渡す" \
  log_has agentctl '.args[0:2] == ["pr","codex-record"] and .args[(.args | index("--after")) + 1] == "2026-11-05T10:30:00Z"'
reset_logs
FAKE_GH_MODE=completed review
check "review github: 要約のコメントがまだなければ --after を付けない" \
  log_has agentctl '.args[0:2] == ["pr","codex-record"] and (.args | index("--after")) == null'
reset_logs
FAKE_GH_MODE=completed FAKE_PR_VIEW=fail FAKE_CODEX_MODE=good review
check "review github: pr view が失敗したら依頼せずに切り替える" \
  test "$RC" = 0 -a -z "$(jq -c 'select(.args[0] == "comment" and (.args | index("--body")) != null)' "$FAKE_LOG/agentctl.log")"
check "review github: pr view の失敗の後はローカルの Codex" jq -e '.reviewer_version == "codex-cli 0.99.0"' "$tmp/record.json"

# 保護されたパスの変更があれば、GitHub 連携のレビューに続けて、これまでの経路のレビューも行う（両方の記録を出す）
reset_logs
FAKE_GH_MODE=completed FAKE_PROTECTED_FILES=$'scripts/agents/review.sh\ntools/agentctl/pr.go' FAKE_CODEX_MODE=good review
check "review github（保護されたパス）: 成功する" test "$RC" = 0
check "review github（保護されたパス）: agentctl pr protected-files で確かめる" \
  log_has agentctl '.args[0:6] == ["pr","protected-files","--repo","K-Kizuku/raft-game","--pr","12"]'
check "review github（保護されたパス）: ローカルの Codex のレビューも行う" test "$(wc -l <"$FAKE_LOG/codex.log" | tr -d ' ')" = 1
check "review github（保護されたパス）: 両方の記録を出す（GitHub 連携が先）" \
  test "$(grep -c '<!-- agent-review:v1 -->' "$tmp/out")" = 2 -a \
  "$(grep -o 'chatgpt-codex-connector (GitHub)\|codex-cli 0.99.0' "$tmp/out" | head -n 1)" = "chatgpt-codex-connector (GitHub)"
check "review github（保護されたパス）: --out はローカルの記録（GitHub 連携の記録の次の round）" \
  jq -e '.reviewer_version == "codex-cli 0.99.0" and .round == 3 and .protected_paths_ack.reviewed == true' "$tmp/record.json"
check "review github（保護されたパス）: GitHub 連携の記録を前回としてプロンプトに入れる" log_has codex '.args | last | contains("C4101")'
check "review github（保護されたパス）: 一時的な Worktree を残さない" no_worktree_left
reset_logs
FAKE_GH_MODE=completed FAKE_PROTECTED_FILES='scripts/agents/review.sh' FAKE_CODEX_MODE=auth review
check "review github（保護されたパス）: ローカルの Codex が使えなければ Claude Code" \
  jq -e '.reviewer == "claude-fallback" and .fallback_reason == "auth_error"' "$tmp/record.json"
reset_logs
FAKE_GH_MODE=completed FAKE_PROTECTED_RC=3 FAKE_CODEX_MODE=good review
check "review github: 保護されたパスを確かめられなければ、これまでの経路のレビューも行う" \
  test "$RC" = 0 -a "$(grep -c '<!-- agent-review:v1 -->' "$tmp/out")" = 2
reset_logs
FAKE_GH_MODE=completed FAKE_PROTECTED_FILES=$'scripts/agents/review.sh' FAKE_CODEX_MODE=good run_rc env AGENT_ROLE=orchestrator \
  bash "$A/review.sh" 12 --base "$base_sha" --head "$head_sha" --issue-file "$tmp/issue.md" --out "$tmp/record.json"
check "review github（保護されたパス）: 両方の記録を PR に載せる" \
  test "$(jq -s '[.[] | select(.args[0] == "comment" and (.args | index("--body-file")) != null)] | length' "$FAKE_LOG/agentctl.log")" = 2

# ================= wt.sh =================
mk_wt() { # mk_wt <issue> : agent/issue-<n> の Worktree を作って push する
  local n="$1" d="$repo/.claude/worktrees/issue-$1"
  git -C "$repo" worktree add --quiet -b "agent/issue-$n" "$d" main
  printf '%s\n' "$n" >"$d/n$n.txt"
  git -C "$d" add -A
  git -C "$d" commit --quiet -m "issue $n"
  git -C "$d" push --quiet origin "agent/issue-$n"
  git -C "$d" rev-parse HEAD
}
sha8="$(mk_wt 8)"
sha9="$(mk_wt 9)"
sha10="$(mk_wt 10)"
sha11="$(mk_wt 11)"
printf 'dirty\n' >>"$repo/.claude/worktrees/issue-9/n9.txt"
git -C "$repo" worktree lock "$repo/.claude/worktrees/issue-8"
# issue-11 は push していないコミットがある（PR の HEAD は 1 つ前）
printf 'more\n' >>"$repo/.claude/worktrees/issue-11/n11.txt"
git -C "$repo/.claude/worktrees/issue-11" commit --quiet -am more
git -C "$repo" push --quiet origin --delete agent/issue-11
mkdir -p "$repo/.agents/logs"
: >"$repo/.agents/logs/issue-8-20261106T000000Z.jsonl"
AGENT_CONFIG="" bash "$DS" acquire --path "$repo/.claude/worktrees/issue-8" >/dev/null
mkdir -p "$repo/.agents/run"
jq -n '{issue:8, pid:999999}' >"$repo/.agents/run/issue-8.json"
jq -n --arg a "$sha8" --arg b "$sha9" --arg c "$sha10" --arg d "$sha11" '[
  {headRefName:"agent/issue-8", state:"MERGED", number:108, headRefOid:$a},
  {headRefName:"agent/issue-9", state:"MERGED", number:109, headRefOid:$b},
  {headRefName:"agent/issue-10", state:"OPEN", number:110, headRefOid:$c},
  {headRefName:"agent/issue-11", state:"MERGED", number:111, headRefOid:$c}
]' >"$tmp/prs.json"

run_rc env AGENT_ROLE=orchestrator bash "$A/wt.sh" list --pr-state-file "$tmp/prs.json"
check "wt list: 一覧を出す" test "$RC" = 0 -a "$(grep -c '^issue-' "$tmp/out")" = 4
check "wt list: 未コミットの変更を出す" grep -q "^issue-9.*dirty=yes" "$tmp/out"
run_rc env AGENT_ROLE=orchestrator bash "$A/wt.sh" cleanup --dry-run --pr-state-file "$tmp/prs.json"
check "wt cleanup --dry-run: 何も消さない" test -d "$repo/.claude/worktrees/issue-8" -a ! -f "$repo/.agents/archive/issue-8.json"
check "wt cleanup --dry-run: 消す予定を出す" grep -q "消す予定: issue-8" "$tmp/out"
run_rc env AGENT_ROLE=worker bash "$A/wt.sh" cleanup --pr-state-file "$tmp/prs.json"
check "wt cleanup: Worker の役割では拒否する" test "$RC" = 1 -a -d "$repo/.claude/worktrees/issue-8"
run_rc env AGENT_ROLE=orchestrator bash "$A/wt.sh" cleanup --pr-state-file "$tmp/prs.json"
check "wt cleanup: マージ済み・変更なし → 消す" test "$RC" = 0 -a ! -d "$repo/.claude/worktrees/issue-8"
check "wt cleanup: 記録を残す" jq -e --arg s "$sha8" '.issue == 8 and .branch == "agent/issue-8" and .last_sha == $s and .pr == 108 and (.logs | length) == 1 and (.removed_at | test("Z$"))' "$repo/.agents/archive/issue-8.json"
check "wt cleanup: ローカルのブランチを消す" test -z "$(git -C "$repo" branch --list agent/issue-8)"
check "wt cleanup: DEV_SLOT を返す" test -z "$(ls "$repo/.git/agent-slots" 2>/dev/null || true)"
check "wt cleanup: 状態のファイルを消す" test ! -f "$repo/.agents/run/issue-8.json"
check "wt cleanup: 未コミットの変更あり → 残す" test -d "$repo/.claude/worktrees/issue-9"
check "wt cleanup: 未コミットの理由を出す" grep -q "issue-9（未コミットの変更がある）" "$tmp/out"
check "wt cleanup: PR が開いている → 残す" test -d "$repo/.claude/worktrees/issue-10"
check "wt cleanup: PR が開いている理由を出す" grep -q "issue-10（PR が開いている）" "$tmp/out"
check "wt cleanup: push していない → 残す" test -d "$repo/.claude/worktrees/issue-11"
check "wt cleanup: 残したものは記録しない" test ! -f "$repo/.agents/archive/issue-9.json" -a ! -f "$repo/.agents/archive/issue-10.json"
check "wt cleanup: 消したのは 1 つだけ" test "$(git -C "$repo" worktree list | wc -l | tr -d ' ')" = 4
# --issue と gh（偽の gh pr list）
reset_logs
git -C "$repo/.claude/worktrees/issue-9" checkout -- n9.txt
jq '(.[] | select(.headRefName == "agent/issue-10") | .state) = "CLOSED"' "$tmp/prs.json" >"$tmp/prs2.json"
FAKE_PR_LIST="$tmp/prs2.json" run_rc env AGENT_ROLE=orchestrator bash "$A/wt.sh" cleanup --issue 10
check "wt cleanup --issue 10: 閉じた PR・変更なし → 消す" test "$RC" = 0 -a ! -d "$repo/.claude/worktrees/issue-10" -a -d "$repo/.claude/worktrees/issue-9"
check "wt cleanup: gh pr list --head <branch> --state all で PR を調べる" \
  log_has gh '.args[0:4] == ["pr","list","--head","agent/issue-10"] and (.args | index("all")) != null'

# ================= merge-train.sh・autospawn.sh（#154）=================
# 別のリポジトリ（origin は上と同じ bare）に、merge-train.sh・autospawn.sh・lib.sh と、偽の merge.sh・
# review.sh・spawn-worker.sh を置く。gh と agentctl は、$TRAIN_DIR のファイルで答える偽物を PATH の先頭に置く。
# 周の間隔は 0 にし、AGENT_TRAIN_CYCLES で周の数を決める（実時間を待たない）。
TRAIN_DIR="$tmp/train-fake"
export TRAIN_DIR
# （origin の clone にする。マージの後の git pull --ff-only を確かめるため。scripts/agents は別の場所に置く）
trepo="$tmp/trainrepo"
git clone --quiet "$tmp/origin.git" "$trepo"
TS="$trepo/.train-scripts/scripts/agents"
mkdir -p "$TS" "$fake/bin-train" "$TRAIN_DIR"
printf '.train-scripts/\n.agents/run/\n.agents/logs/\n' >>"$trepo/.git/info/exclude"
cp "$A/merge-train.sh" "$A/autospawn.sh" "$A/lib.sh" "$TS/"
export TRAIN_ROOT="$trepo"

cat >"$TS/merge.sh" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$FAKE_LOG/merge.log"
# merge-sets-merged があれば、終了コードに関わらず PR をマージ済みにする（不具合 4）
[ ! -f "$TRAIN_DIR/merge-sets-merged" ] || echo MERGED >"$TRAIN_DIR/state-$1"
exit "$(cat "$TRAIN_DIR/merge-rc" 2>/dev/null || echo 0)"
EOF
cat >"$TS/review.sh" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$FAKE_LOG/review.log"
sleep "${FAKE_REVIEW_SLEEP:-0}"
EOF
cat >"$TS/spawn-worker.sh" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$FAKE_LOG/spawn.log"
# FAKE_WORKER_SLEEP があれば、本物の --background と同じく、Worker（sleep）を nohup で背景に起動して PID を残す（#240）
if [ -n "${FAKE_WORKER_SLEEP:-}" ]; then
  (exec nohup sleep "$FAKE_WORKER_SLEEP") </dev/null >/dev/null 2>&1 &
  printf '%s\n' "$!" >"$TRAIN_DIR/worker-$1.pid"
fi
exit "$(cat "$TRAIN_DIR/spawn-rc-$1" 2>/dev/null || echo 0)"
EOF
chmod +x "$TS/merge.sh" "$TS/review.sh" "$TS/spawn-worker.sh"

cat >"$fake/bin-train/gh" <<'EOF'
#!/usr/bin/env bash
jq -cn --arg cwd "$PWD" '{cwd:$cwd, args:$ARGS.positional}' --args -- "$@" >>"$FAKE_LOG/gh.log"
# Issue #282: 受け取った GH_TOKEN も記録する
printf '%s %s\n' "${GH_TOKEN:-none}" "$*" >>"$FAKE_LOG/gh-env.log"
n="${3:-}"
case "$1 $2" in
  "pr list")
    # gh pr list --head <ブランチ> … -q '.[0].number // empty'（#240）: headpr-<ブランチの / を - に>の番号を出す
    h="" prev=""
    for a in "$@"; do
      [ "$prev" != "--head" ] || h="$a"
      prev="$a"
    done
    if [ -n "$h" ]; then
      cat "$TRAIN_DIR/headpr-$(printf '%s' "$h" | tr / -)" 2>/dev/null || true
      exit 0
    fi
    cat "$TRAIN_DIR/prs.json" 2>/dev/null || echo '[]'
    ;;
  "pr view")
    st="$(cat "$TRAIN_DIR/state-$n" 2>/dev/null || echo OPEN)"
    case " $* " in
      *" -q "*) printf '%s\n' "$st"; exit 0 ;;
    esac
    bf="$TRAIN_DIR/body-$n.md"
    [ -f "$bf" ] || bf=/dev/null
    ck="$(cat "$TRAIN_DIR/checks-$n.json" 2>/dev/null || echo '[{"name":"ci-ok","bucket":"pass"}]')"
    jq -n --argjson n "$n" --arg st "$st" --arg h "$(cat "$TRAIN_DIR/head-$n" 2>/dev/null || echo "h$n")" \
      --argjson c "$(cat "$TRAIN_DIR/comments-$n" 2>/dev/null || echo 0)" --rawfile b "$bf" --argjson ck "$ck" \
      '{number:$n, state:$st, headRefOid:$h, updatedAt:"2026-11-07T00:00:00Z", comments:[range($c) | {body:"c"}],
        statusCheckRollup:[$ck[] | {name, conclusion:.bucket}], body:$b}'
    ;;
  "pr checks")
    cat "$TRAIN_DIR/checks-$n.json" 2>/dev/null || echo '[{"name":"ci-ok","bucket":"pass"}]'
    exit "$(cat "$TRAIN_DIR/checks-rc-$n" 2>/dev/null || echo 0)"
    ;;
  "api -X")
    # gh api -X PUT repos/<repo>/pulls/<n>/update-branch
    n="${4%/update-branch}"
    n="${n##*/}"
    # 失敗の出力は update-msg-<n>（なければ gh が HTTP 422 のコンフリクトで出す形）
    if [ -f "$TRAIN_DIR/update-rc-$n" ]; then
      if [ -f "$TRAIN_DIR/update-msg-$n" ]; then
        cat "$TRAIN_DIR/update-msg-$n" >&2
      else
        echo '{"message":"merge conflict between base and head","status":"422"}'
        echo 'gh: merge conflict between base and head (HTTP 422)' >&2
      fi
      exit "$(cat "$TRAIN_DIR/update-rc-$n")"
    fi
    ;;
  "issue view")
    # gh issue view <n> --json updatedAt -q .updatedAt（判定の目印。issue-rc-<n> があれば失敗する）
    case " $* " in
      *" updatedAt "*)
        [ ! -f "$TRAIN_DIR/issue-rc-$n" ] || exit "$(cat "$TRAIN_DIR/issue-rc-$n")"
        cat "$TRAIN_DIR/issue-updated-$n" 2>/dev/null || echo "2026-11-07T00:00:00Z"
        exit 0
        ;;
    esac
    # gh issue view <n> --json labels -q '.labels[].name'
    [ ! -f "$TRAIN_DIR/labels-rc-$n" ] || exit "$(cat "$TRAIN_DIR/labels-rc-$n")"
    cat "$TRAIN_DIR/labels-$n" 2>/dev/null || true
    ;;
  "pr edit")
    prev=""
    for a in "$@"; do
      [ "$prev" = "--body-file" ] && cp "$a" "$FAKE_LOG/edit-$n.md"
      prev="$a"
    done
    ;;
  "api rate_limit")
    [ -n "${FAKE_RATE_RESET:-}" ] || { echo "HTTP 500" >&2; exit 1; }
    printf '%s %s\n' "${FAKE_RATE_REMAINING:-0}" "$FAKE_RATE_RESET"
    ;;
esac
EOF
cat >"$fake/bin-train/agentctl" <<'EOF'
#!/usr/bin/env bash
jq -cn --arg cwd "$PWD" '{cwd:$cwd, args:$ARGS.positional}' --args -- "$@" >>"$FAKE_LOG/agentctl.log"
case "$1" in
  gate)
    n="$5"
    [ ! -f "$TRAIN_DIR/gate-$n.json" ] || cat "$TRAIN_DIR/gate-$n.json"
    if [ -f "$TRAIN_DIR/gate-rc-$n" ]; then exit "$(cat "$TRAIN_DIR/gate-rc-$n")"; fi
    case "$(jq -r '.verdict // ""' "$TRAIN_DIR/gate-$n.json" 2>/dev/null)" in
      pass) exit 0 ;; fail) exit 1 ;; pending) exit 2 ;; *) exit 3 ;;
    esac
    ;;
  state)
    # state-rc-<Issue>-<状態> があれば、その終了コードで失敗する
    exit "$(cat "$TRAIN_DIR/state-rc-$2-$3" 2>/dev/null || echo 0)"
    ;;
  plan)
    # FAKE_PLAN_STOP_AFTER 回目の plan で止めのファイルを作る
    cat "$TRAIN_DIR/plan.json"
    k="$(jq -s '[.[] | select(.args[0] == "plan")] | length' "$FAKE_LOG/agentctl.log")"
    [ "$k" -lt "${FAKE_PLAN_STOP_AFTER:-999999}" ] || : >"$TRAIN_ROOT/.agents/run/autospawn.stop"
    ;;
esac
EOF
chmod +x "$fake/bin-train/gh" "$fake/bin-train/agentctl"

train_reset() {
  reset_logs
  rm -rf "$TRAIN_DIR" "$trepo/.agents/run" "$trepo/.agents/logs"
  mkdir -p "$TRAIN_DIR"
  : >"$FAKE_LOG/merge.log"
  : >"$FAKE_LOG/review.log"
  : >"$FAKE_LOG/spawn.log"
}
# mkgate <PR> <verdict> <C2> <C6> <C7> <C9> <C10> [closed] [C12] [head]: 偽の gate の結果を置く
mkgate() {
  jq -n --argjson pr "$1" --arg v "$2" --arg c2 "$3" --arg c6 "$4" --arg c7 "$5" --arg c9 "$6" --arg c10 "$7" \
    --argjson closed "${8:-false}" --arg c12 "${9:-pass}" --arg h "${10:-h$1}" \
    '{verdict:$v, repo:"K-Kizuku/raft-game", pr:$pr, head_sha:$h, closed:$closed,
    results:[{id:"C1",status:"pass"},{id:"C2",status:$c2},{id:"C4",status:"pass"},{id:"C6",status:$c6},
      {id:"C7",status:$c7},{id:"C9",status:$c9},{id:"C10",status:$c10},{id:"C12",status:$c12}]}' >"$TRAIN_DIR/gate-$1.json"
}
mkprs() { # mkprs <PR:ブランチ>…
  local a out="[]"
  for a in "$@"; do
    out="$(jq -c --argjson n "${a%%:*}" --arg b "${a#*:}" '. + [{number:$n, headRefName:$b}]' <<<"$out")"
  done
  printf '%s\n' "$out" >"$TRAIN_DIR/prs.json"
}
train() { # train <周の数> [<環境変数>…] -- [<merge-train.sh の引数>…]
  local c="$1" e=()
  shift
  while [ "$#" -gt 0 ] && [ "$1" != -- ]; do
    e+=("$1")
    shift
  done
  [ "$#" -eq 0 ] || shift
  (cd "$trepo" && run_rc env PATH="$fake/bin-train:$PATH" AGENT_ROLE=orchestrator AGENT_TRAIN_INTERVAL=0 \
    AGENT_TRAIN_CYCLES="$c" ${e[@]+"${e[@]}"} bash "$TS/merge-train.sh" "$@" && printf '%s\n' "$RC" >"$tmp/rc")
  RC="$(cat "$tmp/rc")"
}
count_lines() { grep -c . "$1" 2>/dev/null || true; }
gate_calls() { jq -s --arg n "$1" '[.[] | select(.args[0] == "gate" and .args[4] == $n)] | length' "$FAKE_LOG/agentctl.log"; }
gh_calls() { jq -s --arg a "$1" --arg n "$2" '[.[] | select((.args[0:2] | join(" ")) == $a and .args[2] == $n)] | length' "$FAKE_LOG/gh.log"; }
update_calls() {
  jq -s --arg p "repos/K-Kizuku/raft-game/pulls/$1/update-branch" \
    '[.[] | select(.args == ["api","-X","PUT",$p])] | length' "$FAKE_LOG/gh.log"
}
# wait_reviews: 起動した偽の review.sh が終わるまで待つ（最大 10 秒）
wait_reviews() {
  local f p i
  for f in "$trepo/.agents/run/train"/review-*.pid; do
    [ -f "$f" ] || continue
    p="$(cat "$f")"
    i=0
    while kill -0 "$p" 2>/dev/null && [ "$i" -lt 100 ]; do
      sleep 0.1
      i=$((i + 1))
    done
  done
}
# kill_reviews: 起動した偽の review.sh を止める（長く眠らせたもの）
kill_reviews() {
  local f
  for f in "$trepo/.agents/run/train"/review-*.pid; do
    [ -f "$f" ] || continue
    kill "$(cat "$f")" 2>/dev/null || true
  done
  wait_reviews
}
# setround <PR> <round>: 偽の gate の結果に最新のレビュー記録（latest_review.round）を足す（#240）
setround() {
  jq --argjson r "$2" '. + {latest_review:{comment_id:1, author:"K-Kizuku", round:$r, head_sha:.head_sha, verdict:"request_changes", reviewer:"codex"}}' \
    "$TRAIN_DIR/gate-$1.json" >"$TRAIN_DIR/gate-$1.tmp" && mv "$TRAIN_DIR/gate-$1.tmp" "$TRAIN_DIR/gate-$1.json"
}
# setdetail <PR> <ID> <detail>: 偽の gate の結果の条件に detail を足す（#240）
setdetail() {
  jq --arg id "$2" --arg d "$3" '.results |= map(if .id == $id then . + {detail:$d} else . end)' \
    "$TRAIN_DIR/gate-$1.json" >"$TRAIN_DIR/gate-$1.tmp" && mv "$TRAIN_DIR/gate-$1.tmp" "$TRAIN_DIR/gate-$1.json"
}
# bg_group <ディレクトリ> <コマンド…>: 別のプロセスグループで背景に起動する（グループの番号＝PID を $tmp/bg.pid に書く）。
# そのグループに SIGTERM を送り、TaskStop・Ctrl-C でプロセスグループごと止めた場合を真似る（#240）
bg_group() {
  local d="$1"
  shift
  (
    set -m
    (cd "$d" && exec "$@") </dev/null >"$tmp/bg.out" 2>"$tmp/bg.err" &
    printf '%s\n' "$!" >"$tmp/bg.pid"
  )
}
# wait_until <条件のコマンド…>: 条件が成り立つまで待つ（最大 15 秒）。成り立たなければ 1
wait_until() {
  local i=0
  until "$@"; do
    [ "$i" -lt 150 ] || return 1
    sleep 0.1
    i=$((i + 1))
  done
}
pid_dead() { ! kill -0 "$1" 2>/dev/null; }

# ---- 役割 ----
train_reset
mkprs "21:agent/issue-121"
mkgate 21 pass pass pass pass pass pass
(cd "$trepo" && run_rc env PATH="$fake/bin-train:$PATH" AGENT_ROLE=worker AGENT_TRAIN_CYCLES=1 bash "$TS/merge-train.sh" &&
  printf '%s\n' "$RC" >"$tmp/rc")
check "train: Worker の役割では拒否する" test "$(cat "$tmp/rc")" = 1 -a ! -s "$FAKE_LOG/merge.log" -a ! -s "$FAKE_LOG/gh.log"
train 1 -- 21
check "train: PR:Issue の形でない引数は拒否する" test "$RC" = 1 -a ! -s "$FAKE_LOG/merge.log"
check "train --help" bash "$TS/merge-train.sh" --help

# ---- pass の PR が 2 つ → 前の 1 つだけを 1 周でマージする（PR 番号の小さい順・agent/issue-* だけ）----
train_reset
mkprs "22:agent/issue-122" "23:feature/x" "21:agent/issue-121"
mkgate 21 pass pass pass pass pass pass
mkgate 22 pass pass pass pass pass pass
mkgate 23 pass pass pass pass pass pass
train 1
check "train: 成功する（1 周）" test "$RC" = 0
check "train: pass の PR が 2 つなら、前の 1 つ（#21）だけをマージする" test "$(cat "$FAKE_LOG/merge.log")" = "21"
check "train: agent/issue-* でない PR は判定しない" test "$(gate_calls 23)" = 0
check "train: gh pr list で開いた PR を見つける" log_has gh '.args[0:2] == ["pr","list"] and (.args | index("open")) != null'
check "train: 見つけた PR の Issue を in-review にする" \
  log_has agentctl '.args == ["state","121","in-review","--repo","K-Kizuku/raft-game","--pr","21"]'
check "train: 見つけた PR の Issue を in-review にする（2 つ目）" log_has agentctl '.args[0:3] == ["state","122","in-review"]'
check "train: Merge Gate を --repo --pr --json で判定する" log_has agentctl '.args == ["gate","--repo","K-Kizuku/raft-game","--pr","22","--json"]'
check "train: pass の残りの PR には何もしない" test "$(update_calls 22):$(gh_calls "pr edit" 22)" = "0:0" -a ! -s "$FAKE_LOG/review.log"
# 2 周目: マージした PR は列から外し、Issue を in-review にするのは一度だけ
train_reset
mkprs "21:agent/issue-121" "22:agent/issue-122"
mkgate 21 pass pass pass pass pass pass
mkgate 22 pass pass pass pass pass pass
train 2
check "train: 2 周で 1 つずつマージする" test "$(tr '\n' ' ' <"$FAKE_LOG/merge.log")" = "21 22 "
check "train: in-review にするのは一度だけ" \
  test "$(jq -s '[.[] | select(.args[0:3] == ["state","121","in-review"])] | length' "$FAKE_LOG/agentctl.log")" = 1
check "train: マージの前に Issue を ready-to-merge にする" \
  log_has agentctl '.args == ["state","121","ready-to-merge","--repo","K-Kizuku/raft-game"]'
# 起動し直しても、in-review にした印（.agents/run/train/inreview-<PR>）があればもう一度はしない
reset_logs
: >"$FAKE_LOG/merge.log"
mkprs "24:agent/issue-124"
mkgate 24 pending pass pending pending pass fail
train 1
train 1
check "train: 起動し直しても in-review にするのは一度だけ" \
  test "$(jq -s '[.[] | select(.args[0:3] == ["state","124","in-review"])] | length' "$FAKE_LOG/agentctl.log")" = 1
# in-review への遷移が（一時的に）失敗したら印を作らず、次の周で試す。成功したら印を作る
inreview_calls() { jq -s --arg n "$1" '[.[] | select(.args[0:3] == ["state",$n,"in-review"])] | length' "$FAKE_LOG/agentctl.log"; }
train_reset
mkprs "26:agent/issue-126"
mkgate 26 pending pass pending pending pass fail
echo 3 >"$TRAIN_DIR/state-rc-126-in-review"
printf '%s\n' "status:in-progress" "area:agents" >"$TRAIN_DIR/labels-126"
train 2
check "train: in-review への遷移が失敗し、まだ in-progress なら印を作らない" test ! -e "$trepo/.agents/run/train/inreview-26"
check "train: in-review への遷移が失敗したら次の周で試す" test "$(inreview_calls 126)" = 2
check "train: in-review にできないことを出す" grep -q "Issue #126: in-review にできない（次の周で試す）" "$tmp/err"
rm -f "$TRAIN_DIR/state-rc-126-in-review"
train 2
check "train: in-review への遷移に成功したら印を作る" test -e "$trepo/.agents/run/train/inreview-26"
check "train: 遷移に成功したあとは in-review にしない" test "$(inreview_calls 126)" = 3
# 状態を読めないときも印を作らない
train_reset
mkprs "27:agent/issue-127"
mkgate 27 pending pass pending pending pass fail
echo 4 >"$TRAIN_DIR/state-rc-127-in-review"
echo 1 >"$TRAIN_DIR/labels-rc-127"
train 1
check "train: in-review にできず状態も読めなければ印を作らない" test ! -e "$trepo/.agents/run/train/inreview-27"
# すでに in-review 以降の状態なら（遷移は拒否されるが）印を作り、もう試さない
for st in in-review changes-requested ci-pending ready-to-merge merged; do
  train_reset
  mkprs "28:agent/issue-128"
  mkgate 28 pending pass pending pending pass fail
  echo 4 >"$TRAIN_DIR/state-rc-128-in-review"
  printf '%s\n' "area:agents" "status:$st" >"$TRAIN_DIR/labels-128"
  train 2
  check "train: すでに ${st} なら印を作る" test -e "$trepo/.agents/run/train/inreview-28"
  check "train: すでに ${st} なら in-review を一度だけ試す" test "$(inreview_calls 128)" = 1
done
# マージしたら、メインの作業ツリーで git pull --ff-only を実行する（失敗したら pull しない）
train_reset
git -C "$repo" switch --quiet main
git -C "$repo" commit --quiet --allow-empty -m "設定を変えたつもり"
git -C "$repo" push --quiet origin main
before="$(git -C "$trepo" rev-parse HEAD)"
mkprs "25:agent/issue-125"
mkgate 25 pass pass pass pass pass pass
echo 1 >"$TRAIN_DIR/merge-rc"
train 1
check "train: merge.sh が失敗したら git pull しない" test "$(git -C "$trepo" rev-parse HEAD)" = "$before"
rm -f "$TRAIN_DIR/merge-rc"
train 1
check "train: マージしたらメインの作業ツリーで git pull --ff-only を実行する" \
  test "$(git -C "$trepo" rev-parse HEAD)" = "$(git -C "$repo" rev-parse main)"
check "train: 最新にしたことを出す" grep -q "メインの作業ツリーを最新にした" "$tmp/err"
# skip のファイル
train_reset
mkprs "21:agent/issue-121" "22:agent/issue-122"
mkgate 21 pass pass pass pass pass pass
mkgate 22 pass pass pass pass pass pass
mkdir -p "$trepo/.agents/run/train"
printf '21\n' >"$trepo/.agents/run/train/skip"
train 1
check "train: skip に書いた PR は飛ばす" test "$(cat "$FAKE_LOG/merge.log"):$(gate_calls 21)" = "22:0"
# 開いた PR がない
train_reset
mkprs
train 1
check "train: 開いた PR がなければ何もしない" test "$RC" = 0 -a ! -s "$FAKE_LOG/merge.log" -a "$(jq -s '[.[] | select(.args[0] == "gate")] | length' "$FAKE_LOG/agentctl.log")" = 0
check "train: 開いた PR がないことを出す" grep -q "開いた PR がない" "$tmp/err"

# ---- C10 が fail → update-branch ----
train_reset
mkprs "31:agent/issue-131"
mkgate 31 fail pass pending pending pass fail
train 1
check "train: C10 が fail なら main の取り込み（update-branch）を依頼する" test "$(update_calls 31)" = 1
check "train: update-branch の周はレビューを依頼しない" test ! -s "$FAKE_LOG/review.log"
# update-branch が失敗（コンフリクト）→ 止まった。目印が変わらなければ繰り返さない
train_reset
mkprs "32:agent/issue-132"
mkgate 32 fail pass pending pending pass fail
echo 1 >"$TRAIN_DIR/update-rc-32"
train 2
check "train: update-branch が失敗したら止まった（理由を出す）" grep -q "PR #32: 止まった.*コンフリクト" "$tmp/err"
check "train: 止まった PR は、目印が変わるまで update-branch を繰り返さない" test "$(update_calls 32)" = 1
check "train: 止まっても終わらない" test "$RC" = 0
# 止まった PR の HEAD が変わったら（修正）、また進める
rm -f "$TRAIN_DIR/update-rc-32"
echo h32b >"$TRAIN_DIR/head-32"
train 1
check "train: 止まった PR の HEAD が変わったら判定し直して進める" test "$(update_calls 32)" = 2 -a "$(gate_calls 32)" = 2
# update-branch の一時的な失敗（通信・HTTP 5xx・API の上限）は止まった扱いにせず、次の周で試す
for msg in "gh: Bad Gateway (HTTP 502)" "gh: API rate limit exceeded for user ID 1. (HTTP 403)" \
  "error connecting to api.github.com" "gh: Validation Failed (HTTP 422)"; do
  train_reset
  mkprs "36:agent/issue-136"
  mkgate 36 fail pass pending pending pass fail
  echo 1 >"$TRAIN_DIR/update-rc-36"
  printf '%s\n' "$msg" >"$TRAIN_DIR/update-msg-36"
  train 2
  check "train: update-branch の一時的な失敗（${msg}）は止まった扱いにしない" \
    test "$(grep -c "PR #36: 止まった" "$tmp/err" || true)" = 0 -a ! -e "$trepo/.agents/run/train/stopped-36"
  check "train: update-branch の一時的な失敗（${msg}）は次の周で試す" test "$(update_calls 36)" = 2
  check "train: update-branch の一時的な失敗（${msg}）を出す" grep -q "PR #36: update-branch に失敗した（次の周で試す）" "$tmp/err"
done
# 一時的な失敗のあと、成功すれば取り込みを依頼したことになる
rm -f "$TRAIN_DIR/update-rc-36"
train 1
check "train: update-branch の一時的な失敗のあとに成功すれば依頼する" grep -q "PR #36: main の取り込みを依頼した" "$tmp/err"

# ---- C9・C6・C7 → 止まった ----
for c in "33:C9:fail pass pending pending fail pass" "34:C6:fail pass fail pass pass pass" "35:C7:fail pass pass fail pass pass"; do
  n="${c%%:*}"
  rest="${c#*:}"
  id="${rest%%:*}"
  train_reset
  mkprs "$n:agent/issue-1$n"
  # shellcheck disable=SC2086 # 空白で区切った C2 C6 C7 C9 C10 を渡す
  mkgate "$n" ${rest#*:}
  train 1
  check "train: ${id} が fail なら止まった" grep -q "PR #$n: 止まった.*${id}" "$tmp/err"
  check "train: ${id} が fail ならレビューを依頼しない・update-branch しない" \
    test ! -s "$FAKE_LOG/review.log" -a "$(update_calls "$n")" = 0
done

# ---- C6 が pending → review.sh を 1 回だけ（PID のファイルで判定）・判定の使い回し ----
train_reset
mkprs "41:agent/issue-141"
mkgate 41 pending fail pending pending pass pass
train 2 FAKE_REVIEW_SLEEP=2
wait_reviews
check "train: C6 が pending なら review.sh を 1 回だけ呼ぶ" test "$(cat "$FAKE_LOG/review.log")" = "41"
check "train: review.sh の PID のファイルを残す" test -s "$trepo/.agents/run/train/review-41.pid"
check "train: 目印が同じなら前の判定を使う（gate は 1 回）" test "$(gate_calls 41)" = 1
check "train: C6 が pending の PR はマージしない" test ! -s "$FAKE_LOG/merge.log"
# review.sh が終わっていれば起動し直す
train 1
wait_reviews
check "train: review.sh が終わっていれば起動し直す" test "$(count_lines "$FAKE_LOG/review.log")" = 2
# C10 が fail なら review.sh より update-branch
train_reset
mkprs "42:agent/issue-142"
mkgate 42 pending pass pending pending pass fail
train 1
check "train: C6 が pending でも C10 が fail ならレビューを依頼しない" test ! -s "$FAKE_LOG/review.log"
# main の HEAD が変わったら判定し直す
train_reset
mkprs "43:agent/issue-143"
mkgate 43 pending pass pass pass pass pass
train 1
git -C "$repo" switch --quiet main
git -C "$repo" commit --quiet --allow-empty -m "main を進める"
git -C "$repo" push --quiet origin main
train 1
check "train: main の HEAD が変わったら判定し直す" test "$(gate_calls 43)" = 2
# 一時的な pending（C9 の mergeable の未計算）は、目印が同じでも 2 周ごとに判定し直す
train_reset
mkprs "44:agent/issue-144"
mkgate 44 pending pass pass pass pending pass
train 2
check "train: C9 が pending の判定は 2 周まで使う" test "$(gate_calls 44)" = 1
train 1
check "train: C9 が pending の判定を 2 周使ったら、目印が同じでも判定し直す" test "$(gate_calls 44)" = 2
check "train: 判定し直す理由を出す" grep -q "PR #44: 一時的な pending（C9）の判定を 2 周使ったので、判定し直す" "$tmp/err"
# 判定し直して mergeable が計算済み（pass）になれば、その結果でマージする
mkgate 44 pass pass pass pass pass pass
train 2
check "train: 判定し直した結果が pass ならマージする" test "$(cat "$FAKE_LOG/merge.log")" = "44" -a "$(gate_calls 44)" = 3
# C9 が pending でない pending（C6 など、目印に表れる）は目印が変わるまで使い続ける
train_reset
mkprs "45:agent/issue-145"
mkgate 45 pending pass pass pending pass pass
train 3
check "train: C9 が pending でなければ、目印が同じ間は前の判定を使い続ける" test "$(gate_calls 45)" = 1

# ---- C2 だけが fail で ci-ok が pass → CI の AC だけを [x] にする（不具合 1: ほかのチェックの失敗で gh pr checks が 1）----
train_reset
mkprs "51:agent/issue-151"
mkgate 51 fail fail pass pass pass pass
printf '%s\n' "Closes #151" "" "## Acceptance Criteria" "- [x] AC-1: 一つ目" \
  "- [ ] AC-2: CI（\`ci-ok\`）が通る（CI の結果待ち）" "- [ ] AC-3: なにか" "  - 補足: CI（\`ci-ok\`）が通る" >"$TRAIN_DIR/body-51.md"
printf '%s\n' '[{"name":"ci-ok","bucket":"pass"},{"name":"merge-gate","bucket":"fail"}]' >"$TRAIN_DIR/checks-51.json"
echo 1 >"$TRAIN_DIR/checks-rc-51"
train 1
check "train: C2 が fail で ci-ok が pass なら gh pr edit --body-file を呼ぶ（gh pr checks が 1 でも）" test -f "$FAKE_LOG/edit-51.md"
check "train: CI の AC の行だけを [x] にし、末尾の（…結果待ち）を外す" \
  test "$(cat "$FAKE_LOG/edit-51.md" 2>/dev/null)" = "$(printf '%s\n' "Closes #151" "" "## Acceptance Criteria" "- [x] AC-1: 一つ目" \
    "- [x] AC-2: CI（\`ci-ok\`）が通る" "- [ ] AC-3: なにか" "  - 補足: CI（\`ci-ok\`）が通る")"
check "train: C6 が pass ならレビューを依頼しない" test ! -s "$FAKE_LOG/review.log"
# ci-ok が pass でなければ本文を変えない
train_reset
mkprs "52:agent/issue-152"
mkgate 52 fail fail pass pass pass pass
printf '%s\n' "- [ ] AC-1: CI（\`ci-ok\`）が通る" >"$TRAIN_DIR/body-52.md"
printf '%s\n' '[{"name":"ci-ok","bucket":"pending"}]' >"$TRAIN_DIR/checks-52.json"
echo 8 >"$TRAIN_DIR/checks-rc-52"
train 1
check "train: ci-ok が pass でなければ本文を変えない" test "$(gh_calls "pr edit" 52)" = 0
# CI の AC の行がない（すでにチェック済み）なら gh pr edit を呼ばない
train_reset
mkprs "53:agent/issue-153"
mkgate 53 fail fail pass pass pass pass
printf '%s\n' "- [x] AC-1: CI（\`ci-ok\`）が通る" "- [ ] AC-2: ほか" >"$TRAIN_DIR/body-53.md"
train 1
check "train: 変える行がなければ gh pr edit を呼ばない" test "$(gh_calls "pr edit" 53)" = 0

# ---- 小さな PR（#243）: Merge Gate が C5〜C8 を「レビューを省いた」として pass にする → レビューを依頼しない ----
# mksmallgate <PR> <verdict> <C2>: 小さな PR の gate の結果（C5〜C8 の detail に省いた理由が入る）を置く
mksmallgate() {
  jq -n --argjson pr "$1" --arg v "$2" --arg c2 "$3" \
    '"小さな PR なのでレビューを省いた（1 ファイルがすべて review.skip_paths に一致・保護されたパスの変更なし・追加と削除が 50 行（上限 300 行））" as $d
    | {verdict:$v, repo:"K-Kizuku/raft-game", pr:$pr, head_sha:"h\($pr)", closed:false,
    results:[{id:"C1",status:"pass"},{id:"C2",status:$c2},{id:"C4",status:"pass"},
      {id:"C5",status:"pass",detail:$d},{id:"C6",status:"pass",detail:$d},{id:"C7",status:"pass",detail:$d},
      {id:"C8",status:"pass",detail:$d},{id:"C9",status:"pass"},{id:"C10",status:"pass"},{id:"C11",status:"pass"},
      {id:"C12",status:"pass"}]}' >"$TRAIN_DIR/gate-$1.json"
}
train_reset
mkprs "54:agent/issue-154"
mksmallgate 54 fail fail
printf '%s\n' "- [x] AC-1: 一つ目" "- [ ] AC-2: CI（\`ci-ok\`）が通る" >"$TRAIN_DIR/body-54.md"
train 2
wait_reviews
check "train: 小さな PR（C5〜C8 を省いた）にはレビューを依頼しない" test ! -s "$FAKE_LOG/review.log" -a ! -e "$trepo/.agents/run/train/review-54.pid"
check "train: 小さな PR でも CI の AC にはチェックを付ける" grep -q '^- \[x\] AC-2: CI' "$FAKE_LOG/edit-54.md"
check "train: 小さな PR は止まった扱いにしない" test ! -e "$trepo/.agents/run/train/stopped-54"
mksmallgate 54 pass pass
echo 1 >"$TRAIN_DIR/comments-54"
train 1
check "train: 小さな PR は Merge Gate が pass ならレビューなしでマージする" test "$(cat "$FAKE_LOG/merge.log")" = "54" -a ! -s "$FAKE_LOG/review.log"

# ---- 不具合 2: gate が空の結果を返したら、その周はその PR に何もしない ----
for rc in 3 0 2; do
  train_reset
  mkprs "61:agent/issue-161"
  : >"$TRAIN_DIR/gate-61.json"
  echo "$rc" >"$TRAIN_DIR/gate-rc-61"
  train 1
  wait_reviews
  check "train（不具合 2）: gate が空（終了コード ${rc}）ならレビューを依頼しない" test ! -s "$FAKE_LOG/review.log"
  check "train（不具合 2）: gate が空（終了コード ${rc}）なら update-branch・edit・マージをしない" \
    test "$(update_calls 61):$(gh_calls "pr edit" 61)" = "0:0" -a ! -s "$FAKE_LOG/merge.log"
  check "train（不具合 2）: 判定できないことを出す" grep -q "PR #61: Merge Gate を判定できない" "$tmp/err"
done
train_reset
mkprs "62:agent/issue-162"
echo '{"verdict":"pending","results":[]}' >"$TRAIN_DIR/gate-62.json"
train 1
check "train（不具合 2）: results が空の判定も使わない" test ! -s "$FAKE_LOG/review.log"
# 判定できなかった結果は使い回さない（次の周で判定し直す）
train_reset
mkprs "63:agent/issue-163"
: >"$TRAIN_DIR/gate-63.json"
echo 3 >"$TRAIN_DIR/gate-rc-63"
train 1
rm -f "$TRAIN_DIR/gate-rc-63"
mkgate 63 pass pass pass pass pass pass
train 1
check "train（不具合 2）: 判定できなかった周の後は判定し直す" test "$(gate_calls 63):$(cat "$FAKE_LOG/merge.log")" = "2:63"

# ---- 不具合 3: コメントの数が変わったら判定し直す ----
train_reset
mkprs "71:agent/issue-171"
mkgate 71 pending pass pending pending pass pass
train 1
wait_reviews
mkgate 71 pass pass pass pass pass pass
train 1
wait_reviews
check "train（不具合 3）: 目印が同じなら gate を呼ばない" test "$(gate_calls 71)" = 1 -a ! -s "$FAKE_LOG/merge.log"
echo 1 >"$TRAIN_DIR/comments-71"
train 1
check "train（不具合 3）: approve の記録のコメントが付いたら（コメントの数が変わったら）判定し直す" test "$(gate_calls 71)" = 2
check "train（不具合 3）: 判定し直した結果でマージする" test "$(cat "$FAKE_LOG/merge.log")" = "71"

# ---- Issue の更新時刻が変わったら判定し直す（Issue の側だけで AC・変更予定ファイルを直したとき）----
train_reset
mkprs "72:agent/issue-172"
mkgate 72 fail fail pass pass pass pass
train 2
check "train: 目印（Issue の更新時刻を含む）が同じなら gate を呼ばない" test "$(gate_calls 72)" = 1
check "train: Issue の更新時刻を gh issue view --json updatedAt で読む" \
  log_has gh '.args == ["issue","view","172","--repo","K-Kizuku/raft-game","--json","updatedAt","-q",".updatedAt"]'
mkgate 72 pass pass pass pass pass pass
echo "2026-11-07T01:00:00Z" >"$TRAIN_DIR/issue-updated-172"
train 1
check "train: Issue の更新時刻が変わったら、PR と main が同じでも判定し直す" test "$(gate_calls 72)" = 2
check "train: Issue を直して判定し直した結果でマージする" test "$(cat "$FAKE_LOG/merge.log")" = "72"
# Issue を読めなければ、その周はその PR に何もしない
train_reset
mkprs "73:agent/issue-173"
mkgate 73 pass pass pass pass pass pass
echo 1 >"$TRAIN_DIR/issue-rc-173"
train 1
check "train: Issue を読めなければ判定しない・マージしない" test "$(gate_calls 73)" = 0 -a ! -s "$FAKE_LOG/merge.log"
check "train: Issue を読めないことを出す" grep -q "PR #73: Issue #173 を読めない（次の周で試す）" "$tmp/err"

# ---- C6 が pass で C12 が fail（保護されたパスの確認の記録が足りない）→ HEAD ごとに 1 回だけ review.sh ----
train_reset
mkprs "74:agent/issue-174"
mkgate 74 fail pass pass pass pass pass false fail
train 1
wait_reviews
check "train: C6 が pass で C12 が fail なら review.sh を呼ぶ" test "$(cat "$FAKE_LOG/review.log")" = "74"
check "train: C12 でレビューを依頼したことを出す" grep -q "PR #74: 保護されたパスの確認の記録が足りない（C12）" "$tmp/err"
# 同じ HEAD でレビューのあとも C12 が fail（記録が付いても通らない）なら、もう依頼せずに止まった
echo 1 >"$TRAIN_DIR/comments-74"
train 2
wait_reviews
check "train: 同じ HEAD で C12 のレビューを 2 回は依頼しない" test "$(count_lines "$FAKE_LOG/review.log")" = 1
check "train: レビューのあとも C12 が fail なら止まった" grep -q "PR #74: 止まった.*C12" "$tmp/err"
check "train: C12 で止まった PR は、目印が同じ間は止まったまま" grep -q "PR #74: 止まったまま" "$tmp/err"
# HEAD が変わったら（修正）、また 1 回だけ依頼する
echo h74b >"$TRAIN_DIR/head-74"
mkgate 74 fail pass pass pass pass pass false fail h74b
train 1
wait_reviews
check "train: HEAD が変わったら C12 のレビューをもう一度依頼する" test "$(count_lines "$FAKE_LOG/review.log")" = 2
# review.sh が動いている間は、止まった扱いにせず 2 つ目も起動しない
train_reset
mkprs "75:agent/issue-175"
mkgate 75 fail pass pass pass pass pass false fail
train 2 FAKE_REVIEW_SLEEP=2
check "train: C12 のレビューが動いている間は止まった扱いにしない" test "$(grep -c "PR #75: 止まった" "$tmp/err" || true)" = 0
wait_reviews
check "train: C12 のレビューが動いている間は 2 つ目を起動しない" test "$(count_lines "$FAKE_LOG/review.log")" = 1
# C12 が fail でも C10 が fail なら update-branch を先にする
train_reset
mkprs "76:agent/issue-176"
mkgate 76 fail pass pass pass pass fail false fail
train 1
check "train: C12 が fail でも C10 が fail ならレビューを依頼しない" test ! -s "$FAKE_LOG/review.log" -a "$(update_calls 76)" = 1

# ---- 凍結（#240）: 保護されたパスの PR のレビュー中は、ほかの PR をマージしない ----
train_reset
mkprs "101:agent/issue-301"
mkgate 101 pending pass pending pending pass pass false pending
train 1 FAKE_REVIEW_SLEEP=30
check "train（凍結）: 保護されたパスの PR（C12 が pending）の review.sh を起動する" test "$(cat "$FAKE_LOG/review.log")" = "101"
check "train（凍結）: 保護されたパスの PR のレビューを起動したら凍結の印を作る" test -s "$trepo/.agents/run/train/freeze-101"
check "train（凍結）: 凍結することを出す" grep -q "PR #101: 保護されたパスの PR のレビュー中は、ほかの PR をマージしない（凍結" "$tmp/err"
mkprs "101:agent/issue-301" "102:agent/issue-302"
mkgate 102 pass pass pass pass pass pass
train 2 FAKE_REVIEW_SLEEP=30
check "train（凍結）: 保護されたパスの PR のレビュー中は、ほかの pass の PR を merge.sh に渡さない" test ! -s "$FAKE_LOG/merge.log"
check "train（凍結）: マージしない理由を出す" \
  grep -q "PR #102: Merge Gate が pass だが、保護されたパスの PR #101 のレビュー中なので、マージしない（凍結）" "$tmp/err"
check "train（凍結）: 凍結の間に ready-to-merge にしない" \
  bash -c '! jq -e "select(.args[0:3] == [\"state\",\"302\",\"ready-to-merge\"])" "$1" >/dev/null' _ "$FAKE_LOG/agentctl.log"
check "train（凍結）: 凍結の間も review.sh は PR ごとに 1 つまで" test "$(count_lines "$FAKE_LOG/review.log")" = 1
# review.sh の待ちの上限（AGENT_CODEX_GITHUB_TIMEOUT＋AGENT_CODEX_TIMEOUT）を超えたら凍結を解く
train 1 AGENT_CODEX_GITHUB_TIMEOUT=0 AGENT_CODEX_TIMEOUT=0
check "train（凍結）: review.sh の待ちの上限を超えたら凍結を解いてマージする" test "$(cat "$FAKE_LOG/merge.log")" = "102"
check "train（凍結）: 上限で凍結を解いたことを出す" grep -q "PR #101: review.sh の待ちの上限（0 秒）を超えたので、凍結を解く" "$tmp/err"
check "train（凍結）: 上限で解いたら凍結の印を消す" test ! -e "$trepo/.agents/run/train/freeze-101"
kill_reviews
# review.sh が終われば凍結を解く
train_reset
mkprs "103:agent/issue-303"
mkgate 103 pending pass pending pending pass pass false fail
train 1
wait_reviews
mkprs "103:agent/issue-303" "104:agent/issue-304"
mkgate 104 pass pass pass pass pass pass
train 1
wait_reviews
check "train（凍結）: 保護されたパスの PR の review.sh が終われば凍結を解いてマージする" test "$(cat "$FAKE_LOG/merge.log")" = "104"
check "train（凍結）: レビューが終わって凍結を解いたことを出す" \
  grep -q "PR #103: 保護されたパスの PR のレビューが終わったので、凍結を解く" "$tmp/err"
# C12 が pass でも、detail が保護されたパスの変更を示せば（前の記録の確認）凍結する。変更なしなら凍結しない
train_reset
mkprs "105:agent/issue-305" "106:agent/issue-306"
mkgate 105 pending pass pending pending pass pass
setdetail 105 C12 "保護されたパスの変更（scripts/agents/x.sh）は確認済み"
mkgate 106 pending pass pending pending pass pass
setdetail 106 C12 "保護されたパスの変更なし"
train 1 FAKE_REVIEW_SLEEP=30
check "train（凍結）: C12 の detail が保護されたパスの変更を示せば凍結する" test -e "$trepo/.agents/run/train/freeze-105"
check "train（凍結）: 保護されたパスの変更がない PR のレビューでは凍結しない" test ! -e "$trepo/.agents/run/train/freeze-106"
kill_reviews
# 凍結した PR 自身は、pass ならマージする
train_reset
mkprs "107:agent/issue-307" "108:agent/issue-308"
mkgate 107 pending pass pending pending pass pass false pending
train 1 FAKE_REVIEW_SLEEP=30
mkgate 107 pass pass pass pass pass pass
mkgate 108 pass pass pass pass pass pass
echo 1 >"$TRAIN_DIR/comments-107"
train 1 FAKE_REVIEW_SLEEP=30
check "train（凍結）: 凍結した PR 自身は pass ならマージする" test "$(cat "$FAKE_LOG/merge.log")" = "107"
kill_reviews

# ---- レビューは review.max_rounds 回まで（#240）----
train_reset
mkprs "111:agent/issue-311"
mkgate 111 pending pass pending pending pass pass
setround 111 2
train 1
wait_reviews
check "train（2 往復まで）: round 2 で C6 が pass でなければ review.sh を呼ばない" test ! -s "$FAKE_LOG/review.log"
check "train（2 往復まで）: ci-ok が pass なら強制マージ待ちの記録を作る" \
  jq -e '.pr == 111 and .issue == 311 and .round == 2 and .max_rounds == 2 and .head_sha == "h111" and .ci_ok == "pass"
    and (.failed | index("C6=pending")) != null and (.recorded_at | length) > 0' "$trepo/.agents/run/train/force-merge-111.json"
check "train（2 往復まで）: 強制マージ待ちをログに出す" grep -q "PR #111: 強制マージ待ち（Issue #311）" "$tmp/err"
check "train（2 往復まで）: 止まった扱いにしない" test ! -e "$trepo/.agents/run/train/stopped-111"
train 1
wait_reviews
check "train（2 往復まで）: 次の周もレビューを依頼しない" test ! -s "$FAKE_LOG/review.log"
check "train（2 往復まで）: 記録が同じ HEAD なら、強制マージ待ちのままと出す" grep -q "PR #111: 強制マージ待ちのまま" "$tmp/err"
# round 3 で C6 が fail（request_changes）でも、止まった扱いにせず強制マージ待ちにする
train_reset
mkprs "112:agent/issue-312"
mkgate 112 fail pass fail pass pass pass
setround 112 3
train 1
check "train（2 往復まで）: round 3 で C6 が fail でも強制マージ待ちにする" \
  test -s "$trepo/.agents/run/train/force-merge-112.json" -a ! -e "$trepo/.agents/run/train/stopped-112"
# round 2 で C6 が pass、C12 が fail でも review.sh を呼ばない
train_reset
mkprs "113:agent/issue-313"
mkgate 113 fail pass pass pass pass pass false fail
setround 113 2
train 1
wait_reviews
check "train（2 往復まで）: round 2 で C12 が fail でも review.sh を呼ばない" \
  test ! -s "$FAKE_LOG/review.log" -a -s "$trepo/.agents/run/train/force-merge-113.json"
# ci-ok が pass でなければ記録を作らない（レビューも依頼しない）
train_reset
mkprs "114:agent/issue-314"
mkgate 114 pending pass pending pending pass pass
setround 114 2
printf '%s\n' '[{"name":"ci-ok","bucket":"pending"}]' >"$TRAIN_DIR/checks-114.json"
train 1
wait_reviews
check "train（2 往復まで）: ci-ok が pass でなければ強制マージ待ちの記録を作らない" \
  test ! -e "$trepo/.agents/run/train/force-merge-114.json" -a ! -s "$FAKE_LOG/review.log"
check "train（2 往復まで）: ci-ok を待つことを出す" grep -q "PR #114: レビューを 2 回受けた（上限 2）ので、もうレビューを依頼しない。ci-ok が pass になるのを待つ（pending）" "$tmp/err"
# round 1 なら（上限より少ない）これまでどおり review.sh を呼ぶ
train_reset
mkprs "115:agent/issue-315"
mkgate 115 pending pass pending pending pass pass
setround 115 1
train 1
wait_reviews
check "train（2 往復まで）: round 1 なら review.sh を呼ぶ" test "$(cat "$FAKE_LOG/review.log")" = "115" -a ! -e "$trepo/.agents/run/train/force-merge-115.json"
# 上限は .agents/config.json の review.max_rounds から読む
train_reset
mkprs "116:agent/issue-316"
mkgate 116 pending pass pending pending pass pass
setround 116 2
jq -n '{review:{max_rounds:3}}' >"$tmp/config-rounds.json"
train 1 AGENT_CONFIG="$tmp/config-rounds.json"
wait_reviews
check "train（2 往復まで）: review.max_rounds が 3 なら round 2 でも review.sh を呼ぶ" test "$(cat "$FAKE_LOG/review.log")" = "116"
train_reset
mkprs "117:agent/issue-317"
mkgate 117 pending pass pending pending pass pass
setround 117 1
jq -n '{review:{max_rounds:1}}' >"$tmp/config-rounds.json"
train 1 AGENT_CONFIG="$tmp/config-rounds.json"
wait_reviews
check "train（2 往復まで）: review.max_rounds が 1 なら round 1 で強制マージ待ちにする" \
  test ! -s "$FAKE_LOG/review.log" -a -s "$trepo/.agents/run/train/force-merge-117.json"
# 強制マージ待ちの PR がマージされたら（オーナーの強制マージのあと、gate の closed）記録を消す
mkgate 117 fail pass pending pending pass pass true
echo 1 >"$TRAIN_DIR/comments-117"
train 1 AGENT_CONFIG="$tmp/config-rounds.json"
check "train（2 往復まで）: 列から外したら強制マージ待ちの記録を消す" test ! -e "$trepo/.agents/run/train/force-merge-117.json"

# ---- merge-train を止めても、起動した review.sh は止まらない（#240）----
train_reset
mkprs "118:agent/issue-318"
mkgate 118 pending pass pending pending pass pass false pending
bg_group "$trepo" env PATH="$fake/bin-train:$PATH" AGENT_ROLE=orchestrator AGENT_TRAIN_INTERVAL=30 AGENT_TRAIN_CYCLES=0 \
  FAKE_REVIEW_SLEEP=30 bash "$TS/merge-train.sh"
bgp="$(cat "$tmp/bg.pid")"
check "train（親を止める）: review.sh を起動する" wait_until test -s "$FAKE_LOG/review.log"
rp="$(cat "$trepo/.agents/run/train/review-118.pid" 2>/dev/null || echo 0)"
kill -TERM -- "-$bgp" 2>/dev/null || true
check "train（親を止める）: プロセスグループに SIGTERM を送ると merge-train は止まる" wait_until pid_dead "$bgp"
sleep 0.3
check "train（親を止める）: merge-train のプロセスグループごと止めても review.sh は止まらない" kill -0 "$rp"
kill "$rp" 2>/dev/null || true

# ---- 不具合 4: merge.sh がマージの後の処理で 0 以外を返しても、マージ済みなら列から外す ----
train_reset
mkgate 81 pass pass pass pass pass pass
echo 1 >"$TRAIN_DIR/merge-rc"
: >"$TRAIN_DIR/merge-sets-merged"
train 2 -- 81:181
check "train（不具合 4）: マージ済みの PR を列から外す（merge.sh は 1 回）" test "$(cat "$FAKE_LOG/merge.log")" = "81"
check "train（不具合 4）: 外した後は判定しない" test "$(gate_calls 81)" = 1
check "train（不具合 4）: 引数の PR:Issue を使い、gh pr list を呼ばない" \
  test -z "$(jq -c 'select(.args[0:2] == ["pr","list"])' "$FAKE_LOG/gh.log")"
check "train: 引数の Issue を in-review にする" log_has agentctl '.args[0:3] == ["state","181","in-review"]'
# マージされていなければ次の周で試す
train_reset
mkgate 82 pass pass pass pass pass pass
echo 1 >"$TRAIN_DIR/merge-rc"
train 2 -- 82:182
check "train: merge.sh が失敗し、PR が開いたままなら次の周で試す" test "$(count_lines "$FAKE_LOG/merge.log")" = 2
# gate が closed を返したら列から外す
train_reset
mkgate 83 fail pass pass pass pass pass true
train 2 -- 83:183
check "train: 閉じた PR（gate の closed）は列から外す" test "$(gate_calls 83)" = 1 -a ! -s "$FAKE_LOG/merge.log"

# ---- API の上限: 残りが 300 未満なら戻る時刻まで休む ----
train_reset
mkprs "91:agent/issue-191"
mkgate 91 pass pass pass pass pass pass
train 1 PATH="$fake/bin-sleep:$fake/bin-train:$PATH" FAKE_RATE_REMAINING=12 FAKE_RATE_RESET="$(($(date +%s) + 120))"
check "train: API の残りが 300 未満なら、その周は判定しない" test ! -s "$FAKE_LOG/merge.log" -a "$(gate_calls 91)" = 0
check "train: 戻る時刻まで休む" \
  test "$(head -n 1 "$FAKE_LOG/sleep.log" 2>/dev/null || echo 0)" -ge 110 -a "$(head -n 1 "$FAKE_LOG/sleep.log" 2>/dev/null || echo 999)" -le 122
train_reset
mkprs "91:agent/issue-191"
mkgate 91 pass pass pass pass pass pass
train 1 FAKE_RATE_REMAINING=4000 FAKE_RATE_RESET="$(($(date +%s) + 120))"
check "train: API の残りが十分なら判定する" test "$(cat "$FAKE_LOG/merge.log")" = "91"
# gh が失敗しても止まらない
train_reset
mkprs "92:agent/issue-192"
mkgate 92 pass pass pass pass pass pass
printf 'not json\n' >"$TRAIN_DIR/prs.json"
train 2
check "train: 開いた PR を読めなくても止まらない" test "$RC" = 0 -a ! -s "$FAKE_LOG/merge.log"

# ---- autospawn.sh ----
autospawn() { # autospawn [<環境変数>…]
  (cd "$trepo" && run_rc env PATH="$fake/bin-train:$PATH" AGENT_ROLE=orchestrator AGENT_AUTOSPAWN_INTERVAL=0 \
    "$@" bash "$TS/autospawn.sh" && printf '%s\n' "$RC" >"$tmp/rc")
  RC="$(cat "$tmp/rc")"
}
train_reset
mkdir -p "$trepo/.agents/run"
jq -n '{ready:[{number:201},{number:202},{number:203}], deferred:[{number:204}], active:[], capacity:2}' >"$TRAIN_DIR/plan.json"
autospawn FAKE_PLAN_STOP_AFTER=1
check "autospawn: 成功する" test "$RC" = 0
check "autospawn: ready が 3 つで capacity が 2 なら 2 つだけ起動する" \
  test "$(tr '\n' ',' <"$FAKE_LOG/spawn.log")" = "201 --background,202 --background,"
check "autospawn: agentctl plan --repo --json を見る" log_has agentctl '.args == ["plan","--repo","K-Kizuku/raft-game","--json"]'
check "autospawn: 止めのファイルで終わり、そのファイルを消す" test ! -e "$trepo/.agents/run/autospawn.stop"
check "autospawn: deferred は起動しない" bash -c '! grep -q "^204" "$1"' _ "$FAKE_LOG/spawn.log"
# 拒否された Issue（終了コード 4）は飛ばし、枠を使わない
train_reset
mkdir -p "$trepo/.agents/run"
jq -n '{ready:[{number:201},{number:202},{number:203}], capacity:2}' >"$TRAIN_DIR/plan.json"
echo 4 >"$TRAIN_DIR/spawn-rc-201"
autospawn FAKE_PLAN_STOP_AFTER=1
check "autospawn: 拒否された Issue は飛ばして、次の Issue を起動する" \
  test "$(tr '\n' ',' <"$FAKE_LOG/spawn.log")" = "201 --background,202 --background,203 --background,"
check "autospawn: 拒否の理由を出す" grep -q "Issue #201 は起動できない（終了コード 4）" "$tmp/err"
# 始めるときに古い止めのファイルを消す。止めのファイルは次の周で効く（2 回目の plan で作る → 2 周で終わる）
train_reset
mkdir -p "$trepo/.agents/run"
: >"$trepo/.agents/run/autospawn.stop"
jq -n '{ready:[], conflicts:[{number:205, reasons:["#201 と docs/x.md が重なる"]}],
  deferred:[{number:204, reasons:["実装中の #202 と重なる"]}], capacity:2}' >"$TRAIN_DIR/plan.json"
autospawn FAKE_PLAN_STOP_AFTER=2
check "autospawn: 始めるときに古い止めのファイルを消し、止めのファイルの次の周で終わる" \
  test "$RC:$(jq -s '[.[] | select(.args[0] == "plan")] | length' "$FAKE_LOG/agentctl.log")" = "0:2"
check "autospawn: conflicts・deferred の Issue と理由をログに出す" \
  grep -q "#204: 実装中の #202 と重なる" "$tmp/err"
check "autospawn: conflicts の理由も出す" grep -q "#205: #201 と docs/x.md が重なる" "$tmp/err"
check "autospawn: 理由は前の周と変わったときだけ出す" test "$(grep -c "選ばなかった Issue" "$tmp/err")" = 1
# capacity が 0 なら起動しない
train_reset
mkdir -p "$trepo/.agents/run"
jq -n '{ready:[{number:201}], capacity:0}' >"$TRAIN_DIR/plan.json"
autospawn FAKE_PLAN_STOP_AFTER=1
check "autospawn: capacity が 0 なら起動しない" test "$RC" = 0 -a ! -s "$FAKE_LOG/spawn.log"
# 役割
train_reset
mkdir -p "$trepo/.agents/run"
jq -n '{ready:[{number:201}], capacity:1}' >"$TRAIN_DIR/plan.json"
autospawn AGENT_ROLE=worker FAKE_PLAN_STOP_AFTER=1
check "autospawn: Worker の役割では拒否する" test "$RC" = 1 -a ! -s "$FAKE_LOG/spawn.log"
check "autospawn --help" bash "$TS/autospawn.sh" --help

# ---- 状態の自動の直し（#240）: in-progress・Worker なし・PR あり → in-review ----
train_reset
mkdir -p "$trepo/.agents/run"
jq -n '{ready:[], active:[{number:207, status:"in-progress"}, {number:208, status:"in-progress"},
  {number:209, status:"in-progress"}, {number:210, status:"in-review"}, {number:211, status:"in-progress"},
  {number:212, status:"changes-requested"}], capacity:0}' >"$TRAIN_DIR/plan.json"
echo 1207 >"$TRAIN_DIR/headpr-agent-issue-207"
echo 1208 >"$TRAIN_DIR/headpr-agent-issue-208"
echo 1210 >"$TRAIN_DIR/headpr-agent-issue-210"
echo 1211 >"$TRAIN_DIR/headpr-agent-issue-211"
echo 1212 >"$TRAIN_DIR/headpr-agent-issue-212"
# 208 は Worker が動いている（pid はこのテストのシェル）。207 は Worker の記録の pid が終わっている
jq -n --argjson p "$$" '{issue:208, pid:$p}' >"$trepo/.agents/run/issue-208.json"
sleep 0 &
deadpid=$!
wait "$deadpid" 2>/dev/null || true
jq -n --argjson p "$deadpid" '{issue:207, pid:$p}' >"$trepo/.agents/run/issue-207.json"
echo 4 >"$TRAIN_DIR/state-rc-211-in-review"
autospawn FAKE_PLAN_STOP_AFTER=1
state_calls() { jq -s --arg n "$1" '[.[] | select(.args[0:3] == ["state",$n,"in-review"])] | length' "$FAKE_LOG/agentctl.log"; }
check "autospawn（状態の直し）: in-progress・Worker なし・PR あり → state in-review を呼ぶ" \
  log_has agentctl '.args == ["state","207","in-review","--repo","K-Kizuku/raft-game","--pr","1207"]'
check "autospawn（状態の直し）: 開いた PR を gh pr list --head で探す" \
  log_has gh '.args[0:2] == ["pr","list"] and (.args | index("--head")) != null and (.args | index("agent/issue-207")) != null'
check "autospawn（状態の直し）: 移したことを出す" grep -q "Issue #207: in-progress のままだが、Worker が動いておらず PR #1207 があるので、in-review に移した" "$tmp/err"
check "autospawn（状態の直し）: Worker が動いていれば移さない" test "$(state_calls 208)" = 0
check "autospawn（状態の直し）: PR がなければ移さない" test "$(state_calls 209)" = 0
check "autospawn（状態の直し）: in-progress でなければ移さない" test "$(state_calls 210):$(state_calls 212)" = "0:0"
check "autospawn（状態の直し）: 移せなければ出して続ける" \
  grep -q "Issue #211: in-progress のままで Worker が動いておらず PR #1211 があるが、in-review に移せない（次の周で試す）" "$tmp/err"
check "autospawn（状態の直し）: 移せなくても成功する" test "$RC" = 0

# ---- autospawn を止めても、起動した Worker は止まらない（#240）----
train_reset
mkdir -p "$trepo/.agents/run"
jq -n '{ready:[{number:213}], capacity:1}' >"$TRAIN_DIR/plan.json"
bg_group "$trepo" env PATH="$fake/bin-train:$PATH" AGENT_ROLE=orchestrator AGENT_AUTOSPAWN_INTERVAL=30 \
  FAKE_WORKER_SLEEP=30 bash "$TS/autospawn.sh"
bgp="$(cat "$tmp/bg.pid")"
check "autospawn（親を止める）: Worker を起動する" wait_until test -s "$TRAIN_DIR/worker-213.pid"
wp="$(cat "$TRAIN_DIR/worker-213.pid" 2>/dev/null || echo 0)"
kill -TERM -- "-$bgp" 2>/dev/null || true
check "autospawn（親を止める）: プロセスグループに SIGTERM を送ると autospawn は止まる" wait_until pid_dead "$bgp"
sleep 0.3
check "autospawn（親を止める）: autospawn のプロセスグループごと止めても Worker は止まらない" kill -0 "$wp"
kill "$wp" 2>/dev/null || true

# ---- 見張り（merge-train.sh・autospawn.sh）の直接の gh も App のトークン（#282 のレビュー F7）----
# キャッシュに有効な App のトークンを置き（App の API は呼ばない）、偽の gh が受け取った GH_TOKEN を見る
app_watch_env=(AGENT_APP_ID=5659452 "AGENT_APP_KEY=$app_key" AGENT_REPO=o/r)
# 上で export した GH_TOKEN があるとそのまま使うので、この節では外す（最後に戻す）
saved_gh_token="${GH_TOKEN:-}"
unset GH_TOKEN GITHUB_TOKEN
put_app_cache() {
  rm -rf "$app_cache_dir"
  (umask 077 && mkdir -p "$app_cache_dir" && printf '%s\n%s\n' "$(date +%s)" "$app_tok" >"$app_cache")
}
put_app_cache
train_reset
mkprs "93:agent/issue-193"
mkgate 93 pass pass pass pass pass pass
train 1 "${app_watch_env[@]}" FAKE_RATE_REMAINING=4000 FAKE_RATE_RESET="$(($(date +%s) + 120))"
check "train（App）: 判定してマージする" test "$RC:$(cat "$FAKE_LOG/merge.log")" = "0:93"
check "train（App）: gh api rate_limit を App のトークンで呼ぶ" grep -q "^$app_tok api rate_limit " "$FAKE_LOG/gh-env.log"
check "train（App）: gh pr list を App のトークンで呼ぶ" grep -q "^$app_tok pr list " "$FAKE_LOG/gh-env.log"
check "train（App）: 直接の gh をすべて App のトークンで呼ぶ" test -z "$(grep -v "^$app_tok " "$FAKE_LOG/gh-env.log" || true)"
check "train（App）: 標準出力・標準エラーにトークンと鍵の中身を出さない" no_secret "$tmp/out" "$tmp/err"
# 上限は App のトークンの gh api rate_limit で見る（App の残りが少なければ休む）
train_reset
mkprs "93:agent/issue-193"
mkgate 93 pass pass pass pass pass pass
train 1 "${app_watch_env[@]}" PATH="$fake/bin-sleep:$fake/bin-train:$PATH" FAKE_RATE_REMAINING=12 FAKE_RATE_RESET="$(($(date +%s) + 120))"
check "train（App）: 上限は App のトークンの gh api rate_limit で見る" \
  test ! -s "$FAKE_LOG/merge.log" -a -n "$(grep "^$app_tok api rate_limit " "$FAKE_LOG/gh-env.log" || true)"
# 鍵がなければ gh のログイン（オーナー）で続け、標準エラーに理由を出す
rm -rf "$app_cache_dir"
train_reset
mkprs "93:agent/issue-193"
mkgate 93 pass pass pass pass pass pass
train 1 AGENT_APP_ID=5659452 AGENT_APP_KEY="$app_dir/missing.pem" AGENT_REPO=o/r \
  FAKE_RATE_REMAINING=4000 FAKE_RATE_RESET="$(($(date +%s) + 120))"
check "train（鍵がない）: gh のログイン（オーナー）で続ける" \
  test "$RC:$(cat "$FAKE_LOG/merge.log")" = "0:93" -a -z "$(grep -v '^none ' "$FAKE_LOG/gh-env.log" || true)"
# （merge-train.sh は gh の標準エラーを 2>/dev/null で捨てるので、理由の 1 行は出ない。出すことは上の gh（lib）・ghx の節で確かめる）
check "train（鍵がない）: 鍵の中身を出さない" no_secret "$tmp/out" "$tmp/err"
# autospawn.sh の gh pr list（状態の直し）も App のトークン
put_app_cache
train_reset
mkdir -p "$trepo/.agents/run"
jq -n '{ready:[], active:[{number:207, status:"in-progress"}], capacity:0}' >"$TRAIN_DIR/plan.json"
echo 1207 >"$TRAIN_DIR/headpr-agent-issue-207"
autospawn "${app_watch_env[@]}" FAKE_PLAN_STOP_AFTER=1
check "autospawn（App）: gh pr list を App のトークンで呼ぶ" \
  test "$RC" = 0 -a -n "$(grep "^$app_tok pr list .*agent/issue-207" "$FAKE_LOG/gh-env.log" || true)"
check "autospawn（App）: 標準出力・標準エラーにトークンと鍵の中身を出さない" no_secret "$tmp/out" "$tmp/err"
rm -rf "$app_cache_dir"
export GH_TOKEN="$saved_gh_token"

echo "agents: $((total - failures))/$total ok"
if [ "$failures" -ne 0 ]; then
  echo "失敗: $failures" >&2
  exit 1
fi
