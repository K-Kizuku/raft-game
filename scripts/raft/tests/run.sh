#!/usr/bin/env bash
# scripts/raft/verify-patch.sh のテスト（T-003, #11）。
# 偽の上流・同梱の木を一時ディレクトリに作って S-1〜S-10 を確かめ（ネットワーク・go は不要）、
# go が使えるときは本物の上流との比較（R-1）も行う。
# 実行: bash scripts/raft/tests/run.sh
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
script="$here/../verify-patch.sh"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

# 利用者の Git の設定（署名・フックなど）に影響されないようにする。
export GIT_CONFIG_NOSYSTEM=1
export GIT_CONFIG_GLOBAL="$tmp/gitconfig"
git config --file "$GIT_CONFIG_GLOBAL" user.name "test"
git config --file "$GIT_CONFIG_GLOBAL" user.email "test@example.invalid"
git config --file "$GIT_CONFIG_GLOBAL" init.defaultBranch main
unset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE
# 一時ディレクトリの上にある Git の作業ツリーを、偽の同梱の木の一覧に使わない。
export GIT_CEILING_DIRECTORIES="$tmp"

failures=0
pass() { echo "ok   - $1"; }
fail() {
  echo "not ok - $1"
  failures=$((failures + 1))
}

# expect DESC CODE [NEEDLE...] -- ARGS...: verify-patch.sh を ARGS で実行し、終了コードが
# CODE で、出力（標準出力と標準エラー）に NEEDLE がすべて含まれることを確かめる。
expect() {
  local desc="$1" want="$2"
  shift 2
  local needles=()
  while [ $# -gt 0 ] && [ "$1" != "--" ]; do
    needles+=("$1")
    shift
  done
  shift
  local rc=0
  bash "$script" "$@" >"$tmp/out" 2>&1 || rc=$?
  local ok=1 n
  [ "$rc" -eq "$want" ] || ok=0
  if [ "${#needles[@]}" -gt 0 ]; then
    for n in "${needles[@]}"; do
      grep -qF -- "$n" "$tmp/out" || ok=0
    done
  fi
  if [ "$ok" -eq 1 ]; then
    pass "$desc"
  else
    fail "${desc}（終了コード ${rc}、期待 ${want}）"
    sed 's/^/    | /' "$tmp/out"
  fi
}

# --- 偽の木 -------------------------------------------------------------------
# make_trees DIR: DIR/up（上流）と DIR/vd（パッチを当てた同梱）を作る。
make_trees() {
  local d="$1"
  rm -rf "$d"
  mkdir -p "$d/up/sub" "$d/up/tla" "$d/up/scripts" "$d/up/.github"
  cat >"$d/up/raft.go" <<'EOF'
package raft

var globalRand = &lockedRand{}

type raft struct {
	electionTimeout           int
	randomizedElectionTimeout int
}

func (r *raft) resetRandomizedElectionTimeout() {
	r.randomizedElectionTimeout = r.electionTimeout + globalRand.Intn(r.electionTimeout)
}
EOF
  printf 'package raft\n\nfunc a() int { return 1 }\n' >"$d/up/a.go"
  printf 'package sub\n\nfunc B() int { return 2 }\n' >"$d/up/sub/b.go"
  printf 'package sub\n\nfunc S() int { return 3 }\n' >"$d/up/sub/with space.go"
  printf '#!/bin/sh\necho tla\n' >"$d/up/tla/x.sh"
  printf '#!/bin/sh\necho scripts\n' >"$d/up/scripts/y.sh"
  printf 'name: z\n' >"$d/up/.github/z.yml"
  printf 'test:\n\t./scripts/y.sh\n' >"$d/up/Makefile"

  mkdir -p "$d/vd/sub"
  cp "$d/up/a.go" "$d/vd/a.go"
  cp "$d/up/sub/b.go" "$d/vd/sub/b.go"
  cp "$d/up/sub/with space.go" "$d/vd/sub/with space.go"
  cat >"$d/vd/raft.go" <<'EOF'
package raft

var globalRand = &lockedRand{}

type raft struct {
	electionTimeout           int
	randomizedElectionTimeout int
	randIntn                  func(n int) int // raft-game patch
}

func (r *raft) resetRandomizedElectionTimeout() {
	r.randomizedElectionTimeout = r.electionTimeout + r.randIntn(r.electionTimeout)
}
EOF
  cat >"$d/vd/PATCHES.md" <<'EOF'
# 偽の同梱

## 取り込んだ版

| 項目 | 値 |
|---|---|
| 版 | `v0.0.1` |

## 当てたパッチ

```diff
--- raft.go.orig
+++ raft.go
@@ -6,2 +6,3 @@
 	randomizedElectionTimeout int
+	randIntn                  func(n int) int // raft-game patch
 }
@@ -10 +11 @@
-	r.randomizedElectionTimeout = r.electionTimeout + globalRand.Intn(r.electionTimeout)
+	r.randomizedElectionTimeout = r.electionTimeout + r.randIntn(r.electionTimeout)
```

## 次の節

```diff
+この節のブロックは比べない
```
EOF
}

trees() {
  make_trees "$tmp/$1"
  up="$tmp/$1/up"
  vd="$tmp/$1/vd"
}

trees s1
expect "S-1: 正しい偽の同梱の木（空白を含むファイル名を含む）は 0" 0 "パッチの行数: 3" -- \
  --upstream-dir "$up" --vendored-dir "$vd"

trees s2
printf 'package raft\n' >"$vd/extra.go"
expect "S-2: 同梱にだけあるファイルは 1" 1 "extra.go" "同梱にだけある" -- \
  --upstream-dir "$up" --vendored-dir "$vd"

trees s3
rm "$vd/sub/b.go"
expect "S-3: 上流のファイルが同梱にないと 1" 1 "sub/b.go" "上流にだけある" -- \
  --upstream-dir "$up" --vendored-dir "$vd"

trees s4
printf 'package raft\n\nfunc a() int { return 2 }\n' >"$vd/a.go"
expect "S-4: raft.go 以外のファイルを 1 バイト変えると 1" 1 "a.go" "内容" -- \
  --upstream-dir "$up" --vendored-dir "$vd"

trees s5
mkdir -p "$vd/tla"
cp "$up/tla/x.sh" "$vd/tla/x.sh"
expect "S-5: 外したパスが同梱に残っていると 1" 1 "tla/x.sh" "外したはずのファイルがある" -- \
  --upstream-dir "$up" --vendored-dir "$vd"

trees s6
printf '\nvar patched = true\n' >>"$vd/raft.go"
expect "S-6: raft.go に PATCHES.md にない変更があると 1" 1 "var patched = true" "一致しない" -- \
  --upstream-dir "$up" --vendored-dir "$vd"

trees s7
cp "$up/raft.go" "$vd/raft.go"
expect "S-7: raft.go にパッチが当たっていないと 1" 1 "パッチが当たっていない" -- \
  --upstream-dir "$up" --vendored-dir "$vd"

trees s8
printf '\nfunc jitter() int { return globalRand.Intn(3) }\n' >>"$up/a.go"
cp "$up/a.go" "$vd/a.go"
expect "S-8: 上流の別のファイルで globalRand を使うと 1" 1 "上流の乱数の使い方" "a.go" -- \
  --upstream-dir "$up" --vendored-dir "$vd"

trees s9
git -C "$vd" init --quiet
git -C "$vd" add -A
git -C "$vd" commit --quiet -m vendored
expect "S-9 の前提: Git で追跡した正しい同梱の木は 0" 0 -- \
  --upstream-dir "$up" --vendored-dir "$vd"
printf 'a.go\n.gitignore\n' >"$vd/.gitignore"
git -C "$vd" rm --quiet --cached a.go
git -C "$vd" commit --quiet -m "untrack a.go"
expect "S-9: 作業ツリーにあっても追跡から外れたファイルは「上流にだけある」で 1" 1 "a.go" "上流にだけある" -- \
  --upstream-dir "$up" --vendored-dir "$vd"
trees s9b
git -C "$vd" init --quiet
git -C "$vd" add -A
git -C "$vd" commit --quiet -m vendored
printf 'package raft\n' >"$vd/untracked.go"
expect "S-9: 追跡されていないファイルは無視して 0" 0 -- \
  --upstream-dir "$up" --vendored-dir "$vd"

trees s10
expect "S-10: 未知のオプションは 2" 2 "不明な引数" -- --unknown
expect "S-10: オプションの値がないと 2" 2 -- --upstream-dir
expect "S-10: 上流の木がないと 2" 2 -- --upstream-dir "$tmp/none" --vendored-dir "$vd"
awk '/^## 当てたパッチ/ { skip = 1 } /^## 次の節/ { skip = 0 } !skip' "$vd/PATCHES.md" >"$tmp/patches.md"
cp "$tmp/patches.md" "$vd/PATCHES.md"
expect "S-10: PATCHES.md の diff ブロックがないと 1" 1 "パッチの記載が見つからない" -- \
  --upstream-dir "$up" --vendored-dir "$vd"

trees s10b
snapshot() { (cd "$tmp/s10b" && find . -type f -exec cksum {} + | LC_ALL=C sort); }
before="$(snapshot)"
bash "$script" --upstream-dir "$up" --vendored-dir "$vd" >/dev/null 2>&1 || true
after="$(snapshot)"
if [ "$before" = "$after" ]; then
  pass "比べる木のファイルを書き換えない"
else
  fail "比べる木のファイルを書き換えない"
fi

# --- 本物の上流との比較（R-1） ---------------------------------------------------------
# go が PATH にないときは、GitHub のランナーのイメージが設定する GOROOT_1_*_X64 のうち
# 版がいちばん新しいものを使う。
if ! command -v go >/dev/null 2>&1; then
  goroot_var="$(env | sed -n 's/^\(GOROOT_1_[0-9]*_X64\)=.*/\1/p' | sort -t_ -k3,3n | tail -n 1)"
  if [ -n "$goroot_var" ] && [ -x "${!goroot_var}/bin/go" ]; then
    export PATH="${!goroot_var}/bin:$PATH"
    echo "# go: $goroot_var の ${!goroot_var}/bin/go を使う"
  fi
fi
if command -v go >/dev/null 2>&1; then
  echo "# go: $(go version)"
  expect "R-1: 上流 v3.7.0 との比較（verify-patch.sh --with-tests が 0）" 0 "パッチの行数:" -- --with-tests
elif [ "${GITHUB_ACTIONS:-}" = "true" ]; then
  fail "R-1: 上流 v3.7.0 との比較（go がない。CI では飛ばさない）"
else
  echo "ok   - R-1: 上流 v3.7.0 との比較（対象なし: go がない）"
fi

if [ "$failures" -ne 0 ]; then
  echo "$failures 件失敗" >&2
  exit 1
fi
echo "すべて成功"
