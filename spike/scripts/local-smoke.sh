#!/usr/bin/env bash
# スパイク（T-007a / #134）のローカルでの確認（M-12）。cf dev で Worker・Durable Object・コンテナ・WS の中継が動くか。
#
#   DEV_SLOT=<n> bash spike/scripts/local-smoke.sh
#
# - ポートは DEV_SLOT から計算する（scripts/dev-env.sh の DEV_WEB_PORT + 1。ADR-0019 の予約の範囲）
# - トークンは毎回作り、spike/.dev.vars（spike/.gitignore で無視）に書いて、終わったら消す
# - ローカルの Durable Object の状態（spike/.cloudflare/state）は毎回消してから始める（起動の回数の上限を数え直すため）
# - 結果は spike/.cloudflare/local-smoke.json（無視される場所）
# - 終わり（失敗・Ctrl-C を含む）に cf dev を止め、名前・イメージに raft-game-spike を含むコンテナが残っていれば
#   その ID だけを docker stop する（prune しない。イメージは消さない。消し方は spike/README.md）
#
# 終了コード: 必須の確認がすべて成功なら 0、失敗は 1、DEV_SLOT がないなど使い方の誤りは 2。
# bash 3.2（macOS）で動く書き方にする。

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
SPIKE="$ROOT/spike"
PROG="local-smoke"

if [ -z "${DEV_SLOT:-}" ]; then
  echo "$PROG: DEV_SLOT が要る（例: DEV_SLOT=\"\$(scripts/dev-slot.sh acquire)\" bash spike/scripts/local-smoke.sh）" >&2
  exit 2
fi
dev_env="$(bash "$ROOT/scripts/dev-env.sh" env --path "$ROOT")" || exit 2
eval "$dev_env"
SPIKE_DEV_PORT=$((DEV_WEB_PORT + 1))
export SPIKE_DEV_PORT
BASE="http://127.0.0.1:$SPIKE_DEV_PORT"

for cmd in docker openssl curl node pnpm; do
  command -v "$cmd" >/dev/null 2>&1 || {
    echo "$PROG: $cmd が要る" >&2
    exit 1
  }
done
docker info >/dev/null 2>&1 || {
  echo "$PROG: Docker が動いていない" >&2
  exit 1
}

# cf dev（workerd）は docker の context を見ず、DOCKER_HOST がなければ /var/run/docker.sock を使う。
# Docker Desktop（macOS）などではそこにソケットがないので、今の context の宛先を渡す。
if [ -z "${DOCKER_HOST:-}" ]; then
  docker_host="$(docker context inspect --format '{{.Endpoints.docker.Host}}' 2>/dev/null || true)"
  if [ -n "$docker_host" ]; then
    export DOCKER_HOST="$docker_host"
    echo "$PROG: DOCKER_HOST=${DOCKER_HOST}（docker context から）"
  fi
fi

if (exec 3<>"/dev/tcp/127.0.0.1/$SPIKE_DEV_PORT") 2>/dev/null; then
  echo "$PROG: ポート $SPIKE_DEV_PORT が使われている（DEV_SLOT=${DEV_SLOT}）" >&2
  exit 1
fi

DEV_VARS="$SPIKE/.dev.vars"
OUT_DIR="$SPIKE/.cloudflare"
OUT="$OUT_DIR/local-smoke.json"
LOG="$OUT_DIR/local-smoke.cf-dev.log"
mkdir -p "$OUT_DIR"
cf_pid=""

cleanup() {
  local status=$?
  trap - EXIT INT TERM
  if [ -n "$cf_pid" ] && kill -0 "$cf_pid" 2>/dev/null; then
    echo "$PROG: cf dev を止める（pid ${cf_pid}）"
    kill -TERM -- "-$cf_pid" 2>/dev/null || kill -TERM "$cf_pid" 2>/dev/null || true
    local i
    for i in 1 2 3 4 5 6 7 8 9 10; do
      kill -0 "$cf_pid" 2>/dev/null || break
      sleep 1
      [ "$i" -lt 10 ] || kill -KILL -- "-$cf_pid" 2>/dev/null || true
    done
  fi
  rm -f "$DEV_VARS"
  local left
  left="$(docker ps --format '{{.ID}} {{.Names}} {{.Image}}' | grep 'raft-game-spike' || true)"
  if [ -n "$left" ]; then
    echo "$PROG: 動いたまま残っていたコンテナ（docker stop する）:"
    echo "$left"
    local id _rest
    while read -r id _rest; do
      docker stop "$id" >/dev/null || true
    done <<EOF_LEFT
$left
EOF_LEFT
  else
    echo "$PROG: 動いたまま残ったコンテナはない"
  fi
  local exited
  exited="$(docker ps -a --filter status=exited --format '{{.ID}} {{.Names}}' | grep 'raft-game-spike' || true)"
  if [ -n "$exited" ]; then
    echo "$PROG: 止まったコンテナが残っている（消すなら spike/README.md の「後片付け」）:"
    echo "$exited"
  fi
  echo "$PROG: ローカルのイメージ:"
  docker images --format '{{.Repository}}:{{.Tag}} {{.Size}}' | grep -E 'spikesession|raft-game-spike|proxy-everything' || true
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT TERM

umask 077
printf 'SPIKE_TOKEN=%s\n' "$(openssl rand -hex 32)" >"$DEV_VARS"
rm -rf "$OUT_DIR/state"

echo "$PROG: cf dev を起動する（${BASE}、ログは ${LOG}）"
set -m # cf dev を別のプロセスグループにして、子のプロセスごと止められるようにする
(cd "$SPIKE" && exec pnpm exec cf dev) >"$LOG" 2>&1 &
cf_pid=$!
set +m

t0=$(date +%s)
until curl -fsS --max-time 2 "$BASE/spike/healthz" >/dev/null 2>&1; do
  if ! kill -0 "$cf_pid" 2>/dev/null; then
    echo "$PROG: cf dev が終わった。ログ:" >&2
    tail -n 40 "$LOG" >&2 || true
    exit 1
  fi
  if [ $(($(date +%s) - t0)) -ge 180 ]; then
    echo "$PROG: 180 秒待っても /spike/healthz が応答しない。ログ:" >&2
    tail -n 40 "$LOG" >&2 || true
    exit 1
  fi
  sleep 2
done
echo "$PROG: cf dev の準備ができた（$(($(date +%s) - t0)) 秒）"

token="$(cut -d= -f2- "$DEV_VARS")"
measure_status=0
SPIKE_TOKEN="$token" node "$SPIKE/scripts/measure.ts" --base "$BASE" \
  --only start,ws,ping,sql,plain,egress,crash,cpu,stop --runs 3 --idle 0 --out "$OUT" || measure_status=$?
echo "$PROG: measure.ts の終了コード ${measure_status}（記録だけの項目の失敗を含む）"

# 必須: start・ws（1 KiB と 16 KiB の往復・超過の破棄）・sql・plain（両方）・stop
# 記録だけ: egress・crash・cpu
node - "$OUT" <<'EOF'
const r = JSON.parse(require("node:fs").readFileSync(process.argv[2], "utf8"));
const it = (n) => r.items[n] ?? { ok: false, data: {} };
const ws = it("ws").data;
const checks = [
  ["start（準備完了）", it("start").ok],
  ["ws 1 KiB の往復", (ws.rtt1k?.n ?? 0) > 0 && ws.lost1k === 0],
  ["ws 16 KiB の往復", ws.ok16k === ws.sent16k && ws.sent16k > 0],
  ["ws 16 KiB 超過の破棄", ws.oversizeDropped === true],
  ["sql の追記", it("sql").data.rows1000ClientMs !== undefined && it("sql").ok],
  ["PlainStore", it("plain").data.plainOk === true],
  ["ControlStore", it("plain").data.controlOk === true],
  ["stop", it("stop").ok],
];
const recorded = ["egress", "crash", "cpu"].map((n) => [n, it(n).ok]);
for (const [name, ok] of checks) console.log(`必須    ${ok ? "ok" : "失敗"}  ${name}`);
for (const [name, ok] of recorded) console.log(`記録    ${ok ? "ok" : "失敗"}  ${name}`);
const e = it("egress").data.internet0;
if (e) console.log(`egress（enableInternet: false）: dns=${e.dns} / tcp=${e.tcp} / https=${e.https}`);
process.exitCode = checks.every(([, ok]) => ok) ? 0 : 1;
EOF
