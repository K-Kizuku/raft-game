#!/usr/bin/env bash
# gh を、AI 用の GitHub App のインストールのトークンで実行するラッパー（Issue #282。ADR-0021）。
#
# 使い方: scripts/agents/gh-app.sh <gh のサブコマンドと引数…>
#
# - 呼ぶたびに有効なトークン（lib.sh の app_token。キャッシュが 50 分以内なら使い回す）を GH_TOKEN に入れて
#   本物の gh を exec する。1 時間を超えて動く Worker でもトークンが切れない
# - spawn-worker.sh は、これを呼ぶ小さな gh（.agents/bin/gh-app/gh）を Worker の PATH の先頭に置く
# - App のトークンを使えない（App の設定がない・鍵がない・権限が広い・作れない）ときは、理由を標準エラーに
#   1 行出して（設定がないときは出さずに）、本物の gh をそのまま実行する（gh のログイン＝オーナーのトークン）
# - そのまま実行する場合: GITHUB_TOKEN か GH_TOKEN がすでにある、gh auth のサブコマンド、
#   役割が worker・orchestrator でない、このラッパーの中から呼ばれた（AGENT_GH_APP_ACTIVE）
# - 本物の gh: AGENT_GH_REAL、なければ PATH の gh（AGENT_GH_SHIM_DIR のディレクトリを除く）
# - トークンと鍵の中身は標準出力・標準エラーに出さない。git push（SSH）には関係しない
set -euo pipefail

# shellcheck disable=SC2034 # lib.sh の info などが使う
PROG="gh-app"
# shellcheck source=lib.sh disable=SC1091
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

# find_real_gh: 本物の gh のパスを出す。見つからなければ 1。
find_real_gh() {
  local d self_dir="${AGENT_GH_SHIM_DIR:-}" old_ifs="$IFS"
  if [ -n "${AGENT_GH_REAL:-}" ] && [ -x "$AGENT_GH_REAL" ] && [ ! -d "$AGENT_GH_REAL" ]; then
    case "$AGENT_GH_REAL" in
      "${self_dir:-/nonexistent}/gh") ;;
      *)
        printf '%s\n' "$AGENT_GH_REAL"
        return 0
        ;;
    esac
  fi
  IFS=:
  set -f
  # shellcheck disable=SC2086 # PATH を : で分ける（関数の中なので gh の引数は変わらない）
  set -- $PATH
  set +f
  IFS="$old_ifs"
  for d in "$@"; do
    [ -n "$d" ] || continue
    [ -z "$self_dir" ] || [ "$d" != "$self_dir" ] || continue
    if [ -x "$d/gh" ] && [ ! -d "$d/gh" ]; then
      printf '%s\n' "$d/gh"
      return 0
    fi
  done
  return 1
}

real="$(find_real_gh)" || {
  echo "gh-app: 本物の gh が見つからない（AGENT_GH_REAL で指定する）" >&2
  exit 127
}
export AGENT_GH_REAL="$real"

if [ -n "${AGENT_GH_APP_ACTIVE:-}" ] || [ -n "${GITHUB_TOKEN:-}" ] || [ -n "${GH_TOKEN:-}" ] ||
  [ "${1:-}" = auth ] || ! app_token_wanted; then
  exec "$real" "$@"
fi

# 中で呼ぶ gh（app_token の gh api や gh の拡張）がこのラッパーをもう一度通らないように
export AGENT_GH_APP_ACTIVE=1
t=""
rc=0
t="$(app_token)" || rc=$?
if [ "$rc" = 0 ] && [ -n "$t" ]; then
  export GH_TOKEN="$t"
fi
exec "$real" "$@"
