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
    # gh は lib.sh の関数（下の gh）でもあるので、実行ファイルがあるかを type -P で確かめる
    if [ "$(type -t "$c" 2>/dev/null)" = function ]; then
      type -P "$c" >/dev/null 2>&1 || die "$c が見つからない（インストールして PATH に入れる）"
    else
      command -v "$c" >/dev/null 2>&1 || die "$c が見つからない（インストールして PATH に入れる）"
    fi
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
  [ "$r" = "orchestrator" ] || die "Orchestrator だけが実行できる（AGENT_ROLE=${r}）"
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

# ---- AI 用の GitHub App のトークン（Issue #282。ADR-0021）----
# AI の役割（Orchestrator・Worker・Reviewer）の GitHub の操作（merge.sh のマージ・review.sh の記録を含む）は、
# オーナーのトークンと別の上限（1 時間に 5,000 回）を持つ GitHub App のインストールのトークンで行う。App ID と秘密鍵のパスは AGENT_APP_ID・AGENT_APP_KEY、
# なければ .agents/config.json の github_app.app_id・key_path（App ID がどこにもなければ App を使わない）。
# 秘密鍵はリポジトリの外に置く（chmod 600）。トークンは ~/.config/raft-game/ の下にキャッシュし（umask 077）、
# 作ってから 50 分（APP_TOKEN_TTL 秒）で作り直す。
# トークンと鍵の中身は、標準出力（コマンド置換で受け取る app_token・gh_token_value を除く）・標準エラーに出さない。
APP_TOKEN_TTL=3000

# gh_real <引数…>: 本物の gh（gh-app.sh のラッパーを通さない）。AGENT_GH_REAL があればそれ、なければ PATH の gh。
gh_real() {
  if [ -n "${AGENT_GH_REAL:-}" ]; then
    "$AGENT_GH_REAL" "$@"
  else
    command gh "$@"
  fi
}

# app_settings: App ID と鍵のパスを _app_id・_app_key に入れる。App ID がどこにもなければ 1（App を使わない）。
app_settings() {
  local cf=""
  _app_id="${AGENT_APP_ID:-}"
  _app_key="${AGENT_APP_KEY:-}"
  if [ -z "$_app_id" ] || [ -z "$_app_key" ]; then
    cf="$(config_file 2>/dev/null)" || cf=""
    if [ -n "$cf" ] && [ -f "$cf" ] && command -v jq >/dev/null 2>&1; then
      [ -n "$_app_id" ] || _app_id="$(jq -r '.github_app.app_id // empty' "$cf" 2>/dev/null || true)"
      [ -n "$_app_key" ] || _app_key="$(jq -r '.github_app.key_path // empty' "$cf" 2>/dev/null || true)"
    fi
  fi
  [ -n "$_app_id" ] || return 1
  [ -n "$_app_key" ] || _app_key="$HOME/.config/raft-game/agent-app.pem"
  # 先頭の「~/」はホームディレクトリ（設定の key_path は「~/」で書く）
  case "$_app_key" in
    \~/*) _app_key="$HOME/${_app_key#\~/}" ;;
  esac
  return 0
}

# app_fallback <理由>: App のトークンを使えないことを標準エラーに 1 行出す。
app_fallback() {
  info "GitHub App のトークンを使えない（$1）。オーナーのトークンを使う"
}

# app_key_usable: App の設定があり、秘密鍵が読めて権限が 600 以下なら 0。
# 設定がなければ 2（何も出さない）。鍵が使えなければ理由を標準エラーに 1 行出して 1。
app_key_usable() {
  local mode
  app_settings || return 2
  case "$_app_id" in
    '' | *[!0-9]*)
      app_fallback "github_app.app_id が整数ではない"
      return 1
      ;;
  esac
  if [ ! -f "$_app_key" ] || [ ! -r "$_app_key" ]; then
    app_fallback "秘密鍵がない・読めない（${_app_key}）"
    return 1
  fi
  mode="$(stat -c %a "$_app_key" 2>/dev/null || stat -f %Lp "$_app_key" 2>/dev/null || true)"
  case "$mode" in
    *[0-7]00) ;;
    *)
      app_fallback "秘密鍵の権限が 600 より広い（${_app_key}: ${mode:-不明}。chmod 600 にする）"
      return 1
      ;;
  esac
  return 0
}

# b64url: 標準入力を base64url（パディングなし）にする。
b64url() {
  base64 | tr -d '\n=' | tr '+/' '-_'
}

# app_jwt <App ID> <鍵のパス> <今の時刻（秒）>: RS256 の JWT（iat は 60 秒前、exp は 9 分後）を出す。
app_jwt() {
  local h p sig
  h="$(printf '%s' '{"alg":"RS256","typ":"JWT"}' | b64url)"
  p="$(printf '{"iat":%s,"exp":%s,"iss":"%s"}' "$(($3 - 60))" "$(($3 + 540))" "$1" | b64url)"
  sig="$(printf '%s.%s' "$h" "$p" | openssl dgst -sha256 -sign "$2" -binary 2>/dev/null | b64url)"
  [ -n "$sig" ] || return 1
  printf '%s.%s.%s\n' "$h" "$p" "$sig"
}

# app_api_error <標準エラーのファイル>: gh api の失敗の理由（HTTP の状態コードだけ。応答の本文は出さない）。
app_api_error() {
  local code
  code="$(grep -Eo 'HTTP [0-9]{3}' "$1" 2>/dev/null | tail -n 1 || true)"
  printf '%s\n' "${code:-応答なし}"
}

# app_token: App のインストールのトークンを標準出力に出す（コマンド置換で受け取る。端末やログに出さない）。
# キャッシュが 50 分以内ならそれを使う。設定がなければ 2（何も出さない）。
# 使えない（鍵がない・権限が広い・App が未インストール・時計のずれなど）ときは理由を標準エラーに 1 行出して 1。
app_token() {
  local rc=0 repo cache_dir cache now ts="" tok="" jwt resp inst errf c
  app_key_usable || rc=$?
  [ "$rc" = 0 ] || return "$rc"
  repo="${AGENT_REPO:-}"
  [ -n "$repo" ] || repo="$(repo_slug 2>/dev/null)" || repo=""
  if [ -z "$repo" ]; then
    app_fallback "リポジトリが分からない（AGENT_REPO=OWNER/NAME で指定する）"
    return 1
  fi
  cache_dir="$HOME/.config/raft-game"
  cache="$cache_dir/agent-app-token-$_app_id-$(printf '%s' "$repo" | tr -c 'A-Za-z0-9._-' '_')"
  now="$(date +%s)"
  if [ -f "$cache" ]; then
    {
      IFS= read -r ts || true
      IFS= read -r tok || true
    } <"$cache" 2>/dev/null || true
    case "$ts" in
      '' | *[!0-9]*) ;;
      *)
        if [ -n "$tok" ] && [ "$now" -ge "$ts" ] && [ $((now - ts)) -lt "$APP_TOKEN_TTL" ]; then
          printf '%s\n' "$tok"
          return 0
        fi
        ;;
    esac
  fi
  for c in openssl jq base64; do
    if ! command -v "$c" >/dev/null 2>&1; then
      app_fallback "$c がない"
      return 1
    fi
  done
  if ! jwt="$(app_jwt "$_app_id" "$_app_key" "$now")"; then
    app_fallback "JWT を作れない（openssl で秘密鍵を読めない）"
    return 1
  fi
  if ! errf="$(mktemp)"; then
    app_fallback "一時ファイルを作れない"
    return 1
  fi
  if ! resp="$(gh_real api -H "Authorization: Bearer $jwt" -H "Accept: application/vnd.github+json" \
    "/repos/$repo/installation" 2>"$errf")"; then
    app_fallback "App のインストールが見つからない（${repo}: $(app_api_error "$errf")。未インストールか時計のずれ）"
    rm -f "$errf"
    return 1
  fi
  inst="$(printf '%s' "$resp" | jq -r '.id // empty' 2>/dev/null || true)"
  case "$inst" in
    '' | *[!0-9]*)
      app_fallback "App のインストールの番号を読めない（${repo}）"
      rm -f "$errf"
      return 1
      ;;
  esac
  if ! resp="$(gh_real api -X POST -H "Authorization: Bearer $jwt" -H "Accept: application/vnd.github+json" \
    "/app/installations/$inst/access_tokens" 2>"$errf")"; then
    app_fallback "インストールのトークンを作れない（$(app_api_error "$errf")）"
    rm -f "$errf"
    return 1
  fi
  rm -f "$errf"
  tok="$(printf '%s' "$resp" | jq -r '.token // empty' 2>/dev/null || true)"
  case "$tok" in
    '' | *[!A-Za-z0-9_.-]*)
      app_fallback "インストールのトークンを読めない"
      return 1
      ;;
  esac
  # 作った時刻（API を呼ぶ前の時刻）とトークンを、ほかの人が読めない一時ファイルに書いてから置き換える
  if ! (
    umask 077
    mkdir -p "$cache_dir" && t="$(mktemp "$cache.XXXXXX")" &&
      printf '%s\n%s\n' "$now" "$tok" >"$t" && mv -f "$t" "$cache"
  ) 2>/dev/null; then
    info "GitHub App のトークンをキャッシュに書けない（${cache_dir}）。次も作り直す"
  fi
  printf '%s\n' "$tok"
}

# app_token_wanted: App のトークンを使う場面なら 0。役割（agent_role）が AI（worker・orchestrator・reviewer）のとき。
# merge.sh・review.sh も Orchestrator が動かすので App のトークンを使う（オーナーの判断。2026-11-08）。
# レビュー記録の作者は App（kizuku-claude[bot]）になり、Merge Gate の C5 は trusted_authors でそれを数える。
app_token_wanted() {
  case "$(agent_role)" in
    worker | orchestrator | reviewer) return 0 ;;
  esac
  return 1
}

# gh_token_value: 使うトークンを標準出力に出す（コマンド置換で受け取る）。App のトークンを使う場面で
# 作れれば App のトークン、だめならオーナーのトークン（gh auth token）。どちらもなければ何も出さない。
gh_token_value() {
  local t=""
  if app_token_wanted; then
    t="$(app_token)" || t=""
    if [ -n "$t" ]; then
      printf '%s\n' "$t"
      return 0
    fi
  fi
  type -P gh >/dev/null 2>&1 || return 0
  t="$(command gh auth token 2>/dev/null)" || return 0
  [ -n "$t" ] || return 0
  printf '%s\n' "$t"
}

# ensure_gh_token: GITHUB_TOKEN も GH_TOKEN もなければ、トークン（gh_token_value）を GH_TOKEN に入れて export する
# （#107。agentctl はこの 2 つの環境変数からトークンを読む）。トークンは標準出力・標準エラーに出さない。
# 役割が AI（app_token_wanted）なら App のトークン（#282）、作れなければ gh auth token の値。
# gh がない・未ログインなら何もしない（agentctl が「トークンがない」で止める）。
# App のトークンは 1 時間で切れるので、長く動くシェルでは使わず、コマンドごとに渡す（agentctl・ghx・gh-app.sh）。
ensure_gh_token() {
  if [ -n "${GITHUB_TOKEN:-}" ] || [ -n "${GH_TOKEN:-}" ]; then
    return 0
  fi
  local t
  t="$(gh_token_value)"
  [ -n "$t" ] || return 0
  GH_TOKEN="$t"
  export GH_TOKEN
}

# agentctl を実行する。AGENTCTL → PATH の agentctl → scripts/agents/agentctl.sh の順。
# GITHUB_TOKEN も GH_TOKEN もなければ、呼ぶたびにトークン（gh_token_value）を作り、GH_TOKEN でその子にだけ渡す
# （#282。呼び出し側のシェルには export しない。長く動くシェル（merge-train.sh・review.sh の待ち）で
# App のトークンが切れないように）。
# （command -v はこの関数自身にも一致するので、実行ファイルだけを探す type -P を使う）
agentctl() {
  local exe t=""
  if [ -z "${GITHUB_TOKEN:-}" ] && [ -z "${GH_TOKEN:-}" ]; then
    t="$(gh_token_value)"
  fi
  if [ -n "${AGENTCTL:-}" ]; then
    exe="$AGENTCTL"
  elif exe="$(type -P agentctl)" && [ -n "$exe" ]; then
    :
  else
    exe="$AGENTS_DIR/agentctl.sh"
  fi
  if [ -n "$t" ]; then
    GH_TOKEN="$t" "$exe" "$@"
  else
    "$exe" "$@"
  fi
}

# ghx <gh の引数…>: gh を役割のトークンで実行する（#282。lib.sh を読み込むスクリプトの gh は、下の gh 関数でこれを通る）。
# GITHUB_TOKEN も GH_TOKEN もなく、App のトークンを使う場面（app_token_wanted）なら、呼ぶたびに App のトークンを
# GH_TOKEN でその gh にだけ渡す（呼び出し側のシェルには export しない）。作れなければ（理由は app_token が
# 標準エラーに出す）、そのまま gh を実行する（gh のログイン＝オーナーのトークン）。
ghx() {
  local t="" rc=0
  if [ -z "${GITHUB_TOKEN:-}" ] && [ -z "${GH_TOKEN:-}" ] && app_token_wanted; then
    t="$(app_token)" || rc=$?
    [ "$rc" = 0 ] || t=""
  fi
  if [ -n "$t" ]; then
    GH_TOKEN="$t" command gh "$@"
  else
    command gh "$@"
  fi
}

# gh <引数…>: lib.sh を読み込むスクリプト（merge-train.sh・autospawn.sh・spawn-worker.sh・status.sh・wt.sh・
# merge.sh・review.sh）が直接呼ぶ gh を、ghx に通す（#282 のレビュー F7）。Orchestrator の見張りも、呼ぶたびに
# 有効な App のトークンで gh を呼ぶ（オーナーのトークンの上限に達しても止まらない）。
# gh auth と、gh-app.sh の中（AGENT_GH_APP_ACTIVE）は、そのまま本物の gh を実行する。
# 関数なので、bash -c・xargs などの別のプロセスには効かない（その場合は ghx か gh-app.sh を使う）。
gh() {
  if [ "${1:-}" = auth ] || [ -n "${AGENT_GH_APP_ACTIVE:-}" ]; then
    command gh "$@"
  else
    ghx "$@"
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
