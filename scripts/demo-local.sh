#!/usr/bin/env bash
# デモの保険: ノート PC で Go（standalone・デモ操作あり）と Vite を起動する（T-504 / make demo-local）。
#
# 使い方:
#   scripts/demo-local.sh [--check] [--dry-run] [--data DIR] [--path P]
#     （オプションなし）  Go（standalone・デモ操作あり）と Vite を起動し、両方が応答したら URL を出す。Ctrl-C で両方を止める
#     --check     起動せず、準備ができているか（ネットワークなしで Go をビルドできるか・依存が入っているか・ポートが空いているか）を確かめる
#     --dry-run   起動せず、実行するはずのコマンドを「+ 」を付けて出す
#     --data DIR  ステージ定義のディレクトリ（islands.json と stages/*.yaml。既定は <作業ツリー>/data）
#     --path P    作業ツリーの根（既定は git rev-parse --show-toplevel）
#     -h, --help  使い方を出して 0 で終わる
#   環境変数:
#     DEV_SLOT    0〜99（未設定なら 0。0 は人が手で使う枠。ポートの計算は scripts/dev-env.sh）
#   終了コード: 成功・--check の合格は 0。使い方の誤り・準備の不足・起動の失敗・Go か Vite が止まったときは 1。
#              Ctrl-C は 130、SIGTERM は 143（scripts/dev-env.sh dev と同じ）
#
# make dev（scripts/dev-env.sh dev）との違い:
#   1. デモ操作を有効にする（Go に DEMO_CONTROLS=1。D・F・N・Space のキー）
#   2. 会場でネットワークがなくても起動できるかを前もって確かめられる（--check）
#   3. Go と Vite の両方が応答するまで待ってから URL を 1 行で出し、どちらかが止まったらもう片方も止めて知らせる
#   4. DEV_SLOT が未設定でも、人が手で使う枠の 0 で起動する（慌てているときに準備を要らなくする）
#
# ポートは scripts/dev-env.sh env から得る（ADR-0019。このスクリプトに式を書かない）。
# bash 3.2（macOS）で動く書き方にする（mapfile・wait -n・${v,,}・空の配列の "${a[@]}" と set -u を避ける）。

set -euo pipefail

PROG="demo-local"

# 待ち時間と間隔（秒）。ゲームの数値ではない。
readonly GO_WAIT_SECONDS=30
readonly WEB_WAIT_SECONDS=60
readonly POLL_INTERVAL=0.5
readonly WATCH_INTERVAL=1
readonly STOP_WAIT_SECONDS=5

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"

usage() {
  cat <<'EOF'
使い方:
  scripts/demo-local.sh [--check] [--dry-run] [--data DIR] [--path P]
    （オプションなし）  Go（standalone・デモ操作あり）と Vite を起動し、両方が応答したら URL を出す。Ctrl-C で両方を止める
    --check     起動せず、準備ができているか（ネットワークなしで Go をビルドできるか・依存が入っているか・ポートが空いているか）を確かめる
    --dry-run   起動せず、実行するはずのコマンドを「+ 」を付けて出す
    --data DIR  ステージ定義のディレクトリ（islands.json と stages/*.yaml。既定は <作業ツリー>/data）
    --path P    作業ツリーの根（既定は git rev-parse --show-toplevel）
    -h, --help  使い方を出して 0 で終わる
環境変数:
  DEV_SLOT    0〜99（未設定なら 0。0 は人が手で使う枠。ポートの計算は scripts/dev-env.sh）
EOF
}

die() {
  echo "$PROG: $*" >&2
  exit 1
}

usage_error() {
  echo "$PROG: $*" >&2
  usage >&2
  exit 1
}

# show <args...>: 実行するはずのコマンドを「+ 」を付けて 1 行で出す（scripts/dev-env.sh と同じ形）。
show() {
  local out="+" a
  for a in "$@"; do
    out="$out $a"
  done
  printf '%s\n' "$out"
}

DEMO_TMP=""
DEMO_GO_PID=""
DEMO_WEB_PID=""

# group_alive <pgid>: そのプロセスグループにまだプロセスが残っているか。
group_alive() {
  kill -0 -- "-$1" 2>/dev/null
}

# cleanup: 裏で動かしている Vite と Go の両方に TERM を送り、終わるのを待ってから一時ディレクトリを消す。
# 子はそれぞれのプロセスグループで動かしている（cmd_start の set -m）ので、グループごと止める。
# pnpm は sh のラッパーから node を起動し、さらに vite の node を起動するため、PID だけに送ると孫が残る。
cleanup() {
  local pid deadline
  for pid in "$DEMO_WEB_PID" "$DEMO_GO_PID"; do
    if [ -n "$pid" ]; then kill -TERM -- "-$pid" 2>/dev/null || kill "$pid" 2>/dev/null || true; fi
  done
  for pid in "$DEMO_WEB_PID" "$DEMO_GO_PID"; do
    if [ -n "$pid" ]; then wait "$pid" 2>/dev/null || true; fi
  done
  # 孫が TERM で終わるのを少し待ち、残っていれば KILL する。
  deadline=$((SECONDS + STOP_WAIT_SECONDS))
  for pid in "$DEMO_WEB_PID" "$DEMO_GO_PID"; do
    if [ -z "$pid" ]; then continue; fi
    while group_alive "$pid" && [ "$SECONDS" -lt "$deadline" ]; do
      sleep "$POLL_INTERVAL"
    done
    if group_alive "$pid"; then kill -KILL -- "-$pid" 2>/dev/null || true; fi
  done
  DEMO_WEB_PID=""
  DEMO_GO_PID=""
  if [ -n "$DEMO_TMP" ]; then
    rm -rf "$DEMO_TMP"
    DEMO_TMP=""
  fi
}

install_traps() {
  trap cleanup EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM
}

# alive <pid>: 子のプロセスがまだ動いているか。
alive() {
  kill -0 "$1" 2>/dev/null
}

# port_in_use <port>: 127.0.0.1:<port> に接続できたら使用中（0 を返す）。
port_in_use() {
  (: </dev/tcp/127.0.0.1/"$1") 2>/dev/null
}

# resolve_root <path or empty>: ROOT を決める（物理パス）。
resolve_root() {
  local p="$1"
  if [ -n "$p" ]; then
    [ -d "$p" ] || die "--path のディレクトリがない: $p"
  else
    p="$(git rev-parse --show-toplevel 2>/dev/null)" || die "作業ツリーの中で実行するか --path を指定する"
    [ -n "$p" ] || die "作業ツリーの中で実行するか --path を指定する"
  fi
  ROOT="$(cd "$p" && pwd -P)" || die "--path のディレクトリに入れない: $p"
}

# resolve_data <dir or empty>: DATA_DIR を絶対パスにする。
resolve_data() {
  local d="$1"
  [ -n "$d" ] || d="$ROOT/data"
  if [ -d "$d" ]; then
    DATA_DIR="$(cd "$d" && pwd -P)" || die "--data のディレクトリに入れない: $d"
  else
    case "$d" in
      /*) DATA_DIR="$d" ;;
      *) DATA_DIR="$PWD/$d" ;;
    esac
  fi
}

# has_stages: <DATA_DIR>/stages/*.yaml が 1 つ以上あるか。
has_stages() {
  local f
  for f in "$DATA_DIR"/stages/*.yaml; do
    if [ -f "$f" ]; then return 0; fi
  done
  return 1
}

check_files() {
  [ -f "$ROOT/server/cmd/server/main.go" ] || die "server/cmd/server/main.go がない"
  [ -f "$ROOT/web/package.json" ] || die "web/package.json がない"
  if [ ! -f "$DATA_DIR/islands.json" ] || ! has_stages; then
    die "ステージ定義がない: ${DATA_DIR}（islands.json と stages/*.yaml が要る。--data で別のディレクトリを指定できる）"
  fi
}

check_tools() {
  local c
  for c in go pnpm curl; do
    command -v "$c" >/dev/null 2>&1 || die "$c がない"
  done
  [ -e "$ROOT/web/node_modules/.bin/vite" ] ||
    die "web の依存が入っていない（ネットワークのあるところで pnpm install を先に実行する）"
}

check_ports() {
  local port
  for port in "$DEV_SERVER_PORT" "$DEV_WEB_PORT"; do
    if port_in_use "$port"; then
      die "ポート $port は使用中（別の make dev・make demo-local を止めるか、DEV_SLOT を変える）"
    fi
  done
}

cmd_dry_run() {
  show mktemp -d
  show go -C "$ROOT/server" build -o "<tmp>/server" ./cmd/server
  show MODE=standalone "PORT=$DEV_SERVER_PORT" "DATA_DIR=$DATA_DIR" "ALLOWED_ORIGINS=$ORIGINS" DEMO_CONTROLS=1 "<tmp>/server" "&"
  show curl -fsS -o /dev/null --max-time 1 "http://127.0.0.1:$DEV_SERVER_PORT/api/healthz"
  show DEV_BACKEND=standalone "DEV_SERVER_PORT=$DEV_SERVER_PORT" VITE_TURNSTILE_SITE_KEY='' pnpm -C "$ROOT/web" exec vite --host 127.0.0.1 --port "$DEV_WEB_PORT" --strictPort "&"
  show curl -fsS -o /dev/null --max-time 2 "http://127.0.0.1:$DEV_WEB_PORT/"
}

cmd_check() {
  check_tools
  check_ports
  install_traps
  DEMO_TMP="$(mktemp -d)" || die "一時ディレクトリを作れない"
  # モジュールのキャッシュだけでビルドできるか（Go のツールチェーンの取得が要る場合も失敗する）。
  if ! GOPROXY=off GOFLAGS=-mod=readonly go -C "$ROOT/server" build -o "$DEMO_TMP/server" ./cmd/server; then
    die "ネットワークなしでは Go をビルドできない（ネットワークのあるところで go -C server build ./cmd/server を一度実行してキャッシュを作る）"
  fi
  cleanup
  echo "$PROG: 準備 OK（ネットワークなしで起動できる。当日は make demo-local）"
}

# wait_go: Go の /api/healthz が応答するまで待つ。
wait_go() {
  local deadline=$((SECONDS + GO_WAIT_SECONDS))
  while :; do
    if ! alive "$DEMO_GO_PID"; then
      die "Go のサーバーが起動しなかった（上のログを見る）"
    fi
    if curl -fsS -o /dev/null --max-time 1 "http://127.0.0.1:$DEV_SERVER_PORT/api/healthz" 2>/dev/null &&
      alive "$DEMO_GO_PID"; then
      return 0
    fi
    [ "$SECONDS" -lt "$deadline" ] || die "Go のサーバーが ${GO_WAIT_SECONDS} 秒以内に応答しない"
    sleep "$POLL_INTERVAL"
  done
}

# wait_web: Vite が応答するまで待つ。
wait_web() {
  local deadline=$((SECONDS + WEB_WAIT_SECONDS))
  while :; do
    if ! alive "$DEMO_WEB_PID"; then
      die "Vite が起動しなかった（上のログを見る）"
    fi
    if ! alive "$DEMO_GO_PID"; then
      die "Go のサーバーが止まった"
    fi
    if curl -fsS -o /dev/null --max-time 2 "http://127.0.0.1:$DEV_WEB_PORT/" 2>/dev/null &&
      alive "$DEMO_WEB_PID"; then
      return 0
    fi
    [ "$SECONDS" -lt "$deadline" ] || die "Vite が ${WEB_WAIT_SECONDS} 秒以内に応答しない"
    sleep "$POLL_INTERVAL"
  done
}

cmd_start() {
  check_tools
  check_ports
  # 前で動かすと、bash はそのコマンドが終わるまで TERM の trap を遅らせる。Go と Vite は裏で起動し、
  # 待つ間の sleep は短くする。trap の exit から EXIT の trap（cleanup）が両方に kill を送る。
  install_traps
  DEMO_TMP="$(mktemp -d)" || die "一時ディレクトリを作れない"
  # ジョブ制御を有効にし、裏で起動する Go と Vite をそれぞれ別のプロセスグループにする（cleanup でグループごと止める）。
  # 端末の Ctrl-C はこのスクリプトだけに届き、INT の trap から両方を止める。
  set -m
  go -C "$ROOT/server" build -o "$DEMO_TMP/server" ./cmd/server || die "Go のビルドに失敗した"

  MODE=standalone PORT="$DEV_SERVER_PORT" DATA_DIR="$DATA_DIR" ALLOWED_ORIGINS="$ORIGINS" DEMO_CONTROLS=1 "$DEMO_TMP/server" &
  DEMO_GO_PID=$!
  wait_go

  # 手元のシェルや web/.env* にサイトキーがあっても、Turnstile のスクリプト（インターネット上）を読まないよう空にする。
  DEV_BACKEND=standalone DEV_SERVER_PORT="$DEV_SERVER_PORT" VITE_TURNSTILE_SITE_KEY='' pnpm -C "$ROOT/web" exec vite --host 127.0.0.1 --port "$DEV_WEB_PORT" --strictPort &
  DEMO_WEB_PID=$!
  wait_web

  echo "$PROG: 準備完了 → http://127.0.0.1:$DEV_WEB_PORT/ （デモ操作あり。止めるときは Ctrl-C）"

  # bash 3.2 には wait -n がないので、kill -0 で両方を見張る。
  while :; do
    if ! alive "$DEMO_GO_PID"; then
      die "Go のサーバーが止まった"
    fi
    if ! alive "$DEMO_WEB_PID"; then
      die "Vite が止まった"
    fi
    sleep "$WATCH_INTERVAL"
  done
}

main() {
  local check=0 dry=0 data="" data_set=0 path="" path_set=0
  while [ $# -gt 0 ]; do
    case "$1" in
      -h | --help)
        usage
        exit 0
        ;;
      --check)
        [ "$check" = 0 ] || usage_error "--check を 2 回指定した"
        check=1
        shift
        ;;
      --dry-run)
        [ "$dry" = 0 ] || usage_error "--dry-run を 2 回指定した"
        dry=1
        shift
        ;;
      --data)
        [ "$data_set" = 0 ] || usage_error "--data を 2 回指定した"
        [ $# -ge 2 ] || usage_error "--data に値がない"
        [ -n "$2" ] || usage_error "--data に値がない"
        data="$2"
        data_set=1
        shift 2
        ;;
      --path)
        [ "$path_set" = 0 ] || usage_error "--path を 2 回指定した"
        [ $# -ge 2 ] || usage_error "--path に値がない"
        [ -n "$2" ] || usage_error "--path に値がない"
        path="$2"
        path_set=1
        shift 2
        ;;
      *) usage_error "未知の引数: $1" ;;
    esac
  done
  if [ "$check" = 1 ] && [ "$dry" = 1 ]; then
    usage_error "--check と --dry-run は同時に指定できない"
  fi

  resolve_root "$path"

  if [ -z "${DEV_SLOT:-}" ]; then
    DEV_SLOT=0
    echo "$PROG: DEV_SLOT が未設定なので 0 を使う（人が手で使う枠）"
  fi
  local env_out
  env_out="$(DEV_SLOT="$DEV_SLOT" bash "$SCRIPT_DIR/dev-env.sh" env --path "$ROOT")" || exit 1
  eval "$env_out"

  resolve_data "$data"
  check_files
  ORIGINS="http://127.0.0.1:$DEV_WEB_PORT,http://localhost:$DEV_WEB_PORT"

  echo "$PROG: DEV_SLOT=$DEV_SLOT DEV_SERVER_PORT=$DEV_SERVER_PORT DEV_WEB_PORT=$DEV_WEB_PORT DATA_DIR=$DATA_DIR"

  if [ "$dry" = 1 ]; then
    cmd_dry_run
  elif [ "$check" = 1 ]; then
    cmd_check
  else
    cmd_start
  fi
}

main "$@"
