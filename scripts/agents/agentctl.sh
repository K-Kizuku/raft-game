#!/usr/bin/env bash
# tools/agentctl を .agents/bin/agentctl にビルドして実行するラッパー（Issue #5）。
#
# 使い方: scripts/agents/agentctl.sh <agentctl のサブコマンドと引数…>
#         （agentctl 自身の使い方は scripts/agents/agentctl.sh help）
#
# - .agents/bin/agentctl がないか、tools/agentctl の中にそれより新しいファイルがあれば作り直す
# - ビルドと実行の場所はメインの作業ツリー（git-common-dir の親）
# - GITHUB_TOKEN も GH_TOKEN もなければ、gh auth token の値を GH_TOKEN に入れて渡す（トークンは出力しない）
set -euo pipefail

# shellcheck disable=SC2034 # lib.sh の die などが使う
PROG="agentctl.sh"
# shellcheck source=lib.sh disable=SC1091
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

case "${1:-}" in
  -h | --help)
    usage_from_header "$0"
    exit 0
    ;;
esac

need git go
root="$(main_root)"
src="$root/tools/agentctl"
bin="$root/.agents/bin/agentctl"
[ -f "$src/go.mod" ] || die "$src/go.mod がない"

if [ ! -x "$bin" ] || [ -n "$(find "$src" -type f -newer "$bin" -print -quit 2>/dev/null)" ]; then
  mkdir -p "$root/.agents/bin"
  go -C "$src" build -o "$bin" . || die "agentctl をビルドできない"
fi
ensure_gh_token
exec "$bin" "$@"
