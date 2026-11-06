#!/usr/bin/env bash
# PreToolUse（GitHub の MCP のマージ・自動マージ・ブランチの更新・レビューの投稿）の
# hook（Issue #5）。orchestrator 以外からの呼び出しを拒否する。
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh disable=SC1091
. "$here/lib.sh"

hook_read_input
CWD="$(hook_cwd)"
detect_role "$CWD"

tool="$(hook_field '.tool_name')"
if [ "$ROLE" != "orchestrator" ]; then
  deny "${tool:-この MCP のツール}（マージ・自動マージ・ブランチの更新・レビューの投稿）は Orchestrator だけが使う"
fi
exit 0
