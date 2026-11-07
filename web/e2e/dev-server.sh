#!/usr/bin/env bash
# E2E テスト（T-506）の開発のサーバー。playwright.config.ts の webServer から起動する。
#
# 使い方: web/e2e/dev-server.sh <ログのファイル> <Vite のポート>
#   scripts/dev-env.sh dev（make dev と同じ。DEV_SLOT のポート）を Go の standalone・モックなし
#   （VITE_GAME_MOCK=off）で起動し、出力をログのファイルに書く。
#   サーバーが自分で終わった（起動に失敗した）とき、または準備ができる前に止められた（Playwright の
#   待ち時間切れ）ときは、ログの末尾を標準エラーに出して失敗にする。
#
# 環境変数: DEV_SLOT（必須。playwright.config.ts が渡す）
set -uo pipefail

log="${1:?ログのファイルを指定する}"
port="${2:?Vite のポートを指定する}"
root="$(cd "$(dirname "$0")/../.." && pwd -P)"

mkdir -p "$(dirname "$log")"
: >"$log"

# show_tail <理由>: ログの末尾を標準エラーに出す。
show_tail() {
  {
    echo "e2e: 開発のサーバー: $1（ログ: $log）"
    echo "----- ログの末尾 -----"
    tail -n 40 "$log"
    echo "----------------------"
  } >&2
}

ready() {
  command -v curl >/dev/null 2>&1 || return 1
  curl -fs -o /dev/null --max-time 2 "http://127.0.0.1:$port/"
}

stopping=0
was_ready=0
pid=""
on_stop() {
  stopping=1
  if [ -n "$pid" ]; then kill -TERM "$pid" 2>/dev/null || true; fi
}
trap on_stop TERM INT

VITE_GAME_MOCK=off DEV_BACKEND=standalone bash "$root/scripts/dev-env.sh" dev >>"$log" 2>&1 &
pid=$!

# 子が生きている間、1 秒ごとに準備ができたかを確かめる（止められたときにログの末尾を出すかを決めるため）。
while kill -0 "$pid" 2>/dev/null; do
  if [ "$stopping" = 0 ] && [ "$was_ready" = 0 ] && ready; then was_ready=1; fi
  sleep 1 &
  wait "$!" 2>/dev/null || true
done
rc=0
wait "$pid" || rc=$?

if [ "$stopping" = 1 ]; then
  if [ "$was_ready" = 0 ]; then show_tail "準備ができる前に止められた"; fi
  exit 0
fi
show_tail "起動に失敗した（終了コード $rc）"
if [ "$rc" = 0 ]; then rc=1; fi
exit "$rc"
