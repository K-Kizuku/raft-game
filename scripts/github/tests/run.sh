#!/usr/bin/env bash
# scripts/github/install-workflows.sh のテスト（Issue #2）。
# 一時的な Git リポジトリ（bare の origin と作業用の clone）と偽の `gh` を使う。
# 実行: bash scripts/github/tests/run.sh
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
script="$here/../install-workflows.sh"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

# 利用者の Git の設定（署名・フックなど）に影響されないようにする。
export GIT_CONFIG_NOSYSTEM=1
export GIT_CONFIG_GLOBAL="$tmp/gitconfig"
git config --file "$GIT_CONFIG_GLOBAL" user.name "test"
git config --file "$GIT_CONFIG_GLOBAL" user.email "test@example.invalid"
git config --file "$GIT_CONFIG_GLOBAL" init.defaultBranch main
unset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE
# テストを AI エージェントの環境で実行しても、既定では人として動かす。
unset AGENT_ROLE

# 偽の gh: 引数をログに書く。
mkdir -p "$tmp/bin"
cat >"$tmp/bin/gh" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$FAKE_GH_LOG"
EOF
chmod +x "$tmp/bin/gh"
export PATH="$tmp/bin:$PATH"
export FAKE_GH_LOG="$tmp/gh.log"
: >"$FAKE_GH_LOG"

failures=0
pass() { echo "ok   - $1"; }
fail() {
  echo "FAIL - $1" >&2
  failures=$((failures + 1))
}
check() {
  local desc="$1"
  shift
  if "$@"; then pass "$desc"; else fail "$desc"; fi
}

remote_branches() {
  git -C "$tmp/origin.git" for-each-ref --format='%(refname:short)' refs/heads/
}

# --- 準備: origin の main に workflow-sources と、ソースにない既存のワークフローを置く ---
git init --quiet --bare "$tmp/origin.git"
git init --quiet "$tmp/seed"
(
  cd "$tmp/seed"
  mkdir -p .github/workflow-sources .github/workflows
  printf 'name: ci\n' >.github/workflow-sources/ci.yml
  printf 'not a workflow\n' >.github/workflow-sources/README.md
  printf 'name: other\n' >.github/workflows/other.yml
  git add -A
  git commit --quiet -m init
  git remote add origin "$tmp/origin.git"
  git push --quiet origin main
)
git clone --quiet "$tmp/origin.git" "$tmp/work"
cd "$tmp/work"

# --- AGENT_ROLE=worker・reviewer・orchestrator は拒否 ---
for role in worker reviewer orchestrator; do
  if AGENT_ROLE="$role" bash "$script" >"$tmp/out" 2>&1; then
    fail "AGENT_ROLE=$role を拒否する"
  else
    check "AGENT_ROLE=$role を拒否する" grep -q "AGENT_ROLE=$role" "$tmp/out"
  fi
done
check "拒否したときは gh を呼ばない" test ! -s "$FAKE_GH_LOG"

# --- 作業ツリーが汚れていたら止まる ---
printf 'dirty\n' >>.github/workflow-sources/ci.yml
if bash "$script" >"$tmp/out" 2>&1; then
  fail "作業ツリーが汚れていたら止まる"
else
  check "作業ツリーが汚れていたら止まる" grep -q "汚れている" "$tmp/out"
fi
git checkout --quiet -- .github/workflow-sources/ci.yml
printf 'x\n' >.github/workflows/untracked.yml
if bash "$script" >/dev/null 2>&1; then
  fail "取り込み先に未追跡のファイルがあれば止まる"
else
  pass "取り込み先に未追跡のファイルがあれば止まる"
fi
rm .github/workflows/untracked.yml
check "汚れていたときは gh を呼ばない" test ! -s "$FAKE_GH_LOG"
check "汚れていたときはブランチを作らない" test "$(remote_branches)" = "main"

# --- --dry-run は差分の一覧だけを出し、何も変えない ---
head_before="$(git rev-parse HEAD)"
branch_before="$(git symbolic-ref --short HEAD)"
if bash "$script" --dry-run >"$tmp/out" 2>&1; then
  pass "--dry-run は終了コード 0"
else
  fail "--dry-run は終了コード 0"
  cat "$tmp/out" >&2
fi
check "--dry-run は差分を一覧する" grep -q "追加: .github/workflows/ci.yml" "$tmp/out"
check "--dry-run は .yml 以外を対象にしない" bash -c "! grep -q README.md '$tmp/out'"
check "--dry-run は HEAD を変えない" test "$(git rev-parse HEAD)" = "$head_before"
check "--dry-run はブランチを切り替えない" test "$(git symbolic-ref --short HEAD)" = "$branch_before"
check "--dry-run はブランチを作らない" test "$(git for-each-ref --format=x refs/heads/human | wc -l)" -eq 0
check "--dry-run は push しない" test "$(remote_branches)" = "main"
check "--dry-run は gh を呼ばない" test ! -s "$FAKE_GH_LOG"
check "--dry-run は作業ツリーを変えない" test -z "$(git status --porcelain)"

# --- 差分あり: コピーしてコミットし、push して gh pr create を呼ぶ ---
if bash "$script" >"$tmp/out" 2>&1; then
  pass "差分ありは終了コード 0"
else
  fail "差分ありは終了コード 0"
  cat "$tmp/out" >&2
fi
new_branch="$(remote_branches | grep '^human/install-workflows-' || true)"
check "human/install-workflows-<日時> ブランチを push する" \
  bash -c "printf '%s\n' '$new_branch' | grep -Eq '^human/install-workflows-[0-9]{8}-[0-9]{6}$'"
if [ -n "$new_branch" ]; then
  r="$tmp/origin.git"
  check "ソースを .github/workflows/ にコピーする" \
    test "$(git -C "$r" show "$new_branch:.github/workflows/ci.yml")" = "name: ci"
  check "ソースにないファイルは消さない" \
    test "$(git -C "$r" show "$new_branch:.github/workflows/other.yml")" = "name: other"
  check ".yml 以外はコピーしない" \
    bash -c "! git -C '$r' cat-file -e '$new_branch:.github/workflows/README.md' 2>/dev/null"
  check "origin/main の上に 1 コミットを積む" \
    test "$(git -C "$r" rev-list --count "main..$new_branch")" -eq 1
fi
check "gh pr create を 1 回呼ぶ" test "$(grep -c '^pr create' "$FAKE_GH_LOG")" -eq 1
check "gh pr create に --base main と --head を渡す" \
  grep -q -- "--base main --head $new_branch" "$FAKE_GH_LOG"
check "実行後は元のブランチに戻る" test "$(git symbolic-ref --short HEAD)" = "$branch_before"
check "main は変えない" test "$(git -C "$tmp/origin.git" rev-parse main)" = "$head_before"

# --- 差分なし: 何もしない ---
# 取り込みの PR がマージされた状態を作る。
git -C "$tmp/origin.git" update-ref refs/heads/main "refs/heads/$new_branch"
git -C "$tmp/origin.git" update-ref -d "refs/heads/$new_branch"
: >"$FAKE_GH_LOG"
if bash "$script" >"$tmp/out" 2>&1; then
  pass "差分なしは終了コード 0"
else
  fail "差分なしは終了コード 0"
  cat "$tmp/out" >&2
fi
check "差分なしは「取り込むものはない」と出す" grep -q "取り込むものはない" "$tmp/out"
check "差分なしは gh を呼ばない" test ! -s "$FAKE_GH_LOG"
check "差分なしはブランチを作らない" test "$(remote_branches)" = "main"
check "差分なしはローカルにもブランチを作らない" \
  test "$(git for-each-ref --format=x 'refs/heads/human/*' | wc -l)" -eq 1

# --- 更新: ソースが変わったら「更新」として取り込む ---
(
  cd "$tmp/seed"
  git pull --quiet origin main
  printf 'name: ci\non: push\n' >.github/workflow-sources/ci.yml
  git commit --quiet -am update
  git push --quiet origin main
)
if bash "$script" --dry-run --base main >"$tmp/out" 2>&1; then
  check "ソースの変更は「更新」として一覧する" grep -q "更新: .github/workflows/ci.yml" "$tmp/out"
else
  fail "ソースの変更は「更新」として一覧する"
  cat "$tmp/out" >&2
fi

# --- 引数の誤り ---
if bash "$script" --unknown >/dev/null 2>&1; then
  fail "不明な引数はエラー"
else
  pass "不明な引数はエラー"
fi

if [ "$failures" -ne 0 ]; then
  echo "$failures 件失敗" >&2
  exit 1
fi
echo "すべて成功"
