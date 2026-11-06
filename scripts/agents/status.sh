#!/usr/bin/env bash
# 動いている Worker・DEV_SLOT・Worktree の一覧を出す（Issue #5）。
#
# 使い方: scripts/agents/status.sh
set -euo pipefail

# shellcheck disable=SC2034 # lib.sh の die などが使う
PROG="status"
# shellcheck source=lib.sh disable=SC1091
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

case "${1:-}" in
  -h | --help)
    usage_from_header "$0"
    exit 0
    ;;
  '') ;;
  *) die "不明な引数: $1" ;;
esac

need git jq
root="$(main_root)"
cd "$root"

echo "## Worker（.agents/run）"
any=0
for f in "$root"/.agents/run/issue-*.json; do
  [ -f "$f" ] || continue
  any=1
  pid="$(jq -r '.pid // empty' "$f")"
  if pid_alive "$pid"; then st="running"; else st="stopped"; fi
  jq -r --arg st "$st" '"issue-\(.issue)\t\($st)\tpid=\(.pid)\tmode=\(.mode)\tslot=\(.slot)\tbranch=\(.branch)\tsession=\(.session)\tstarted_at=\(.started_at)"' "$f"
done
[ "$any" = 1 ] || echo "（なし）"

echo
echo "## DEV_SLOT"
slots="$("$AGENTS_DIR/../dev-slot.sh" list)"
if [ -n "$slots" ]; then printf '%s\n' "$slots"; else echo "（なし）"; fi

echo
echo "## Worktree"
git worktree list
