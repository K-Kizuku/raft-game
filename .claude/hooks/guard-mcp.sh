#!/usr/bin/env bash
# PreToolUse（GitHub の MCP のマージ・自動マージ・ブランチの更新・レビューの投稿）の
# hook（Issue #5）。orchestrator 以外からの呼び出しを拒否する。
# 想定外のエラーでは終了コード 2（拒否）にする（fail-closed。lib.sh の hook_on_exit）。
set -euo pipefail
# lib.sh を読む前の失敗も拒否にする（lib.sh を読むと、lib.sh の trap に置き換わる）
trap 'echo "[guard] 拒否（内部エラー）: lib.sh を読み込めない" >&2; exit 2' EXIT

# （入れ子のコマンド置換の内側の失敗は伝わらないので、1 つずつ検査する）
here="$(dirname "${BASH_SOURCE[0]}")" || exit 2
here="$(cd "$here" && pwd)" || exit 2
# shellcheck source=lib.sh disable=SC1091
. "$here/lib.sh"

hook_read_input
CWD="$(hook_cwd)" || internal_error "作業ディレクトリが分からない"
detect_role "$CWD"

tool="$(hook_field '.tool_name')" || internal_error "ツールの名前を読めない"
if [ "$ROLE" != "orchestrator" ]; then
  deny "${tool:-この MCP のツール}（マージ・自動マージ・ブランチの更新・レビューの投稿）は Orchestrator だけが使う"
fi
allow
