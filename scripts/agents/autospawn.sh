#!/usr/bin/env bash
# Worker の自動起動（Issue #154）。一定の間隔で agentctl plan を見て、Ready の Issue を空いた枠の分だけ
# spawn-worker.sh --background で起動する。Orchestrator だけが使う。
#
# 使い方: scripts/agents/autospawn.sh
#
# 環境変数:
#   AGENT_AUTOSPAWN_INTERVAL  周の間隔（秒、既定 300）
#
# - 1 周で agentctl plan --repo <repo> --json の ready を出た順に、capacity の数まで
#   spawn-worker.sh <n> --background で起動する。拒否された Issue（終了コード 4 など）はその周は飛ばす
# - plan の deferred（実装中の Issue と変更予定ファイルが重なる）は起動しない。conflicts・deferred の
#   Issue と理由は、前の周と変わったときだけログに出す
# - 状態の自動の直し（Issue #240）: plan の active のうち status が in-progress で、動いている Worker
#   （.agents/run/issue-<n>.json の pid が生きているもの）がなく、開いた PR（ブランチ agent/issue-<n>）が
#   ある Issue を in-review に移す（API の上限などで移しそこねると、枠を食い続けるため）。枠は次の周で反映される
# - spawn-worker.sh は親（autospawn）と別のセッション（setsid がなければ、ジョブ制御で別のプロセスグループ）で
#   実行する。autospawn を止めても（プロセスグループごとでも）、起動した Worker は止まらない
# - 止め方: .agents/run/autospawn.stop を作ると、次の周で終わる（始めるときと終わるときにそのファイルを消す）
set -euo pipefail

# shellcheck disable=SC2034 # lib.sh の die などが使う
PROG="autospawn"
# shellcheck source=lib.sh disable=SC1091
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

while [ "$#" -gt 0 ]; do
  case "$1" in
    -h | --help)
      usage_from_header "$0"
      exit 0
      ;;
    *) die "不明な引数: $1" ;;
  esac
done

interval="${AGENT_AUTOSPAWN_INTERVAL:-300}"
case "$interval" in '' | *[!0-9]*) die "AGENT_AUTOSPAWN_INTERVAL は 0 以上の整数にする（${interval}）" ;; esac

need git jq
require_orchestrator
if in_linked_worktree; then
  die "リンクした Worktree の中では実行しない（メインの作業ツリーで実行する）"
fi
root="$(main_root)"
cd "$root"
repo="$(repo_slug)"
prefix="$(cfg '.branch_prefix' 'agent/issue-')"
mkdir -p "$root/.agents/run"
stop_file="$root/.agents/run/autospawn.stop"

log() {
  echo "${PROG}: $(date '+%H:%M:%S') $*" >&2
}

# 前の周の「選ばなかった Issue」（conflicts・deferred と理由）
last_why=""

# run_detached <コマンド…>: 親（autospawn）と別のセッション（setsid がなければ、ジョブ制御（set -m）で別の
# プロセスグループ）で実行し、終わるまで待って終了コードを返す。コマンドが起動した子（Worker）も、そのセッション・
# プロセスグループに入るので、親をプロセスグループごと止めても（TaskStop・Ctrl-C）止まらない（bash 3.2 でも動く）
run_detached() {
  if command -v setsid >/dev/null 2>&1; then
    # 子はプロセスグループの先頭ではないので、setsid は fork せずに exec する（終了コードがそのまま返る）
    setsid "$@" </dev/null
  else
    (
      set -m
      "$@" </dev/null &
      wait "$!"
    )
  fi
}

# fix_stale <plan の JSON>: in-progress で、動いている Worker がなく、開いた PR がある Issue を in-review に移す
fix_stale() {
  local n p pr
  for n in $(printf '%s' "$1" | jq -r '.active // [] | .[] | select(.status == "in-progress") | .number' 2>/dev/null || true); do
    case "$n" in '' | *[!0-9]*) continue ;; esac
    p="$(jq -r '.pid // empty' "$root/.agents/run/issue-$n.json" 2>/dev/null || true)"
    if pid_alive "$p"; then
      continue
    fi
    pr="$(gh pr list --repo "$repo" --head "$prefix$n" --state open --json number -q '.[0].number // empty' 2>/dev/null)" || {
      log "Issue #${n}: in-progress で Worker が動いていないが、PR を読めない（次の周で試す）"
      continue
    }
    case "$pr" in '' | *[!0-9]*) continue ;; esac
    if agentctl state "$n" in-review --repo "$repo" --pr "$pr" >/dev/null 2>&1; then
      log "Issue #${n}: in-progress のままだが、Worker が動いておらず PR #${pr} があるので、in-review に移した"
    else
      log "Issue #${n}: in-progress のままで Worker が動いておらず PR #${pr} があるが、in-review に移せない（次の周で試す）"
    fi
  done
  return 0
}

one_cycle() {
  local out capacity ready n started=0 rc why
  out="$(agentctl plan --repo "$repo" --json 2>/dev/null)" || {
    log "agentctl plan に失敗した（次の周で試す）"
    return 0
  }
  fix_stale "$out"
  why="$(printf '%s' "$out" | jq -r '[.conflicts[]?, .deferred[]?]
    | map("#\(.number): \((.reasons // []) | join(" / ") | .[0:120])") | .[]' 2>/dev/null || true)"
  if [ -n "$why" ] && [ "$why" != "$last_why" ]; then
    log "選ばなかった Issue:"
    printf '%s\n' "$why" | sed 's/^/  /' >&2
  fi
  last_why="$why"
  capacity="$(printf '%s' "$out" | jq -r '.capacity // 0' 2>/dev/null || true)"
  case "$capacity" in '' | *[!0-9]*)
    log "agentctl plan の capacity が読めない（次の周で試す）"
    return 0
    ;;
  esac
  ready="$(printf '%s' "$out" | jq -r '.ready // [] | .[] | .number' 2>/dev/null)" || {
    log "agentctl plan の ready が読めない（次の周で試す）"
    return 0
  }
  if [ "$capacity" -le 0 ] || [ -z "$ready" ]; then
    log "起動しない（空いた枠 ${capacity}、Ready $(printf '%s' "$ready" | grep -c . || true) 件）"
    return 0
  fi
  for n in $ready; do
    [ "$started" -lt "$capacity" ] || break
    case "$n" in '' | *[!0-9]*) continue ;; esac
    set +e
    run_detached "$AGENTS_DIR/spawn-worker.sh" "$n" --background
    rc=$?
    set -e
    if [ "$rc" = 0 ]; then
      started=$((started + 1))
      log "Issue #${n} の Worker を起動した（${started}/${capacity}）"
    else
      log "Issue #${n} は起動できない（終了コード ${rc}）。この周は飛ばす"
    fi
  done
  return 0
}

rm -f "$stop_file"
log "始める（間隔 ${interval} 秒、リポジトリ ${repo}。止めるには ${stop_file} を作る）"
while :; do
  if [ -e "$stop_file" ]; then
    rm -f "$stop_file"
    log "止めのファイルがあるので終わる"
    exit 0
  fi
  one_cycle || log "周の途中で失敗した（次の周で試す）"
  sleep "$interval"
done
