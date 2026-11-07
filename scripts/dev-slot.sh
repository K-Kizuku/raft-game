#!/usr/bin/env bash
# DEV_SLOT（並列開発の作業用の番号。ADR-0019）を確保・解放する（Issue #5）。
#
# 使い方: scripts/dev-slot.sh <acquire|release|list|current> [オプション]
#   acquire [--path P] [--pid PID]  P（既定: 今の作業ツリー）の枠を確保して番号を出す。
#                                    すでに P の枠があれば同じ番号を出す（--pid で持ち主を更新）
#   release [N | --path P]          枠 N（既定: P の枠）を解放する
#   list                            枠の一覧（番号・パス・stale の印）
#   current [--path P]              P の枠の番号を出す（なければ終了コード 1）
#
# - 枠は $(git rev-parse --git-common-dir)/agent-slots/<番号> を mkdir で原子的に作り、
#   中の path に作業ツリーのパスを書く。番号は 1〜dev_slots（.agents/config.json、既定 15）
# - パスが存在せず、持ち主の pid も生きていない枠は古いものとして回収する
# - 空いている枠がなければ終了コード 1
set -euo pipefail

PROG="dev-slot"

die() {
  echo "$PROG: $*" >&2
  exit 1
}

usage() {
  sed -n '2,/^set -euo/p' "$0" | sed '$d' | sed 's/^# \{0,1\}//'
}

command -v git >/dev/null 2>&1 || die "git が見つからない"

abs_dir() {
  (cd "$1" 2>/dev/null && pwd -P)
}

common="$(git rev-parse --git-common-dir 2>/dev/null)" || die "Git リポジトリの中で実行する"
case "$common" in
  /*) ;;
  *) common="$PWD/$common" ;;
esac
common="$(abs_dir "$common")"
slots_dir="$common/agent-slots"
root="$(dirname "$common")"

max_slots=15
cfg="${AGENT_CONFIG:-$root/.agents/config.json}"
if [ -f "$cfg" ] && command -v jq >/dev/null 2>&1; then
  v="$(jq -r '.dev_slots // empty' "$cfg" 2>/dev/null || true)"
  case "$v" in
    '' | *[!0-9]*) ;;
    *) max_slots="$v" ;;
  esac
fi

default_path() {
  local t
  t="$(git rev-parse --show-toplevel 2>/dev/null)" || die "作業ツリーの中で実行するか --path を指定する"
  abs_dir "$t"
}

# norm_path <P>: 存在すれば物理パス、なければ絶対パスのまま。
norm_path() {
  local p="$1"
  case "$p" in
    /*) ;;
    *) p="$PWD/$p" ;;
  esac
  p="${p%/}"
  if [ -d "$p" ]; then abs_dir "$p"; else printf '%s\n' "$p"; fi
}

slot_path() {
  cat "$slots_dir/$1/path" 2>/dev/null || true
}

slot_pid() {
  cat "$slots_dir/$1/pid" 2>/dev/null || true
}

is_stale() {
  local p pid
  p="$(slot_path "$1")"
  pid="$(slot_pid "$1")"
  if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
    return 1
  fi
  # path を書く前の枠（mkdir の直後）は stale としない
  [ -f "$slots_dir/$1/path" ] || return 1
  [ ! -e "$p" ]
}

# find_slot <P>: P の枠の番号（なければ空）。
find_slot() {
  local i
  for ((i = 1; i <= max_slots; i++)); do
    if [ -d "$slots_dir/$i" ] && [ "$(slot_path "$i")" = "$1" ]; then
      printf '%s\n' "$i"
      return 0
    fi
  done
}

sub="${1:-}"
[ "$#" -gt 0 ] && shift
path=""
pid=""
num=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --path)
      [ "$#" -ge 2 ] || die "--path には値が必要"
      path="$2"
      shift
      ;;
    --pid)
      [ "$#" -ge 2 ] || die "--pid には値が必要"
      pid="$2"
      shift
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    [0-9]*) num="$1" ;;
    *) die "不明な引数: $1" ;;
  esac
  shift
done

case "$sub" in
  acquire)
    [ -n "$path" ] || path="$(default_path)"
    path="$(norm_path "$path")"
    mkdir -p "$slots_dir"
    n="$(find_slot "$path")"
    if [ -n "$n" ]; then
      [ -z "$pid" ] || printf '%s\n' "$pid" >"$slots_dir/$n/pid"
      printf '%s\n' "$n"
      exit 0
    fi
    for ((i = 1; i <= max_slots; i++)); do
      if [ -d "$slots_dir/$i" ] && is_stale "$i"; then
        echo "$PROG: 古い枠 $i を回収する（$(slot_path "$i") がない）" >&2
        rm -rf "${slots_dir:?}/$i"
      fi
      if mkdir "$slots_dir/$i" 2>/dev/null; then
        [ -z "$pid" ] || printf '%s\n' "$pid" >"$slots_dir/$i/pid"
        printf '%s\n' "$path" >"$slots_dir/$i/path.tmp"
        mv "$slots_dir/$i/path.tmp" "$slots_dir/$i/path"
        printf '%s\n' "$i"
        exit 0
      fi
    done
    die "空いている DEV_SLOT がない（dev_slots=${max_slots}）。scripts/dev-slot.sh list で確かめる"
    ;;
  release)
    if [ -z "$num" ]; then
      [ -n "$path" ] || path="$(default_path)"
      path="$(norm_path "$path")"
      num="$(find_slot "$path")"
      [ -n "$num" ] || {
        echo "$PROG: $path の枠はない" >&2
        exit 0
      }
    fi
    case "$num" in
      *[!0-9]*) die "枠の番号が不正: $num" ;;
    esac
    rm -rf "${slots_dir:?}/$num"
    ;;
  list)
    [ -d "$slots_dir" ] || exit 0
    for ((i = 1; i <= max_slots; i++)); do
      [ -d "$slots_dir/$i" ] || continue
      mark=""
      if is_stale "$i"; then mark=" (stale)"; fi
      printf '%s\t%s%s\n' "$i" "$(slot_path "$i")" "$mark"
    done
    ;;
  current)
    [ -n "$path" ] || path="$(default_path)"
    path="$(norm_path "$path")"
    n="$(find_slot "$path")"
    [ -n "$n" ] || die "$path の枠はない"
    printf '%s\n' "$n"
    ;;
  -h | --help | help)
    usage
    ;;
  *)
    usage >&2
    exit 1
    ;;
esac
