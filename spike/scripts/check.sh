#!/usr/bin/env bash
# スパイク（T-007a / #134）の AI の部分の検査を一度に行う。どこから実行してもリポジトリのルートで動く。
#
#   bash spike/scripts/check.sh
#
# 1. pnpm -C spike typecheck・test
# 2. Go: gofmt・vet・test -race
# 3. docker info（cf build にローカルの Docker が要る）
# 4. pnpm -C spike exec cf build
# 5. node spike/scripts/check-config.ts spike/.cloudflare/output/v0（本番と分かれていること）
# 6. コンテナのイメージが linux/amd64 であること
# 7. pnpm -C spike exec cf deploy --prebuilt --dry-run（反映しない）
# 8. shellcheck（あれば）
#
# 終了コード: すべて成功なら 0、1 つでも失敗したら 1。
# bash 3.2（macOS）で動く書き方にする。

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
cd "$ROOT"

step() {
  printf '\n== %s\n' "$*"
}

fail() {
  echo "check: 失敗: $*" >&2
  exit 1
}

step "1. typecheck・test（spike）"
pnpm -C spike typecheck || fail "pnpm -C spike typecheck"
pnpm -C spike test || fail "pnpm -C spike test"

step "2. Go（spike/container）"
gofmt_bin="$(go -C spike/container env GOROOT)/bin/gofmt"
unformatted="$("$gofmt_bin" -l spike/container)"
[ -z "$unformatted" ] || fail "gofmt の差分がある: $unformatted"
go -C spike/container vet ./... || fail "go vet"
go -C spike/container test -race -count=1 ./... || fail "go test"

step "3. Docker"
docker info >/dev/null 2>&1 || fail "Docker が要る（cf build がローカルの Docker でイメージを作る）。Docker を起動してから実行する"

step "4. cf build"
pnpm -C spike exec cf build || fail "cf build"

step "5. ビルドの出力の検査"
summary="$(node spike/scripts/check-config.ts spike/.cloudflare/output/v0)" || fail "check-config"
echo "$summary"

step "6. イメージの OS/アーキテクチャ"
images="$(printf '%s' "$summary" | node -e 'let s="";process.stdin.on("data",d=>s+=d).on("end",()=>{for(const i of JSON.parse(s).images)console.log(i)})')"
[ -n "$images" ] || fail "コンテナのイメージがない"
while IFS= read -r image; do
  platform="$(docker image inspect --format '{{.Os}}/{{.Architecture}}' "$image")" || fail "docker image inspect $image"
  echo "$image: $platform"
  [ "$platform" = "linux/amd64" ] || fail "$image が linux/amd64 でない（$platform）"
done <<EOF
$images
EOF

step "7. cf deploy --prebuilt --dry-run"
dry="$(pnpm -C spike exec cf deploy --prebuilt --dry-run 2>&1)" || {
  echo "$dry"
  fail "cf deploy --prebuilt --dry-run"
}
echo "$dry"
case "$dry" in
  *"SpikeSession (durable_object)"*) ;;
  *) fail "dry-run の出力に「SpikeSession (durable_object)」がない" ;;
esac

step "8. shellcheck"
if command -v shellcheck >/dev/null 2>&1; then
  shellcheck spike/scripts/*.sh || fail "shellcheck"
else
  echo "shellcheck がない（CI の shell のジョブで確かめる）"
fi

printf '\ncheck: すべて成功\n'
