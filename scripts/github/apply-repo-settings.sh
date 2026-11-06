#!/usr/bin/env bash
# リポジトリの設定と Ruleset（.github/rulesets/main.json）を GitHub に反映する（Issue #5）。
# オーナーが手で実行する（AI エージェントの hooks は …/rulesets への書き込みを拒否する）。
#
# 使い方: scripts/github/apply-repo-settings.sh [--dry-run] [--repo OWNER/NAME]
#   --dry-run   送る内容（メソッド・パス・本文）を出すだけで、書き込まない（読み取りの API は呼ぶ）
#   --repo R    対象のリポジトリ（既定: gh repo view の nameWithOwner）
#
# 行うこと（冪等）:
#   1. PATCH repos/R: デフォルトブランチを main に、マージの方式を squash だけに、
#      マージ後にブランチを自動で削除する
#   2. GET repos/R/rulesets で同じ名前の Ruleset を探し、あれば PUT repos/R/rulesets/<id>、
#      なければ POST repos/R/rulesets
# - AGENT_ROLE が worker・reviewer・orchestrator のときは拒否する（人だけが実行する）
set -euo pipefail

die() {
  echo "apply-repo-settings: $*" >&2
  exit 1
}

usage() {
  sed -n '2,/^set -euo/p' "$0" | sed '$d' | sed 's/^# \{0,1\}//'
}

dry_run=0
repo=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --dry-run) dry_run=1 ;;
    --repo)
      [ "$#" -ge 2 ] || die "--repo には値が必要"
      repo="$2"
      shift
      ;;
    --repo=*) repo="${1#--repo=}" ;;
    -h | --help)
      usage
      exit 0
      ;;
    *) die "不明な引数: $1" ;;
  esac
  shift
done

case "${AGENT_ROLE:-}" in
  worker | reviewer | orchestrator)
    die "AGENT_ROLE=${AGENT_ROLE} では実行できない（オーナーが手で実行する）"
    ;;
esac

command -v jq >/dev/null 2>&1 || die "jq が見つからない"
command -v gh >/dev/null 2>&1 || die "gh が見つからない"

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ruleset_file="$here/../../.github/rulesets/main.json"
[ -f "$ruleset_file" ] || die "$ruleset_file がない"
jq -e . "$ruleset_file" >/dev/null || die "$ruleset_file が JSON ではない"
name="$(jq -r '.name' "$ruleset_file")"
if [ -z "$name" ] || [ "$name" = "null" ]; then
  die "$ruleset_file に name がない"
fi

if [ -z "$repo" ]; then
  repo="$(gh repo view --json nameWithOwner -q .nameWithOwner)" || die "リポジトリが分からない（--repo で指定する）"
fi
case "$repo" in
  */*) ;;
  *) die "--repo は OWNER/NAME の形で指定する（$repo）" ;;
esac

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

# send <メソッド> <パス> <本文のファイル>
send() {
  if [ "$dry_run" = 1 ]; then
    echo "$1 $2"
    jq . "$3"
    echo
  else
    gh api -X "$1" "$2" --input "$3" >/dev/null || die "$1 $2 に失敗した"
    echo "反映した: $1 $2"
  fi
}

jq -n '{default_branch: "main", allow_squash_merge: true, allow_merge_commit: false,
  allow_rebase_merge: false, delete_branch_on_merge: true}' >"$tmp/settings.json"
send PATCH "repos/$repo" "$tmp/settings.json"

existing="$(gh api "repos/$repo/rulesets" --paginate)" || die "Ruleset の一覧を読めない（repos/$repo/rulesets）"
id="$(printf '%s' "$existing" | jq -s -r --arg n "$name" '[.[] | if type == "array" then .[] else . end
  | select(.name == $n and .source_type != "Organization")] | first | .id // empty')"
if [ -n "$id" ]; then
  send PUT "repos/$repo/rulesets/$id" "$ruleset_file"
else
  send POST "repos/$repo/rulesets" "$ruleset_file"
fi

if [ "$dry_run" = 1 ]; then
  echo "（--dry-run: 書き込んでいない）"
fi
