#!/usr/bin/env bash
# hooks の共通の関数（Issue #5）。guard-*.sh から source する。
#
# - 入力（標準入力の JSON）は HOOK_INPUT に読み込む
# - 役割は AGENT_ROLE（orchestrator・worker・reviewer・human）。未設定のときは、
#   リンクした Worktree の中（git-dir と git-common-dir が異なる）なら worker、
#   そうでなければ orchestrator
# - 拒否するときは deny "理由" → 理由を標準エラーに出して終了コード 2
# shellcheck shell=bash

HOOK_INPUT=""
ROLE=""

deny() {
  echo "[guard] 拒否（AGENT_ROLE=${ROLE:-?}）: $*" >&2
  exit 2
}

hook_read_input() {
  command -v jq >/dev/null 2>&1 || deny "jq がないため安全に判定できない"
  HOOK_INPUT="$(cat)"
  printf '%s' "$HOOK_INPUT" | jq -e . >/dev/null 2>&1 || deny "hooks の入力が JSON ではない"
}

# hook_field <jq のパス>: 入力の JSON から文字列を取り出す（なければ空）。
hook_field() {
  printf '%s' "$HOOK_INPUT" | jq -r "($1) // empty | tostring"
}

# abs_dir <dir>: 実在するディレクトリの物理パス。
abs_dir() {
  (cd "$1" 2>/dev/null && pwd -P)
}

# 作業ディレクトリ（入力の cwd → CLAUDE_PROJECT_DIR → PWD）の物理パス。
hook_cwd() {
  local d
  d="$(hook_field '.cwd')"
  if [ -z "$d" ] || [ ! -d "$d" ]; then
    d="${CLAUDE_PROJECT_DIR:-$PWD}"
  fi
  [ -d "$d" ] || d="$PWD"
  abs_dir "$d"
}

# git_common_dir <dir>: git-common-dir の物理パス（Git の外なら空）。
git_common_dir() {
  local c
  c="$(git -C "$1" rev-parse --git-common-dir 2>/dev/null)" || return 0
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
  gd="$(git -C "$cwd" rev-parse --absolute-git-dir 2>/dev/null)" || return 0
  gd="$(abs_dir "$gd")"
  cd="$(git_common_dir "$cwd")"
  if [ -n "$gd" ] && [ -n "$cd" ] && [ "$gd" != "$cd" ]; then
    ROLE="worker"
  fi
}

# repo_toplevel <cwd>: 作業ツリーのルート（物理パス）。Git の外なら空。
repo_toplevel() {
  local t
  t="$(git -C "$1" rev-parse --show-toplevel 2>/dev/null)" || return 0
  abs_dir "$t"
}

# repo_main_root <cwd>: メインの作業ツリーのルート（git-common-dir の親）。
repo_main_root() {
  local cd
  cd="$(git_common_dir "$1")"
  [ -n "$cd" ] || return 0
  dirname "$cd"
}

# agent_issue <cwd>: Worker の担当の Issue 番号。AGENT_ISSUE、なければ
# Worktree のディレクトリ名 issue-<n> から。分からなければ空。
agent_issue() {
  local top base
  if [ -n "${AGENT_ISSUE:-}" ]; then
    printf '%s\n' "$AGENT_ISSUE"
    return 0
  fi
  top="$(repo_toplevel "$1")"
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
