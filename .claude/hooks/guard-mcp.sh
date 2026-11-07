#!/usr/bin/env bash
# PreToolUse（GitHub の MCP のマージ・自動マージ・ブランチの更新・レビューの投稿）の
# hook（Issue #5）。orchestrator 以外からの呼び出しを拒否する。
# 想定外のエラーでは終了コード 2（拒否）にする（fail-closed。lib.sh の hook_on_exit）。
set -euo pipefail
# lib.sh を読む前の失敗も拒否にする（lib.sh を読むと、lib.sh の trap に置き換わる）
trap 'echo "[guard] 拒否（内部エラー）: lib.sh を読み込めない" >&2; exit 2' EXIT

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
allow
