#!/usr/bin/env bash
# .claude/hooks/guard-*.sh のテスト（Issue #5）。
# 一時的な Git リポジトリ（メインの作業ツリーと、リンクした Worktree）で、
# 役割ごとに拒否（終了コード 2）と許可（0）を確かめる。
# 実行: bash .claude/hooks/tests/run.sh
# （`[ … ] && pass || fail …` は pass が失敗しないので if の代わりに使う）
# shellcheck disable=SC2015
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
hooks="$(cd "$here/.." && pwd)"

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
unset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE AGENT_ROLE AGENT_ISSUE AGENT_ALLOW_PROTECTED CLAUDE_PROJECT_DIR

repo="$tmp/repo"
git init --quiet "$repo"
mkdir -p "$repo/.agents"
cat >"$repo/.agents/config.json" <<'EOF'
{ "protected_paths": [".github/workflows/**", ".claude/hooks/**", "scripts/agents/**", ".agents/config.json"] }
EOF
printf 'x\n' >"$repo/README.md"
git -C "$repo" add -A
git -C "$repo" commit --quiet -m init
wt="$repo/.claude/worktrees/issue-5"
git -C "$repo" worktree add --quiet -b agent/issue-5 "$wt"
wt6="$repo/.claude/worktrees/issue-6"
git -C "$repo" worktree add --quiet -b agent/issue-6 "$wt6"

failures=0
total=0
pass() { total=$((total + 1)); }
fail() {
  total=$((total + 1))
  echo "FAIL - $1" >&2
  failures=$((failures + 1))
}

# run <hook> <role|-> <cwd> <tool_input の JSON> [tool_name] : 終了コードを出す
run() {
  local hook="$1" role="$2" cwd="$3" input="$4" tool="${5:-Bash}" json rc
  json="$(jq -cn --arg cwd "$cwd" --arg tool "$tool" --argjson ti "$input" \
    '{session_id:"s", cwd:$cwd, hook_event_name:"PreToolUse", tool_name:$tool, tool_input:$ti}')"
  set +e
  if [ "$role" = "-" ]; then
    (unset AGENT_ROLE; printf '%s' "$json" | bash "$hooks/$hook" >/dev/null 2>"$tmp/stderr")
  else
    printf '%s' "$json" | AGENT_ROLE="$role" bash "$hooks/$hook" >/dev/null 2>"$tmp/stderr"
  fi
  rc=$?
  set -e
  echo "$rc"
}

bash_input() { jq -cn --arg c "$1" '{command:$c}'; }

# expect <期待する終了コード> <役割> <cwd> <コマンド>
expect_bash() {
  local want="$1" role="$2" cwd="$3" cmd="$4" got
  got="$(run guard-bash.sh "$role" "$cwd" "$(bash_input "$cmd")")"
  if [ "$got" = "$want" ]; then
    pass
  else
    fail "guard-bash role=$role want=$want got=$got: $cmd"
  fi
  if [ "$want" = 2 ] && [ "$got" = 2 ] && [ ! -s "$tmp/stderr" ]; then
    fail "拒否の理由が標準エラーに出ていない: $cmd"
  fi
}

expect_write() {
  local want="$1" role="$2" cwd="$3" path="$4" got
  got="$(run guard-write.sh "$role" "$cwd" "$(jq -cn --arg p "$path" '{file_path:$p, content:"x"}')" Write)"
  if [ "$got" = "$want" ]; then pass; else fail "guard-write role=$role want=$want got=$got: $path"; fi
}

expect_mcp() {
  local want="$1" role="$2" tool="$3" got
  got="$(run guard-mcp.sh "$role" "$repo" '{"pullNumber":1}' "$tool")"
  if [ "$got" = "$want" ]; then pass; else fail "guard-mcp role=$role want=$want got=$got: $tool"; fi
}

# ---------------- guard-bash: 全員に共通 ----------------
common_denied=(
  "git push origin main"
  "git push origin HEAD:main"
  "git push origin agent/issue-5:refs/heads/main"
  "git push origin +agent/issue-5"
  "git push --force origin agent/issue-5"
  "git push -f origin agent/issue-5"
  "git push -uf origin agent/issue-5"
  "git push --all origin"
  "git push --force-with-lease origin feature/x"
  "gh pr merge 12 --squash"
  "cd x && gh pr merge 12"
  "gh api -X PUT repos/o/r/pulls/12/merge"
  "gh api repos/o/r/rulesets --input main.json"
  "gh api -X PUT repos/o/r/branches/main/protection"
  "gh api repos/o/r/actions/secrets/FOO"
  "gh api repos/o/r/actions/variables"
  "gh secret set FOO"
  "gh variable list"
  "gh ruleset list"
  "gh pr edit 12 --add-label human-approved"
  "gh issue edit 5 --add-label 'human-approved'"
  "git worktree remove --force .claude/worktrees/issue-5"
  "git worktree remove -f x"
  "rm -rf /"
  "rm -rf ~"
  "rm -rf \$HOME"
  "rm -rf .."
  "rm -fr $repo"
  "rm -r -f ."
  "cf deploy"
  "pnpm exec cf deploy --prebuilt"
  "(cd web && pnpm exec cf deploy)"
  "npx wrangler deploy"
  "wrangler secret put X"
  "curl -X POST https://api.github.com/repos/o/r/issues"
  "curl -d '{}' https://api.github.com/repos/o/r/pulls/1/merge"
  "wget --method=DELETE https://api.github.com/repos/o/r"
  "bash -c 'git push origin main'"
  "FOO=1 git push origin main"
  "echo \$(gh pr merge 1)"
)
for role in orchestrator worker reviewer human; do
  for cmd in "${common_denied[@]}"; do
    expect_bash 2 "$role" "$repo" "$cmd"
  done
done
# ブランチを指定しない push で今のブランチが main
for role in orchestrator human; do
  expect_bash 2 "$role" "$repo" "git push"
  expect_bash 2 "$role" "$repo" "git push origin"
  expect_bash 2 "$role" "$repo" "git push -u origin HEAD"
done
# install-workflows.sh と .github/workflows/ への書き込みは人だけ
for role in orchestrator worker reviewer; do
  expect_bash 2 "$role" "$repo" "echo x > .github/workflows/ci.yml"
  expect_bash 2 "$role" "$repo" "cat a.yml >> \".github/workflows/ci.yml\""
  expect_bash 2 "$role" "$repo" "cp a.yml .github/workflows/ci.yml"
  expect_bash 2 "$role" "$repo" "sed -i s/a/b/ .github/workflows/ci.yml"
  expect_bash 2 "$role" "$repo" "scripts/github/install-workflows.sh"
  expect_bash 2 "$role" "$repo" "bash scripts/github/install-workflows.sh --dry-run"
done
expect_bash 0 human "$repo" "scripts/github/install-workflows.sh --dry-run"
expect_bash 0 human "$repo" "echo x > .github/workflows/ci.yml"

# eval・git の別名・環境変数での git の設定は、判定をすり抜けられるので拒否する（human を除く）
agents_denied=(
  "eval git push origin main"
  "eval 'echo hi'"
  "command eval x"
  "git -c alias.p=push p origin main"
  "git -c Alias.P=push p origin agent/issue-5"
  "git -calias.p=push p origin main"
  "git --config-env=alias.p=X p origin main"
  "git --config-env alias.p=X p origin main"
  "git -C . -c alias.st=status st"
  "git config alias.p push"
  "git config --global alias.p '!git push origin main'"
  "GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=alias.p GIT_CONFIG_VALUE_0=push git p origin main"
  "env GIT_CONFIG_PARAMETERS=x git p"
)
for role in orchestrator worker reviewer; do
  for cmd in "${agents_denied[@]}"; do
    expect_bash 2 "$role" "$wt" "$cmd"
  done
done
expect_bash 0 human "$repo" "git -c alias.st=status st"
expect_bash 0 human "$repo" "eval 'echo hi'"
expect_bash 0 worker "$wt" "git -c user.name=x commit -m 'x (#5)'"
expect_bash 0 worker "$wt" "git config --get alias.p"
expect_bash 0 worker "$wt" "git config user.name x"
expect_bash 0 worker "$wt" "echo evaluate"

# ---------------- 通常の作業は通す ----------------
for role in orchestrator worker human; do
  for cmd in \
    "go test ./..." \
    "go -C tools/agentctl test -race ./..." \
    "git status" \
    "git diff --stat 2>&1 | head" \
    "cf deploy --dry-run" \
    "(cd web && pnpm exec cf deploy --prebuilt --dry-run)" \
    "rm -rf ./build" \
    "rm -rf $tmp/scratch" \
    "curl -s https://api.github.com/repos/o/r" \
    "gh pr view 12 --json state" \
    "bash scripts/github/tests/run.sh" \
    "shellcheck scripts/github/install-workflows.sh" \
    "cat .github/workflows/ci.yml" \
    "git worktree remove .claude/worktrees/issue-9"; do
    expect_bash 0 "$role" "$wt" "$cmd"
  done
done

# ---------------- Worker ----------------
expect_bash 0 worker "$wt" "git push origin agent/issue-5"
expect_bash 0 worker "$wt" "git push -u origin agent/issue-5"
expect_bash 0 worker "$wt" "git push"
expect_bash 0 worker "$wt" "git push -u origin HEAD"
expect_bash 0 worker "$wt" "git push --force-with-lease origin agent/issue-5"
expect_bash 0 worker "$wt" "git add -A && git commit -m 'feat: x (#5)'"
expect_bash 0 worker "$wt" "gh pr create --title t --body-file b.md"
# 役割を AGENT_ROLE で指定しなくても、リンクした Worktree の中なら worker
got="$(run guard-bash.sh - "$wt" "$(bash_input "git push origin agent/issue-6")")"
[ "$got" = 2 ] && pass || fail "役割が未設定で Worktree の中なら worker として扱う（got=$got）"
got="$(run guard-bash.sh - "$wt" "$(bash_input "scripts/agents/merge.sh 12")")"
[ "$got" = 2 ] && pass || fail "役割が未設定で Worktree の中なら merge.sh を拒否する（got=$got）"
got="$(run guard-bash.sh - "$repo" "$(bash_input "scripts/agents/merge.sh 12")")"
[ "$got" = 0 ] && pass || fail "役割が未設定でメインの作業ツリーなら orchestrator（got=$got）"
for cmd in \
  "git push origin agent/issue-6" \
  "git push origin HEAD:agent/issue-6" \
  "git push --force-with-lease origin agent/issue-6" \
  "git push origin v1.0" \
  "scripts/agents/merge.sh 12" \
  "bash scripts/agents/review.sh 12" \
  "scripts/agents/spawn-worker.sh 6" \
  "./scripts/agents/wt.sh cleanup" \
  "scripts/agents/agentctl.sh state 5 merged" \
  "go -C tools/agentctl run . state 5 ready-to-merge --pr 12" \
  "agentctl state --repo o/r 5 status:merged" \
  "scripts/agents/agentctl.sh state 5 in-review --pr 12" \
  "gh pr review 12 --approve"; do
  expect_bash 2 worker "$wt" "$cmd"
done
# Worktree issue-6 では Issue 6 のブランチに push できる（AGENT_ISSUE がなければディレクトリ名から）
expect_bash 0 worker "$wt6" "git push origin agent/issue-6"
got="$(AGENT_ISSUE=6 run guard-bash.sh worker "$wt" "$(bash_input "git push origin agent/issue-6")")"
[ "$got" = 0 ] && pass || fail "AGENT_ISSUE=6 なら agent/issue-6 へ push できる（got=$got）"

# Worker: Bash からの保護されたパスへの書き込みは、AGENT_ALLOW_PROTECTED=1 のときだけ
worker_protected_denied=(
  "sed -i s/exit/true/ .claude/hooks/guard-bash.sh"
  "sed -i.bak 1d $wt/.claude/hooks/guard-bash.sh"
  "sed --in-place 1d ./.claude/hooks/lib.sh"
  "echo 'exit 0' > .claude/hooks/guard-bash.sh"
  "printf x >> \"./.claude/hooks/lib.sh\""
  "cat x >| .claude/hooks/lib.sh"
  "cat x | tee .claude/hooks/guard-write.sh"
  "cat x | tee -a scripts/agents/lib.sh"
  "cp /tmp/x .claude/hooks/guard-bash.sh"
  "cp -R /tmp/hooks .claude/hooks"
  "mv x scripts/agents/merge.sh"
  "rm -rf .claude/hooks"
  "rm -rf .claude"
  "rm scripts/agents/merge.sh"
  "ln -sf /tmp/x .agents/config.json"
  "touch scripts/agents/new.sh"
  "chmod -x .claude/hooks/guard-bash.sh"
  "python3 -c \"open('.claude/hooks/guard-bash.sh','w').write('')\""
  "perl -pi -e s/a/b/ scripts/agents/lib.sh"
  "node -e \"require('fs').writeFileSync('.agents/config.json','{}')\""
  "git checkout origin/main -- .claude/hooks/guard-bash.sh"
  "git restore --source=HEAD~1 scripts/agents/merge.sh"
  "git rm -q scripts/agents/merge.sh"
  "dd if=/dev/zero of=.agents/config.json"
  "cd sub && cp x ../.claude/hooks/y"
  "tar -xf x.tar -C .claude/hooks"
)
for cmd in "${worker_protected_denied[@]}"; do
  expect_bash 2 worker "$wt" "$cmd"
done
# 役割が未設定（Worktree の中＝worker）でも同じ
got="$(run guard-bash.sh - "$wt" "$(bash_input "sed -i 1d .claude/hooks/guard-bash.sh")")"
[ "$got" = 2 ] && pass || fail "役割が未設定の Worker でも保護されたパスへの sed -i を拒否する（got=$got）"
for cmd in "${worker_protected_denied[@]}"; do
  got="$(AGENT_ALLOW_PROTECTED=1 run guard-bash.sh worker "$wt" "$(bash_input "$cmd")")"
  [ "$got" = 0 ] && pass || fail "AGENT_ALLOW_PROTECTED=1 なら Bash から保護されたパスに書ける（got=$got）: $cmd"
done
for cmd in \
  "sed -i s/a/b/ server/x.go" \
  "echo x > server/out.txt" \
  "cp a b" \
  "rm -rf .claude/worktrees/tmp" \
  "cat .claude/hooks/guard-bash.sh" \
  "grep -n deny .claude/hooks/guard-bash.sh > /dev/null" \
  "sed -n 1,10p scripts/agents/lib.sh" \
  "shellcheck .claude/hooks/guard-bash.sh scripts/agents/*.sh" \
  "bash .claude/hooks/tests/run.sh" \
  "git diff scripts/agents/merge.sh" \
  "git add scripts/agents/merge.sh" \
  "git log -- .claude/hooks" \
  "python3 scripts/check-docs.py" \
  "gofmt -l scripts"; do
  expect_bash 0 worker "$wt" "$cmd"
done
# orchestrator・human には Bash の保護されたパスの判定をかけない（orchestrator の書き込みは guard-write と手順で止める）
expect_bash 0 human "$wt" "sed -i 1d .claude/hooks/guard-bash.sh"

# ---------------- Reviewer ----------------
for cmd in \
  "git commit -m x" \
  "git push origin agent/issue-5" \
  "git add -A" \
  "gh pr comment 12 --body x" \
  "gh issue view 5" \
  "gh issue comment 5 --body x" \
  "gh pr edit 12 --title x" \
  "gh pr review 12 --approve" \
  "echo x > a.txt" \
  "echo x >> a.txt" \
  "go test ./... | tee out.txt" \
  "sed -i s/a/b/ a.txt" \
  "git checkout -- a.txt" \
  "gh api -X POST repos/o/r/issues/1/comments" \
  "scripts/agents/merge.sh 1" \
  "python3 -c \"open('x','w').write('1')\"" \
  "python -c 1" \
  "python3.12 x.py" \
  "/usr/bin/python3 x.py" \
  "env python3 x.py" \
  "node -e 1" \
  "perl -e 1" \
  "perl -pi -e s/a/b/ a.txt" \
  "ruby -e 1" \
  "awk 'BEGIN { print 1 > \"x\" }'" \
  "awk 'BEGIN { system(\"rm x\") }'"; do
  expect_bash 2 reviewer "$wt" "$cmd"
done
for cmd in \
  "go test ./..." \
  "git diff origin/main...HEAD" \
  "git log --oneline -5" \
  "git branch --show-current" \
  "go vet ./... 2>&1 | head -50" \
  "grep -rn 'a > b' ." \
  "ls > /dev/null 2>&1" \
  "gh pr view 12 --json body" \
  "awk '{ print \$1 }' a.txt" \
  "shellcheck a.sh"; do
  expect_bash 0 reviewer "$wt" "$cmd"
done

# ---------------- agentctl の書き込み（Issue #86） ----------------
# comment（PR へのコメント）・state・claim・attempt は Worker・Reviewer では拒否し、Orchestrator では許す。
agentctl_write_cmds=(
  "agentctl comment --repo o/r --pr 12 --body-file b.md"
  "scripts/agents/agentctl.sh comment --repo o/r --pr 12 --body-file b.md"
  "./scripts/agents/agentctl.sh comment --pr 12"
  "bash scripts/agents/agentctl.sh comment --pr 12"
  ".agents/bin/agentctl comment --pr 12"
  "$repo/.agents/bin/agentctl comment --pr 12"
  "go -C tools/agentctl run . comment --repo o/r --pr 12"
  "go run ./tools/agentctl comment --pr 12"
  "cd tools/agentctl && go run . comment --pr 12"
  "env GH_TOKEN=x agentctl comment --pr 12"
  "timeout 60 scripts/agents/agentctl.sh comment --pr 12"
  ". scripts/agents/lib.sh && agentctl comment --pr 12"
  "agentctl state 5 in-review --repo o/r --pr 12"
  "scripts/agents/agentctl.sh state 5 changes-requested --repo o/r"
  "go -C tools/agentctl run . state 5 ready --repo o/r --reason x"
  "agentctl claim 5 --repo o/r"
  "scripts/agents/agentctl.sh claim 5 --resume --repo o/r"
  "go -C tools/agentctl run . claim 5 --repo o/r"
  "agentctl attempt 5 --cause ci:ci-ok --repo o/r"
  "scripts/agents/agentctl.sh attempt 5 --cause review:F1 --repo o/r"
  "go -C tools/agentctl run . attempt 5 --cause x --repo o/r"
  "agentctl comment --dry-run=false --pr 12"
  # Worker・Reviewer では --dry-run の例外がない（PR #87 の Codex の P1 を受けて安全側に倒した）
  "agentctl comment --repo o/r --pr 12 --body-file b.md --dry-run"
  "scripts/agents/agentctl.sh state 5 in-review --repo o/r --pr 12 --dry-run"
  "go -C tools/agentctl run . claim 5 --repo o/r --dry-run"
  "scripts/agents/agentctl.sh attempt 5 --cause x --repo o/r --dry-run"
  "agentctl state 5 in-review --repo o/r --dry-run=true"
  "agentctl claim 5 --resume --dry-run --worker issue-5 --session s --repo o/r"
  # シェルのコメントの中の --dry-run・値をとるシェルのオプション（PR #87 の Codex の P1）
  "agentctl state 5 in-review --repo o/r # --dry-run"
  "bash -o pipefail scripts/agents/agentctl.sh attempt 5 --cause x --repo o/r"
  "bash -o pipefail scripts/agents/agentctl.sh attempt 5 --cause x --repo o/r --dry-run"
  # 引用符で語を分けても、引用符を取り除いてから読む
  "agentctl st''ate 5 in-review --repo o/r"
  "agentctl \"comment\" --pr 12"
  "'agentctl' claim 5"
  "agentctl\\ state 5 in-review"
  # gate の --comment・--set-status
  "go -C tools/agentctl run . gate --repo o/r --pr 12 --comment"
  "agentctl gate --repo o/r --pr 12 --set-status"
  "agentctl gate --repo o/r --pr 12 -comment=true"
  "agentctl gate --repo o/r --pr 12 --set-status=true --json"
  # 字句の判定なので、agentctl の語と書き込みの語があれば読み取りの形でも拒否する（安全側）
  "grep -n comment tools/agentctl/main.go"
  "agentctl plan --repo o/r | grep state"
  # 前回までの --dry-run の読み違いの例（いまは --dry-run の有無を問わず拒否）
  "agentctl state 5 in-review --repo o/r --reason '--dry-run'"
  "agentctl state 5 in-review --repo o/r --reason --dry-run"
  "agentctl state 5 in-review --repo o/r --dry-run --dry-run=false"
  "agentctl state 5 in-review --repo o/r --dry-run=false"
  "scripts/agents/agentctl.sh claim 5 --worker --dry-run --repo o/r"
  "agentctl attempt 5 --cause --dry-run --repo o/r"
  "agentctl comment --pr 12 --body --dry-run"
  "agentctl comment --pr 12 --body-file --dry-run"
  "agentctl state 5 in-review --repo o/r --session=x --config --dry-run"
  "agentctl state 5 in-review --repo o/r --dry-run=1"
  "agentctl state 5 in-review --repo o/r --dry-run=yes"
  "agentctl state 5 in-review --repo o/r --reason \"a --dry-run\""
  "agentctl state 5 in-review --repo o/r --reason 'a' --dry-run"
  "agentctl state 5 in-review --repo o/r -- --dry-run"
  "agentctl state 5 in-review --repo o/r \$DRY"
  "agentctl state 5 in-review --repo o/r --dry-run=\$X"
  "agentctl --dry-run state 5 in-review --repo o/r"
  "agentctl comment 12 --dry-run"
)
for role in worker reviewer; do
  for cmd in "${agentctl_write_cmds[@]}"; do
    expect_bash 2 "$role" "$wt" "$cmd"
  done
done
# 役割が未設定（Worktree の中＝worker）でも同じ
got="$(run guard-bash.sh - "$wt" "$(bash_input "scripts/agents/agentctl.sh comment --pr 12")")"
[ "$got" = 2 ] && pass || fail "役割が未設定の Worker でも agentctl comment を拒否する（got=$got）"
for cmd in "${agentctl_write_cmds[@]}"; do
  expect_bash 0 orchestrator "$repo" "$cmd"
done
# 読み取りのサブコマンドは、Worker・Reviewer でも許す
for role in worker reviewer orchestrator; do
  for cmd in \
    "scripts/agents/agentctl.sh plan --repo o/r --json" \
    "agentctl plan --repo o/r" \
    "scripts/agents/agentctl.sh recover --repo o/r --json" \
    ".agents/bin/agentctl pr view 12 --repo o/r --json" \
    "agentctl pr codex-record --repo o/r --pr 12" \
    "bash -o pipefail scripts/agents/agentctl.sh pr protected-files --repo o/r --pr 12" \
    "go -C tools/agentctl run . gate --repo o/r --pr 12 --json --input $tmp/pass.json" \
    "agentctl gate --repo o/r --pr 12 --json" \
    "go -C tools/agentctl run . review validate $tmp/review.json" \
    "go -C tools/agentctl run . review render $tmp/review.json" \
    "go -C tools/agentctl test -race ./..." \
    "go test ./tools/agentctl/... -run TestComment" \
    "go -C tools/agentctl test ./internal/state/..." \
    "grep -n plan tools/agentctl/main.go" \
    "echo 'fix: state を comment にする'" \
    "go test ./server/... -run TestClaim" \
    "git log --oneline -- tools/agentctl"; do
    expect_bash 0 "$role" "$wt" "$cmd"
  done
done

# ---------------- guard-write ----------------
expect_write 2 reviewer "$wt" "$wt/README.md"
expect_write 0 worker "$wt" "$wt/README.md"
expect_write 0 worker "$wt" "README.md"
expect_write 0 worker "$wt" "$wt/server/new/file.go"
expect_write 2 worker "$wt" "$repo/README.md"
expect_write 2 worker "$wt" "$wt6/README.md"
expect_write 2 worker "$wt" "$wt/../issue-6/README.md"
expect_write 2 worker "$wt" "/etc/passwd"
expect_write 2 worker "$wt" "$wt/.git/config"
expect_write 2 worker "$wt" "$wt/.github/workflows/ci.yml"
expect_write 2 worker "$wt" "$wt/.claude/hooks/guard-bash.sh"
expect_write 2 worker "$wt" "$wt/scripts/agents/merge.sh"
expect_write 2 worker "$wt" "$wt/.agents/config.json"
got="$(AGENT_ALLOW_PROTECTED=1 run guard-write.sh worker "$wt" "$(jq -cn --arg p "$wt/scripts/agents/merge.sh" '{file_path:$p}')" Write)"
[ "$got" = 0 ] && pass || fail "AGENT_ALLOW_PROTECTED=1 なら保護されたパスに書ける（got=$got）"
got="$(AGENT_ALLOW_PROTECTED=1 run guard-write.sh worker "$wt" "$(jq -cn --arg p "$wt/.github/workflows/ci.yml" '{file_path:$p}')" Write)"
[ "$got" = 2 ] && pass || fail "AGENT_ALLOW_PROTECTED=1 でも .github/workflows/ には書けない（got=$got）"
expect_write 0 worker "$wt" "$wt/.github/workflow-sources/ci.yml"
got="$(run guard-write.sh worker "$wt" "$(jq -cn --arg p "$wt/x.ipynb" '{notebook_path:$p}')" NotebookEdit)"
[ "$got" = 0 ] && pass || fail "NotebookEdit の notebook_path を読む（got=$got）"
expect_write 2 orchestrator "$repo" "$repo/README.md"
expect_write 2 orchestrator "$repo" "$repo/scripts/agents/merge.sh"
expect_write 2 orchestrator "$repo" "$tmp/outside.txt"
expect_write 0 orchestrator "$repo" "$repo/.agents/run/issue-5.json"
expect_write 0 orchestrator "$repo" "$repo/.claude/worktrees/issue-5/NOTE.md"
expect_write 2 orchestrator "$repo" "$repo/.agents/../README.md"
expect_write 2 orchestrator "$repo" "$repo/.github/workflows/ci.yml"
expect_write 0 human "$repo" "$repo/README.md"
expect_write 0 human "$repo" "$repo/.github/workflows/ci.yml"
got="$(run guard-write.sh - "$wt" "$(jq -cn --arg p "$repo/README.md" '{file_path:$p}')" Write)"
[ "$got" = 2 ] && pass || fail "役割が未設定で Worktree の中なら worker として Worktree の外を拒否する（got=$got）"

# ---------------- guard-mcp ----------------
for tool in mcp__github__merge_pull_request mcp__github__enable_pr_auto_merge \
  mcp__github__update_pull_request_branch mcp__github__pull_request_review_write; do
  expect_mcp 0 orchestrator "$tool"
  for role in worker reviewer human; do
    expect_mcp 2 "$role" "$tool"
  done
done

# 不正な入力・役割
set +e
printf 'not json' | AGENT_ROLE=worker bash "$hooks/guard-bash.sh" >/dev/null 2>&1
rc=$?
set -e
[ "$rc" = 2 ] && pass || fail "入力が JSON でなければ拒否する（rc=$rc）"
expect_bash 2 admin "$wt" "go test ./..."

echo "hooks: $((total - failures))/$total ok"
if [ "$failures" -ne 0 ]; then
  echo "失敗: $failures" >&2
  exit 1
fi
