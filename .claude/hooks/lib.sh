#!/usr/bin/env bash
# hooks の共通の関数（Issue #5）。guard-*.sh から source する。
#
# - 入力（標準入力の JSON）は HOOK_INPUT に読み込む
# - 役割は AGENT_ROLE（orchestrator・worker・reviewer・human）。未設定のときは、
#   リンクした Worktree の中（git-dir と git-common-dir が異なる）なら worker、
#   そうでなければ orchestrator
# - 拒否するときは deny "理由" → 理由を標準エラーに出して終了コード 2
# - 許可するときは allow → 終了コード 0。exit 0 で直接終わらない
# - 想定外のエラー（set -e・set -u での終了など）でも終了コード 2（拒否）にする（fail-closed、#107）。
#   Claude Code は終了コード 2 だけを拒否として扱い、1 などでは操作を通してしまう。
#   allow・deny を通らずに終わったら、終了コードによらず拒否にする（bash 3.2 では、set -u などで
#   終わるときの EXIT の trap の $? が 0 になることがあり、終了コードでは見分けられない）
# - bash 3.2（macOS の /bin/bash）でも動く書き方にする。変数の直後に ASCII 以外の文字が続くときは
#   ${var} と書く（bash 3.2 と UTF-8 のロケールでは、$t の直後に「）」があると、そのバイトまで変数名として読まれる。
#   scripts/tests/test_shell_scripts.py が検査する）
# - 判定に使うデータを作るコマンド（tr・jq・sed・grep・git など）の失敗は、親のシェルで明示的に検査し、
#   internal_error で拒否する。set -e に頼らない（コマンド置換の中・if や || の中の関数では set -e が効かず、
#   プロセス置換 < <(…) の失敗はそもそも親に伝わらない）。そのため:
#   - プロセス置換は使わず、いったん変数に受けて（x="$(…)" || internal_error …）、<<<"$x" で読む
#   - 一致の判定は printf … | grep -q ではなく re_match を使う（grep のエラーを「一致しない」と区別する）
#   - コマンド置換を引数の中に入れ子にしない（"$(f "$(g)")" の g の失敗は伝わらない）
# shellcheck shell=bash

HOOK_INPUT=""
ROLE=""
HOOK_RESULT=""

# hook_on_exit: EXIT の trap。allow（0）・deny（2）で終わったのでなければ、2（拒否）に付け替える。
hook_on_exit() {
  local rc=$?
  case "${HOOK_RESULT}:${rc}" in
    allow:0 | deny:2) return 0 ;;
  esac
  echo "[guard] 拒否（内部エラー）: hook が想定外のエラーで終了した（終了コード ${rc}・${0##*/}）。安全のため拒否する" >&2
  exit 2
}
trap hook_on_exit EXIT

deny() {
  echo "[guard] 拒否（AGENT_ROLE=${ROLE:-?}）: $*" >&2
  HOOK_RESULT="deny"
  exit 2
}

allow() {
  HOOK_RESULT="allow"
  exit 0
}

# internal_error "理由": 想定外のエラー。終了コード 2 で拒否する（fail-closed）。
# コマンド置換 $(…) の中で呼ぶと、そのサブシェルが 2 で終わるだけなので、呼び出し側でも終了コードを検査する。
internal_error() {
  echo "[guard] 拒否（内部エラー）: $*。安全のため拒否する" >&2
  HOOK_RESULT="deny"
  exit 2
}

# re_match <文字列> <grep の引数…>: 文字列が一致すれば 0、しなければ 1。grep のエラー（終了コード 2 以上）は
# internal_error で拒否する。パイプ（printf … | grep -q）では、grep のエラーと「一致しない」を見分けられず、
# パイプの中の exit も親に届かないので、文字列は <<< で渡す。
re_match() {
  local s="$1" rc=0
  shift
  grep -q "$@" <<<"$s" || rc=$?
  [ "$rc" -le 1 ] || internal_error "grep が失敗した（終了コード ${rc}）"
  return "$rc"
}

# grep_out <文字列> <grep の引数…>: 文字列を grep に掛けた出力（一致しなければ空で 0）。
# grep のエラー（終了コード 2 以上）では、その終了コードを返す（呼び出し側で internal_error にする）。
grep_out() {
  local s="$1" rc=0
  shift
  grep "$@" <<<"$s" || rc=$?
  [ "$rc" -le 1 ] || return "$rc"
  return 0
}

hook_read_input() {
  command -v jq >/dev/null 2>&1 || deny "jq がないため安全に判定できない"
  HOOK_INPUT="$(cat)" || internal_error "hooks の入力を読めない"
  printf '%s' "$HOOK_INPUT" | jq -e . >/dev/null 2>&1 || deny "hooks の入力が JSON ではない"
}

# hook_field <jq のパス>: 入力の JSON から文字列を取り出す（なければ空）。jq が失敗したら 0 以外を返す。
hook_field() {
  printf '%s' "$HOOK_INPUT" | jq -r "($1) // empty | tostring"
}

# abs_dir <dir>: 実在するディレクトリの物理パス。
abs_dir() {
  (cd "$1" 2>/dev/null && pwd -P)
}

# 作業ディレクトリ（入力の cwd → CLAUDE_PROJECT_DIR → PWD）の物理パス。入力を読めなければ 0 以外を返す。
hook_cwd() {
  local d
  d="$(hook_field '.cwd')" || return 1
  if [ -z "$d" ] || [ ! -d "$d" ]; then
    d="${CLAUDE_PROJECT_DIR:-$PWD}"
  fi
  [ -d "$d" ] || d="$PWD"
  abs_dir "$d"
}

# git_out <dir> <git の引数…>: git -C <dir> <引数…> の標準出力。Git の外（終了コード 128）なら空で 0。
# それ以外の失敗（git がない 127・シグナルなど）では 1 を返す（呼び出し側で internal_error にする）。
git_out() {
  local d="$1" out rc=0
  shift
  out="$(git -C "$d" "$@" 2>/dev/null)" || rc=$?
  case "$rc" in
    0) printf '%s\n' "$out" ;;
    128) ;;
    *) return 1 ;;
  esac
}

# git_common_dir <dir>: git-common-dir の物理パス（Git の外なら空）。git が失敗したら 0 以外を返す。
git_common_dir() {
  local c
  c="$(git_out "$1" rev-parse --git-common-dir)" || return 1
  [ -n "$c" ] || return 0
  case "$c" in
    /*) ;;
    *) c="$1/$c" ;;
  esac
  abs_dir "$c"
}

# detect_role <cwd>: ROLE を決める。
detect_role() {
  local cwd="$1" gd cd
  if [ -n "${AGENT_ROLE:-}" ]; then
    ROLE="$AGENT_ROLE"
    case "$AGENT_ROLE" in
      orchestrator | worker | reviewer | human) ;;
      *) deny "AGENT_ROLE の値が不正（orchestrator・worker・reviewer・human のどれか）" ;;
    esac
    return 0
  fi
  ROLE="orchestrator"
  gd="$(git_out "$cwd" rev-parse --absolute-git-dir)" || internal_error "役割を決められない（git rev-parse --absolute-git-dir が失敗した）"
  [ -n "$gd" ] || return 0
  gd="$(abs_dir "$gd")" || internal_error "役割を決められない（git-dir の物理パスが分からない）"
  cd="$(git_common_dir "$cwd")" || internal_error "役割を決められない（git-common-dir が分からない）"
  if [ -n "$gd" ] && [ -n "$cd" ] && [ "$gd" != "$cd" ]; then
    ROLE="worker"
  fi
}

# repo_toplevel <cwd>: 作業ツリーのルート（物理パス）。Git の外なら空。git が失敗したら 0 以外を返す。
repo_toplevel() {
  local t
  t="$(git_out "$1" rev-parse --show-toplevel)" || return 1
  [ -n "$t" ] || return 0
  abs_dir "$t"
}

# repo_main_root <cwd>: メインの作業ツリーのルート（git-common-dir の親）。git が失敗したら 0 以外を返す。
repo_main_root() {
  local cd
  cd="$(git_common_dir "$1")" || return 1
  [ -n "$cd" ] || return 0
  dirname "$cd"
}

# agent_issue <cwd>: Worker の担当の Issue 番号。AGENT_ISSUE、なければ
# Worktree のディレクトリ名 issue-<n> から。分からなければ空。git が失敗したら 0 以外を返す。
agent_issue() {
  local top base
  if [ -n "${AGENT_ISSUE:-}" ]; then
    printf '%s\n' "$AGENT_ISSUE"
    return 0
  fi
  top="$(repo_toplevel "$1")" || return 1
  base="${top##*/}"
  case "$base" in
    issue-*)
      base="${base#issue-}"
      case "$base" in
        '' | *[!0-9]*) ;;
        *) printf '%s\n' "$base" ;;
      esac
      ;;
  esac
}
