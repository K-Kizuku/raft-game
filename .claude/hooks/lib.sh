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
#   - プロセス置換は使わず、いったん変数に受けて（x="$(…)" || internal_error …）、split_lines・split_fields で
#     配列に分けてから読む
#   - here-string（<<<）とヒアドキュメント（<<）を使わない。bash 3.2（と、大きな入力の bash 5）は一時ファイルを作り、
#     作れない（読み取り専用の環境など）とリダイレクトが失敗して、while … done <<<"$x" の検査を飛ばしたまま
#     末尾の allow に着く（set -e では止まらない。F8）。一時ファイルを作らない形（パイプ・パラメータの展開）にする
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

# re_match <文字列> <grep の引数…>: 文字列が一致すれば 0、しなければ 1。grep のエラー（終了コード 2 以上）と、
# 文字列を grep に渡しきれなかったとき（一致しないのに printf が失敗した）は internal_error で拒否する。
# here-string（<<<）は一時ファイルを作り、作れないと「一致しない」になるので使わない（F8）。パイプの終了コードは
# PIPESTATUS で 1 つずつ見る（pipefail では、grep -q が先に終わったときの printf の SIGPIPE が失敗に見える）。
# パイプの中の exit は親に届かないので、internal_error はパイプの外で呼ぶ。
re_match() {
  local s="$1" ps
  shift
  if printf '%s\n' "$s" 2>/dev/null | grep -q "$@"; then ps="${PIPESTATUS[*]}"; else ps="${PIPESTATUS[*]}"; fi
  case "$ps" in
    *" 0") return 0 ;; # grep が一致を見つけた（printf は SIGPIPE で終わりうる）
    "0 1") return 1 ;;
  esac
  internal_error "grep が失敗した（終了コード ${ps}）"
}

# grep_out <文字列> <grep の引数…>: 文字列を grep に掛けた出力（一致しなければ空で 0）。
# grep のエラー（終了コード 2 以上）や、文字列を grep に渡しきれなかったときは 0 以外を返す
# （呼び出し側で internal_error にする）。here-string（<<<）を使わない理由は re_match と同じ。
grep_out() {
  local s="$1" ps
  shift
  if printf '%s\n' "$s" 2>/dev/null | grep "$@"; then ps="${PIPESTATUS[*]}"; else ps="${PIPESTATUS[*]}"; fi
  case "$ps" in
    "0 0" | "0 1") return 0 ;;
  esac
  return 2
}

# split_lines <文字列>: 文字列を改行で分けた行を、配列 SPLIT に入れる（while IFS= read -r … <<<"$x" と同じ行。
# 空の文字列は空の行 1 つ。末尾の改行のあとにも空の行 1 つ）。一時ファイルを作らないよう、パラメータの展開で分ける。
SPLIT=()
split_lines() {
  local s="$1" nl=$'\n'
  SPLIT=()
  while :; do
    case "$s" in
      *"$nl"*)
        SPLIT+=("${s%%"$nl"*}")
        s="${s#*"$nl"}"
        ;;
      *)
        SPLIT+=("$s")
        return 0
        ;;
    esac
  done
}

# split_fields <区切り（IFS）> <文字列>: 文字列を区切りで分けた単語を、配列 SPLIT に入れる
# （IFS=… read -r -a … <<<"$x" の代わり。改行を含まない文字列に使う）。パス名の展開はしない（set -f）。
split_fields() {
  local IFS="$1" had_f=0
  case "$-" in
    *f*) had_f=1 ;;
  esac
  set -f
  # shellcheck disable=SC2206
  SPLIT=($2)
  [ "$had_f" = 1 ] || set +f
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

# dotgit_above <dir>: <dir> かその親のディレクトリのどれかに .git があれば 0、なければ 1。
dotgit_above() {
  local p
  p="$(abs_dir "$1")" || p="$1"
  while :; do
    [ -e "$p/.git" ] && return 0
    case "$p" in
      '' | /) return 1 ;;
    esac
    p="${p%/*}"
  done
}

# git_not_repo <dir> <git の引数…>: git -C <dir> <引数…> が終了コード 128 で失敗したとき、それが本当に
# 「Git の外」によるものなら 0、そうでなければ（設定の読み込みの失敗・safe.directory・-C の先がないなど）1。
# 128 は Git の外だけでなく、git の設定の読み込みの失敗（GIT_CONFIG_COUNT=broken など）でも返るので、
# 終了コードだけでは見分けない（F10。Worker を Orchestrator と誤って判定し、マージの操作を許していた）。
# 同じコマンドを LC_ALL=C でもう一度実行して標準エラーの文言を見る。文言が「not a git repository」でも、
# <dir> か親に .git があれば Git の外とは扱わない（GIT_DIR・GIT_CEILING_DIRECTORIES などで見失ったとき）。
git_not_repo() {
  local d="$1" err rc=0
  shift
  err="$(LC_ALL=C LANGUAGE=C git -C "$d" "$@" 2>&1 >/dev/null)" || rc=$?
  [ "$rc" = 128 ] || return 1
  case "$err" in
    "fatal: not a git repository"*) ;;
    *) return 1 ;;
  esac
  dotgit_above "$d" && return 1
  return 0
}

# git_out <dir> <git の引数…>: git -C <dir> <引数…> の標準出力。Git の外（git_not_repo）なら空で 0。
# それ以外の失敗（設定の読み込みの失敗などの 128・git がない 127・シグナルなど）では 1 を返す
# （呼び出し側で internal_error にする）。
git_out() {
  local d="$1" out rc=0
  shift
  out="$(git -C "$d" "$@" 2>/dev/null)" || rc=$?
  case "$rc" in
    0) printf '%s\n' "$out" ;;
    128) git_not_repo "$d" "$@" || return 1 ;;
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
