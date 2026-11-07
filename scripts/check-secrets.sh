#!/usr/bin/env bash
# gitleaks で git の履歴にシークレットがないかを検査する（T-009 / Issue #17）。
#
# 使い方: bash scripts/check-secrets.sh（引数なし）。make check-secrets と CI の secrets ジョブが呼ぶ。
# 終了コード: 0 = 見つからなかった
#             1 = 見つかった・自己テストが失敗した・.gitleaksignore の書式が不正
#             2 = 環境の問題（go も版の合う gitleaks もない・浅いクローン・git のリポジトリの外）
#
# - gitleaks の版の正本は下の GITLEAKS_VERSION の 1 か所（ci.yml・Makefile には書かない）。
#   版を上げるときはこの 1 行だけを変え、.gitleaksignore を作り直す（.gitleaks.toml のコメント）。
# - PATH に同じ版の gitleaks があればそれを使い、なければ go install で一時ディレクトリにビルドする。
#   ビルド済みのバイナリのダウンロードとサードパーティのアクションは使わない。
# - HEAD から辿れる履歴だけを検査する（--log-opts=HEAD）。まだコミットしていない変更は見ない。
# - 見つかった値はログに出さない（--redact。CI のログは公開される）。
#   --verbose は使わない（--redact は各検出の値だけを隠し、--verbose はその前後も出すので、
#   同じ行に並んだ別のシークレットの値が周辺の表示から漏れる）。見つかった場所は、
#   値を含まない項目（ファイル・行・列・規則・指紋）だけのテンプレートでレポートを一時ディレクトリに作って出す。
#   レポートは成果物として上げず、trap で消す。
# - bash 3.2（macOS）で動く書き方にする（mapfile・連想配列・空の配列と set -u を避ける）。
#   日本語の直前の変数は ${name} と書く（bash 3.2 は続く多バイト文字を変数名の一部として読むことがある）。
set -euo pipefail

GITLEAKS_VERSION="v8.30.1"

die_env() {
  echo "check-secrets: $*" >&2
  exit 2
}

# --- 前提の確認 -------------------------------------------------------------
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
if ! root="$(git -C "$script_dir" rev-parse --show-toplevel 2>/dev/null)"; then
  die_env "git のリポジトリの外では検査できない（${script_dir}）"
fi
if [ "$(git -C "$root" rev-parse --is-shallow-repository)" = "true" ]; then
  die_env "浅いクローンでは履歴を検査できない（CI では fetch-depth: 0。手元では git fetch --unshallow）"
fi

tmp="$(mktemp -d "${TMPDIR:-/tmp}/check-secrets.XXXXXX")"
trap 'rm -rf "$tmp"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

# --- .gitleaksignore の書式の確認 -------------------------------------------
# 各行は <コミット>:<ファイル>:<規則>:<行>。コミットは origin/main（なければ HEAD）の祖先に限る。
# PR のブランチの途中のコミットは、スカッシュでマージすると main に残らないため。
ignore_file="$root/.gitleaksignore"
if [ ! -f "$ignore_file" ]; then
  echo "check-secrets: .gitleaksignore がない（${ignore_file}）" >&2
  exit 1
fi
base="HEAD"
if git -C "$root" rev-parse --verify --quiet "refs/remotes/origin/main^{commit}" >/dev/null; then
  base="origin/main"
fi
bad=0
lineno=0
while IFS= read -r line || [ -n "$line" ]; do
  lineno=$((lineno + 1))
  line="${line%$'\r'}"
  case "$line" in
    "" | "#"*) continue ;;
  esac
  if ! printf '%s\n' "$line" | grep -Eq '^[0-9a-f]{40}:[^:]+:[a-z0-9-]+:[0-9]+$'; then
    echo "check-secrets: .gitleaksignore:$lineno: 指紋の書式（<コミット>:<ファイル>:<規則>:<行>）でない: $line" >&2
    bad=1
    continue
  fi
  commit="${line%%:*}"
  if ! git -C "$root" merge-base --is-ancestor "$commit" "$base" 2>/dev/null; then
    echo "check-secrets: .gitleaksignore:$lineno: コミットが $base の祖先でない（足してよいのは main にあるコミットの指紋だけ）: $line" >&2
    bad=1
  fi
done <"$ignore_file"
if [ "$bad" -ne 0 ]; then
  exit 1
fi

# --- gitleaks の用意 --------------------------------------------------------
want="${GITLEAKS_VERSION#v}"
gitleaks=""
if command -v gitleaks >/dev/null 2>&1; then
  have="$(gitleaks version 2>/dev/null | head -n 1 || true)"
  have="${have#v}"
  if [ "$have" = "$want" ]; then
    gitleaks="$(command -v gitleaks)"
  else
    echo "check-secrets: PATH の gitleaks ${have:-（版が不明）} は固定の版 $GITLEAKS_VERSION と違うので使わない"
  fi
fi
if [ -z "$gitleaks" ]; then
  if ! command -v go >/dev/null 2>&1; then
    die_env "go がない。server/go.mod の Go を入れるか、gitleaks $GITLEAKS_VERSION を PATH に置く"
  fi
  echo "check-secrets: gitleaks $GITLEAKS_VERSION を go install でビルドする（初回は時間がかかる）"
  if ! GOBIN="$tmp/bin" GOTOOLCHAIN="${GOTOOLCHAIN:-auto}" go install "github.com/zricethezav/gitleaks/v8@${GITLEAKS_VERSION}"; then
    die_env "gitleaks $GITLEAKS_VERSION をビルドできなかった（Go のモジュールのプロキシに届かないなど）"
  fi
  gitleaks="$tmp/bin/gitleaks"
fi

# --- 自己テスト -------------------------------------------------------------
# .gitleaks.toml が検出を止めていないことを、既定の規則（github-pat）が必ず検出する偽のトークンで確かめる。
# 偽のトークンは実行時に組み立てる（このファイルに検出される形の文字列を書くと、本物の検査で引っかかるため）。
st="$tmp/selftest"
mkdir -p "$st"
git -C "$st" -c init.defaultBranch=main init -q
hash="$(printf 'raft-game check-secrets self-test\n' | git hash-object --stdin)"
prefix="ghp"
token="${prefix}_$(printf '%s' "$hash" | cut -c 1-36)"
printf 'token = "%s"\n' "$token" >"$st/selftest.txt"
git -C "$st" add -f selftest.txt
git -C "$st" \
  -c user.name=check-secrets \
  -c user.email=check-secrets@example.invalid \
  -c commit.gpgsign=false \
  -c core.hooksPath=/dev/null \
  commit -q -m "self-test"

set +e
"$gitleaks" git \
  --config "$root/.gitleaks.toml" \
  --log-opts=HEAD \
  --redact \
  --no-banner \
  --exit-code 1 \
  "$st" >"$tmp/selftest.log" 2>&1
rc=$?
set -e
if [ "$rc" -ne 1 ]; then
  cat "$tmp/selftest.log" >&2
  echo "check-secrets: 自己テストの失敗: .gitleaks.toml が検出を止めている（偽のトークンを検出しなかった。gitleaks の終了コード ${rc}）" >&2
  exit 1
fi
echo "check-secrets: 自己テスト OK（偽のトークンを検出した）"

# --- 本物の検査 -------------------------------------------------------------
# 見つかったものの一覧は、値を含まない項目だけのテンプレートで出す（.Secret・.Match・.Line・
# .Message などを足さない。--redact でも、それらには周辺の文字や別のシークレットが残りうる）。
report_template="$tmp/report.tmpl"
report="$tmp/report.txt"
printf '%s' '{{ range . }}  {{ .File }}:{{ .StartLine }}:{{ .StartColumn }}  rule={{ .RuleID }}  fingerprint={{ .Fingerprint }}{{ "\n" }}{{ end }}' >"$report_template"
set +e
"$gitleaks" git \
  --config "$root/.gitleaks.toml" \
  --gitleaks-ignore-path "$root/.gitleaksignore" \
  --log-opts=HEAD \
  --redact \
  --no-banner \
  --exit-code 1 \
  --report-format template \
  --report-template "$report_template" \
  --report-path "$report" \
  "$root"
rc=$?
set -e

if [ "$rc" -eq 0 ]; then
  echo "check-secrets: OK（HEAD の履歴にシークレットは見つからなかった）"
  exit 0
fi

if [ -s "$report" ]; then
  echo "check-secrets: 見つかった場所（<ファイル>:<行>:<列>・規則・指紋。値は出さない）:" >&2
  cat "$report" >&2
fi
cat >&2 <<'EOF'

check-secrets: シークレットらしいものが見つかった（値は出さない）。対応の手順:
  - 偽陽性（テストの偽の値など）なら、その行に gitleaks:allow のコメントを付けるか、
    値を実行時に組み立てる（例: "x".repeat(32)）。
  - 途中のコミットで出たなら、そのコミットを直す（agent/issue-* なら --force-with-lease）。
  - .gitleaksignore に足してよいのは main にあるコミットの指紋だけ。
  - 本物のシークレットなら、まずそれを無効化する（履歴から消しても、公開されたものは取り消せない）。
EOF
if [ "$rc" -ne 1 ]; then
  echo "check-secrets: gitleaks の終了コード $rc" >&2
fi
exit 1
