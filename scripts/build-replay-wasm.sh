#!/usr/bin/env bash
# ブラウザでのリプレイの WASM（server/cmd/replaywasm）をビルドする（T-720b / ADR-0028 の R2・DesignDoc §10.4）。
#
# 使い方: scripts/build-replay-wasm.sh [--verify] [--out DIR]
#   --out DIR  出力のディレクトリ（既定: web/public/wasm。相対パスはリポジトリの根から）。
#              raft-replay.wasm と wasm_exec.js を置く（コミットしない。.gitignore）
#   --verify   ビルドの後に、ネイティブの simcli replay と WASM の traceHash が一致するか、
#              GOOS=js GOARCH=wasm での go test・go vet が通るかを確かめる
# make wasm（--verify なし）・make wasm-check（--verify）が呼ぶ。
# 環境変数 BUILD_ID（既定: dev）を -X main.build に入れる（応答の build）。
# 終了コード: 0 = 成功
#             1 = ビルドの失敗・WASM が生で 25 MiB を超えた・--verify の不一致や失敗・node か gzip がない
#             2 = 引数の誤り
#
# - wasm_exec.js・wasm_exec_node.js は $(go -C server env GOROOT) から取る（server/go.mod の Go の版のもの。
#   リポジトリの根の go env GOROOT は手元の Go の版になることがあり、版が違うと動かない）
# - 一時ファイルは mktemp -d の中だけに作り、trap で消す
# - bash 3.2（macOS）で動く書き方にする。日本語の直前の変数は ${name} と書く
set -euo pipefail

PROG="build-replay-wasm"
# Workers の静的ファイルの 1 ファイルの上限（25 MiB）。
MAX_RAW_BYTES=26214400
# --verify で入力ログを作るステージとボット（シードは 1）。
VERIFY_CASES="stage1:greedy stage2:steady demo-izu:greedy"

die() {
  echo "${PROG}: $*" >&2
  exit 1
}

usage() {
  sed -n '2,/^set -euo/p' "$0" | sed '$d' | sed 's/^# \{0,1\}//'
}

verify=0
out="web/public/wasm"
while [ $# -gt 0 ]; do
  case "$1" in
    --verify) verify=1 ;;
    --out)
      [ $# -ge 2 ] || { echo "${PROG}: --out に値がない" >&2; exit 2; }
      out="$2"
      shift
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      echo "${PROG}: 未知の引数: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
  shift
done
[ -n "$out" ] || { echo "${PROG}: --out が空" >&2; exit 2; }

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(cd "$script_dir/.." && pwd)"
case "$out" in
  /*) ;;
  *) out="$root/$out" ;;
esac

command -v go >/dev/null 2>&1 || die "go がない"
command -v gzip >/dev/null 2>&1 || die "gzip がない（WASM の gzip の大きさを測るのに使う）"
if [ "$verify" -eq 1 ]; then
  command -v node >/dev/null 2>&1 || die "node がない（--verify は Node で WASM を動かす）"
fi

goroot="$(go -C "$root/server" env GOROOT)"
[ -f "$goroot/lib/wasm/wasm_exec.js" ] || die "${goroot}/lib/wasm/wasm_exec.js がない"

# --- ビルド -------------------------------------------------------------------
echo "==> make -s sync-data" >&2
make -s -C "$root" sync-data >&2

mkdir -p "$out"
out="$(cd "$out" && pwd)"
wasm="$out/raft-replay.wasm"
echo "==> GOOS=js GOARCH=wasm go build ./cmd/replaywasm（build=${BUILD_ID:-dev}）" >&2
GOOS=js GOARCH=wasm go -C "$root/server" build -trimpath -tags embeddata \
  -ldflags "-s -w -X main.build=${BUILD_ID:-dev}" -o "$wasm" ./cmd/replaywasm
# GOROOT がモジュールのキャッシュ（読み取り専用）のことがあるので、前のものを消してから写し、書けるようにする。
rm -f "$out/wasm_exec.js"
cp "$goroot/lib/wasm/wasm_exec.js" "$out/"
chmod u+w "$out/wasm_exec.js"

# --- 大きさ -------------------------------------------------------------------
raw="$(wc -c <"$wasm" | tr -d ' ')"
gz="$(gzip -9 -c "$wasm" | wc -c | tr -d ' ')"
echo "raft-replay.wasm: raw=${raw} gzip=${gz}" >&2
if [ "$raw" -gt "$MAX_RAW_BYTES" ]; then
  die "raft-replay.wasm が生で ${raw} バイト（上限 ${MAX_RAW_BYTES} バイト = 25 MiB。Workers の静的ファイルの 1 ファイルの上限）"
fi
echo "${PROG}: ${out} に raft-replay.wasm と wasm_exec.js を置いた" >&2

if [ "$verify" -eq 0 ]; then
  exit 0
fi

# --- 確かめ（--verify） ---------------------------------------------------------
tmp="$(mktemp -d "${TMPDIR:-/tmp}/build-replay-wasm.XXXXXX")"
trap 'rm -rf "$tmp"' EXIT

echo "==> go build ./cmd/simcli（ネイティブ）" >&2
go -C "$root/server" build -o "$tmp/simcli" ./cmd/simcli

status=0
for c in $VERIFY_CASES; do
  st="${c%%:*}"
  bot="${c#*:}"
  name="${st}-${bot}-1"
  input="$tmp/inputs/${name}.json"
  if ! "$tmp/simcli" record --data "$root/data" --stage "$st" --bot "$bot" --seed 1 \
    --inputs-dir "$tmp/inputs" >/dev/null 2>"$tmp/record.err"; then
    cat "$tmp/record.err" >&2
    echo "${name}: NG simcli record が失敗した"
    status=1
    continue
  fi
  native=""
  if line="$("$tmp/simcli" replay --data "$root/data" --input "$input" --invariants=false 2>"$tmp/replay.err")"; then
    native="$(printf '%s\n' "$line" | sed -n 's/.*"traceHash":"\([^"]*\)".*/\1/p')"
  else
    cat "$tmp/replay.err" >&2
  fi
  wasmhash=""
  if line="$(node "$goroot/lib/wasm/wasm_exec_node.js" "$wasm" --input "$input" 2>"$tmp/node.err")"; then
    wasmhash="$(printf '%s\n' "$line" | sed -n 's/^traceHash=\([^ ]*\) .*/\1/p')"
  else
    cat "$tmp/node.err" >&2
  fi
  if [ -n "$native" ] && [ "$native" = "$wasmhash" ]; then
    echo "${name}: ok ${native}"
  else
    echo "${name}: NG native=${native:-（なし）} wasm=${wasmhash:-（なし）}"
    status=1
  fi
done

echo "==> GOOS=js GOARCH=wasm go test ./cmd/replaywasm/" >&2
if ! PATH="$goroot/lib/wasm:$PATH" GOOS=js GOARCH=wasm go -C "$root/server" test -count=1 ./cmd/replaywasm/; then
  echo "${PROG}: GOOS=js GOARCH=wasm の go test が失敗した" >&2
  status=1
fi
echo "==> GOOS=js GOARCH=wasm go vet ./cmd/replaywasm/" >&2
if ! GOOS=js GOARCH=wasm go -C "$root/server" vet ./cmd/replaywasm/; then
  echo "${PROG}: GOOS=js GOARCH=wasm の go vet が失敗した" >&2
  status=1
fi

if [ "$status" -ne 0 ]; then
  die "--verify: 失敗したものがある"
fi
echo "${PROG}: --verify: すべて一致した" >&2
