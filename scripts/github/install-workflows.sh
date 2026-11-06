#!/usr/bin/env bash
# .github/workflow-sources/*.yml を .github/workflows/ に取り込む PR を作る（Issue #2）。
#
# AI エージェントの GitHub の認証には `workflow` 権限がなく、.github/workflows/ を
# 変更できない。これを「有効なワークフローを AI が書き換えられない」仕組みとして使う。
# AI は .github/workflow-sources/ に正本を書き、人がこのスクリプトで取り込む。
#
# 使い方: scripts/github/install-workflows.sh [--dry-run] [--base main]
#   --dry-run   取り込む差分の一覧だけを出す（何も変えない）
#   --base B    取り込みの元と PR の宛先のブランチ（既定: main）
#
# - origin/<base> の .github/workflow-sources/*.yml を、origin/<base> から作った
#   human/install-workflows-<YYYYMMDD-HHMMSS> ブランチの .github/workflows/ にコピーし、
#   コミット・push・`gh pr create` する
# - ソースにないファイルは .github/workflows/ から消さない
# - 差分がなければ「取り込むものはない」と出して終了コード 0
# - 作業ツリーが汚れていたら止める
# - AGENT_ROLE が worker・reviewer・orchestrator のときは拒否する（人だけが実行する）
set -euo pipefail

SRC_DIR=".github/workflow-sources"
DST_DIR=".github/workflows"

usage() {
  sed -n '2,/^set -euo/p' "$0" | sed '$d' | sed 's/^# \{0,1\}//'
}

die() {
  echo "install-workflows: $*" >&2
  exit 1
}

dry_run=0
base="main"
while [ "$#" -gt 0 ]; do
  case "$1" in
    --dry-run) dry_run=1 ;;
    --base)
      [ "$#" -ge 2 ] || die "--base には値が必要"
      base="$2"
      shift
      ;;
    --base=*) base="${1#--base=}" ;;
    -h | --help)
      usage
      exit 0
      ;;
    *) die "不明な引数: $1" ;;
  esac
  shift
done
[ -n "$base" ] || die "--base が空"

case "${AGENT_ROLE:-}" in
  worker | reviewer | orchestrator)
    die "AGENT_ROLE=${AGENT_ROLE} では実行できない（人だけが実行する）"
    ;;
esac

top="$(git rev-parse --show-toplevel 2>/dev/null)" || die "Git リポジトリの中で実行する"
cd "$top"

# 作業ツリーが汚れていたら止める（追跡中のファイルの変更と、取り込み先の未追跡ファイル）。
if [ -n "$(git status --porcelain --untracked-files=no)" ] ||
  [ -n "$(git ls-files --others --exclude-standard -- "$DST_DIR")" ]; then
  git status --short >&2
  die "作業ツリーが汚れている。コミットするか片付けてから実行する"
fi

git fetch --quiet origin "$base" || die "origin/$base を取得できない"
ref="origin/$base"

# origin/<base> のソースと取り込み先を比べ、差分のあるファイルを集める。
changed=()
while IFS= read -r -d '' src; do
  case "$src" in
    *.yml) ;;
    *) continue ;;
  esac
  name="${src##*/}"
  dst="$DST_DIR/$name"
  src_blob="$(git rev-parse "$ref:$src")"
  if dst_blob="$(git rev-parse --quiet --verify "$ref:$dst" 2>/dev/null)"; then
    if [ "$src_blob" = "$dst_blob" ]; then
      continue
    fi
    echo "更新: $dst"
  else
    echo "追加: $dst"
  fi
  changed+=("$name")
done < <(git ls-tree -z --name-only "$ref" -- "$SRC_DIR/")

if [ "${#changed[@]}" -eq 0 ]; then
  echo "取り込むものはない（$ref の $DST_DIR は $SRC_DIR と同じ）"
  exit 0
fi

if [ "$dry_run" -eq 1 ]; then
  echo "--dry-run: 何も変更していない"
  exit 0
fi

branch="human/install-workflows-$(date +%Y%m%d-%H%M%S)"
orig="$(git symbolic-ref --quiet --short HEAD || git rev-parse HEAD)"
pushed=0

# 途中（コミット・push・gh pr create）で失敗したら、元のブランチに戻し、
# 後片付けの方法を表示する。
on_exit() {
  local status=$?
  [ "$status" -ne 0 ] || return 0
  if git checkout --quiet "$orig"; then
    echo "install-workflows: 失敗した（終了コード $status）。元のブランチ $orig に戻った" >&2
  else
    echo "install-workflows: 失敗した（終了コード $status）。元のブランチ $orig に戻れなかった" >&2
  fi
  echo "install-workflows: 後片付けをするには、次を実行する:" >&2
  echo "  git branch -D $branch" >&2
  if [ "$pushed" -eq 1 ]; then
    echo "  git push origin --delete $branch" >&2
  fi
  return "$status"
}

git switch --quiet --no-track -c "$branch" "$ref"
trap on_exit EXIT
mkdir -p "$DST_DIR"
for name in "${changed[@]}"; do
  git show "$ref:$SRC_DIR/$name" >"$DST_DIR/$name"
  git add -- "$DST_DIR/$name"
done

list="$(printf -- "- \`%s\`\n" "${changed[@]}")"
git commit --quiet -m "ci: $SRC_DIR のワークフローを $DST_DIR に取り込む" \
  -m "$list"
git push --quiet -u origin "$branch"
pushed=1

body="\`$SRC_DIR/\` のワークフローを \`$DST_DIR/\` に取り込む（\`scripts/github/install-workflows.sh\` で作成）。

$list"
gh pr create --base "$base" --head "$branch" \
  --title "ci: ワークフローを取り込む ($branch)" --body "$body"

trap - EXIT
git checkout --quiet "$orig" || echo "install-workflows: 元のブランチ $orig に戻れなかった" >&2
echo "作成したブランチ: $branch"
