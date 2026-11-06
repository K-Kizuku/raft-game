// Package gate は Merge Gate の判定をする。
//
// Evaluate は純粋な関数で、設定と GitHub の状態のスナップショット（Input）だけから結果を決める。
// すべての条件を満たしたときだけ pass。待ちの状態があれば pending、それ以外の違反があれば fail。
package gate

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/config"
	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/issuebody"
	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/prbody"
	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/review"
)

// Verdict は判定の結果。
type Verdict string

const (
	Pass    Verdict = "pass"
	Pending Verdict = "pending"
	Fail    Verdict = "fail"
)

// ExitCode は終了コード（pass=0・fail=1・pending=2）。
func (v Verdict) ExitCode() int {
	switch v {
	case Pass:
		return 0
	case Pending:
		return 2
	default:
		return 1
	}
}

// SelfCheckNames は、C4 で失敗を数えない check run の名前（Merge Gate 自身と、そのワークフローのジョブ）。
var SelfCheckNames = []string{config.SelfContext, "merge-gate-evaluate"}

// BlockingLabels は、付いていると C1 が fail になるラベル。
var BlockingLabels = []string{"status:blocked", "needs-human"}

var failingConclusions = []string{"failure", "cancelled", "timed_out", "action_required"}

var sha40Re = regexp.MustCompile(`^[0-9a-f]{40}$`)

// Result は条件ごとの結果。
type Result struct {
	ID     string  `json:"id"`
	Title  string  `json:"title"`
	Status Verdict `json:"status"`
	Detail string  `json:"detail"`
}

// ReviewRef は判定に使った最新のレビュー記録。
type ReviewRef struct {
	CommentID int64  `json:"comment_id"`
	Author    string `json:"author"`
	Round     int    `json:"round"`
	HeadSHA   string `json:"head_sha"`
	Verdict   string `json:"verdict"`
	Reviewer  string `json:"reviewer"`
}

// Report は判定の結果の全体。
type Report struct {
	Verdict Verdict `json:"verdict"`
	Repo    string  `json:"repo"`
	PR      int     `json:"pr"`
	// HeadSHA は判定に使った PR の HEAD の SHA。pass のとき、レビュー・CI がこの SHA に対するものであることを確かめている。
	HeadSHA string `json:"head_sha"`
	// Closed は PR が closed・merged であること（このときはステータスを付けない）。
	Closed       bool       `json:"closed"`
	Results      []Result   `json:"results"`
	Notes        []string   `json:"notes"`
	LatestReview *ReviewRef `json:"latest_review,omitempty"`
}

// Failed は fail と pending の条件の ID を返す。
func (r *Report) Failed() (fails, pendings []string) {
	for _, res := range r.Results {
		switch res.Status {
		case Fail:
			fails = append(fails, res.ID)
		case Pending:
			pendings = append(pendings, res.ID)
		}
	}
	return fails, pendings
}

// Evaluate は判定する。cfg が nil または不正なら G0 で fail にする。
func Evaluate(cfg *config.Config, in Input) Report {
	if cfg == nil {
		return EvaluateConfigError(fmt.Errorf("設定がない"), in)
	}
	if err := cfg.Validate(); err != nil {
		return EvaluateConfigError(err, in)
	}
	e := &evaluator{cfg: cfg, in: in}
	return e.run()
}

// EvaluateConfigError は、設定を読めなかったときの結果（G0 で fail）を返す。
func EvaluateConfigError(err error, in Input) Report {
	rep := newReport(in)
	rep.Results = []Result{{ID: "G0", Title: titles["G0"], Status: Fail, Detail: err.Error()}}
	rep.Verdict = Fail
	return rep
}

func newReport(in Input) Report {
	pr := in.PullRequest
	return Report{
		Repo:    in.Repo,
		PR:      pr.Number,
		HeadSHA: pr.HeadSHA,
		Closed:  pr.State != "open" || pr.Merged,
		Notes:   []string{},
	}
}

var titles = map[string]string{
	"G0":  "設定が正しい",
	"C1":  "PR が開いていて draft ではなく、ベースが既定のブランチで、止めるラベルがない",
	"C2":  "Issue をちょうど 1 つ閉じ、AC がすべて転記・チェック済み",
	"C3":  "「テスト」「ドキュメント」のチェックボックスがすべてチェック済み",
	"C4":  "必須のチェックが HEAD で成功し、失敗したチェックがない",
	"C5":  "信頼する作者のレビュー記録がある",
	"C6":  "最新のレビュー記録が HEAD に対する approve",
	"C7":  "重大な指摘が残っていない",
	"C8":  "レビュー役が codex か、正しいフォールバック",
	"C9":  "コンフリクトがない",
	"C10": "ベースより遅れていない",
	"C11": "変更ファイルが Issue の範囲に収まる",
	"C12": "保護されたパスの変更が確認済みで、ガードを弱めない",
}

type record struct {
	rec     *review.Record
	comment Comment
	created time.Time
	posted  time.Time
}

type evaluator struct {
	cfg     *config.Config
	in      Input
	rep     Report
	records []record
	latest  *record
	issue   *issuebody.Doc
}

func (e *evaluator) note(format string, a ...any) {
	e.rep.Notes = append(e.rep.Notes, fmt.Sprintf(format, a...))
}

func (e *evaluator) add(id string, v Verdict, format string, a ...any) {
	e.rep.Results = append(e.rep.Results, Result{ID: id, Title: titles[id], Status: v, Detail: fmt.Sprintf(format, a...)})
}

func (e *evaluator) run() Report {
	e.rep = newReport(e.in)
	if e.in.Issue != nil {
		e.issue = issuebody.Parse(e.in.Issue.Body)
	}
	e.collectReviews()

	e.add("G0", Pass, "OK")
	e.c1()
	e.c2()
	e.c3()
	e.c4()
	e.c5()
	e.c6()
	e.c7()
	e.c8()
	e.c9()
	e.c10()
	e.c11()
	e.c12()

	e.rep.Verdict = Pass
	for _, r := range e.rep.Results {
		if r.Status == Fail {
			e.rep.Verdict = Fail
			break
		}
		if r.Status == Pending {
			e.rep.Verdict = Pending
		}
	}
	return e.rep
}

// collectReviews は、信頼する作者の正しいレビュー記録を集め、古い順に並べる。
// 並びは round、記録の created_at、コメントの作成日時、コメントの ID の順で比べる。
func (e *evaluator) collectReviews() {
	pr := e.in.PullRequest.Number
	for _, c := range e.in.Comments {
		if !strings.Contains(c.Body, review.Marker) {
			continue
		}
		if !e.cfg.IsTrustedAuthor(c.Author) {
			e.note("コメント %d: 信頼しない作者 %q のレビュー記録を無視した", c.ID, c.Author)
			continue
		}
		rec, err := review.FromComment(c.Body)
		if err != nil {
			e.note("コメント %d: レビュー記録を無視した（%v）", c.ID, err)
			continue
		}
		if rec.PR != pr {
			e.note("コメント %d: 別の PR（#%d）のレビュー記録を無視した", c.ID, rec.PR)
			continue
		}
		posted, _ := time.Parse(time.RFC3339, c.CreatedAt)
		e.records = append(e.records, record{rec: rec, comment: c, created: rec.CreatedTime(), posted: posted})
	}
	sort.SliceStable(e.records, func(i, j int) bool {
		a, b := e.records[i], e.records[j]
		if a.rec.Round != b.rec.Round {
			return a.rec.Round < b.rec.Round
		}
		if !a.created.Equal(b.created) {
			return a.created.Before(b.created)
		}
		if !a.posted.Equal(b.posted) {
			return a.posted.Before(b.posted)
		}
		return a.comment.ID < b.comment.ID
	})
	if n := len(e.records); n > 0 {
		e.latest = &e.records[n-1]
		l := e.latest
		e.rep.LatestReview = &ReviewRef{
			CommentID: l.comment.ID, Author: l.comment.Author, Round: l.rec.Round,
			HeadSHA: l.rec.HeadSHA, Verdict: l.rec.Verdict, Reviewer: l.rec.Reviewer,
		}
	}
}

func (e *evaluator) c1() {
	pr := e.in.PullRequest
	var errs []string
	if pr.Merged {
		errs = append(errs, "マージ済み")
	} else if pr.State != "open" {
		errs = append(errs, fmt.Sprintf("開いていない（%s）", pr.State))
	}
	if pr.Draft {
		errs = append(errs, "draft")
	}
	if pr.BaseRef != e.cfg.DefaultBranch {
		errs = append(errs, fmt.Sprintf("ベースが %q（%q にする）", pr.BaseRef, e.cfg.DefaultBranch))
	}
	if !sha40Re.MatchString(pr.HeadSHA) {
		errs = append(errs, fmt.Sprintf("HEAD の SHA が不正（%q）", pr.HeadSHA))
	}
	for _, l := range pr.Labels {
		for _, b := range BlockingLabels {
			if strings.EqualFold(l, b) {
				errs = append(errs, fmt.Sprintf("ラベル %q", l))
			}
		}
	}
	if len(errs) > 0 {
		e.add("C1", Fail, "%s", strings.Join(errs, "・"))
		return
	}
	e.add("C1", Pass, "OK")
}

func (e *evaluator) c2() {
	pr := prbody.Parse(e.in.PullRequest.Body)
	refs := pr.ClosingIssues()
	if len(refs) != 1 {
		e.add("C2", Fail, "PR の本文の Closes/Fixes/Resolves #N が %d 個（ちょうど 1 つにする）", len(refs))
		return
	}
	iss := e.in.Issue
	switch {
	case iss == nil:
		e.add("C2", Fail, "Issue #%d が見つからない", refs[0])
		return
	case iss.Number != refs[0]:
		e.add("C2", Fail, "リンクした Issue が #%d ではなく #%d", refs[0], iss.Number)
		return
	case iss.IsPullRequest:
		e.add("C2", Fail, "#%d は Issue ではなく PR", iss.Number)
		return
	case iss.State != "open":
		e.add("C2", Fail, "Issue #%d が開いていない（%s）", iss.Number, iss.State)
		return
	}
	want := len(e.issue.AcceptanceCriteria())
	if want == 0 {
		e.add("C2", Fail, "Issue #%d に AC のチェックボックスがない", iss.Number)
		return
	}
	got := pr.AcceptanceCriteria()
	if len(got) != want {
		e.add("C2", Fail, "PR の AC が %d 件（Issue は %d 件）", len(got), want)
		return
	}
	if n := unchecked(got); n > 0 {
		e.add("C2", Fail, "PR の AC のうち %d 件がチェックされていない", n)
		return
	}
	e.add("C2", Pass, "Issue #%d、AC %d 件", iss.Number, want)
}

func unchecked(cbs []issuebody.Checkbox) int {
	n := 0
	for _, c := range cbs {
		if !c.Checked {
			n++
		}
	}
	return n
}

func (e *evaluator) c3() {
	pr := prbody.Parse(e.in.PullRequest.Body)
	var errs []string
	for _, sec := range []string{prbody.SectionTests, prbody.SectionDocs} {
		cbs := pr.Checkboxes(sec)
		if len(cbs) == 0 {
			errs = append(errs, fmt.Sprintf("「%s」にチェックボックスがない", sec))
		} else if n := unchecked(cbs); n > 0 {
			errs = append(errs, fmt.Sprintf("「%s」の %d 件がチェックされていない", sec, n))
		}
	}
	if len(errs) > 0 {
		e.add("C3", Fail, "%s", strings.Join(errs, "・"))
		return
	}
	e.add("C3", Pass, "OK")
}

func (e *evaluator) c4() {
	head := e.in.PullRequest.HeadSHA
	runs := make([]CheckRun, 0, len(e.in.CheckRuns))
	for _, cr := range e.in.CheckRuns {
		if cr.HeadSHA != "" && cr.HeadSHA != head {
			continue
		}
		runs = append(runs, cr)
	}
	required := e.cfg.RequiredChecks
	var fails, pendings []string

	for _, name := range required {
		found, anyFail, anyPending, anySuccess := false, false, false, false
		for _, cr := range runs {
			if cr.Name != name {
				continue
			}
			found = true
			switch {
			case cr.Status != "completed":
				anyPending = true
			case cr.Conclusion == "success":
				anySuccess = true
			default:
				anyFail = true
			}
		}
		for _, st := range e.in.Statuses {
			if st.Context != name {
				continue
			}
			found = true
			switch st.State {
			case "success":
				anySuccess = true
			case "pending":
				anyPending = true
			default:
				anyFail = true
			}
		}
		switch {
		case !found:
			pendings = append(pendings, fmt.Sprintf("%s は未実行", name))
		case anyFail:
			fails = append(fails, fmt.Sprintf("必須の %s が成功していない", name))
		case anyPending:
			pendings = append(pendings, fmt.Sprintf("%s は実行中", name))
		case !anySuccess:
			pendings = append(pendings, fmt.Sprintf("%s は未実行", name))
		}
	}

	skip := func(name string) bool {
		return contains(required, name) || contains(SelfCheckNames, name)
	}
	for _, cr := range runs {
		if skip(cr.Name) || cr.Status != "completed" {
			continue
		}
		if contains(failingConclusions, cr.Conclusion) {
			fails = append(fails, fmt.Sprintf("check run %s が %s", cr.Name, cr.Conclusion))
		}
	}
	for _, st := range e.in.Statuses {
		if skip(st.Context) {
			continue
		}
		if st.State == "failure" || st.State == "error" {
			fails = append(fails, fmt.Sprintf("ステータス %s が %s", st.Context, st.State))
		}
	}

	switch {
	case len(fails) > 0:
		e.add("C4", Fail, "%s", strings.Join(append(fails, pendings...), "・"))
	case len(pendings) > 0:
		e.add("C4", Pending, "%s", strings.Join(pendings, "・"))
	default:
		e.add("C4", Pass, "%s が成功（%s）", strings.Join(required, ", "), short(head))
	}
}

func (e *evaluator) c5() {
	if len(e.records) == 0 {
		e.add("C5", Pending, "信頼する作者（%s）のレビュー記録がない", strings.Join(e.cfg.Review.TrustedAuthors, ", "))
		return
	}
	e.add("C5", Pass, "%d 件", len(e.records))
}

const noReview = "レビュー記録がない"

func (e *evaluator) c6() {
	l := e.latest
	if l == nil {
		e.add("C6", Pending, noReview)
		return
	}
	if l.rec.HeadSHA != e.in.PullRequest.HeadSHA {
		e.add("C6", Pending, "最新の記録（round %d）は古い SHA %s のもの。HEAD %s の再レビュー待ち",
			l.rec.Round, short(l.rec.HeadSHA), short(e.in.PullRequest.HeadSHA))
		return
	}
	if l.rec.Verdict != "approve" {
		e.add("C6", Fail, "最新の記録（round %d）の verdict が %s", l.rec.Round, l.rec.Verdict)
		return
	}
	e.add("C6", Pass, "round %d が %s を approve", l.rec.Round, short(l.rec.HeadSHA))
}

func (e *evaluator) c7() {
	l := e.latest
	if l == nil {
		e.add("C7", Pending, noReview)
		return
	}
	var errs []string
	inLatest := map[string]review.Finding{}
	for _, f := range l.rec.Findings {
		inLatest[f.ID] = f
		if e.cfg.IsBlocking(f.Severity) && f.Status == "open" {
			errs = append(errs, fmt.Sprintf("%s（%s）が open", f.ID, f.Severity))
		}
	}
	reported := map[string]bool{}
	for _, r := range e.records[:len(e.records)-1] {
		for _, f := range r.rec.Findings {
			if !e.cfg.IsBlocking(f.Severity) || reported[f.ID] {
				continue
			}
			cur, ok := inLatest[f.ID]
			switch {
			case !ok:
				errs = append(errs, fmt.Sprintf("round %d の %s（%s）が最新の記録にない", r.rec.Round, f.ID, f.Severity))
				reported[f.ID] = true
			case cur.Status != "resolved":
				errs = append(errs, fmt.Sprintf("round %d の %s（%s）が resolved ではない（%s）", r.rec.Round, f.ID, f.Severity, cur.Status))
				reported[f.ID] = true
			}
		}
	}
	if len(errs) > 0 {
		e.add("C7", Fail, "%s", strings.Join(errs, "・"))
		return
	}
	e.add("C7", Pass, "OK")
}

func (e *evaluator) c8() {
	l := e.latest
	if l == nil {
		e.add("C8", Pending, noReview)
		return
	}
	if err := l.rec.CheckReviewer(e.cfg); err != nil {
		e.add("C8", Fail, "%v", err)
		return
	}
	d := l.rec.Reviewer
	if l.rec.FallbackReason != "" {
		d += "（" + l.rec.FallbackReason + "）"
	}
	e.add("C8", Pass, "%s", d)
}

func (e *evaluator) c9() {
	pr := e.in.PullRequest
	switch {
	case pr.Mergeable != nil && !*pr.Mergeable:
		e.add("C9", Fail, "コンフリクトがある（mergeable=false）")
	case pr.MergeableState == "dirty":
		e.add("C9", Fail, "コンフリクトがある（mergeable_state=dirty）")
	case pr.Mergeable == nil:
		e.add("C9", Pending, "mergeable が未計算")
	default:
		e.add("C9", Pass, "OK")
	}
}

func (e *evaluator) c10() {
	c := e.in.Compare
	switch {
	case c == nil:
		e.add("C10", Fail, "ベースとの比較の結果がない")
	case c.BehindBy > 0:
		e.add("C10", Fail, "%s より %d コミット遅れている（最新の %s を取り込む）", e.cfg.DefaultBranch, c.BehindBy, e.cfg.DefaultBranch)
	case c.BehindBy < 0:
		e.add("C10", Fail, "behind_by が不正（%d）", c.BehindBy)
	default:
		e.add("C10", Pass, "OK")
	}
}

// paths は変更ファイルのパス（名前を変えたファイルは、前の名前も含む）。
func (e *evaluator) paths() []string {
	var out []string
	for _, f := range e.in.Files {
		out = append(out, f.Filename)
		if f.PreviousFilename != "" && f.PreviousFilename != f.Filename {
			out = append(out, f.PreviousFilename)
		}
	}
	return out
}

func (e *evaluator) c11() {
	if e.in.Issue == nil {
		e.add("C11", Fail, "Issue がない")
		return
	}
	if e.in.PullRequest.ChangedFiles != len(e.in.Files) {
		e.add("C11", Fail, "変更ファイルの一覧が不完全（%d 件中 %d 件）", e.in.PullRequest.ChangedFiles, len(e.in.Files))
		return
	}
	planned := e.issue.PlannedFiles()
	forbidden := e.issue.ForbiddenFiles()
	if len(planned) == 0 {
		e.add("C11", Fail, "Issue に「%s」がない", issuebody.SectionPlannedFiles)
		return
	}
	for _, p := range append(append([]string{}, planned...), forbidden...) {
		if err := issuebody.ValidatePattern(p); err != nil {
			e.add("C11", Fail, "Issue の glob %q が不正: %v", p, err)
			return
		}
	}
	var outside, inForbidden []string
	for _, p := range e.paths() {
		if ok, _ := issuebody.MatchAny(planned, p); !ok {
			outside = append(outside, p)
		}
		if ok, _ := issuebody.MatchAny(forbidden, p); ok {
			inForbidden = append(inForbidden, p)
		}
	}
	var errs []string
	if len(outside) > 0 {
		errs = append(errs, "変更予定ファイルの外: "+strings.Join(outside, ", "))
	}
	if len(inForbidden) > 0 {
		errs = append(errs, "変更禁止範囲: "+strings.Join(inForbidden, ", "))
	}
	if len(errs) > 0 {
		e.add("C11", Fail, "%s", strings.Join(errs, "・"))
		return
	}
	e.add("C11", Pass, "%d ファイル", len(e.in.Files))
}

func (e *evaluator) c12() {
	var hit []string
	for _, p := range e.paths() {
		if ok, _ := issuebody.MatchAny(e.cfg.ProtectedPaths, p); ok {
			hit = append(hit, p)
		}
	}
	if len(hit) == 0 {
		e.add("C12", Pass, "保護されたパスの変更なし")
		return
	}
	l := e.latest
	if l == nil {
		e.add("C12", Pending, "保護されたパスの変更（%s）があり、%s", strings.Join(hit, ", "), noReview)
		return
	}
	a := l.rec.ProtectedPathsAck
	if a == nil || a.Reviewed == nil || a.WeakensGuards == nil || !*a.Reviewed || *a.WeakensGuards {
		e.add("C12", Fail, "保護されたパスの変更（%s）を、最新の記録が「確認した・ガードを弱めない」としていない", strings.Join(hit, ", "))
		return
	}
	e.add("C12", Pass, "保護されたパスの変更（%s）は確認済み", strings.Join(hit, ", "))
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
