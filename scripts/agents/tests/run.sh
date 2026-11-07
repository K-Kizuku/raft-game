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
  AGENT_REPO AGENTCTL AGENT_FALLBACK_REASON CLAUDE_PROJECT_DIR DEV_SLOT
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
case "$1 $2" in
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
jq -cn --arg cwd "$PWD" --arg role "${AGENT_ROLE:-}" --arg settings "$settings" --argjson hooks "$hook_cmds" \
  --arg bash_rc "$bash_rc" --arg write_rc "$write_rc" \
  '{cwd:$cwd, role:$role, settings:$settings, hook_cmds:$hooks, bash_rc:$bash_rc, write_rc:$write_rc, args:$ARGS.positional}' \
  --args -- "$@" >>"$FAKE_LOG/claude.log"
case "${FAKE_CLAUDE_MODE:-good}" in
  good)
    echo '{"type":"result","is_error":false,"structured_output":{"verdict":"request_changes","summary":"直すところがある","protected_paths_ack":{"reviewed":true,"weakens_guards":false},"findings":[{"id":"F1","severity":"high","category":"correctness","file":"a.txt","line":1,"title":"t","detail":"d","status":"open","resolution":""}]}}'
    ;;
  fail) exit 1 ;;
esac
EOF
chmod +x "$fake/bin/"*
for c in agentctl gh claude; do
  ln -s "$fake/bin/$c" "$fake/bin-nocodex/$c"
done

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
cp "$src_root/.agents/config.json" "$repo/.agents/config.json"
mkdir -p "$repo/.claude"
cp -R "$src_root/.claude/agents" "$repo/.claude/agents"
cp "$src_root/.claude/settings.json" "$repo/.claude/settings.json"
cp -R "$src_root/.claude/hooks" "$repo/.claude/hooks"
rm -rf "$repo/.claude/hooks/tests"
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

# ================= review.sh =================
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
check "review: gh pr comment --body-file で載せる" log_has gh '.args[0:3] == ["pr","comment","12"] and (.args | index("--body-file")) != null'
check "review: 載せた本文に目印がある" grep -q "<!-- agent-review:v1 -->" "$FAKE_LOG/comment.md"

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

echo "agents: $((total - failures))/$total ok"
if [ "$failures" -ne 0 ]; then
  echo "失敗: $failures" >&2
  exit 1
fi
