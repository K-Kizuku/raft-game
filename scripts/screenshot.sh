#!/usr/bin/env bash
# 開発のサーバー（make dev の Vite）の各画面のスクリーンショットを、ヘッドレスの Chrome で撮る（T-424a）。
#
# 使い方: scripts/screenshot.sh [オプション] <URL> <出力先のディレクトリ>
#   <URL>                  開発のサーバーの URL（例: http://127.0.0.1:<DEV_WEB_PORT>。ポートは make dev-env）
#   <出力先のディレクトリ>  画像を置くディレクトリ（なければ作る）。ファイル名は <画面>-<幅>x<高さ>.png
#
#   --size WxH         撮る大きさ（繰り返せる。既定: 1470x860（基準の画面。docs/11 §15）と 1600x900 と 1280x720）
#   --page NAME=PATH   撮る画面（繰り返せる。既定: title=/ stage-select=/stages intro-stage1=/stages/stage1
#                      intro-stage2=/stages/stage2 intro-stage3=/stages/stage3 howto=/howto credits=/credits settings=/settings）
#   --wait MS          読み込んでから撮るまでの時間（Chrome の --virtual-time-budget。既定: 3000）
#   --dry-run          実行せず、実行するはずのコマンドを「+ 」を付けて出す
#   -h, --help         この説明を出す
#
# 環境変数:
#   CHROME  Chrome（Chromium）の実行ファイル。未設定なら、macOS の Google Chrome、
#           Linux の chromium・chromium-browser・google-chrome・google-chrome-stable の順に探す
#
# 例:
#   eval "$(scripts/dev-env.sh env)"   # DEV_WEB_PORT などを読む（DEV_SLOT が要る）
#   make dev &                         # Vite を起動しておく
#   scripts/screenshot.sh "http://127.0.0.1:$DEV_WEB_PORT" "$(mktemp -d)"
#
# 終了コード: 成功は 0。使い方の誤り・Chrome がない・撮れなかったときは 1。
# bash 3.2（macOS）で動く書き方にする（mapfile・空の配列の "${a[@]}" と set -u を避ける）。
set -euo pipefail

PROG="screenshot"

die() {
  echo "$PROG: $*" >&2
  exit 1
}

usage() {
  sed -n '2,/^set -euo/p' "$0" | sed '$d' | sed 's/^# \{0,1\}//'
}

usage_error() {
  echo "$PROG: $*" >&2
  usage >&2
  exit 1
}

# show <args...>: 実行するはずのコマンドを「+ 」を付けて 1 行で出す。
show() {
  local out="+" a
  for a in "$@"; do
    out="$out $a"
  done
  printf '%s\n' "$out"
}

find_chrome() {
  if [ -n "${CHROME:-}" ]; then
    printf '%s\n' "$CHROME"
    return 0
  fi
  local mac="/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" c
  if [ -x "$mac" ]; then
    printf '%s\n' "$mac"
    return 0
  fi
  for c in chromium chromium-browser google-chrome google-chrome-stable; do
    if command -v "$c" >/dev/null 2>&1; then
      command -v "$c"
      return 0
    fi
  done
  return 1
}

SIZES=""
PAGES=""
WAIT=3000
DRY=0
URL=""
OUT=""

while [ $# -gt 0 ]; do
  case "$1" in
    --size)
      [ $# -ge 2 ] || usage_error "--size に値がない"
      [[ "$2" =~ ^[1-9][0-9]*x[1-9][0-9]*$ ]] || usage_error "--size の値が不正: '$2'（例: 1600x900）"
      SIZES="$SIZES $2"
      shift 2
      ;;
    --page)
      [ $# -ge 2 ] || usage_error "--page に値がない"
      [[ "$2" =~ ^[A-Za-z0-9_-]+=/[^[:space:]]*$ ]] || usage_error "--page の値が不正: '$2'（例: howto=/howto）"
      PAGES="$PAGES $2"
      shift 2
      ;;
    --wait)
      [ $# -ge 2 ] || usage_error "--wait に値がない"
      [[ "$2" =~ ^[0-9]+$ ]] || usage_error "--wait の値が不正: '$2'（ミリ秒の整数）"
      WAIT="$2"
      shift 2
      ;;
    --dry-run)
      DRY=1
      shift
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    -*)
      usage_error "知らないオプション: $1"
      ;;
    *)
      if [ -z "$URL" ]; then
        URL="$1"
      elif [ -z "$OUT" ]; then
        OUT="$1"
      else
        usage_error "引数が多すぎる: $1"
      fi
      shift
      ;;
  esac
done

[ -n "$URL" ] || usage_error "URL がない"
[ -n "$OUT" ] || usage_error "出力先のディレクトリがない"
[[ "$URL" =~ ^https?:// ]] || usage_error "URL は http:// か https:// で始める: '$URL'"
URL="${URL%/}"
[ -n "$SIZES" ] || SIZES="1470x860 1600x900 1280x720"
[ -n "$PAGES" ] || PAGES="title=/ stage-select=/stages intro-stage1=/stages/stage1 intro-stage2=/stages/stage2 intro-stage3=/stages/stage3 howto=/howto credits=/credits settings=/settings"

CHROME_BIN="$(find_chrome)" || die "Chrome（Chromium）が見つからない。CHROME で実行ファイルを指定する"

PROFILE=""
cleanup() {
  if [ -n "$PROFILE" ]; then
    rm -rf "$PROFILE"
  fi
}

if [ "$DRY" = 1 ]; then
  show mkdir -p "$OUT"
  show mktemp -d
else
  if [ ! -x "$CHROME_BIN" ] && ! command -v "$CHROME_BIN" >/dev/null 2>&1; then
    die "Chrome を実行できない: $CHROME_BIN"
  fi
  mkdir -p "$OUT"
  OUT="$(cd "$OUT" && pwd -P)"
  # 動いている Chrome とプロファイルを共有しないよう、一時的なプロファイルを使う。
  PROFILE="$(mktemp -d)"
  trap cleanup EXIT
fi

failed=0
for size in $SIZES; do
  w="${size%x*}"
  h="${size#*x}"
  for page in $PAGES; do
    name="${page%%=*}"
    path="${page#*=}"
    file="$OUT/$name-${w}x${h}.png"
    set -- "$CHROME_BIN" --headless=new --disable-gpu --hide-scrollbars --no-first-run \
      --no-default-browser-check --disable-extensions \
      "--user-data-dir=${PROFILE:-<tmp>}" "--window-size=$w,$h" \
      "--virtual-time-budget=$WAIT" "--screenshot=$file" "$URL$path"
    if [ "$DRY" = 1 ]; then
      show "$@"
      continue
    fi
    rm -f "$file"
    if "$@" >/dev/null 2>&1 && [ -s "$file" ]; then
      echo "$file"
    else
      echo "$PROG: 撮れなかった: ${URL}${path}（${w}x${h}）" >&2
      failed=1
    fi
  done
done

[ "$failed" = 0 ] || exit 1
