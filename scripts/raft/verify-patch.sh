#!/usr/bin/env bash
# 同梱の etcd-io/raft（server/third_party/etcd-raft）と上流の版を比べ、違いが
# PATCHES.md の「外したファイル」と「当てたパッチ」だけであることを確かめる
# （T-003, #11。ADR-0005 の「運用」、DesignDoc §14 の「パッチの検証」）。
#
# 使い方: scripts/raft/verify-patch.sh [--upstream-dir DIR] [--vendored-dir DIR] [--with-tests]
#   --upstream-dir DIR   上流のモジュールの木を DIR から読む（取得しない。h1 の照合もしない）。テスト用
#   --vendored-dir DIR   同梱の木（既定: <リポジトリのルート>/server/third_party/etcd-raft）
#   --with-tests         比較が通ったあとで、同梱の上流のテストとパッチのテストも実行する
# 終了コード: 0 = 一致、1 = 差分がある、2 = 使い方の誤り・環境の不備
#
# リポジトリの中のファイルは書き換えない。macOS の bash 3.2 と BSD のコマンドでも動く
# 書き方にする（mapfile・連想配列・sed -i・readlink -f を使わない）。
set -euo pipefail

# --- 定数 -------------------------------------------------------------------
# PATCHES.md の「外したファイル」の表と食い違わないようにする。表を変えるときは、
# 同じ PR でこの定数も変える。
EXCLUDED_DIRS=("tla" "scripts" ".github") # この下のファイルをすべて外した
EXCLUDED_FILES=("Makefile")                # 最上位のファイルを外した
VENDORED_ONLY=("PATCHES.md")               # 同梱の側にだけあってよいファイル
PATCHED_FILE="raft.go"                     # パッチを当てたファイル
MODULE="go.etcd.io/raft/v3"
RAND_USE='globalRand.Intn(r.electionTimeout)'

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

usage() {
  sed -n '6,10s/^# \{0,1\}//p' "${BASH_SOURCE[0]}" >&2
}
die_usage() {
  echo "verify-patch: $1" >&2
  usage
  exit 2
}
die_env() {
  echo "verify-patch: $1" >&2
  exit 2
}

upstream_dir=""
vendored_dir=""
vendored_given=0
with_tests=0
while [ $# -gt 0 ]; do
  case "$1" in
    --upstream-dir)
      [ $# -ge 2 ] || die_usage "--upstream-dir に値がない"
      upstream_dir="$2"
      shift 2
      ;;
    --vendored-dir)
      [ $# -ge 2 ] || die_usage "--vendored-dir に値がない"
      vendored_dir="$2"
      vendored_given=1
      shift 2
      ;;
    --with-tests)
      with_tests=1
      shift
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      die_usage "不明な引数: $1"
      ;;
  esac
done

if [ -z "$vendored_dir" ]; then
  vendored_dir="$ROOT/server/third_party/etcd-raft"
fi
[ -d "$vendored_dir" ] || die_env "同梱の木がない: $vendored_dir"
vendored_dir="$(cd "$vendored_dir" && pwd)"
if [ -n "$upstream_dir" ]; then
  [ -d "$upstream_dir" ] || die_env "上流の木がない: $upstream_dir"
  upstream_dir="$(cd "$upstream_dir" && pwd)"
fi
patches_md="$vendored_dir/PATCHES.md"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

failures=0
ng() {
  echo "NG [$1] $2"
  failures=$((failures + 1))
}
# list_lines FILE: ファイルの各行を字下げして出す。
list_lines() {
  sed 's/^/    /' "$1"
}

# PATCHES.md の「取り込んだ版」の表から、項目の値（`…` の中身）を取り出す。
patches_value() {
  [ -f "$patches_md" ] || return 0
  awk -v key="$1" '
    index($0, "| " key " |") == 1 {
      line = $0
      sub("^\\| " key " \\| *`", "", line)
      sub("`.*$", "", line)
      print line
      exit
    }' "$patches_md"
}

# --- 1. 版 -------------------------------------------------------------------
patches_version="$(patches_value "版")"
patches_sum="$(patches_value "モジュールの \`h1:\` ハッシュ")"
version="$patches_version"
if [ "$vendored_given" -eq 0 ]; then
  [ -n "$patches_version" ] || ng "版" "PATCHES.md の「取り込んだ版」の表に「版」がない"
  gomod="$ROOT/server/go.mod"
  [ -f "$gomod" ] || die_env "server/go.mod がない: $gomod"
  # 1 行の require と、require ( … ) の中の行の両方を読む。replace の行は除く。
  gomod_version="$(awk -v m="$MODULE" '
    /=>/ { next }
    {
      line = $0
      sub("^[ \t]*require[ \t]+", "", line)
      sub("^[ \t]+", "", line)
      n = split(line, f, "[ \t]+")
      if (n >= 2 && f[1] == m) { print f[2]; exit }
    }' "$gomod")"
  if [ -z "$gomod_version" ]; then
    ng "版" "server/go.mod に require $MODULE がない"
  else
    if [ -n "$patches_version" ] && [ "$gomod_version" != "$patches_version" ]; then
      ng "版" "server/go.mod の版 $gomod_version と PATCHES.md の版 $patches_version が違う"
    fi
    version="$gomod_version"
  fi
  if ! grep -Eq "^[[:space:]]*(replace[[:space:]]+)?go\.etcd\.io/raft/v3[[:space:]]*=>[[:space:]]*\./third_party/etcd-raft[[:space:]]*$" "$gomod"; then
    ng "版" "server/go.mod に replace $MODULE => ./third_party/etcd-raft がない"
  fi
fi

# --- 2. 上流の取得 -------------------------------------------------------------
if [ -z "$upstream_dir" ]; then
  [ -n "$version" ] || die_env "取得する版がわからない（server/go.mod と PATCHES.md を確かめる）"
  command -v go >/dev/null 2>&1 || die_env "go がない（上流の取得に必要）"
  command -v python3 >/dev/null 2>&1 || die_env "python3 がない（go mod download の JSON の読み取りに必要）"
  mkdir "$tmp/mod"
  json="$tmp/download.json"
  rc=0
  (cd "$tmp/mod" && GOFLAGS=-mod=mod go mod download -json "$MODULE@$version") >"$json" 2>"$tmp/download.err" || rc=$?
  jget() {
    python3 -I -c 'import json,sys
try:
    d = json.load(open(sys.argv[1]))
except Exception:
    d = {}
v = d.get(sys.argv[2]) or ""
print(v)' "$json" "$1"
  }
  dl_error="$(jget Error)"
  upstream_dir="$(jget Dir)"
  dl_sum="$(jget Sum)"
  if [ "$rc" -ne 0 ] || [ -n "$dl_error" ] || [ -z "$upstream_dir" ]; then
    {
      echo "verify-patch: 上流 $MODULE@$version の取得に失敗した（go mod download の終了コード ${rc}）"
      [ -z "$dl_error" ] || echo "  Error: $dl_error"
      cat "$tmp/download.err"
      cat "$json"
      case "$dl_error$(cat "$tmp/download.err")" in
        *toolchain* | *"requires go"* | *"go >="*)
          echo "  Go の版が古い可能性がある。GOTOOLCHAIN=auto で実行する（現在: GOTOOLCHAIN=${GOTOOLCHAIN:-<未設定>}）"
          ;;
      esac
    } >&2
    exit 2
  fi
  [ -d "$upstream_dir" ] || die_env "go mod download が返した Dir がない: $upstream_dir"
  if [ "$dl_sum" != "$patches_sum" ]; then
    ng "上流の取得" "モジュールの h1 ハッシュが PATCHES.md と違う: 取得=$dl_sum PATCHES.md=${patches_sum:-<なし>}"
  fi
fi

# --- 3. ファイルの一覧 ---------------------------------------------------------
is_excluded() {
  local p="$1" d f
  for d in "${EXCLUDED_DIRS[@]}"; do
    case "$p" in "$d"/*) return 0 ;; esac
  done
  for f in "${EXCLUDED_FILES[@]}"; do
    [ "$p" != "$f" ] || return 0
  done
  return 1
}
is_vendored_only() {
  local p="$1" f
  for f in "${VENDORED_ONLY[@]}"; do
    [ "$p" != "$f" ] || return 0
  done
  return 1
}

(cd "$upstream_dir" && find . -type f) | sed 's|^\./||' >"$tmp/upstream.raw"
if git -C "$vendored_dir" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  vendored_source="git ls-files"
  git -C "$vendored_dir" ls-files -z | tr '\0' '\n' >"$tmp/vendored.raw"
else
  vendored_source="find"
  (cd "$vendored_dir" && find . -type f) | sed 's|^\./||' >"$tmp/vendored.raw"
fi

: >"$tmp/upstream.list"
while IFS= read -r p; do
  [ -n "$p" ] || continue
  is_excluded "$p" || printf '%s\n' "$p" >>"$tmp/upstream.list"
done <"$tmp/upstream.raw"

: >"$tmp/vendored.list"
: >"$tmp/vendored.excluded"
while IFS= read -r p; do
  [ -n "$p" ] || continue
  if is_vendored_only "$p"; then
    continue
  elif is_excluded "$p"; then
    printf '%s\n' "$p" >>"$tmp/vendored.excluded"
  else
    printf '%s\n' "$p" >>"$tmp/vendored.list"
  fi
done <"$tmp/vendored.raw"

LC_ALL=C sort -u -o "$tmp/upstream.list" "$tmp/upstream.list"
LC_ALL=C sort -u -o "$tmp/vendored.list" "$tmp/vendored.list"
LC_ALL=C comm -23 "$tmp/upstream.list" "$tmp/vendored.list" >"$tmp/only-upstream"
LC_ALL=C comm -13 "$tmp/upstream.list" "$tmp/vendored.list" >"$tmp/only-vendored"
LC_ALL=C comm -12 "$tmp/upstream.list" "$tmp/vendored.list" >"$tmp/common"

if [ -s "$tmp/only-upstream" ]; then
  ng "ファイルの一覧" "上流にだけある（同梱の木の一覧は ${vendored_source}）:"
  list_lines "$tmp/only-upstream"
fi
if [ -s "$tmp/only-vendored" ]; then
  ng "ファイルの一覧" "同梱にだけある:"
  list_lines "$tmp/only-vendored"
fi
if [ -s "$tmp/vendored.excluded" ]; then
  ng "ファイルの一覧" "外したはずのファイルがある:"
  LC_ALL=C sort "$tmp/vendored.excluded" | sed 's/^/    /'
fi

# --- 4. 内容 -------------------------------------------------------------------
: >"$tmp/differ"
while IFS= read -r p; do
  [ "$p" != "$PATCHED_FILE" ] || continue
  if [ ! -f "$vendored_dir/$p" ] || ! cmp -s "$upstream_dir/$p" "$vendored_dir/$p"; then
    printf '%s\n' "$p" >>"$tmp/differ"
  fi
done <"$tmp/common"
if [ -s "$tmp/differ" ]; then
  ng "内容" "上流と内容が違う（$PATCHED_FILE 以外は 1 バイトも違ってはいけない）:"
  list_lines "$tmp/differ"
fi

# --- 5. パッチ -----------------------------------------------------------------
# 差分から、変わった行（+・- で始まる行。+++ ・--- の見出しを除く）を順に取り出す。
changed_lines() {
  grep -E '^[+-]' | grep -Ev '^(\+\+\+|---) ' || true
}

# PATCHES.md の「## 当てたパッチ」の節の、最初の ```diff ブロックの中身を出す。
awk '
  /^## / { insec = ($0 == "## 当てたパッチ"); if (!insec && found) exit; next }
  insec && !inb && $0 == "```diff" { inb = 1; found = 1; next }
  inb && /^```/ { exit }
  inb { print }
' "$patches_md" 2>/dev/null | changed_lines >"$tmp/patch.expected" || true

patch_lines=0
if [ ! -f "$upstream_dir/$PATCHED_FILE" ] || [ ! -f "$vendored_dir/$PATCHED_FILE" ]; then
  ng "パッチ" "$PATCHED_FILE が上流か同梱の木にない"
elif [ ! -s "$tmp/patch.expected" ]; then
  ng "パッチ" "パッチの記載が見つからない（PATCHES.md の「## 当てたパッチ」の節に \`\`\`diff ブロックがないか、空）"
else
  rc=0
  git diff --no-index --no-color -U0 "$upstream_dir/$PATCHED_FILE" "$vendored_dir/$PATCHED_FILE" \
    >"$tmp/patch.diff" 2>"$tmp/patch.err" || rc=$?
  if [ "$rc" -gt 1 ]; then
    cat "$tmp/patch.err" >&2
    die_env "git diff --no-index に失敗した（終了コード ${rc}）"
  fi
  changed_lines <"$tmp/patch.diff" >"$tmp/patch.actual"
  if cmp -s "$tmp/patch.expected" "$tmp/patch.actual"; then
    patch_lines="$(wc -l <"$tmp/patch.expected" | tr -d ' ')"
  else
    ng "パッチ" "$PATCHED_FILE の変わった行が PATCHES.md のパッチと一致しない"
    echo "  PATCHES.md の記載（$(wc -l <"$tmp/patch.expected" | tr -d ' ') 行）:"
    list_lines "$tmp/patch.expected"
    echo "  上流との実際の差分（$(wc -l <"$tmp/patch.actual" | tr -d ' ') 行）:"
    if [ -s "$tmp/patch.actual" ]; then
      list_lines "$tmp/patch.actual"
    else
      echo "    （なし: パッチが当たっていない）"
    fi
  fi
fi

# --- 6. 上流の乱数の使い方 -------------------------------------------------------
: >"$tmp/rand.uses"
for f in "$upstream_dir"/*.go; do
  [ -f "$f" ] || continue
  case "$f" in *_test.go) continue ;; esac
  grep -nF 'globalRand.' "$f" | sed "s|^|$(basename "$f"):|" >>"$tmp/rand.uses" || true
done
rand_count="$(wc -l <"$tmp/rand.uses" | tr -d ' ')"
if [ "$rand_count" -ne 1 ] || ! grep -qF "$RAND_USE" "$tmp/rand.uses"; then
  ng "上流の乱数の使い方" "上流の globalRand. の利用は「${RAND_USE}」の 1 行だけのはずが、$rand_count 行ある（パッチで覆えない使い方がないかを確かめ、ADR を書く）:"
  list_lines "$tmp/rand.uses"
fi

if [ "$failures" -ne 0 ]; then
  echo "verify-patch: $failures 件の検査に失敗した（上流 $MODULE@${version:-?}、同梱 ${vendored_dir}）" >&2
  exit 1
fi

# --- 7. テスト -----------------------------------------------------------------
if [ "$with_tests" -eq 1 ]; then
  command -v go >/dev/null 2>&1 || die_env "go がない（--with-tests に必要）"
  echo "テスト: go -C $vendored_dir test -count=1 ./..."
  if ! go -C "$vendored_dir" test -count=1 ./...; then
    echo "verify-patch: 同梱の上流のテストが失敗した" >&2
    exit 1
  fi
  echo "テスト: go -C $ROOT/server test -count=1 ./internal/raftcheck/..."
  if ! go -C "$ROOT/server" test -count=1 ./internal/raftcheck/...; then
    echo "verify-patch: パッチのテストが失敗した" >&2
    exit 1
  fi
fi

echo "版: $MODULE@${version:-?}"
echo "ファイル数: $(wc -l <"$tmp/common" | tr -d ' ')（上流と同梱の両方にあるもの。PATCHES.md を除く）"
echo "パッチの行数: ${patch_lines}（変わった行）"
