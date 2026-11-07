#!/usr/bin/env bash
# マージの列（Issue #154）。開いた PR を見張り、Merge Gate が pass したものから 1 周に 1 つずつ
# merge.sh でマージし、残りの PR を 1 歩ずつ進める（GitHub の merge queue の代わり）。Orchestrator だけが使う。
#
# 使い方: scripts/agents/merge-train.sh [PR:Issue …]
#   引数がなければ、周ごとに開いた PR（ブランチ agent/issue-<n>）を PR 番号の小さい順に見つける。
#   .agents/run/train/skip に書いた PR 番号（1 行に 1 つ）は飛ばす。PR の Issue は一度だけ in-review にする
#   （印は .agents/run/train/inreview-<PR>。遷移に成功したか、すでに in-review 以降の状態のときだけ作る）
#
# 環境変数:
#   AGENT_TRAIN_INTERVAL  周の間隔（秒、既定 180）
#   AGENT_TRAIN_CYCLES    この数の周で終わる（テスト用。既定 0 は終わらない）
#
# 1 周の内容:
#   0. gh api rate_limit の残りが 300 未満なら、戻る時刻まで休む
#   1. 前から順に Merge Gate（agentctl gate）を判定し、pass の最初の 1 つを、Issue を ready-to-merge に
#      してから merge.sh でマージする。マージしたら、メインの作業ツリーで git pull --ff-only を実行する
#      （.agents/config.json などの変更を、ほかの道具の判定にすぐ反映するため）。
#      PR の HEAD・更新時刻・コメントの数・チェックの状態と main の HEAD が前の周と同じなら、前の判定を使う
#      （一時的な pending＝C9 の mergeable の未計算を含む判定は 2 周まで。過ぎたら判定し直す）
#   2. 残りを 1 歩ずつ進める
#      C10 が fail → main の取り込み（PUT pulls/<PR>/update-branch。HTTP 422 の merge conflict は「止まった」、
#      それ以外の失敗＝通信・5xx・API の上限などは次の周で試す）
#      C9 が fail →「止まった」。C6 が fail か、C6 が pass で C7 が fail →「止まった」
#      C6 が pass でなく C10 が pass → review.sh をバックグラウンドで起動（PR ごとに 1 つまで）
#      C2 が fail で ci-ok が pass → 本文の「- [ ] AC-n: CI（`ci-ok`）が通る」の行だけを [x] にする
# - 判定できない（gate の結果が空・不正）ときは、その周はその PR に何もしない
# - マージ済み・閉じた PR は列から外す。止まった PR は、新しい PR か修正（判定の目印が変わる）を待つ
set -euo pipefail

# shellcheck disable=SC2034 # lib.sh の die などが使う
PROG="merge-train"
# shellcheck source=lib.sh disable=SC1091
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

queue_fixed=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    -h | --help)
      usage_from_header "$0"
      exit 0
      ;;
    -*) die "不明な引数: $1" ;;
    *)
      printf '%s\n' "$1" | grep -Eq '^[0-9]+:[0-9]+$' || die "引数は PR:Issue の形にする: $1"
      queue_fixed="$queue_fixed $1"
      ;;
  esac
  shift
done

interval="${AGENT_TRAIN_INTERVAL:-180}"
cycles="${AGENT_TRAIN_CYCLES:-0}"
case "$interval" in '' | *[!0-9]*) die "AGENT_TRAIN_INTERVAL は 0 以上の整数にする（${interval}）" ;; esac
case "$cycles" in '' | *[!0-9]*) die "AGENT_TRAIN_CYCLES は 0 以上の整数にする（${cycles}）" ;; esac

need git jq
require_orchestrator
if in_linked_worktree; then
  die "リンクした Worktree の中では実行しない（メインの作業ツリーで実行する）"
fi
need gh
root="$(main_root)"
cd "$root"
repo="$(repo_slug)"
prefix="$(cfg '.branch_prefix' 'agent/issue-')"
default_branch="$(cfg '.default_branch' 'main')"
state_dir="$root/.agents/run/train"
log_dir="$root/.agents/logs"
mkdir -p "$state_dir" "$log_dir"

log() {
  echo "${PROG}: $(date '+%H:%M:%S') $*" >&2
}

# 列から外した PR（マージ済み・閉じた。空白で区切る）
done_prs=" "

has_word() { # has_word <リスト> <語>
  case "$1" in *" $2 "*) return 0 ;; esac
  return 1
}

# skipped <PR>: .agents/run/train/skip に書いてあれば 0
skipped() {
  local f="$state_dir/skip"
  [ -f "$f" ] || return 1
  awk -v p="$1" '{ sub(/\r$/, ""); for (i = 1; i <= NF; i++) if ($i == p) f = 1 } END { exit f ? 0 : 1 }' "$f"
}

# rate_limit_pause: API の残りが 300 未満なら、戻る時刻まで休んで 0 を返す。読めなければ 1（休まない）
rate_limit_pause() {
  local rl remaining reset now w
  rl="$(gh api rate_limit --jq '"\(.resources.core.remaining) \(.resources.core.reset)"' 2>/dev/null)" || return 1
  remaining="${rl%% *}"
  reset="${rl#* }"
  case "$remaining" in '' | *[!0-9]*) return 1 ;; esac
  case "$reset" in '' | *[!0-9]*) return 1 ;; esac
  [ "$remaining" -lt 300 ] || return 1
  now="$(date +%s)"
  w=$((reset - now + 1))
  [ "$w" -ge 1 ] || w=1
  log "GitHub の API の残りが ${remaining} 回。戻るまで ${w} 秒休む"
  sleep "$w"
  return 0
}

# list_queue: この周の列を「PR:Issue」の行で出す。失敗したら 1
list_queue() {
  local out item
  if [ -n "$queue_fixed" ]; then
    for item in $queue_fixed; do
      printf '%s\n' "$item"
    done
    return 0
  fi
  out="$(gh pr list --repo "$repo" --state open --limit 100 --json number,headRefName 2>/dev/null)" || return 1
  printf '%s' "$out" | jq -r --arg p "$prefix" '
    [.[] | select(.headRefName | startswith($p)) | select(.headRefName[($p | length):] | test("^[0-9]+$"))]
    | sort_by(.number) | .[] | "\(.number):\(.headRefName[($p | length):])"' 2>/dev/null || return 1
}

# pr_checks <PR>: gh pr checks の JSON（name・bucket）。gh pr checks はほかのチェックの失敗で 1、
# 待ちで 8 を返すので、終了コードではなく出力で判断する（不具合 1）。読めなければ []
pr_checks() {
  local out
  out="$(gh pr checks "$1" --repo "$repo" --json name,bucket 2>/dev/null)" || true
  if printf '%s' "$out" | jq -e 'type == "array"' >/dev/null 2>&1; then
    printf '%s' "$out" | jq -c 'sort_by(.name)'
  else
    echo '[]'
  fi
}

# main_head: リモートの既定のブランチの HEAD（API を使わない）。読めなければ空
main_head() {
  git ls-remote origin "refs/heads/$default_branch" 2>/dev/null | awk 'NR == 1 { print $1 }' || true
}

# gate_valid <JSON>: 判定として使えるか（verdict が pass・fail・pending で、results がある）
gate_valid() {
  printf '%s' "$1" | jq -e '(.verdict == "pass" or .verdict == "fail" or .verdict == "pending")
    and (.results | type == "array" and length > 0)' >/dev/null 2>&1
}

# status_of <gate の JSON> <ID>: その条件の status（なければ空）
status_of() {
  printf '%s' "$1" | jq -r --arg id "$2" '[.results[]? | select(.id == $id) | .status] | first // ""' 2>/dev/null || true
}

# 一時的な pending の判定を使う周の数（判定した周を含む）。過ぎたら目印が同じでも判定し直す
TRANSIENT_CYCLES=2

# transient_pending <gate の JSON>: 目印に表れない一時的な pending（C9 の mergeable の未計算）を含めば 0
transient_pending() {
  [ "$(status_of "$1" C9)" = pending ]
}

# tick_ci_ac <本文のファイル> <出力のファイル>: 「- [ ] AC-n: CI（`ci-ok`）が通る」の行だけを [x] にする。
# 行の末尾の「（…結果待ち）」は外す。変えた行があれば 0。
# （ロケールに左右されないよう、LC_ALL=C でバイトの列として比べる）
tick_ci_ac() {
  LC_ALL=C awk '
    BEGIN { pre = "- [ ] AC-"; mid = ": CI（`ci-ok`）が通る"; open = "（"; wait = "結果待ち）"; n = 0 }
    {
      line = $0; cr = ""
      if (sub(/\r$/, "", line)) cr = "\r"
      if (substr(line, 1, length(pre)) == pre) {
        rest = substr(line, length(pre) + 1)
        if (match(rest, /^[0-9]+/)) {
          num = substr(rest, 1, RLENGTH)
          rest = substr(rest, RLENGTH + 1)
          if (substr(rest, 1, length(mid)) == mid) {
            tail = substr(rest, length(mid) + 1)
            sub(/[ \t]+$/, "", tail)
            ok = (tail == "")
            if (!ok && substr(tail, 1, length(open)) == open && length(tail) >= length(open) + length(wait) &&
              substr(tail, length(tail) - length(wait) + 1) == wait) ok = 1
            if (ok) { print "- [x] AC-" num mid cr; n++; next }
          }
        }
      }
      print $0
    }
    END { exit (n > 0 ? 0 : 1) }' "$1" >"$2"
}

# review_running <PR>: その PR の review.sh が動いていれば 0
review_running() {
  local f="$state_dir/review-$1.pid" p
  [ -f "$f" ] || return 1
  p="$(cat "$f" 2>/dev/null || true)"
  pid_alive "$p"
}

start_review() { # start_review <PR>
  local pr="$1" lf p
  if review_running "$pr"; then
    log "PR #${pr}: review.sh が動いている（2 つ目は起動しない）"
    return 0
  fi
  lf="$log_dir/review-$pr-$(date -u +%Y%m%dT%H%M%SZ).log"
  nohup "$AGENTS_DIR/review.sh" "$pr" >"$lf" 2>&1 </dev/null &
  p=$!
  printf '%s\n' "$p" >"$state_dir/review-$pr.pid"
  log "PR #${pr}: review.sh を起動した（pid ${p}、ログ ${lf}）"
}

# stop_pr <PR> <目印> <理由>: 止まった PR を記録する（同じ目印の間は繰り返さない）
stop_pr() {
  printf '%s\n' "$2" >"$state_dir/stopped-$1"
  log "PR #${1}: 止まった（$3）。新しい PR か修正を待つ"
}

# update_conflict <出力のファイル>: update-branch の失敗が確定したコンフリクト（HTTP 422 の merge conflict）なら 0
update_conflict() {
  grep -q 'HTTP 422' "$1" 2>/dev/null && grep -qi 'merge conflict' "$1" 2>/dev/null
}

# mark_in_review <PR> <Issue>: Issue を一度だけ in-review にする。遷移に成功したか、すでに in-review 以降の
# 状態なら印（.agents/run/train/inreview-<PR>）を作る。それ以外（一時的な失敗など）は印を作らず、次の周で試す
mark_in_review() {
  local pr="$1" issue="$2" labels
  [ ! -f "$state_dir/inreview-$pr" ] || return 0
  if agentctl state "$issue" in-review --repo "$repo" --pr "$pr" >/dev/null 2>&1; then
    : >"$state_dir/inreview-$pr"
    return 0
  fi
  labels="$(gh issue view "$issue" --repo "$repo" --json labels -q '.labels[].name' 2>/dev/null)" || {
    log "Issue #${issue}: in-review にできず、状態も読めない（次の周で試す）"
    return 0
  }
  if printf '%s\n' "$labels" | grep -Eqx 'status:(in-review|changes-requested|ci-pending|ready-to-merge|merged)'; then
    : >"$state_dir/inreview-$pr"
    log "Issue #${issue}: すでに in-review 以降の状態なので、in-review にしない"
  else
    log "Issue #${issue}: in-review にできない（次の周で試す）"
  fi
  return 0
}

# pr_gone <PR>: マージ済み・閉じていれば 0
pr_gone() {
  local st
  st="$(gh pr view "$1" --repo "$repo" --json state -q .state 2>/dev/null)" || return 1
  case "$st" in MERGED | CLOSED) return 0 ;; esac
  return 1
}

# advance <PR> <gate の JSON> <本文のファイル> <目印>: 1 歩だけ進める
advance() {
  local pr="$1" g="$2" body_file="$3" key="$4"
  local c2 c6 c7 c9 c10 ci out
  if [ -f "$state_dir/stopped-$pr" ] && [ "$(cat "$state_dir/stopped-$pr")" = "$key" ]; then
    log "PR #${pr}: 止まったまま（判定の目印が変わるのを待つ）"
    return 0
  fi
  rm -f "$state_dir/stopped-$pr"
  c2="$(status_of "$g" C2)"
  c6="$(status_of "$g" C6)"
  c7="$(status_of "$g" C7)"
  c9="$(status_of "$g" C9)"
  c10="$(status_of "$g" C10)"
  if [ "$c10" = fail ]; then
    if gh api -X PUT "repos/$repo/pulls/$pr/update-branch" >"$state_dir/update-$pr.err" 2>&1; then
      log "PR #${pr}: ${default_branch} の取り込みを依頼した（update-branch）。次の周で判定し直す"
    elif update_conflict "$state_dir/update-$pr.err"; then
      stop_pr "$pr" "$key" "${default_branch} を取り込めない（コンフリクト）: $(grep -i 'merge conflict' "$state_dir/update-$pr.err" | head -n 1 || true)"
    else
      # 通信の失敗・HTTP 5xx・API の上限などは一時的な失敗として、次の周で試す（止まった扱いにしない）
      log "PR #${pr}: update-branch に失敗した（次の周で試す）: $(head -n 1 "$state_dir/update-$pr.err" 2>/dev/null || true)"
    fi
    return 0
  fi
  if [ "$c9" = fail ]; then
    stop_pr "$pr" "$key" "コンフリクトがある（C9）"
    return 0
  fi
  if [ "$c6" = fail ]; then
    stop_pr "$pr" "$key" "HEAD のレビュー記録が request_changes（C6）"
    return 0
  fi
  if [ "$c6" = pass ] && [ "$c7" = fail ]; then
    stop_pr "$pr" "$key" "重大な指摘が残っている（C7）"
    return 0
  fi
  if [ "$c6" != pass ] && [ "$c10" = pass ]; then
    start_review "$pr"
  fi
  if [ "$c2" = fail ]; then
    ci="$(pr_checks "$pr" | jq -r '[.[] | select(.name == "ci-ok") | .bucket] | first // ""' 2>/dev/null || true)"
    if [ "$ci" = pass ]; then
      out="$(mktemp "${TMPDIR:-/tmp}/merge-train-body.XXXXXX")"
      if tick_ci_ac "$body_file" "$out"; then
        if gh pr edit "$pr" --repo "$repo" --body-file "$out" >/dev/null 2>&1; then
          log "PR #${pr}: ci-ok が pass なので、本文の CI の AC にチェックを付けた"
        else
          log "PR #${pr}: 本文を更新できない（次の周で試す）"
        fi
      fi
      rm -f "$out"
    fi
  fi
  return 0
}

# evaluate <PR>: 判定する（使い回せれば使い回す）。結果を ${gate}・${key}、本文を ${state_dir}/body-<PR> に置く。
# 判定できなければ 1
evaluate() {
  local pr="$1" info mh new_key cache_key out rc uses
  # 目印は GraphQL の 1 回（gh pr view）と git ls-remote で作る
  info="$(gh pr view "$pr" --repo "$repo" --json number,state,headRefOid,updatedAt,comments,statusCheckRollup,body 2>/dev/null)" || {
    log "PR #${pr}: gh pr view に失敗した（次の周で試す）"
    return 1
  }
  printf '%s' "$info" | jq -j '.body // ""' >"$state_dir/body-$pr" 2>/dev/null || return 1
  mh="$(main_head)"
  new_key="$(printf '%s' "$info" | jq -r --arg m "$mh" '
    ([.statusCheckRollup // [] | .[] | "\(.name // .context // "")=\(.conclusion // .state // .status // "")"] | sort | join(",")) as $c
    | "head=\(.headRefOid // "") updated=\(.updatedAt // "") comments=\(.comments // [] | length) checks=\($c) main=\($m)"' 2>/dev/null)" ||
    return 1
  key="$new_key"
  cache_key="$(cat "$state_dir/gate-$pr.key" 2>/dev/null || true)"
  if [ -n "$mh" ] && [ "$cache_key" = "$new_key" ] && [ -f "$state_dir/gate-$pr.json" ]; then
    gate="$(cat "$state_dir/gate-$pr.json")"
    if gate_valid "$gate"; then
      if ! transient_pending "$gate"; then
        return 0
      fi
      # 一時的な pending（C9 の mergeable の未計算）は目印に表れないので、使うのは TRANSIENT_CYCLES 周まで
      uses="$(cat "$state_dir/gate-$pr.uses" 2>/dev/null || echo 1)"
      case "$uses" in '' | *[!0-9]*) uses="$TRANSIENT_CYCLES" ;; esac
      if [ "$uses" -lt "$TRANSIENT_CYCLES" ]; then
        printf '%s\n' "$((uses + 1))" >"$state_dir/gate-$pr.uses"
        return 0
      fi
      log "PR #${pr}: 一時的な pending（C9）の判定を ${uses} 周使ったので、判定し直す"
    fi
  fi
  rm -f "$state_dir/gate-$pr.key" "$state_dir/gate-$pr.json" "$state_dir/gate-$pr.uses"
  set +e
  out="$(agentctl gate --repo "$repo" --pr "$pr" --json 2>"$state_dir/gate-$pr.err")"
  rc=$?
  set -e
  if ! gate_valid "$out"; then
    # API の上限などで空・不正な結果のときは「レビュー記録がない」と読まない（不具合 2）
    log "PR #${pr}: Merge Gate を判定できない（終了コード ${rc}）。この周は何もしない: $(head -n 1 "$state_dir/gate-$pr.err" 2>/dev/null || true)"
    return 1
  fi
  gate="$out"
  printf '%s\n' "$gate" >"$state_dir/gate-$pr.json"
  printf '%s\n' "$new_key" >"$state_dir/gate-$pr.key"
  echo 1 >"$state_dir/gate-$pr.uses"
  return 0
}

# pull_main: マージの後、メインの作業ツリーを最新にする（失敗してもログだけ）
pull_main() {
  if git pull --ff-only --quiet >/dev/null 2>"$state_dir/pull.err"; then
    log "メインの作業ツリーを最新にした（git pull --ff-only）"
  else
    log "メインの作業ツリーを最新にできない（git pull --ff-only）: $(head -n 1 "$state_dir/pull.err" 2>/dev/null || true)"
  fi
}

one_cycle() {
  local q item pr issue gate key verdict merged="" rc
  local evaluated=""
  if rate_limit_pause; then
    return 0
  fi
  q="$(list_queue)" || {
    log "開いた PR を読めない（次の周で試す）"
    return 0
  }
  for item in $q; do
    pr="${item%%:*}"
    issue="${item#*:}"
    has_word "$done_prs" "$pr" && continue
    if skipped "$pr"; then
      log "PR #${pr}: skip に書いてあるので飛ばす"
      continue
    fi
    mark_in_review "$pr" "$issue"
    gate="" key=""
    evaluate "$pr" || continue
    if [ "$(printf '%s' "$gate" | jq -r '.closed // false')" = true ]; then
      log "PR #${pr}: マージ済みか閉じているので列から外す"
      done_prs="$done_prs$pr "
      continue
    fi
    verdict="$(printf '%s' "$gate" | jq -r '.verdict')"
    if [ -z "$merged" ] && [ "$verdict" = pass ]; then
      merged="$pr"
      log "PR #${pr}: Merge Gate が pass。merge.sh でマージする"
      # merge.sh が merged にするので、その前に ready-to-merge にする
      agentctl state "$issue" ready-to-merge --repo "$repo" >/dev/null 2>&1 ||
        log "Issue #${issue}: ready-to-merge にできない（すでにその状態か、遷移できない）"
      set +e
      "$AGENTS_DIR/merge.sh" "$pr"
      rc=$?
      set -e
      if [ "$rc" = 0 ]; then
        log "PR #${pr}: マージした"
        done_prs="$done_prs$pr "
        pull_main
      elif pr_gone "$pr"; then
        # マージの後の処理で失敗しても、マージ済みなら列から外す（不具合 4）
        log "PR #${pr}: merge.sh は終了コード ${rc} だが、PR はマージ済みか閉じているので列から外す"
        done_prs="$done_prs$pr "
        pull_main
      else
        log "PR #${pr}: merge.sh が終了コード ${rc} で失敗した（次の周で試す）"
      fi
      continue
    fi
    evaluated="$evaluated$pr "
    printf '%s\n' "$gate" >"$state_dir/cycle-gate-$pr.json"
    printf '%s\n' "$key" >"$state_dir/cycle-key-$pr"
  done
  [ -n "$q" ] || log "開いた PR がない"
  for pr in $evaluated; do
    advance "$pr" "$(cat "$state_dir/cycle-gate-$pr.json")" "$state_dir/body-$pr" "$(cat "$state_dir/cycle-key-$pr")"
    rm -f "$state_dir/cycle-gate-$pr.json" "$state_dir/cycle-key-$pr"
  done
  return 0
}

log "始める（間隔 ${interval} 秒、リポジトリ ${repo}）"
n=0
while :; do
  n=$((n + 1))
  one_cycle || log "周の途中で失敗した（次の周で試す）"
  if [ "$cycles" -gt 0 ] && [ "$n" -ge "$cycles" ]; then
    log "${cycles} 周を終えた"
    exit 0
  fi
  sleep "$interval"
done
