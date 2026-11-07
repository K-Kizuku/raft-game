#!/usr/bin/env bash
# PreToolUse（Bash）の hook（Issue #5）。tool_input.command を調べ、役割ごとの
# 禁止事項に当たれば、理由を標準エラーに出して終了コード 2 で拒否する。
#
# コマンドを完全に解釈することはできないので、控えめに（疑わしいものは拒否する側に）
# 判定する。引用符は取り除き、`;`・`&`・`|`・`(`・`)`・バッククォート・改行で区切った
# まとまり（セグメント）ごとに調べる。字句の判定なので、cd してから相対パスで書く・
# 変数でパスを組み立てる・スクリプトのファイルの中身で書く、などは見分けられない
# （main は Rulesets が、保護されたパスはレビューが最後の砦になる）。
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh disable=SC1091
. "$here/lib.sh"

hook_read_input
CWD="$(hook_cwd)"
detect_role "$CWD"

raw="$(hook_field '.tool_input.command')"
[ -n "$raw" ] || exit 0

TOP="$(repo_toplevel "$CWD")"
MAIN_ROOT="$(repo_main_root "$CWD")"

# 引用符とバックスラッシュを取り除き、ファイル記述子の複製（2>&1 など）を消す。
noquote="$(printf '%s\n' "$raw" | tr -d "\"'\\\\")"
norm="$(printf '%s\n' "$noquote" | sed -E 's/[0-9]*>&[0-9-]+/ /g; s/&>/>/g')"

# ---- Worker の保護されたパス（.agents/config.json の protected_paths） ----
# AGENT_ALLOW_PROTECTED=1 でなければ、Bash からの書き込み（WRITERS・sed -i・リダイレクトなど）も拒否する。
PROTECTED=()
if [ "$ROLE" = "worker" ] && [ "${AGENT_ALLOW_PROTECTED:-0}" != "1" ] && [ -n "$TOP" ] &&
  [ -f "$TOP/.agents/config.json" ]; then
  while IFS= read -r p; do
    [ -n "$p" ] && PROTECTED+=("$p")
  done < <(jq -r '.protected_paths[]? // empty' "$TOP/.agents/config.json" 2>/dev/null || true)
fi

# lex_norm <絶対パス>: . と .. を字句のまま取り除く。
lex_norm() {
  local out="" part
  local parts=()
  IFS=/ read -r -a parts <<<"$1" || true
  for part in "${parts[@]+"${parts[@]}"}"; do
    case "$part" in
      '' | .) ;;
      ..) out="${out%/*}" ;;
      *) out="$out/$part" ;;
    esac
  done
  printf '%s\n' "${out:-/}"
}

# protected_hit <単語>: 単語が保護されたパス（またはその祖先のディレクトリ）を指していれば 0。
# 控えめに、単語に保護されたパスの前半（最初のワイルドカードより前）が含まれるだけでも 0 にする。
# shellcheck disable=SC2053
protected_hit() {
  local w="$1" p pre glob t rel
  [ "${#PROTECTED[@]}" -gt 0 ] || return 1
  case "$w" in
    -*=*) w="${w#*=}" ;;
    -*) return 1 ;;
    *=*) w="${w#*=}" ;; # dd の of=… など
  esac
  [ -n "$w" ] || return 1
  for p in "${PROTECTED[@]}"; do
    pre="${p%%[*?[]*}"
    if [ -n "$pre" ]; then
      case "$w" in *"$pre"*) return 0 ;; esac
    fi
  done
  [ -n "$TOP" ] || return 1
  case "$w" in
    /*) t="$w" ;;
    '~'* | '$'*) return 1 ;;
    *) t="$CWD/$w" ;;
  esac
  t="$(lex_norm "$t")"
  case "$t" in
    "$TOP"/*) rel="${t#"$TOP"/}" ;;
    *) return 1 ;;
  esac
  for p in "${PROTECTED[@]}"; do
    glob="${p//\*\*/*}"
    if [[ "$rel" == $glob ]] || [[ "$rel/" == $glob ]]; then
      return 0
    fi
    pre="${p%%[*?[]*}"
    case "$pre" in
      "$rel"/*) return 0 ;; # 祖先のディレクトリ（rm -r .claude など）
    esac
  done
  return 1
}

PROTECTED_MSG="保護されたパス（.agents/config.json の protected_paths）は、Issue の変更予定ファイルに含まれるとき（AGENT_ALLOW_PROTECTED=1）だけ変更できる"

# ---- 書き込みのリダイレクト ----
# 引用符の中身を消してから、> の先を調べる。
noqstr="$(printf '%s\n' "$raw" | sed -E "s/'[^']*'//g; s/\"[^\"]*\"//g" |
  sed -E 's/[0-9]*>&[0-9-]+/ /g; s/&>/>/g; s/[0-9]*>>?[[:space:]]*\/dev\/null//g')"

if [ "$ROLE" != "human" ]; then
  # .github/workflows/ へのリダイレクト（引用符の中のパスも見るため norm を使う）
  if printf '%s\n' "$norm" | grep -Eq '>>?[[:space:]]*[^[:space:]]*\.github/workflows(/|[[:space:]]|$)'; then
    deny ".github/workflows/ には書き込まない（ワークフローは .github/workflow-sources/ に書き、人が取り込む）"
  fi
fi
if [ "$ROLE" = "reviewer" ]; then
  if printf '%s\n' "$noqstr" | grep -q '>'; then
    deny "Reviewer はファイルを書き換えない（リダイレクト > ・>> は使えない）"
  fi
fi
if [ "${#PROTECTED[@]}" -gt 0 ]; then
  while IFS= read -r target; do
    [ -n "$target" ] || continue
    if protected_hit "$target"; then
      deny "$PROTECTED_MSG（リダイレクトの先: $target）"
    fi
  done < <(printf '%s\n' "$norm" | grep -Eo '>>?\|?[[:space:]]*[^[:space:]<>;&|()]+' | sed -E 's/^>>?\|?[[:space:]]*//' || true)
  # インタプリタのコード（python3 -c "open('…','w')" など）は ( や , で区切られてセグメントに分かれるので、
  # コマンド全体の単語を調べる。
  if printf '%s\n' "$norm" | grep -Eq '(^|[^A-Za-z0-9_.-])(python[0-9.]*|pypy[0-9.]*|node|nodejs|deno|bun|perl[0-9.]*|ruby|php|lua|luajit|tclsh|osascript|Rscript)([[:space:]]|$)'; then
    while IFS= read -r tok; do
      [ -n "$tok" ] || continue
      if protected_hit "$tok"; then
        deny "$PROTECTED_MSG（インタプリタのコードの中: $tok）"
      fi
    done < <(printf '%s\n' "$norm" | tr -c 'A-Za-z0-9_./~$*?=:@+%-' '\n')
  fi
fi

# ---- セグメントごとの判定 ----

is_agent_branch() {
  case "$1" in
    agent/issue-*)
      local n="${1#agent/issue-}"
      case "$n" in
        '' | *[!0-9]*) return 1 ;;
        *) return 0 ;;
      esac
      ;;
  esac
  return 1
}

# check_push <git -C のディレクトリ> <push の後ろの引数…>
check_push() {
  local dir="$1"
  shift
  local force=0 lease=0 a skip=0 opts_done=0
  local pos=()
  for a in "$@"; do
    if [ "$skip" = 1 ]; then
      skip=0
      continue
    fi
    if [ "$opts_done" = 0 ]; then
      case "$a" in
        --) opts_done=1; continue ;;
        --force | --force=*) force=1; continue ;;
        --force-with-lease | --force-with-lease=* | --force-if-includes) lease=1; continue ;;
        --all | --mirror | --branches)
          deny "git push $a は禁止（main を含むおそれがある）"
          ;;
        -o | --push-option | --repo | --receive-pack | --exec) skip=1; continue ;;
        --*) continue ;;
        -?*)
          case "$a" in
            *f*) force=1 ;;
          esac
          case "$a" in
            *o) skip=1 ;;
          esac
          continue
          ;;
      esac
    fi
    pos+=("$a")
  done

  local cur
  cur="$(git -C "$dir" symbolic-ref --quiet --short HEAD 2>/dev/null || true)"
  local dsts=() r src dst i
  if [ "${#pos[@]}" -le 1 ]; then
    dsts+=("${cur:-HEAD}")
  else
    for ((i = 1; i < ${#pos[@]}; i++)); do
      r="${pos[$i]}"
      case "$r" in
        +*) force=1; r="${r#+}" ;;
      esac
      case "$r" in
        *:*)
          src="${r%%:*}"
          dst="${r#*:}"
          [ -n "$dst" ] || dst="$src"
          ;;
        *) dst="$r" ;;
      esac
      case "$dst" in
        HEAD | @) dst="${cur:-HEAD}" ;;
      esac
      dsts+=("$dst")
    done
  fi

  local d
  for d in "${dsts[@]}"; do
    d="${d#refs/heads/}"
    case "$d" in
      main) deny "main への push は禁止（PR を作り、Orchestrator が Merge Gate を通してマージする）" ;;
      *'*'*) deny "ワイルドカードの refspec の push は禁止（main を含むおそれがある）" ;;
    esac
  done
  if [ "$force" = 1 ]; then
    deny "--force・-f・+refspec の push は禁止（必要なら自分の agent/issue-* へ --force-with-lease）"
  fi
  if [ "$lease" = 1 ]; then
    for d in "${dsts[@]}"; do
      d="${d#refs/heads/}"
      is_agent_branch "$d" || deny "--force-with-lease の push は agent/issue-* のブランチへだけ許す（宛先: $d）"
    done
  fi
  if [ "$ROLE" = "worker" ] || [ "$ROLE" = "reviewer" ]; then
    local issue
    issue="$(agent_issue "$dir")"
    [ -n "$issue" ] || deny "担当の Issue（AGENT_ISSUE）が分からないため push できない"
    for d in "${dsts[@]}"; do
      d="${d#refs/heads/}"
      [ "$d" = "agent/issue-$issue" ] || deny "Worker が push できるのは自分のブランチ agent/issue-$issue だけ（宛先: $d）"
    done
  fi
}

# is_alias_key <設定のキー>: alias.* なら 0（大文字と小文字を区別しない）。
is_alias_key() {
  case "$1" in
    [Aa][Ll][Ii][Aa][Ss].*) return 0 ;;
  esac
  return 1
}

# check_git <セグメントの単語…>（先頭は git）
check_git() {
  shift
  local dir="$CWD"
  while [ "$#" -gt 0 ]; do
    case "$1" in
      -C)
        [ "$#" -ge 2 ] || return 0
        case "$2" in
          /*) dir="$2" ;;
          *) dir="$dir/$2" ;;
        esac
        shift 2
        ;;
      -c | --config-env)
        # git -c alias.p=push p … は push などの判定をすり抜けられるので拒否する。
        if [ "$ROLE" != "human" ] && is_alias_key "${2:-}"; then
          deny "git $1 alias.* は使わない（別名で判定をすり抜けられる）"
        fi
        [ "$#" -ge 2 ] || return 0
        shift 2
        ;;
      -c* | --config-env=*)
        local kv="${1#--config-env=}"
        kv="${kv#-c}"
        if [ "$ROLE" != "human" ] && is_alias_key "$kv"; then
          deny "git -c alias.* は使わない（別名で判定をすり抜けられる）"
        fi
        shift
        ;;
      --git-dir | --work-tree | --namespace)
        [ "$#" -ge 2 ] || return 0
        shift 2
        ;;
      -*) shift ;;
      *) break ;;
    esac
  done
  [ "$#" -gt 0 ] || return 0
  local sub="$1"
  shift
  [ -d "$dir" ] || dir="$CWD"

  case "$sub" in
    push)
      [ "$ROLE" = "reviewer" ] && deny "Reviewer は git push しない"
      check_push "$dir" "$@"
      ;;
    worktree)
      if [ "${1:-}" = "remove" ]; then
        local a
        for a in "$@"; do
          case "$a" in
            --force | -f | -ff) deny "git worktree remove --force は禁止（未コミットの変更が消える）" ;;
          esac
        done
      fi
      ;;
    config)
      # 別名を設定して、次のコマンドで判定をすり抜けることを防ぐ（読むだけなら許す）。
      if [ "$ROLE" != "human" ]; then
        local a
        for a in "$@"; do
          if is_alias_key "$a"; then
            case " $* " in
              *" --get"* | *" --list "* | *" -l "*) ;;
              *) deny "git config で alias.* を設定しない（別名で判定をすり抜けられる）" ;;
            esac
          fi
        done
      fi
      ;;
  esac

  if [ "$ROLE" = "reviewer" ]; then
    case "$sub" in
      commit | add | rm | mv | reset | rebase | merge | cherry-pick | revert | apply | am | tag | stash | \
        restore | clean | checkout | switch | pull | worktree | update-ref | notes | submodule | init | clone | gc | prune)
        deny "Reviewer は書き込みにあたる操作をしない（git $sub）"
        ;;
      config)
        case " $* " in
          *" --get"* | *" --list "* | *" -l "*) ;;
          *) deny "Reviewer は git config を書き換えない" ;;
        esac
        ;;
      branch)
        local a list=0 haspos=0
        for a in "$@"; do
          case "$a" in
            -d | -D | -m | -M | -c | -C | -f | --delete | --move | --copy | --force | -u | --set-upstream-to* | \
              --unset-upstream | --edit-description)
              deny "Reviewer はブランチを変更しない（git branch $a）"
              ;;
            --list | -l | --contains | --no-contains | --merged | --no-merged | --points-at) list=1 ;;
            -*) ;;
            *) haspos=1 ;;
          esac
        done
        if [ "$haspos" = 1 ] && [ "$list" = 0 ]; then
          deny "Reviewer はブランチを作らない"
        fi
        ;;
    esac
  fi
}

# check_gh <セグメントの単語…>（先頭は gh）
check_gh() {
  local seg="$*"
  local sub="${2:-}" sub2="${3:-}"
  case "$sub" in
    secret | variable | ruleset) deny "gh $sub は禁止（シークレット・変数・Rulesets は人が管理する）" ;;
  esac
  if [ "$sub" = "pr" ] && [ "$sub2" = "merge" ]; then
    deny "gh pr merge は禁止（マージは Orchestrator が scripts/agents/merge.sh で Merge Gate を通して行う）"
  fi
  if [ "$sub" = "api" ]; then
    if printf '%s\n' "$seg" | grep -Eq '/merges?([/?[:space:]]|$)|/rulesets|/branches/[^[:space:]]*/protection|/actions/secrets|/actions/variables|/environments/[^[:space:]]*/(secrets|variables)'; then
      deny "gh api での マージ・Rulesets・ブランチ保護・シークレット・変数 の操作は禁止"
    fi
    if printf '%s\n' "$seg" | grep -Eiq 'mergePullRequest|enablePullRequestAutoMerge|RepositoryRuleset|BranchProtectionRule'; then
      deny "gh api graphql でのマージ・Rulesets・ブランチ保護の操作は禁止"
    fi
  fi
  case "$seg" in
    *human-approved*)
      case "$seg" in
        *--remove-label*) ;;
        *) deny "ラベル human-approved は人だけが付ける" ;;
      esac
      ;;
  esac
  if [ "$ROLE" = "worker" ] || [ "$ROLE" = "reviewer" ]; then
    if [ "$sub" = "pr" ] && [ "$sub2" = "review" ]; then
      deny "gh pr review は Worker・Reviewer からは使わない（レビューは scripts/agents/review.sh が記録する）"
    fi
  fi
  if [ "$ROLE" = "reviewer" ]; then
    case "$sub" in
      issue | label | release | gist) deny "Reviewer は gh $sub を使わない（書き込みにあたる操作をしない）" ;;
      pr)
        case "$sub2" in
          comment | edit | create | close | reopen | ready | merge | review | lock | unlock)
            deny "Reviewer は gh pr $sub2 を使わない（書き込みにあたる操作をしない）"
            ;;
        esac
        ;;
      repo)
        case "$sub2" in
          view | list | '') ;;
          *) deny "Reviewer は gh repo $sub2 を使わない" ;;
        esac
        ;;
      workflow | run)
        case "$sub2" in
          view | list | watch | download | '') ;;
          *) deny "Reviewer は gh $sub $sub2 を使わない" ;;
        esac
        ;;
      api)
        if printf '%s\n' " $seg " | grep -Eq '[[:space:]](-X|--method)[[:space:]=]*[^Gg[:space:]]|[[:space:]](-f|-F|--field|--raw-field|--input)([[:space:]=]|$)'; then
          deny "Reviewer は gh api で書き込まない"
        fi
        ;;
    esac
  fi
}

check_curl() {
  local seg=" $* "
  case "$seg" in
    *api.github.com*) ;;
    *) return 0 ;;
  esac
  if printf '%s\n' "$seg" | grep -Eiq '[[:space:]](-X[[:space:]]*|--request[[:space:]=]+|--method[[:space:]=]+)(POST|PUT|PATCH|DELETE)'; then
    deny "curl・wget での api.github.com への書き込みは禁止"
  fi
  if printf '%s\n' "$seg" | grep -Eq '[[:space:]](-d|--data[a-z-]*|-F|--form[a-z-]*|--json|-T|--upload-file|--post-data|--post-file|--body-data|--body-file)([[:space:]=]|$)|[[:space:]]-d[^[:space:]]'; then
    deny "curl・wget での api.github.com への書き込みは禁止"
  fi
}

# resolve_target <パス>: rm の対象を物理パスにする（存在しなければ字句のまま）。
# （~ や $HOME は字句のまま比べる）
# shellcheck disable=SC2088,SC2016
resolve_target() {
  local t="$1"
  case "$t" in
    '~' | '~/'*) t="$HOME${t#\~}" ;;
    '$HOME'* | '${HOME}'*) t="$HOME${t#\$HOME}"; t="${t#\{HOME\}}" ;;
  esac
  case "$t" in
    /*) ;;
    *) t="$CWD/$t" ;;
  esac
  while :; do
    case "$t" in
      */) [ "$t" = "/" ] && break; t="${t%/}" ;;
      */\*) t="${t%/\*}" ;;
      */.) t="${t%/.}" ;;
      *) break ;;
    esac
  done
  [ -n "$t" ] || t="/"
  if [ -d "$t" ]; then
    abs_dir "$t"
  else
    printf '%s\n' "$t"
  fi
}

# shellcheck disable=SC2088,SC2016
check_rm() {
  shift
  local rec=0 a
  local targets=()
  for a in "$@"; do
    case "$a" in
      --no-preserve-root) deny "rm --no-preserve-root は禁止" ;;
      --recursive) rec=1 ;;
      --*) ;;
      -*)
        case "$a" in
          *[rR]*) rec=1 ;;
        esac
        ;;
      *) targets+=("$a") ;;
    esac
  done
  [ "$rec" = 1 ] || return 0
  local home_p t r
  home_p="$(abs_dir "$HOME" || printf '%s' "$HOME")"
  for t in "${targets[@]+"${targets[@]}"}"; do
    case "$t" in
      / | '/*' | '~' | '~/' | '~/*' | '$HOME' | '$HOME/' | '$HOME/*' | '${HOME}' | '${HOME}/' | '${HOME}/*' | .. | ../ | '../*')
        deny "rm -r の対象が危険（$t）"
        ;;
    esac
    r="$(resolve_target "$t")"
    if [ "$r" = "/" ] || [ "$r" = "$home_p" ] || [ "$r" = "$HOME" ] ||
      { [ -n "$TOP" ] && [ "$r" = "$TOP" ]; } || { [ -n "$MAIN_ROOT" ] && [ "$r" = "$MAIN_ROOT" ]; }; then
      deny "rm -r の対象が /・ホーム・リポジトリのルートになっている（$t）"
    fi
  done
}

# invoked_script <単語…>: 実行するスクリプトのパスを出す（なければ空）。
invoked_script() {
  local c0="${1:-}" c0b="${1##*/}"
  case "$c0b" in
    bash | sh | zsh | dash | source | .)
      local a
      shift
      for a in "$@"; do
        case "$a" in
          -*) ;;
          *) printf '%s\n' "$a"; return 0 ;;
        esac
      done
      ;;
    *.sh) printf '%s\n' "$c0" ;;
  esac
}

# agentctl_write_hit <引用符と \ を取り除いたコマンドの全体>: コマンドの文字列のどこかに agentctl の語と
# 書き込みの語の両方があれば、見つけた書き込みの語を出す（なければ空）。安全側の字句の判定で、構文は読まない
# （シェルのコメント・引用符の中・フラグの値・パイプの先にあっても拒否する。--dry-run の例外もない）。
# - agentctl の語: 部分文字列 agentctl（agentctl・agentctl.sh・.agents/bin/agentctl・tools/agentctl など）。
#   cd してからの go run . を見分けられないので、単語 go と run の両方があるときも agentctl の語とみなす
# - 書き込みの語: 単語 comment・state・claim・attempt と、gate のフラグ --comment・--set-status（- 1 つも）
# 単語は英数字と _ . / = - の並び（tools/agentctl/internal/state や TestComment は単語 state・comment ではない）。
# 読み取り（plan・recover・review・pr view・pr codex-record・pr protected-files・--comment と --set-status の
# ない gate）は、書き込みの語を含まなければ通る。
agentctl_write_hit() {
  local s="$1" t mention=0 has_go=0 has_run=0 sub=""
  case "$s" in
    *agentctl*) mention=1 ;;
  esac
  while IFS= read -r t; do
    case "$t" in
      go) has_go=1 ;;
      run) has_run=1 ;;
      comment | state | claim | attempt) [ -n "$sub" ] || sub="$t" ;;
      -comment | --comment | -comment=* | --comment=*) [ -n "$sub" ] || sub="gate --comment" ;;
      -set-status | --set-status | -set-status=* | --set-status=*) [ -n "$sub" ] || sub="gate --set-status" ;;
    esac
  done < <(printf '%s\n' "$s" | tr -c 'A-Za-z0-9_./=\n-' '\n')
  [ "$has_go" = 1 ] && [ "$has_run" = 1 ] && mention=1
  [ "$mention" = 1 ] && [ -n "$sub" ] && printf '%s\n' "$sub"
  return 0
}

WRITERS=" cp mv tee install rsync ln touch rm rmdir mkdir truncate dd patch chmod chown unzip tar python python3 node perl ruby "

# is_interpreter <コマンド名>: 任意のコードを実行できるインタプリタなら 0。
is_interpreter() {
  case "$1" in
    python | python[0-9]* | pypy* | node | nodejs | deno | bun | perl | perl[0-9]* | ruby | php | lua | luajit | tclsh | osascript | Rscript)
      return 0
      ;;
  esac
  return 1
}

check_segment() {
  local seg="$1"
  local w=()
  read -r -a w <<<"$seg" || true
  [ "${#w[@]}" -gt 0 ] || return 0

  # 先頭の変数の代入・ラッパー（env・sudo など）・bash -c を取り除く。
  local changed=1
  while [ "$changed" = 1 ] && [ "${#w[@]}" -gt 0 ]; do
    changed=0
    case "${w[0]}" in
      GIT_CONFIG_*=* | GIT_CONFIG=*)
        # GIT_CONFIG_COUNT・GIT_CONFIG_KEY_n などで alias.* を渡せるので、環境変数での上書きは使わない
        [ "$ROLE" = "human" ] || deny "git の設定を環境変数（${w[0]%%=*}）で上書きしない（別名で判定をすり抜けられる）"
        w=("${w[@]:1}"); changed=1; continue
        ;;
      [A-Za-z_]*=*) w=("${w[@]:1}"); changed=1; continue ;;
      env | sudo | command | builtin | exec | nohup | time | nice | then | do | else | if | while | until | '!' | '{' | '$')
        w=("${w[@]:1}")
        while [ "${#w[@]}" -gt 0 ]; do
          case "${w[0]}" in
            GIT_CONFIG_*=* | GIT_CONFIG=*)
              [ "$ROLE" = "human" ] || deny "git の設定を環境変数（${w[0]%%=*}）で上書きしない（別名で判定をすり抜けられる）"
              w=("${w[@]:1}")
              ;;
            -* | [A-Za-z_]*=*) w=("${w[@]:1}") ;;
            *) break ;;
          esac
        done
        changed=1
        continue
        ;;
      xargs | timeout | gtimeout)
        local isto=0
        case "${w[0]}" in timeout | gtimeout) isto=1 ;; esac
        w=("${w[@]:1}")
        while [ "${#w[@]}" -gt 0 ]; do
          case "${w[0]}" in
            -*) w=("${w[@]:1}") ;;
            *) break ;;
          esac
        done
        if [ "$isto" = 1 ] && [ "${#w[@]}" -gt 0 ]; then
          w=("${w[@]:1}")
        fi
        changed=1
        continue
        ;;
    esac
    case "${w[0]##*/}" in
      bash | sh | zsh | dash)
        if [ "${#w[@]}" -ge 2 ]; then
          case "${w[1]}" in
            -*c*) w=("${w[@]:2}"); changed=1; continue ;;
          esac
        fi
        ;;
    esac
  done
  [ "${#w[@]}" -gt 0 ] || return 0

  local c0b="${w[0]##*/}" s0
  local segstr=" ${w[*]} "

  # eval は中身を字句で調べられない（引用符を外した形と実際の解釈が異なる）ので拒否する
  if [ "$c0b" = "eval" ] && [ "$ROLE" != "human" ]; then
    deny "eval は使わない（中身を安全に判定できない）"
  fi

  # スクリプトの実行
  s0="$(invoked_script "${w[@]}")"
  if [ -n "$s0" ]; then
    case "${s0##*/}" in
      install-workflows.sh)
        [ "$ROLE" = "human" ] || deny "scripts/github/install-workflows.sh は人だけが実行する"
        ;;
      merge.sh | review.sh | spawn-worker.sh | wt.sh)
        if [ "$ROLE" = "worker" ] || [ "$ROLE" = "reviewer" ]; then
          deny "scripts/agents/${s0##*/} は Orchestrator だけが実行する"
        fi
        ;;
    esac
  fi

  # どの単語にも現れてはいけないもの
  local x
  for x in "${w[@]}"; do
    [ "${x##*/}" = "wrangler" ] && deny "wrangler は使わない（インフラは cf で CI だけが反映する。ADR-0016）"
  done
  if printf '%s\n' "$segstr" | grep -Eq '[[:space:]/]cf[[:space:]]+deploy([[:space:]]|$)'; then
    case "$segstr" in
      *" --dry-run "* | *" --dry-run="*) ;;
      *) deny "cf deploy は CI だけが行う（--dry-run だけ許す）" ;;
    esac
  fi

  case "$c0b" in
    git) check_git "${w[@]}" ;;
    gh) check_gh "${w[@]}" ;;
    curl | wget) check_curl "${w[@]}" ;;
    rm) check_rm "${w[@]}" ;;
  esac
  case "$segstr" in
    *human-approved*)
      case "$c0b" in
        curl | wget | gh)
          case "$segstr" in
            *--remove-label*) ;;
            *) deny "ラベル human-approved は人だけが付ける" ;;
          esac
          ;;
      esac
      ;;
  esac

  # .github/workflows/ への書き込み
  if [ "$ROLE" != "human" ]; then
    case "$segstr" in
      *.github/workflows*)
        local wr=0
        case "$WRITERS" in
          *" $c0b "*) wr=1 ;;
        esac
        case "$c0b" in
          sed) case "$segstr" in *" -i"*) wr=1 ;; esac ;;
          git)
            case "$segstr" in
              *" mv "* | *" rm "* | *" checkout "* | *" restore "* | *" apply "* | *" am "*) wr=1 ;;
            esac
            ;;
        esac
        [ "$wr" = 0 ] || deny ".github/workflows/ には書き込まない（ワークフローは .github/workflow-sources/ に書き、人が取り込む）"
        ;;
    esac
  fi

  # Worker: 保護されたパスへの Bash からの書き込み（AGENT_ALLOW_PROTECTED=1 でなければ拒否）
  if [ "${#PROTECTED[@]}" -gt 0 ]; then
    local pw=0
    case "$WRITERS" in
      *" $c0b "*) pw=1 ;;
    esac
    is_interpreter "$c0b" && pw=1
    case "$c0b" in
      sed | gsed)
        for x in "${w[@]:1}"; do
          case "$x" in -i* | --in-place*) pw=1 ;; esac
        done
        ;;
      gofmt | goimports)
        for x in "${w[@]:1}"; do
          [ "$x" = "-w" ] && pw=1
        done
        ;;
      git)
        case "$segstr" in
          *" checkout "* | *" restore "* | *" mv "* | *" rm "* | *" apply "* | *" am "* | *" reset "*) pw=1 ;;
        esac
        ;;
    esac
    if [ "$pw" = 1 ]; then
      for x in "${w[@]:1}"; do
        if protected_hit "$x"; then
          deny "$PROTECTED_MSG（$c0b: $x）"
        fi
      done
    fi
  fi

  # Worker（と Reviewer）: agentctl state … merged|ready-to-merge
  if [ "$ROLE" = "worker" ] || [ "$ROLE" = "reviewer" ]; then
    case "$segstr" in
      *agentctl*)
        local seen=0
        for x in "${w[@]}"; do
          if [ "$seen" = 1 ]; then
            case "$x" in
              merged | ready-to-merge | status:merged | status:ready-to-merge)
                deny "Worker は状態を $x にしない（Orchestrator が行う）"
                ;;
            esac
          fi
          [ "$x" = "state" ] && seen=1
        done
        ;;
    esac
  fi

  # Reviewer: 書き込みにあたる操作
  if [ "$ROLE" = "reviewer" ]; then
    if is_interpreter "$c0b"; then
      deny "Reviewer はインタプリタ（$c0b）を使わない（任意のコードでファイルを書き換えられる）"
    fi
    case "$c0b" in
      tee | cp | mv | rm | rmdir | touch | mkdir | ln | install | truncate | dd | patch | chmod | chown)
        deny "Reviewer はファイルを書き換えない（$c0b）"
        ;;
      sed | gsed)
        for x in "${w[@]:1}"; do
          case "$x" in
            -i* | --in-place*) deny "Reviewer はファイルを書き換えない（$c0b -i）" ;;
          esac
        done
        ;;
      awk | gawk | mawk | nawk)
        # awk のプログラムの中の > ・system() は、引用符の中なので上のリダイレクトの判定に掛からない
        case "$segstr" in
          *'>'* | *system*) deny "Reviewer は awk でファイルを書き換えない・コマンドを実行しない" ;;
        esac
        ;;
    esac
  fi
}

# Worker・Reviewer: agentctl の書き込み（comment・state・claim・attempt、gate --comment・--set-status）（Issue #86）。
# agentctl comment は信頼する作者の認証情報で PR にコメントを書くので、偽のレビュー記録を投稿できてしまう。
# セグメントに分ける前のコマンドの全体で判定する（agentctl_write_hit を参照。--dry-run の例外はない）。
if [ "$ROLE" = "worker" ] || [ "$ROLE" = "reviewer" ]; then
  acsub="$(agentctl_write_hit "$noquote")"
  if [ -n "$acsub" ]; then
    deny "Worker・Reviewer は agentctl の書き込み（$acsub）を含むコマンドを使わない（PR へのコメントと Issue の状態の書き込みは Orchestrator が行う。--dry-run でも、コメントや引用符の中の語でも拒否する）"
  fi
fi

# shellcheck disable=SC2020
while IFS= read -r seg; do
  check_segment "$seg"
done < <(printf '%s\n' "$norm" | tr ';&|()`' '\n\n\n\n\n\n')

exit 0
