#!/usr/bin/env bash
# favicon と OGP の画像（PNG）を、元の SVG・HTML（web/brand/）からヘッドレスの Chrome で作り直す（T-430）。
#
# 使い方: scripts/render-brand-images.sh [オプション]
#   --out DIR    画像を置くディレクトリ（既定: web/public。なければ作る）
#   --dry-run    実行せず、実行するはずのコマンドを「+ 」を付けて出す
#   -h, --help   この説明を出す
#
# 作る画像（元 → 出力）:
#   web/brand/favicon-32.html        → favicon-32.png（32×32。透明な地。絵は web/public/favicon.svg）
#   web/brand/apple-touch-icon.html  → apple-touch-icon.png（180×180。紙の色の地）
#   web/brand/og-image.html          → og-image.png（1200×630。共有のプレビュー）
#
# 環境変数:
#   CHROME  Chrome（Chromium）の実行ファイル。未設定なら、macOS の Google Chrome、
#           Linux の chromium・chromium-browser・google-chrome・google-chrome-stable の順に探す
#
# 色・絵は web/src/ui/theme/tokens.css と web/src/ui/art/art.css を HTML から読み込む（タイトルの画面と同じ絵作り）。
# 文字は手元のフォント（--font-body。macOS ではヒラギノ丸ゴ）で描くので、作り直すと見た目が少し変わることがある。
# 動きは止めて撮る（--force-prefers-reduced-motion）。大きさの目安は og-image.png が 300KB 以下、favicon の PNG が 20KB 以下。
#
# 終了コード: 成功は 0。使い方の誤り・Chrome がない・撮れなかった・大きさが違うときは 1。
# bash 3.2（macOS）で動く書き方にする（mapfile・空の配列の "${a[@]}" と set -u を避ける）。
set -euo pipefail

PROG="render-brand-images"

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

# png_size <file>: PNG の幅と高さを「WxH」で出す（IHDR の 16〜23 バイト目）。
png_size() {
  local w h
  w=$(od -An -tu1 -j16 -N4 "$1" | awk 'NF { print ($1 * 16777216) + ($2 * 65536) + ($3 * 256) + $4 }')
  h=$(od -An -tu1 -j20 -N4 "$1" | awk 'NF { print ($1 * 16777216) + ($2 * 65536) + ($3 * 256) + $4 }')
  printf '%sx%s\n' "$w" "$h"
}

ROOT="$(cd "$(dirname "$0")/.." && pwd -P)"
BRAND="$ROOT/web/brand"
OUT="$ROOT/web/public"
DRY=0

while [ $# -gt 0 ]; do
  case "$1" in
    --out)
      [ $# -ge 2 ] || usage_error "--out に値がない"
      OUT="$2"
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
    *)
      usage_error "知らない引数: $1"
      ;;
  esac
done

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

# 元の HTML・出力のファイル名・大きさ・地（transparent か opaque）
JOBS="favicon-32.html=favicon-32.png=32x32=transparent
apple-touch-icon.html=apple-touch-icon.png=180x180=opaque
og-image.html=og-image.png=1200x630=opaque"

failed=0
while IFS='=' read -r src name size bg; do
  w="${size%x*}"
  h="${size#*x}"
  file="$OUT/$name"
  [ -f "$BRAND/$src" ] || die "元の HTML がない: $BRAND/$src"
  set -- "$CHROME_BIN" --headless=new --disable-gpu --hide-scrollbars --no-first-run \
    --no-default-browser-check --disable-extensions --force-device-scale-factor=1 \
    --force-prefers-reduced-motion "--user-data-dir=${PROFILE:-<tmp>}" "--window-size=$w,$h"
  if [ "$bg" = transparent ]; then
    set -- "$@" --default-background-color=00000000
  fi
  set -- "$@" "--screenshot=$file" "file://$BRAND/$src"
  if [ "$DRY" = 1 ]; then
    show "$@"
    continue
  fi
  rm -f "$file"
  # 標準入力はこのループの一覧なので、Chrome には読ませない（</dev/null）。
  if ! "$@" </dev/null >/dev/null 2>&1 || [ ! -s "$file" ]; then
    echo "$PROG: 撮れなかった: $src" >&2
    failed=1
    continue
  fi
  got="$(png_size "$file")"
  if [ "$got" != "$size" ]; then
    echo "$PROG: 大きさが違う: ${name} は ${got}（期待は ${size}）" >&2
    failed=1
    continue
  fi
  echo "$file ($size, $(wc -c <"$file" | tr -d ' ') bytes)"
done <<EOF
$JOBS
EOF

[ "$failed" = 0 ] || exit 1
