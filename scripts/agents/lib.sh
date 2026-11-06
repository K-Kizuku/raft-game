#!/usr/bin/env bash
# scripts/agents/*.sh の共通の関数（Issue #5）。source して使う。
# bash 3.2 でも動く書き方にする（連想配列を使わない）。
# shellcheck shell=bash

AGENTS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# die <メッセージ>: 標準エラーに出して終了コード 1。PROG は呼び出し側で決める。
die() {
  echo "${PROG:-agents}: $*" >&2
  exit 1
}

# die_code <終了コード> <メッセージ>
die_code() {
  local code="$1"
  shift
  echo "${PROG:-agents}: $*" >&2
  exit "$code"
}

info() {
  echo "${PROG:-agents}: $*" >&2
}

# need <コマンド…>: なければ分かりやすいメッセージで終了コード 1。
need() {
  local c
  for c in "$@"; do
    command -v "$c" >/dev/null 2>&1 || die "$c が見つからない（インストールして PATH に入れる）"
  done
}

abs_dir() {
  (cd "$1" 2>/dev/null && pwd -P)
}

# git_common_dir: カレントディレクトリの git-common-dir の物理パス。
git_common_dir() {
  local c
  c="$(git rev-parse --git-common-dir 2>/dev/null)" || return 1
  case "$c" in
    /*) ;;
    *) c="$PWD/$c" ;;
  esac
  abs_dir "$c"
}

# in_linked_worktree: リンクした Worktree の中なら 0。
in_linked_worktree() {
  local gd cd
  gd="$(git rev-parse --absolute-git-dir 2>/dev/null)" || return 1
  gd="$(abs_dir "$gd")"
  cd="$(git_common_dir)" || return 1
  [ -n "$gd" ] && [ -n "$cd" ] && [ "$gd" != "$cd" ]
}

# agent_role: AGENT_ROLE、未設定ならリンクした Worktree の中で worker、それ以外は orchestrator。
agent_role() {
  if [ -n "${AGENT_ROLE:-}" ]; then
    printf '%s\n' "$AGENT_ROLE"
  elif in_linked_worktree; then
    echo worker
  else
    echo orchestrator
  fi
}

# require_orchestrator: 役割が orchestrator でなければ終了コード 1。
require_orchestrator() {
  local r
  r="$(agent_role)"
  [ "$r" = "orchestrator" ] || die "Orchestrator だけが実行できる（AGENT_ROLE=$r）"
}

# main_root: メインの作業ツリーのルート（git-common-dir の親）。
main_root() {
  local cd
  cd="$(git_common_dir)" || die "Git リポジトリの中で実行する"
  dirname "$cd"
}

# config_file: .agents/config.json のパス（AGENT_CONFIG で上書きできる）。
config_file() {
  if [ -n "${AGENT_CONFIG:-}" ]; then
    printf '%s\n' "$AGENT_CONFIG"
  else
    printf '%s\n' "$(main_root)/.agents/config.json"
  fi
}

# cfg <jq の式> <既定値>: 設定の値を読む。
cfg() {
  local f v
  f="$(config_file)"
  if [ -f "$f" ]; then
    v="$(jq -r "($1) // empty" "$f" 2>/dev/null || true)"
  else
    v=""
  fi
  if [ -n "$v" ]; then printf '%s\n' "$v"; else printf '%s\n' "$2"; fi
}

# repo_slug: OWNER/NAME。AGENT_REPO、なければ origin の URL から。
repo_slug() {
  if [ -n "${AGENT_REPO:-}" ]; then
    printf '%s\n' "$AGENT_REPO"
    return 0
  fi
  local url p
  url="$(git remote get-url origin 2>/dev/null)" || die "origin がない（AGENT_REPO=OWNER/NAME で指定する）"
  url="${url%.git}"
  url="${url%/}"
  p="${url##*[:/]}"
  url="${url%[:/]*}"
  printf '%s/%s\n' "${url##*[:/]}" "$p"
}

# agentctl を実行する。AGENTCTL → PATH の agentctl → scripts/agents/agentctl.sh の順。
# （command -v はこの関数自身にも一致するので、実行ファイルだけを探す type -P を使う）
agentctl() {
  local exe
  if [ -n "${AGENTCTL:-}" ]; then
    "$AGENTCTL" "$@"
  elif exe="$(type -P agentctl)" && [ -n "$exe" ]; then
    "$exe" "$@"
  else
    "$AGENTS_DIR/agentctl.sh" "$@"
  fi
}

now_utc() {
  date -u +%Y-%m-%dT%H:%M:%SZ
}

# pid_alive <pid>
pid_alive() {
  [ -n "${1:-}" ] && [ "$1" != "null" ] && kill -0 "$1" 2>/dev/null
}

# new_uuid: 小文字の UUID。
new_uuid() {
  local u=""
  if [ -r /proc/sys/kernel/random/uuid ]; then
    u="$(cat /proc/sys/kernel/random/uuid)"
  elif command -v uuidgen >/dev/null 2>&1; then
    u="$(uuidgen)"
  elif command -v python3 >/dev/null 2>&1; then
    u="$(python3 -c 'import uuid; print(uuid.uuid4())')"
  fi
  [ -n "$u" ] || die "UUID を作れない（uuidgen か python3 が必要）"
  printf '%s\n' "$u" | tr '[:upper:]' '[:lower:]'
}

# render_template <ファイル> <キー> <値> [<キー> <値> …]: {{キー}} を値で置き換える。
render_template() {
  local file="$1"
  shift
  local text key val pat out rest
  text="$(cat "$file")"
  while [ "$#" -ge 2 ]; do
    key="$1"
    val="$2"
    shift 2
    # ${var//…/…} は bash の版によって & の扱いが違うので、切り出して組み立てる。
    pat="{{$key}}"
    out=""
    rest="$text"
    while case "$rest" in *"$pat"*) true ;; *) false ;; esac; do
      out="$out${rest%%"$pat"*}$val"
      rest="${rest#*"$pat"}"
    done
    text="$out$rest"
  done
  printf '%s\n' "$text"
}

# agent_file <名前>: メインの作業ツリー（Orchestrator が使う main）の .claude/agents/<名前>.md。
# PR の HEAD や Worker の Worktree の中の定義ではなく、こちらを正とする。
agent_file() {
  local f
  f="$(main_root)/.claude/agents/$1.md"
  [ -f "$f" ] || die "$f がない"
  printf '%s\n' "$f"
}

# agent_tools <ファイル>: frontmatter の tools を「Read,Glob,Grep,Bash」の形で出す。
agent_tools() {
  local t
  t="$(awk '
    NR == 1 && /^---[ \t]*$/ { infm = 1; next }
    infm && /^---[ \t]*$/ { exit }
    infm && /^tools:/ { sub(/^tools:[ \t]*/, ""); print; exit }' "$1" | tr -d ' \t\r')"
  [ -n "$t" ] || die "$1 の frontmatter に tools がない"
  printf '%s\n' "$t"
}

# agent_body <ファイル>: frontmatter を除いた本文（システムプロンプト）。
agent_body() {
  awk '
    NR == 1 && /^---[ \t]*$/ { infm = 1; next }
    infm && /^---[ \t]*$/ { infm = 0; next }
    infm { next }
    { print }' "$1"
}

# usage_from_header <スクリプト>: 先頭のコメントを使い方として出す。
usage_from_header() {
  sed -n '2,/^set -euo/p' "$1" | sed '$d' | sed 's/^# \{0,1\}//'
}
