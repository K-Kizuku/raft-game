#!/usr/bin/env bash
# 作業ツリーごとのローカル環境（ポート・cf の状態・Docker の名前）を計算する（T-008 / ADR-0019）。
#
# 使い方:
#   scripts/dev-env.sh env          [--path P]              DEV_SLOT から計算した値を KEY=VALUE で出す（eval 用）
#   scripts/dev-env.sh dev          [--path P] [--dry-run]  Go（あれば）と Vite を起動する（make dev）
#   scripts/dev-env.sh clean-docker [--path P] [--dry-run]  この作業ツリーの Docker のコンテナとイメージを消す
#
#   --path P   作業ツリーの根（既定は git rev-parse --show-toplevel）。物理パスにしてから使う
#   --dry-run  実行せず、実行するはずのコマンドを「+ 」を付けて出す
#
# 環境変数:
#   DEV_SLOT  0〜99（env・dev で必須。scripts/dev-slot.sh acquire で割り当てる。0 は人が手で使う）
#   DOCKER    docker のコマンド（既定は docker。clean-docker で使う）
#   DEV_BACKEND  dev で /api・/ws を受ける先（standalone か worker。未設定なら Go があれば standalone）。
#                standalone は Vite から Go に中継し、worker は @cloudflare/vite-plugin（Worker）が受けて Go を起動しない（ADR-0026）
#
# ポート（それぞれ +1〜+9 はデバッグ用に予約）:
#   DEV_SERVER_PORT = 18080 + DEV_SLOT×10（Go の standalone）
#   DEV_WEB_PORT    = 15173 + DEV_SLOT×10（Vite）
#
# 終了コード: 成功は 0。使い方の誤り・値の不正・失敗は 1。
# bash 3.2（macOS）で動く書き方にする（mapfile・${v,,}・空の配列の "${a[@]}" と set -u を避ける）。

set -euo pipefail

PROG="dev-env"

usage() {
  cat <<'EOF'
使い方:
  scripts/dev-env.sh env          [--path P]              DEV_SLOT から計算した値を KEY=VALUE で出す（eval 用）
  scripts/dev-env.sh dev          [--path P] [--dry-run]  Go（あれば）と Vite を起動する（make dev）
  scripts/dev-env.sh clean-docker [--path P] [--dry-run]  この作業ツリーの Docker のコンテナとイメージを消す
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

# show <args...>: 実行するはずのコマンドを「+ 」を付けて 1 行で出す。
show() {
  local out="+" a
  for a in "$@"; do
    out="$out $a"
  done
  printf '%s\n' "$out"
}

# check_slot: DEV_SLOT を確かめ、DEV_SERVER_PORT・DEV_WEB_PORT を計算する。
check_slot() {
  if [ -z "${DEV_SLOT+x}" ]; then
    # shellcheck disable=SC2016
    die 'DEV_SLOT が未設定。`export DEV_SLOT="$(scripts/dev-slot.sh acquire)"` で割り当てる'
  fi
  local re='^(0|[1-9][0-9]?)$'
  if ! [[ "$DEV_SLOT" =~ $re ]]; then
    die "DEV_SLOT の値が不正: '$DEV_SLOT'（0〜99 の整数。先頭の 0 は不可）"
  fi
  DEV_SERVER_PORT=$((18080 + DEV_SLOT * 10))
  DEV_WEB_PORT=$((15173 + DEV_SLOT * 10))
}

# resolve_root <path or empty>: DEV_WORKTREE を決める。
resolve_root() {
  local p="$1"
  if [ -n "$p" ]; then
    [ -d "$p" ] || die "--path のディレクトリがない: $p"
  else
    p="$(git rev-parse --show-toplevel 2>/dev/null)" || die "作業ツリーの中で実行するか --path を指定する"
    [ -n "$p" ] || die "作業ツリーの中で実行するか --path を指定する"
  fi
  DEV_WORKTREE="$(cd "$p" && pwd -P)" || die "--path のディレクトリに入れない: $p"
}

# docker_names: DEV_WORKTREE から Docker の名前を決める。
docker_names() {
  local name sum
  name="$(basename "$DEV_WORKTREE" | LC_ALL=C tr '[:upper:]' '[:lower:]' |
    LC_ALL=C sed -e 's/[^a-z0-9_.-][^a-z0-9_.-]*/-/g' -e 's/^[-._]*//' -e 's/[-._]*$//')"
  name="${name:0:32}"
  name="$(printf '%s' "$name" | LC_ALL=C sed -e 's/[-._]*$//')"
  [ -n "$name" ] || name="wt"
  sum="$(printf '%s' "$DEV_WORKTREE" | cksum | awk '{ print $1 }')"
  DEV_DOCKER_SLUG="$name-$(printf '%08x' "$sum")"
  DEV_DOCKER_IMAGE="raft-game-server:$DEV_DOCKER_SLUG"
  DEV_DOCKER_CONTAINER="raft-game-server-$DEV_DOCKER_SLUG"
  DEV_DOCKER_LABEL="raft-game.worktree=$DEV_WORKTREE"
}

cmd_env() {
  check_slot
  docker_names
  printf '%s=%q\n' \
    DEV_SLOT "$DEV_SLOT" \
    DEV_SERVER_PORT "$DEV_SERVER_PORT" \
    DEV_WEB_PORT "$DEV_WEB_PORT" \
    DEV_WORKTREE "$DEV_WORKTREE" \
    DEV_CF_PERSIST_TO "$DEV_WORKTREE/web/.cloudflare/state" \
    DEV_DOCKER_SLUG "$DEV_DOCKER_SLUG" \
    DEV_DOCKER_IMAGE "$DEV_DOCKER_IMAGE" \
    DEV_DOCKER_CONTAINER "$DEV_DOCKER_CONTAINER" \
    DEV_DOCKER_LABEL "$DEV_DOCKER_LABEL"
}

DEV_TMP=""
DEV_GO_PID=""
DEV_WEB_PID=""

# dev_cleanup: 裏で動かしているフロントエンドと Go の両方に TERM を送り、終わるのを待ってから後始末する。
dev_cleanup() {
  local pid
  for pid in "$DEV_WEB_PID" "$DEV_GO_PID"; do
    if [ -n "$pid" ]; then kill "$pid" 2>/dev/null || true; fi
  done
  for pid in "$DEV_WEB_PID" "$DEV_GO_PID"; do
    if [ -n "$pid" ]; then wait "$pid" 2>/dev/null || true; fi
  done
  DEV_WEB_PID=""
  DEV_GO_PID=""
  if [ -n "$DEV_TMP" ]; then
    rm -rf "$DEV_TMP"
    DEV_TMP=""
  fi
}

cmd_dev() {
  local dry="$1" has_go=0 has_web=0 tmp backend="${DEV_BACKEND:-}"
  check_slot
  # DEV_BACKEND の誤りは、Go のビルドより前に止める（--dry-run でも同じ）。大文字・小文字を区別する。
  case "$backend" in
    "" | standalone | worker) ;;
    *) die "DEV_BACKEND の値が不正: '$backend'（standalone か worker。未設定なら Go があれば standalone）" ;;
  esac
  [ -f "$DEV_WORKTREE/server/cmd/server/main.go" ] && has_go=1
  [ -f "$DEV_WORKTREE/web/package.json" ] && has_web=1
  if [ -z "$backend" ]; then
    if [ "$has_go" = 1 ]; then backend=standalone; else backend=worker; fi
  fi
  if [ "$backend" = standalone ] && [ "$has_go" = 0 ]; then
    die "DEV_BACKEND=standalone だが server/cmd/server/main.go がない"
  fi
  echo "dev: DEV_SLOT=$DEV_SLOT DEV_SERVER_PORT=$DEV_SERVER_PORT DEV_WEB_PORT=$DEV_WEB_PORT"
  if [ "$backend" = standalone ]; then
    echo "dev: /api・/ws は Vite から Go（standalone）の http://127.0.0.1:$DEV_SERVER_PORT に中継する（DEV_BACKEND=standalone）"
  else
    echo "dev: /api・/ws は Worker（@cloudflare/vite-plugin）が受ける（DEV_BACKEND=worker）"
  fi
  if [ "$has_go" = 0 ] && [ "$has_web" = 0 ]; then
    die "起動するものがない（server/cmd/server/main.go も web/package.json もない）"
  fi
  if [ "$backend" = worker ] && [ "$has_go" = 1 ]; then
    # worker では Go をビルド・起動しない（/api・/ws は Worker が受ける）。
    echo "（Go は起動しない: DEV_BACKEND=worker）"
    has_go=0
    [ "$has_web" = 1 ] || die "起動するものがない（web/package.json がなく、DEV_BACKEND=worker では Go を起動しない）"
  elif [ "$has_go" = 0 ]; then
    echo "（対象なし: server/cmd/server がない。T-302 で作る）"
  fi
  [ "$has_web" = 1 ] || echo "（対象なし: web/package.json がない）"
  # Go は作業ツリーの data/ を読み（make sync-data は要らない）、Vite の画面からの Origin を許す（T-302）。
  local origins="http://127.0.0.1:$DEV_WEB_PORT,http://localhost:$DEV_WEB_PORT"

  if [ "$dry" = 1 ]; then
    if [ "$has_go" = 1 ]; then
      show mktemp -d
      show go -C "$DEV_WORKTREE/server" build -o "<tmp>/server" ./cmd/server
      if [ "$has_web" = 1 ]; then
        show MODE=standalone "PORT=$DEV_SERVER_PORT" "DATA_DIR=$DEV_WORKTREE/data" "ALLOWED_ORIGINS=$origins" "<tmp>/server" "&"
      else
        show MODE=standalone "PORT=$DEV_SERVER_PORT" "DATA_DIR=$DEV_WORKTREE/data" "ALLOWED_ORIGINS=$origins" "<tmp>/server"
      fi
    fi
    if [ "$has_web" = 1 ]; then
      show "DEV_BACKEND=$backend" "DEV_SERVER_PORT=$DEV_SERVER_PORT" pnpm -C "$DEV_WORKTREE/web" exec vite --host 127.0.0.1 --port "$DEV_WEB_PORT" --strictPort
    fi
    return 0
  fi

  if [ "$has_web" = 1 ]; then
    command -v pnpm >/dev/null 2>&1 || die "pnpm がない"
  fi
  if [ "$has_go" = 1 ]; then
    command -v go >/dev/null 2>&1 || die "go がない"
  fi

  # 前で動かすと、bash はそのコマンドが終わるまで TERM の trap を遅らせる。どちらも裏で起動し、
  # 割り込める wait で待つ。trap の exit から EXIT の trap（dev_cleanup）が両方に kill を送る。
  trap dev_cleanup EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM

  local pid="" rc=0
  if [ "$has_go" = 1 ]; then
    tmp="$(mktemp -d)" || die "一時ディレクトリを作れない"
    DEV_TMP="$tmp"
    go -C "$DEV_WORKTREE/server" build -o "$tmp/server" ./cmd/server || die "Go のビルドに失敗した"
    MODE=standalone PORT="$DEV_SERVER_PORT" DATA_DIR="$DEV_WORKTREE/data" ALLOWED_ORIGINS="$origins" "$tmp/server" &
    DEV_GO_PID=$!
    pid="$DEV_GO_PID"
  fi
  if [ "$has_web" = 1 ]; then
    DEV_BACKEND="$backend" DEV_SERVER_PORT="$DEV_SERVER_PORT" pnpm -C "$DEV_WORKTREE/web" exec vite --host 127.0.0.1 --port "$DEV_WEB_PORT" --strictPort &
    DEV_WEB_PID=$!
    pid="$DEV_WEB_PID"
  fi
  wait "$pid" || rc=$?
  if [ "$pid" = "$DEV_WEB_PID" ]; then DEV_WEB_PID=""; else DEV_GO_PID=""; fi
  return "$rc"
}

# uniq_lines: 標準入力の一覧から空行を除き、重複を除いて並べ替える（2 つの一覧の和を作る）。
uniq_lines() {
  grep -v '^[[:space:]]*$' | sort -u || true
}

# regex_quote <s>: 正規表現（Go の regexp）の特殊文字を「\」でエスケープして出す。
regex_quote() {
  printf '%s' "$1" | LC_ALL=C sed -e 's/[][\.*^$+?(){}|/]/\\&/g'
}

# exact_name_ids: 「ID 名前」の行から、名前がちょうど <name> のものの ID だけを出す。
exact_name_ids() {
  local want="$1" id names
  while IFS=' ' read -r id names; do
    if [ -n "$id" ] && [ "$names" = "$want" ]; then printf '%s\n' "$id"; fi
  done
}

cmd_clean_docker() {
  local dry="$1" docker="${DOCKER:-docker}" a b line
  local containers=() images=()
  docker_names
  if ! command -v "$docker" >/dev/null 2>&1; then
    echo "（対象なし: docker がない）"
    return 0
  fi
  local conn_err="Docker に接続できない（Docker Desktop が動いているか確かめる）"

  a="$("$docker" ps -a -q --filter "label=$DEV_DOCKER_LABEL")" || die "$conn_err"
  # name の filter は Docker 側で正規表現として照合される。特殊文字（名前に入りうる「.」など）を
  # エスケープし、さらに名前がちょうど一致するものだけを残す（raft.game が raftXgame に当たらないように）。
  b="$("$docker" ps -a --filter "name=^$(regex_quote "$DEV_DOCKER_CONTAINER")\$" --format '{{.ID}} {{.Names}}')" || die "$conn_err"
  b="$(printf '%s\n' "$b" | exact_name_ids "$DEV_DOCKER_CONTAINER")"
  while IFS= read -r line; do
    if [ -n "$line" ]; then containers+=("$line"); fi
  done <<EOF
$(printf '%s\n%s\n' "$a" "$b" | uniq_lines)
EOF

  a="$("$docker" images --filter "label=$DEV_DOCKER_LABEL" --format '{{.Repository}}:{{.Tag}}')" || die "$conn_err"
  b="$("$docker" images --format '{{.Repository}}:{{.Tag}}' "$DEV_DOCKER_IMAGE")" || die "$conn_err"
  while IFS= read -r line; do
    case "$line" in
      "" | *"<none>"*) ;;
      *) images+=("$line") ;;
    esac
  done <<EOF
$(printf '%s\n%s\n' "$a" "$b" | uniq_lines)
EOF

  if [ "${#containers[@]}" -eq 0 ] && [ "${#images[@]}" -eq 0 ]; then
    echo "この作業ツリーの Docker のコンテナ・イメージはない"
    return 0
  fi

  if [ "${#containers[@]}" -gt 0 ]; then
    if [ "$dry" = 1 ]; then
      show "$docker" rm -f "${containers[@]}"
    else
      "$docker" rm -f "${containers[@]}" || die "コンテナを消せなかった"
    fi
  fi
  if [ "${#images[@]}" -gt 0 ]; then
    if [ "$dry" = 1 ]; then
      show "$docker" image rm "${images[@]}"
    else
      "$docker" image rm "${images[@]}" || die "イメージを消せなかった"
    fi
  fi
}

main() {
  local sub="" path="" path_set=0 dry=0
  if [ $# -eq 0 ]; then
    usage_error "サブコマンドがない"
  fi
  case "$1" in
    -h | --help | help)
      usage
      exit 0
      ;;
    env | dev | clean-docker) sub="$1" ;;
    *) usage_error "未知のサブコマンド: $1" ;;
  esac
  shift
  while [ $# -gt 0 ]; do
    case "$1" in
      -h | --help)
        usage
        exit 0
        ;;
      --path)
        [ $# -ge 2 ] || usage_error "--path に値がない"
        [ -n "$2" ] || usage_error "--path に値がない"
        path="$2"
        path_set=1
        shift 2
        ;;
      --dry-run)
        [ "$sub" != env ] || usage_error "env に --dry-run はない"
        dry=1
        shift
        ;;
      *) usage_error "未知の引数: $1" ;;
    esac
  done
  [ "$path_set" = 1 ] || path=""
  resolve_root "$path"
  case "$sub" in
    env) cmd_env ;;
    dev) cmd_dev "$dry" ;;
    clean-docker) cmd_clean_docker "$dry" ;;
  esac
}

main "$@"
