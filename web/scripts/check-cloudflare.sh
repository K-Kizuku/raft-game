#!/usr/bin/env bash
# web/cloudflare.config.ts の 4 つのモードを、ビルドから dry-run まで一度に確かめる（T-601 / #65）。
# どこから実行してもリポジトリのルートで動く。説明は docs/03-design-doc.md §12.6。
#
#   DEV_SLOT=<n> bash web/scripts/check-cloudflare.sh
#
# 1. docker info（cf build がローカルの Docker でイメージを作る）
# 2. make sync-data（server/Dockerfile が要る。T-306）
# 3. production・demo・local-container・development の順に、モードごとに続けて:
#    cf build --mode <モード>（BUILD_ID=git:chk0001）→ node web/scripts/check-build-output.ts
# 4. コンテナのあるモード: イメージが linux/amd64
# 5. production: イメージを起動し、/healthz の build が git:chk0001（buildVars がビルド引数として渡る）
# 6. production・demo: cf deploy --prebuilt --dry-run（反映しない）の Durable Object の表
# 7. cf build --mode staging が「知らないモード」で失敗する
#
# 終了コード: すべて成功 0、どれかの失敗 1、Docker がない 2。
# 起動したコンテナは DEV_DOCKER_LABEL を付け、終わるときに消す。cf build が作るイメージ（cloudflare-build/…）の
# 名前は cf が決める（消し方は §12.6）。残る web/.cloudflare/output は最後にビルドしたモード（development）のもの。
# bash 3.2（macOS）で動く書き方にする。

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
cd "$ROOT"

PROG="check-cloudflare"
BUILD_ID_VALUE="git:chk0001"
OUTPUT="web/.cloudflare/output/v0"
export CF_SEND_TELEMETRY=false

step() {
  printf '\n== %s\n' "$*"
}

fail() {
  echo "${PROG}: 失敗: $*" >&2
  exit 1
}

CONTAINER_ID=""
cleanup() {
  local status=$?
  if [ -n "$CONTAINER_ID" ]; then
    if [ "$status" -ne 0 ]; then
      echo "--- docker logs（最後の 50 行）" >&2
      docker logs --tail 50 "$CONTAINER_ID" 1>&2 || true
    fi
    docker rm -f "$CONTAINER_ID" >/dev/null 2>&1 || true
  fi
  return "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

# json_field <JSON> <式>: node で JSON を読み、式（d を使う）の値を 1 行ずつ出す。
json_field() {
  node -e 'const d = JSON.parse(process.argv[1]); const v = eval(process.argv[2]); for (const x of [].concat(v)) console.log(x);' "$1" "$2"
}

step "1. Docker"
docker info >/dev/null 2>&1 || {
  echo "${PROG}: Docker が要る（cf build がローカルの Docker でイメージを作る）。Docker を起動してから実行する" >&2
  exit 2
}
# eval "$(…)" は中のコマンドの失敗を拾わないので、先に変数に受ける。
ENV_OUT="$(bash scripts/dev-env.sh env)" || fail "scripts/dev-env.sh env（DEV_SLOT を割り当てる）"
eval "$ENV_OUT"
: "${DEV_DOCKER_LABEL:?}"
echo "docker に接続できる（ラベル ${DEV_DOCKER_LABEL}）"

step "2. make sync-data"
make sync-data || fail "make sync-data"

# check_image_platform <検査の出力>: images がすべて linux/amd64 であること。
check_image_platform() {
  local images image platform
  images="$(json_field "$1" 'd.images')"
  [ -n "$images" ] || fail "コンテナのイメージがない"
  while IFS= read -r image; do
    platform="$(docker image inspect --format '{{.Os}}/{{.Architecture}}' "$image")" || fail "docker image inspect $image"
    echo "$image: $platform"
    [ "$platform" = "linux/amd64" ] || fail "$image が linux/amd64 でない（${platform}）"
  done <<EOF
$images
EOF
}

# check_healthz <イメージ>: 起動して /healthz の build が BUILD_ID_VALUE であること。
check_healthz() {
  local image="$1" port_line host_port body build i
  CONTAINER_ID="$(docker run -d --label "$DEV_DOCKER_LABEL" -p 127.0.0.1::8080 "$image")" || fail "docker run $image"
  port_line="$(docker port "$CONTAINER_ID" 8080/tcp | head -n 1)"
  host_port="${port_line##*:}"
  case "$host_port" in '' | *[!0-9]*) fail "ホスト側のポートが読めない: ${port_line}" ;; esac
  i=0
  body=""
  while :; do
    body="$(curl -sf --max-time 2 "http://127.0.0.1:${host_port}/healthz" 2>/dev/null)" && break
    i=$((i + 1))
    [ "$i" -lt 30 ] || fail "/healthz が 15 秒のうちに 200 にならない"
    sleep 0.5
  done
  build="$(json_field "$body" 'd.build')" || fail "/healthz の本文が JSON でない: ${body}"
  echo "/healthz: ${body}"
  [ "$build" = "$BUILD_ID_VALUE" ] || fail "/healthz の build が ${BUILD_ID_VALUE} でない（${build}）。buildVars がビルド引数として渡っていない"
  docker rm -f "$CONTAINER_ID" >/dev/null || fail "docker rm -f"
  CONTAINER_ID=""
}

# dry_run_has <モード> <出力> <文字列>: dry-run の出力に文字列があること。
dry_run_has() {
  case "$2" in
    *"$3"*) ;;
    *) fail "$1 の dry-run の出力に「$3」がない" ;;
  esac
}

# check_dry_run <モード> <Worker の名前>: cf deploy --prebuilt --dry-run が成功し、Durable Object の表が合うこと。
# 「<クラス> (durable_object)」はコンテナの一覧の行（コンテナを持つ GameSession だけが出る）。CostGuard はコンテナを
# 持たないので、バインディングの表の「env.COST_GUARD (CostGuard, defined in raft-game)」の行で確かめる
# （cf@1.0.0-beta.28 の書き方。demo に CostGuard の export がないことは手順 3 の検査で確かめる）。
check_dry_run() {
  local mode="$1" worker="$2" dry
  dry="$(pnpm -C web exec cf deploy --prebuilt --dry-run --mode "$mode" 2>&1)" || {
    echo "$dry"
    fail "cf deploy --prebuilt --dry-run --mode ${mode}"
  }
  echo "$dry"
  dry_run_has "$mode" "$dry" "GameSession (durable_object)"
  dry_run_has "$mode" "$dry" "env.GAME_SESSION (GameSession, defined in ${worker})"
  dry_run_has "$mode" "$dry" "env.COST_GUARD (CostGuard, defined in raft-game)"
}

n=3
for mode in production demo local-container development; do
  step "${n}. ${mode}: cf build → 検査"
  n=$((n + 1))
  BUILD_ID="$BUILD_ID_VALUE" pnpm -C web exec cf build --mode "$mode" || fail "cf build --mode ${mode}"
  summary="$(node web/scripts/check-build-output.ts --mode "$mode" "$OUTPUT")" || fail "check-build-output --mode ${mode}"
  echo "$summary"

  if [ "$mode" != development ]; then
    step "${mode}: イメージの OS/アーキテクチャ"
    check_image_platform "$summary"
  fi
  if [ "$mode" = production ]; then
    step "${mode}: /healthz の build"
    check_healthz "$(json_field "$summary" 'd.images[0]')"
  fi
  if [ "$mode" = production ] || [ "$mode" = demo ]; then
    step "${mode}: cf deploy --prebuilt --dry-run"
    check_dry_run "$mode" "$(json_field "$summary" 'd.worker')"
  fi
done

step "${n}. 知らないモード（staging）"
if staging="$(pnpm -C web exec cf build --mode staging 2>&1)"; then
  fail "cf build --mode staging が成功した"
fi
case "$staging" in
  *"知らないモード"*) echo "cf build --mode staging は「知らないモード」で失敗した" ;;
  *)
    echo "$staging"
    fail "cf build --mode staging の出力に「知らないモード」がない"
    ;;
esac

printf '\n%s: すべて成功\n' "$PROG"
