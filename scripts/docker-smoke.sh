#!/usr/bin/env bash
# server/Dockerfile のイメージをこの作業ツリーの Docker の名前（ADR-0019）でビルドし、
# 起動して /healthz などを確かめ、止める（T-306）。
#
# 使い方: DEV_SLOT=<n> bash scripts/docker-smoke.sh [--build-id <ID>]
#   --build-id  BUILD_ID のビルド引数（既定: smoke-<UNIX 秒>）
# 終了コード: 成功 0、確認の失敗 1、使い方の誤り・docker がない 2
#
# 名前・ラベルは scripts/dev-env.sh env の DEV_DOCKER_IMAGE・DEV_DOCKER_CONTAINER・DEV_DOCKER_LABEL だけを使う。
# 固定の名前・固定のポートは使わない（ホスト側のポートは Docker が空きを選ぶ）。
# 終わるときにコンテナを消す。イメージは次のビルドのキャッシュとして残す（消すのは make clean-docker）。
# bash 3.2（macOS）で動く書き方にする（mapfile・${v,,} を使わない）。

set -euo pipefail

PROG="docker-smoke"

usage() {
  cat <<'EOF'
使い方: DEV_SLOT=<n> bash scripts/docker-smoke.sh [--build-id <ID>]
  --build-id  BUILD_ID のビルド引数（既定: smoke-<UNIX 秒>）
終了コード: 成功 0、確認の失敗 1、使い方の誤り・docker がない 2
EOF
}

usage_error() {
  echo "${PROG}: $*" >&2
  usage >&2
  exit 2
}

fail() {
  echo "${PROG}: 失敗: $*" >&2
  exit 1
}

step() {
  echo "== $*"
}

ok() {
  echo "   ok: $*"
}

BUILD_ID="smoke-$(date +%s)"
while [ $# -gt 0 ]; do
  case "$1" in
    --build-id)
      [ $# -ge 2 ] || usage_error "--build-id に値がない"
      BUILD_ID="$2"
      shift 2
      ;;
    --build-id=*)
      BUILD_ID="${1#--build-id=}"
      shift
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      usage_error "不明な引数: $1"
      ;;
  esac
done

ROOT="$(git rev-parse --show-toplevel 2>/dev/null)" || usage_error "リポジトリの作業ツリーの中で実行する"
cd "$ROOT"

# eval "$(…)" は中のコマンドの失敗を拾わないので、先に変数に受ける。
ENV_OUT="$(bash scripts/dev-env.sh env)" || exit 2
eval "$ENV_OUT"
: "${DEV_DOCKER_IMAGE:?}" "${DEV_DOCKER_CONTAINER:?}" "${DEV_DOCKER_LABEL:?}"
BUILD_IMAGE="${DEV_DOCKER_IMAGE}-build"

for c in docker curl python3; do
  command -v "$c" >/dev/null 2>&1 || usage_error "${c} がない"
done

TMP_DIR=""
STARTED=0
cleanup() {
  local status=$?
  if [ "$STARTED" = 1 ]; then
    if [ "$status" -ne 0 ]; then
      echo "--- docker logs（最後の 50 行）: ${DEV_DOCKER_CONTAINER}" >&2
      docker logs --tail 50 "$DEV_DOCKER_CONTAINER" 1>&2 || true
      echo "---" >&2
    fi
    docker rm -f "$DEV_DOCKER_CONTAINER" >/dev/null 2>&1 || true
  fi
  if [ -n "$TMP_DIR" ]; then rm -rf "$TMP_DIR"; fi
  return "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/docker-smoke.XXXXXX")"

# http_get <path>: HTTP_CODE と本文（$TMP_DIR/body）を得る。接続できなければ HTTP_CODE=000。
HTTP_CODE=""
http_get() {
  HTTP_CODE="$(curl -s -o "$TMP_DIR/body" -w '%{http_code}' --max-time 2 "http://127.0.0.1:${HOST_PORT}$1" 2>/dev/null)" || true
  [ -n "$HTTP_CODE" ] || HTTP_CODE="000"
}

step "1. Docker の確認"
docker info >/dev/null 2>&1 || {
  echo "${PROG}: Docker を起動してから実行する" >&2
  exit 2
}
ok "docker に接続できる"

step "2. make sync-data"
make sync-data

step "3. イメージのビルド（${DEV_DOCKER_IMAGE}、BUILD_ID=${BUILD_ID}）"
docker build --platform linux/amd64 -f server/Dockerfile \
  --build-arg "BUILD_ID=${BUILD_ID}" --label "$DEV_DOCKER_LABEL" \
  -t "$DEV_DOCKER_IMAGE" server || fail "docker build"
ok "ビルドできた"

step "4. ビルドの段の中身（${BUILD_IMAGE}）"
docker build --platform linux/amd64 -f server/Dockerfile --target build \
  --build-arg "BUILD_ID=${BUILD_ID}" --label "$DEV_DOCKER_LABEL" \
  -t "$BUILD_IMAGE" server || fail "docker build --target build"
# data/stages/ の YAML の数（make sync-data がそのままコピーする）
expect_stages=0
for f in data/stages/*.yaml; do
  if [ -f "$f" ]; then expect_stages=$((expect_stages + 1)); fi
done
# shellcheck disable=SC2016  # $… はコンテナの中の sh が展開する
docker run --rm --platform linux/amd64 --entrypoint sh -e "EXPECT_STAGES=${expect_stages}" "$BUILD_IMAGE" -c '
  cd /src || exit 1
  bad=0
  t="$(find . -name "*_test.go" | head -n 5)"
  if [ -n "$t" ]; then echo "*_test.go がある: $t" >&2; bad=1; fi
  t="$(find . -name testdata | head -n 5)"
  if [ -n "$t" ]; then echo "testdata がある: $t" >&2; bad=1; fi
  if [ -e cmd/islandsdata ]; then echo "cmd/islandsdata がある" >&2; bad=1; fi
  if [ ! -f internal/stage/embedded/islands.json ]; then echo "internal/stage/embedded/islands.json がない" >&2; bad=1; fi
  if [ ! -d internal/stage/embedded/stages ]; then echo "internal/stage/embedded/stages/ がない" >&2; bad=1; fi
  n="$(find internal/stage/embedded/stages -name "*.yaml" 2>/dev/null | wc -l | tr -d " ")"
  if [ "$n" != "$EXPECT_STAGES" ]; then echo "stages/ の *.yaml が $n 個（data/stages/ は $EXPECT_STAGES 個）" >&2; bad=1; fi
  echo "   /src: *_test.go・testdata・cmd/islandsdata なし、islands.json あり、stages/*.yaml $n 個"
  exit "$bad"
' || fail "ビルドの段の /src の中身"
ok "ビルドの段の /src に要るものだけがある"

step "5. イメージの設定"
inspect() { docker image inspect --format "$1" "$DEV_DOCKER_IMAGE"; }
platform="$(inspect '{{.Os}}/{{.Architecture}}')"
[ "$platform" = "linux/amd64" ] || fail "Os/Architecture が linux/amd64 でない: ${platform}"
user="$(inspect '{{.Config.User}}')"
[ "$user" = "65532:65532" ] || fail "Config.User が 65532:65532 でない: ${user}"
ports="$(inspect '{{json .Config.ExposedPorts}}')"
case "$ports" in *'"8080/tcp"'*) ;; *) fail "Config.ExposedPorts に 8080/tcp がない: ${ports}" ;; esac
envs="$(inspect '{{json .Config.Env}}')"
case "$envs" in *'"MODE=single"'*) ;; *) fail "Config.Env に MODE=single がない: ${envs}" ;; esac
case "$envs" in *'"PORT=8080"'*) ;; *) fail "Config.Env に PORT=8080 がない: ${envs}" ;; esac
hc="$(inspect '{{json .Config.Healthcheck}}')"
[ "$hc" = "null" ] || fail "Config.Healthcheck がある: ${hc}"
size="$(inspect '{{.Size}}')"
[ "$size" -le 40000000 ] || fail "イメージが 40000000 バイトを超える: ${size}"
ok "${platform}、User ${user}、ExposedPorts ${ports}、MODE=single・PORT=8080、Healthcheck なし、${size} バイト"

step "6. コンテナの起動（${DEV_DOCKER_CONTAINER}）"
if docker container inspect "$DEV_DOCKER_CONTAINER" >/dev/null 2>&1; then
  docker rm -f "$DEV_DOCKER_CONTAINER" >/dev/null
fi
STARTED=1
docker run -d --platform linux/amd64 --name "$DEV_DOCKER_CONTAINER" --label "$DEV_DOCKER_LABEL" \
  --read-only --cap-drop ALL --security-opt no-new-privileges \
  -p 127.0.0.1::8080 "$DEV_DOCKER_IMAGE" >/dev/null || fail "docker run"
port_line="$(docker port "$DEV_DOCKER_CONTAINER" 8080/tcp | head -n 1)"
HOST_PORT="${port_line##*:}"
case "$HOST_PORT" in '' | *[!0-9]*) fail "ホスト側のポートが読めない: ${port_line}" ;; esac
ok "127.0.0.1:${HOST_PORT} → 8080"

step "7. /healthz（最大 15 秒）"
i=0
while :; do
  http_get /healthz
  [ "$HTTP_CODE" = "200" ] && break
  i=$((i + 1))
  [ "$i" -lt 30 ] || fail "/healthz が 15 秒のうちに 200 にならない（最後: ${HTTP_CODE}）"
  sleep 0.5
done
health="$(python3 -c '
import json, sys
with open(sys.argv[1], encoding="utf-8") as f:
    d = json.load(f)
print("%s %s" % (d.get("status", ""), d.get("build", "")))
' "$TMP_DIR/body")" || fail "/healthz の本文が JSON でない: $(cat "$TMP_DIR/body")"
[ "$health" = "ok ${BUILD_ID}" ] || fail "/healthz の status・build が違う（期待: ok ${BUILD_ID}、実際: ${health}）: $(cat "$TMP_DIR/body")"
ok "200 $(cat "$TMP_DIR/body")"

step "8. /ws と /api/healthz"
http_get /ws
[ "$HTTP_CODE" = "426" ] || fail "GET /ws（Upgrade なし）が 426 でない: ${HTTP_CODE}"
ok "GET /ws → 426"
http_get /api/healthz
[ "$HTTP_CODE" = "404" ] || fail "GET /api/healthz が 404 でない: ${HTTP_CODE}"
ok "GET /api/healthz → 404"

step "9. SIGTERM で止める"
t0="$(date +%s)"
docker stop -t 10 "$DEV_DOCKER_CONTAINER" >/dev/null || fail "docker stop"
elapsed=$(($(date +%s) - t0))
[ "$elapsed" -le 10 ] || fail "docker stop に ${elapsed} 秒かかった（10 秒を超えた）"
code="$(docker inspect --format '{{.State.ExitCode}}' "$DEV_DOCKER_CONTAINER")"
[ "$code" = "0" ] || fail "終了コードが 0 でない: ${code}"
docker logs "$DEV_DOCKER_CONTAINER" 2>&1 | grep -F '"mode":"single"' >/dev/null ||
  fail 'docker logs に "mode":"single" の行がない'
ok "${elapsed} 秒で終了コード 0、ログに \"mode\":\"single\" あり"

echo "docker-smoke: 成功（${DEV_DOCKER_IMAGE}、${size} バイト、build=${BUILD_ID}）"
