#!/usr/bin/env bash
# PreToolUse（Bash）の hook（Issue #5）。tool_input.command を調べ、役割ごとの
# 禁止事項に当たれば、理由を標準エラーに出して終了コード 2 で拒否する。
#
# コマンドを完全に解釈することはできないので、控えめに（疑わしいものは拒否する側に）
# 判定する。引用符は取り除き、`;`・`&`・`|`・`(`・`)`・バッククォート・改行で区切った
# まとまり（セグメント）ごとに調べる。
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
      -c | --git-dir | --work-tree | --namespace) shift 2 ;;
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

WRITERS=" cp mv tee install rsync ln touch rm rmdir mkdir truncate dd patch chmod chown unzip tar python python3 node perl ruby "

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
      [A-Za-z_]*=*) w=("${w[@]:1}"); changed=1; continue ;;
      env | sudo | command | exec | nohup | time | nice | then | do | else | if | while | until | '!' | '{' | '$')
        w=("${w[@]:1}")
        while [ "${#w[@]}" -gt 0 ]; do
          case "${w[0]}" in
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
    case "$c0b" in
      tee | cp | mv | rm | rmdir | touch | mkdir | ln | install | truncate | dd | patch | chmod | chown)
        deny "Reviewer はファイルを書き換えない（$c0b）"
        ;;
      sed | perl)
        for x in "${w[@]:1}"; do
          case "$x" in
            -i* | -pi* | --in-place*) deny "Reviewer はファイルを書き換えない（$c0b -i）" ;;
          esac
        done
        ;;
    esac
  fi
}

# shellcheck disable=SC2020
while IFS= read -r seg; do
  check_segment "$seg"
done < <(printf '%s\n' "$norm" | tr ';&|()`' '\n\n\n\n\n\n')

exit 0
