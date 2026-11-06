package state

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/github"
	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/snapshot"
)

// DefaultActor は記録の actor。
const DefaultActor = "orchestrator"

// Env は操作を組み立てるときの前提。
type Env struct {
	Repo         string
	BranchPrefix string
	RetryLimit   int
	Now          time.Time
	Actor        string
	// Trusted は、状態の記録を信頼するコメントの作者か（PR なら、Issue との関係を数える作者か）を返す。
	Trusted func(login string) bool
}

// trusted は Trusted を返す（nil なら、誰も信頼しない）。
func (e *Env) trusted() func(login string) bool {
	if e.Trusted == nil {
		return func(string) bool { return false }
	}
	return e.Trusted
}

// Result は、行う予定の API の操作と、その説明。
type Result struct {
	Ops      []github.Op
	Message  string
	Warnings []string
	// LimitReached は、attempt で再試行の上限に達したこと（終了コード 5）を表す。
	LimitReached bool
}

// RejectError は、遷移や前提の確認で拒否したことを表す。
type RejectError struct {
	// Precondition は、claim の前提を満たさないこと（終了コード 4）を表す。
	Precondition bool
	Msg          string
}

func (e *RejectError) Error() string { return e.Msg }

func reject(format string, a ...any) error { return &RejectError{Msg: fmt.Sprintf(format, a...)} }

func precondition(format string, a ...any) error {
	return &RejectError{Precondition: true, Msg: fmt.Sprintf(format, a...)}
}

// IsReject は err が RejectError かを返す。
func IsReject(err error) (*RejectError, bool) {
	var r *RejectError
	ok := errors.As(err, &r)
	return r, ok
}

func (e *Env) issuePath(n int, suffix string) string {
	return fmt.Sprintf("%s/issues/%d%s", github.RepoPath(e.Repo), n, suffix)
}

// relabelOps は、add のラベルを付けてから remove のラベルを外す操作。
// 途中で失敗すると `status:*` が 2 つ残るので、recover が fix-labels を出せる。
func (e *Env) relabelOps(n int, add []string, remove []string) []github.Op {
	var ops []github.Op
	if len(add) > 0 {
		ops = append(ops, github.Op{Method: "POST", Path: e.issuePath(n, "/labels"), Body: map[string][]string{"labels": add}})
	}
	for _, l := range remove {
		ops = append(ops, github.Op{Method: "DELETE", Path: e.issuePath(n, "/labels/"+url.PathEscape(l))})
	}
	return ops
}

func (e *Env) commentOp(n int, r *Record) github.Op {
	return github.Op{Method: "POST", Path: e.issuePath(n, "/comments"), Body: map[string]string{"body": r.Comment()}}
}

func (e *Env) newRecord(from, to Status) Record {
	actor := e.Actor
	if actor == "" {
		actor = DefaultActor
	}
	return Record{From: from, To: to, Actor: actor, At: FormatTime(e.Now)}
}

func (e *Env) load(snap *snapshot.Snapshot, n int) (*snapshot.Issue, History, []string, error) {
	is := snap.Issue(n)
	if is == nil {
		return nil, History{}, nil, fmt.Errorf("state: Issue #%d がスナップショットにない", n)
	}
	h, warns := ReadHistory(is, e.trusted())
	return is, h, warns, nil
}

// carry は、最後の着手の記録から worker・branch・session・attempt を引き継ぐ。
func (e *Env) carry(r *Record, h History, n int) {
	r.Branch = snapshot.BranchFor(e.BranchPrefix, n)
	if c := h.LatestClaim(); c != nil {
		r.Worker, r.Session = c.Worker, c.Session
		if c.Branch != "" {
			r.Branch = c.Branch
		}
	}
	r.Attempt = h.MaxAttempt()
}

// TransitionOpts は state の追加の情報。
type TransitionOpts struct {
	Reason string
	Cause  string
	PR     int
}

// Transition は、Issue n を to に変える操作を組み立てる（agentctl state）。
func Transition(snap *snapshot.Snapshot, e Env, n int, to Status, o TransitionOpts) (*Result, error) {
	is, h, warns, err := e.load(snap, n)
	if err != nil {
		return nil, err
	}
	from, err := Current(is)
	if err != nil {
		return nil, reject("%v（recover の fix-labels で直す）", err)
	}
	if !CanTransition(from, to) {
		return nil, reject("state: #%d の %s から %s への遷移は許されていない（許す遷移: %v）", n, from, to, Allowed(from))
	}
	branch := snapshot.BranchFor(e.BranchPrefix, n)
	pr := o.PR
	if pr > 0 && snap.Pull(pr) == nil {
		return nil, fmt.Errorf("state: PR #%d がスナップショットにない", pr)
	}
	if to == Merged {
		if pr > 0 {
			p := snap.Pull(pr)
			if !p.Merged || !p.RelatesTo(n, branch, e.trusted()) {
				return nil, reject("state: PR #%d は #%d に関係する（信頼する作者の）マージ済みの PR ではない", pr, n)
			}
		} else {
			merged := snap.MergedPullsFor(n, branch, e.trusted())
			if len(merged) == 0 {
				return nil, reject("state: #%d に関係するマージ済みの PR がないので merged にできない", n)
			}
			pr = merged[0].Number
		}
	}
	r := e.newRecord(from, to)
	e.carry(&r, h, n)
	r.Cause, r.Reason, r.PR = o.Cause, o.Reason, pr
	ops := e.relabelOps(n, []string{to.Label()}, []string{from.Label()})
	ops = append(ops, e.commentOp(n, &r))
	return &Result{Ops: ops, Warnings: warns, Message: fmt.Sprintf("#%d: %s → %s", n, from, to)}, nil
}

// startable は、Issue n に新しく着手してよいか（依存が完了し、ブランチと開いた PR がない）を確かめる。
// PR は、作者が trusted のものだけを数える。依存先が存在しないときは、拒否ではなくエラーを返す。
func startable(snap *snapshot.Snapshot, is *snapshot.Issue, branch string, trusted func(login string) bool) ([]string, error) {
	reasons, err := snap.UnmetDeps(is)
	if err != nil {
		return nil, err
	}
	if snap.HasBranch(branch) {
		reasons = append(reasons, fmt.Sprintf("ブランチ %s がある", branch))
	}
	for _, p := range snap.OpenPullsFor(is.Number, branch, trusted) {
		reasons = append(reasons, fmt.Sprintf("開いた PR #%d がある", p.Number))
	}
	return reasons, nil
}

// Startable は、plan と claim で共通の「着手してよい」の確認（状態を除く）。満たさない理由を返す。
// trusted は、Issue との関係を数える PR の作者かを返す。
func Startable(snap *snapshot.Snapshot, is *snapshot.Issue, branchPrefix string, trusted func(login string) bool) ([]string, error) {
	return startable(snap, is, snapshot.BranchFor(branchPrefix, is.Number), trusted)
}

// Claim は着手の宣言の操作を組み立てる（agentctl claim）。
func Claim(snap *snapshot.Snapshot, e Env, n int, worker, session string, resume bool) (*Result, error) {
	is, h, warns, err := e.load(snap, n)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(worker) == "" {
		return nil, errors.New("state: worker が空")
	}
	cur, err := Current(is)
	if err != nil {
		return nil, precondition("%v", err)
	}
	branch := snapshot.BranchFor(e.BranchPrefix, n)
	r := e.newRecord(cur, InProgress)
	r.Worker, r.Branch, r.Session, r.Attempt = worker, branch, session, h.MaxAttempt()+1

	// --resume は、同じ session でも冪等の判定をせず、前提を確かめてから attempt を 1 増やした記録を付ける
	// （recover の resume-worker が出す最新の session をそのまま渡すのが自然な使い方のため）。
	if resume {
		if cur != InProgress {
			return nil, precondition("state: #%d は %s なので再開できない（in-progress のときだけ）", n, cur)
		}
		if !snap.HasBranch(branch) {
			return nil, precondition("state: #%d のブランチ %s がないので再開できない", n, branch)
		}
		if ps := snap.OpenPullsFor(n, branch, e.trusted()); len(ps) > 0 {
			return nil, precondition("state: #%d には開いた PR #%d があるので再開できない", n, ps[0].Number)
		}
		r.Reason = "再開"
		return &Result{Ops: []github.Op{e.commentOp(n, &r)}, Warnings: warns,
			Message: fmt.Sprintf("#%d: %s が再開（attempt %d）", n, worker, r.Attempt)}, nil
	}

	// 同じ worker と session での 2 回目は、何もせず成功する。
	if c := h.LatestClaim(); cur == InProgress && c != nil && c.Worker == worker && c.Session == session {
		return &Result{Warnings: warns, Message: fmt.Sprintf("#%d: %s（session %q）が着手済み（attempt %d）。何もしない", n, worker, session, c.Attempt)}, nil
	}
	if cur != Ready {
		return nil, precondition("state: #%d は %s なので着手できない（ready のときだけ）", n, cur)
	}
	reasons, err := startable(snap, is, branch, e.trusted())
	if err != nil {
		return nil, err
	}
	if len(reasons) > 0 {
		return nil, precondition("state: #%d に着手できない: %s", n, strings.Join(reasons, "; "))
	}
	r.Reason = "着手"
	ops := e.relabelOps(n, []string{InProgress.Label()}, []string{Ready.Label()})
	ops = append(ops, e.commentOp(n, &r))
	return &Result{Ops: ops, Warnings: warns, Message: fmt.Sprintf("#%d: %s が着手（attempt %d、branch %s）", n, worker, r.Attempt, branch)}, nil
}

// Attempt は再試行を 1 回記録する操作を組み立てる（agentctl attempt）。
// cause の記録が retry_limit に達していたら、blocked と needs-human にする操作を返し、LimitReached を立てる。
func Attempt(snap *snapshot.Snapshot, e Env, n int, cause string) (*Result, error) {
	is, h, warns, err := e.load(snap, n)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(cause) == "" {
		return nil, errors.New("state: cause が空")
	}
	if e.RetryLimit < 1 {
		return nil, errors.New("state: retry_limit が 1 未満")
	}
	cur, err := Current(is)
	if err != nil {
		return nil, reject("%v（recover の fix-labels で直す）", err)
	}
	if cur == Merged {
		return nil, reject("state: #%d は merged なので再試行を記録しない", n)
	}
	count := h.CauseCount(cause)
	if count < e.RetryLimit {
		r := e.newRecord(cur, cur)
		e.carry(&r, h, n)
		r.Cause = cause
		r.Reason = fmt.Sprintf("再試行 %d/%d", count+1, e.RetryLimit)
		return &Result{Ops: []github.Op{e.commentOp(n, &r)}, Warnings: warns,
			Message: fmt.Sprintf("#%d: %s の再試行 %d/%d を記録した", n, cause, count+1, e.RetryLimit)}, nil
	}

	res := &Result{Warnings: warns, LimitReached: true,
		Message: fmt.Sprintf("#%d: %s の再試行が上限 %d に達した。blocked と %s にする", n, cause, e.RetryLimit, NeedsHumanLabel)}
	var add, remove []string
	if cur != Blocked {
		add = append(add, Blocked.Label())
		remove = append(remove, cur.Label())
	}
	if !is.HasLabel(NeedsHumanLabel) {
		add = append(add, NeedsHumanLabel)
	}
	if len(add) == 0 && len(remove) == 0 {
		res.Message = fmt.Sprintf("#%d: %s の再試行が上限 %d に達している（blocked・%s 済み）", n, cause, e.RetryLimit, NeedsHumanLabel)
		return res, nil
	}
	res.Ops = e.relabelOps(n, add, remove)
	r := e.newRecord(cur, Blocked)
	e.carry(&r, h, n)
	r.Cause = cause
	r.Reason = fmt.Sprintf("%s の再試行が上限 %d に達した。人の判断が必要", cause, e.RetryLimit)
	res.Ops = append(res.Ops, e.commentOp(n, &r))
	return res, nil
}
