#!/usr/bin/env bash
# PreToolUse（Edit・Write・MultiEdit・NotebookEdit）の hook（Issue #5）。
# tool_input.file_path（NotebookEdit は notebook_path）を調べる。
#
# - reviewer: すべて拒否
# - human 以外: .github/workflows/ の中は拒否
# - worker: 自分の Worktree の外・.git/ の中は拒否。.agents/config.json の
#   protected_paths に入るファイルは AGENT_ALLOW_PROTECTED=1 のときだけ許す
# - orchestrator: .agents/・.claude/worktrees/ 以外は拒否。リポジトリの外では、Claude Code の
#   このプロジェクトのメモリのディレクトリ（$HOME/.claude/projects/<キー>/memory/ の下）だけを許す（#107）。
#   <キー> はメインの作業ツリーの絶対パスの / を - に置き換えたもの
# - human: 制限しない
# 想定外のエラーでは終了コード 2（拒否）にする（fail-closed。lib.sh の hook_on_exit）。
set -euo pipefail
# lib.sh を読む前の失敗も拒否にする（lib.sh を読むと、lib.sh の trap に置き換わる）
trap 'echo "[guard] 拒否（内部エラー）: lib.sh を読み込めない" >&2; exit 2' EXIT

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh disable=SC1091
. "$here/lib.sh"

hook_read_input
CWD="$(hook_cwd)"
detect_role "$CWD"

[ "$ROLE" = "human" ] && allow
[ "$ROLE" = "reviewer" ] && deny "Reviewer はファイルを書き換えない（指摘だけを返す）"

file="$(hook_field '.tool_input.file_path // .tool_input.notebook_path')"
[ -n "$file" ] || deny "書き込み先のパスが分からない"

# normpath <絶対パス>: . と .. を取り除く。
normpath() {
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

# physpath <絶対パス>: 存在する一番深い親ディレクトリのシンボリックリンクを解決する。
physpath() {
  local p="$1" rest="" d
  while [ "$p" != "/" ] && [ ! -d "$p" ]; do
    rest="/${p##*/}$rest"
    p="$(dirname "$p")"
  done
  d="$(abs_dir "$p")"
  [ "$d" = "/" ] && d=""
  printf '%s\n' "$d$rest"
}

case "$file" in
  /*) ;;
  *) file="$CWD/$file" ;;
esac
path="$(physpath "$(normpath "$file")")"

TOP="$(repo_toplevel "$CWD")"

# rel_to <ルート> <パス>: ルートの中ならルートからの相対パスを出す。
rel_to() {
  case "$2" in
    "$1"/*) printf '%s\n' "${2#"$1"/}" ;;
    *) return 1 ;;
  esac
}

# memory_dir <メインの作業ツリーのルート>: Claude Code のこのプロジェクトのメモリのディレクトリ。
# $HOME/.claude/projects までは物理パスにし（~/.claude がシンボリックリンクでもよい）、<キー>/memory は
# 字句のまま足す（<キー> や memory がシンボリックリンクなら、書き込み先の物理パスがこの下に入らないので拒否になる）。
# HOME が絶対パスでなければ空。
memory_dir() {
  local home="${HOME:-}" projects
  case "$home" in
    /*) ;;
    *) return 0 ;;
  esac
  projects="$(physpath "$(normpath "$home/.claude/projects")")"
  printf '%s/%s/memory\n' "${projects%/}" "${1//\//-}"
}

case "/$path/" in
  */.github/workflows/*)
    deny ".github/workflows/ には書き込まない（ワークフローは .github/workflow-sources/ に書き、人が取り込む）"
    ;;
esac

case "$ROLE" in
  worker)
    [ -n "$TOP" ] || deny "Git の作業ツリーの外では書き込まない"
    rel="$(rel_to "$TOP" "$path")" || deny "自分の Worktree（${TOP}）の外には書き込まない: $path"
    case "/$rel/" in
      */.git/*) deny ".git/ の中には書き込まない" ;;
    esac
    cfg="$TOP/.agents/config.json"
    if [ "${AGENT_ALLOW_PROTECTED:-0}" != "1" ] && [ -f "$cfg" ]; then
      while IFS= read -r pat; do
        [ -n "$pat" ] || continue
        # ** も * も / をまたいで一致させる（控えめに判定する）。
        glob="${pat//\*\*/*}"
        # shellcheck disable=SC2053
        if [[ "$rel" == $glob ]] || [[ "$rel/" == $glob ]]; then
          deny "保護されたパス（${pat}）は、Issue の変更予定ファイルに含まれるとき（AGENT_ALLOW_PROTECTED=1）だけ変更できる: $rel"
        fi
      done < <(jq -r '.protected_paths[]? // empty' "$cfg")
    fi
    ;;
  orchestrator)
    root="$(repo_main_root "$CWD")"
    [ -n "$root" ] || root="$TOP"
    [ -n "$root" ] || deny "Orchestrator は .agents/・.claude/worktrees/ 以外に書き込まない: $path"
    # Claude Code のこのプロジェクトのメモリ（$HOME/.claude/projects/<キー>/memory/ の下）だけは許す（#107）。
    # .. を含むパス（シンボリックリンクの先で .. をたどると外に出うる）と、シンボリックリンクそのものは許さない
    mem="$(memory_dir "$root")"
    if [ -n "$mem" ] && rel_to "$mem" "$path" >/dev/null; then
      case "/$file/" in
        */../*) deny "Orchestrator はメモリのディレクトリへ .. を含むパスで書き込まない: $file" ;;
      esac
      [ ! -L "$path" ] || deny "Orchestrator はメモリのディレクトリの中のシンボリックリンクには書き込まない: $path"
      allow
    fi
    rel="$(rel_to "$root" "$path")" || deny "Orchestrator は .agents/・.claude/worktrees/・このプロジェクトのメモリ（${mem:-?}/）以外に書き込まない: $path"
    case "$rel" in
      .agents/* | .claude/worktrees/*) ;;
      *) deny "Orchestrator はコード・テスト・ドキュメントを直接変更しない（書けるのは .agents/・.claude/worktrees/ だけ）: $rel" ;;
    esac
    ;;
esac

allow
