package gate

import (
	"fmt"
	"strings"
	"time"

	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/codex"
	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/config"
	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/review"
)

// CodexFinding は、Codex の GitHub 連携の指摘（行コメント）。
type CodexFinding struct {
	CommentID int64  `json:"comment_id"`
	Priority  string `json:"priority"` // P0〜P3。バッジがなければ ""
	Severity  string `json:"severity"`
	Path      string `json:"path"`
	Line      int    `json:"line"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	// SHA は指摘の対象の SHA。
	SHA string `json:"sha"`
	// Carried は、前の SHA の重大な指摘を、最新の要約の has_unresolved_findings が true なので引き継いだこと。
	Carried bool `json:"carried,omitempty"`
}

// ID は agent-review/v1 に変換したときの指摘の ID（C<コメントの ID>）。
func (f CodexFinding) ID() string { return fmt.Sprintf("C%d", f.CommentID) }

// CodexReview は、Codex の GitHub 連携の完了したレビュー（要約のコメントと、その SHA の指摘）。
type CodexReview struct {
	// Comment は要約のコメント（codex-review-state を持つもの）。
	Comment Comment
	// SHA は code_review.commit_sha（レビューした SHA）。
	SHA string
	// Updated は要約のコメントの updated_at（なければ created_at）。
	Updated time.Time
	// Unresolved は、最新の要約の code_review.has_unresolved_findings。
	Unresolved bool
	// Findings は、対象の SHA の指摘と、Unresolved のときに前の SHA から引き継いだ重大な指摘（Carried）。
	Findings []CodexFinding
}

// CarriedCount は、前の SHA から引き継いだ指摘の数を返す。
func (c *CodexReview) CarriedCount() int {
	n := 0
	for _, f := range c.Findings {
		if f.Carried {
			n++
		}
	}
	return n
}

// Blocking は、重大な指摘（設定の blocking_severities）を返す。
func (c *CodexReview) Blocking(cfg *config.Config) []CodexFinding {
	var out []CodexFinding
	for _, f := range c.Findings {
		if cfg.IsBlocking(f.Severity) {
			out = append(out, f)
		}
	}
	return out
}

// FindCodexReview は、信頼する Codex の作者（review.codex_bot_authors）の要約のコメントのうち、
// code_review.status が completed で最も新しいものと、その SHA の行コメントの指摘を返す。なければ nil。
// 無視したもの（信頼しない作者・壊れた JSON・未完了）は notes に書く。
//
// 前の SHA の指摘の扱い（Codex は、後のレビューで前の指摘を行コメントとして出し直すとは限らない）:
//   - 最新の要約の has_unresolved_findings が false なら、前の SHA の Codex の指摘はすべて resolved とみなす
//   - true なら、前の SHA の Codex の重大な指摘（P0・P1。バッジがないものを含む）を open のまま引き継ぐ（Carried）。
//     ただし、その行コメントより後の agent-review/v1 の記録（Codex の GitHub 連携の変換ではないもの）が、
//     同じ ID（C<コメントの ID>）を最後に resolved にしていれば引き継がない
//   - 前の SHA の重大でない指摘（P2・P3）は引き継がない
func FindCodexReview(cfg *config.Config, in Input) (*CodexReview, []string) {
	var notes []string
	note := func(format string, a ...any) { notes = append(notes, fmt.Sprintf(format, a...)) }

	var best *CodexReview
	for _, c := range in.Comments {
		if !codex.HasState(c.Body) {
			continue
		}
		if !cfg.IsCodexBot(c.Author) {
			note("コメント %d: 信頼しない作者 %q の Codex のレビューの状態を無視した", c.ID, c.Author)
			continue
		}
		st, err := codex.ParseState(c.Body)
		if err != nil {
			note("警告: コメント %d: Codex のレビューの状態を無視した（%v）", c.ID, err)
			continue
		}
		if st.CodeReview.Status != codex.StatusCompleted {
			note("コメント %d: Codex のレビュー（%s）はまだ終わっていない（%s）", c.ID, short(st.CodeReview.CommitSHA), st.CodeReview.Status)
			continue
		}
		updated := parseTime(c.UpdatedAt)
		if updated.IsZero() {
			updated = parseTime(c.CreatedAt)
		}
		cand := &CodexReview{Comment: c, SHA: st.CodeReview.CommitSHA, Updated: updated, Unresolved: st.CodeReview.HasUnresolvedFindings}
		if best == nil || cand.Updated.After(best.Updated) || (cand.Updated.Equal(best.Updated) && cand.Comment.ID > best.Comment.ID) {
			best = cand
		}
	}
	if best == nil {
		return nil, notes
	}

	var resolvedLater func(id string, at time.Time) bool
	for _, rc := range in.ReviewComments {
		if !codex.IsFinding(rc.Body) {
			continue
		}
		if !cfg.IsCodexBot(rc.Author) {
			note("行コメント %d: 信頼しない作者 %q の Codex の指摘を無視した", rc.ID, rc.Author)
			continue
		}
		sha := rc.ReviewedSHA()
		carried := !strings.EqualFold(sha, best.SHA)
		if carried && !best.Unresolved {
			// 最新の要約が未解決の指摘なしと言っているので、前の SHA の指摘は resolved とみなす。
			continue
		}
		f := CodexFinding{
			CommentID: rc.ID, Priority: codex.Priority(rc.Body), Path: rc.Path, Line: rc.Line,
			Title: codex.Title(rc.Body), Body: rc.Body, SHA: sha, Carried: carried,
		}
		sev, ok := codex.Severity(rc.Body)
		if !ok {
			// 重大度が分からない指摘は、重大なものとして扱う（fail closed）。
			sev = "high"
			note("警告: 行コメント %d: Codex の指摘に重大度のバッジ（P0〜P3）がないので high とみなした", rc.ID)
		}
		f.Severity = sev
		if carried {
			if !cfg.IsBlocking(sev) {
				continue
			}
			if resolvedLater == nil {
				resolvedLater = nonCodexResolver(cfg, in)
			}
			if resolvedLater(f.ID(), parseTime(rc.CreatedAt)) {
				note("行コメント %d: 前の SHA %s の Codex の指摘は、後の記録で resolved になっている", rc.ID, short(sha))
				continue
			}
		}
		if f.Title == "" {
			f.Title = "（本文なし）"
		}
		if f.Line < 0 {
			f.Line = 0
		}
		best.Findings = append(best.Findings, f)
	}
	return best, notes
}

// nonCodexResolver は、id の指摘を、時刻 at より後の agent-review/v1 の記録（Codex の GitHub 連携の変換ではないもの）が
// 最後に resolved にしたかどうかを返す関数を返す。at が分からない（ゼロ）ときは、すべての記録を「後」とみなす。
func nonCodexResolver(cfg *config.Config, in Input) func(id string, at time.Time) bool {
	e := &evaluator{cfg: cfg, in: in}
	e.collectReviews()
	return func(id string, at time.Time) bool {
		status := ""
		for _, r := range e.records {
			if isCodexConverted(r.rec) || (!at.IsZero() && !r.created.After(at)) {
				continue
			}
			for _, f := range r.rec.Findings {
				if f.ID == id {
					status = f.Status
				}
			}
		}
		return status == "resolved"
	}
}

// LatestRecord は、信頼する作者の正しいレビュー記録（agent-review/v1）のうち、最新のものを返す（なければ nil）。
// 並び方は Merge Gate と同じ（round、created_at、コメントの作成日時、コメントの ID）。
func LatestRecord(cfg *config.Config, in Input) *review.Record {
	e := &evaluator{cfg: cfg, in: in}
	e.collectReviews()
	if e.latest == nil {
		return nil
	}
	return e.latest.rec
}

// CodexCategory は、変換した記録の指摘の category（Codex は分類を出さない）。
const CodexCategory = "correctness"

// codexDetailMax は、変換した記録の指摘の detail の最大の文字数。
const codexDetailMax = 2000

// Record は、Codex の GitHub 連携のレビューを agent-review/v1 の記録に変換する。
//
//   - reviewer は codex、reviewer_version は codex.ReviewerVersion
//   - findings は行コメントから（id は C<コメントの ID>、status は open）。前の SHA から引き継いだ
//     Codex の重大な指摘（FindCodexReview を参照）も open で含める
//   - prev（前回の記録）にあって今回の指摘にない open の指摘は引き継ぐ。Codex の指摘（C<コメントの ID>）と
//     重大でない指摘は、今回の SHA の Codex のレビューで指摘されなかったものとして resolved にする。
//     Codex 以外の重大な指摘は、Codex のレビューでは resolved にできないので open のまま（C7 で fail になる）
//   - verdict は、重大な指摘（引き継いだ open のものを含む）があれば request_changes、なければ approve
//   - Codex は保護されたパスの確認をしないので、protected_paths_ack は reviewed: false
func (c *CodexReview) Record(cfg *config.Config, pr int, baseSHA string, prev *review.Record, now time.Time) *review.Record {
	round := 1
	if prev != nil {
		round = prev.Round + 1
	}
	findings := []review.Finding{}
	seen := map[string]bool{}
	blocking, carried := 0, 0
	for _, f := range c.Findings {
		id := f.ID()
		if seen[id] {
			continue
		}
		seen[id] = true
		if cfg.IsBlocking(f.Severity) {
			blocking++
		}
		title := f.Title
		if f.Priority != "" {
			title = f.Priority + ": " + title
		}
		if f.Carried {
			title += "（" + short(f.SHA) + " の指摘を引き継いだ）"
		}
		findings = append(findings, review.Finding{
			ID: id, Severity: f.Severity, Category: CodexCategory, File: f.Path, Line: f.Line,
			Title: title, Detail: codex.Detail(f.Body, codexDetailMax), Status: "open",
		})
	}
	if prev != nil {
		for _, f := range prev.Findings {
			if seen[f.ID] {
				continue
			}
			seen[f.ID] = true
			if f.Status == "open" {
				if cfg.IsBlocking(f.Severity) && !IsCodexFindingID(f.ID) {
					// Codex 以外の重大な指摘は、Codex のレビューでは resolved にしない（open のまま引き継ぐ）。
					carried++
				} else {
					f.Status = "resolved"
					f.Resolution = fmt.Sprintf("Codex（GitHub 連携）の %s のレビューで指摘されなかった", short(c.SHA))
				}
			}
			findings = append(findings, f)
		}
	}
	verdict := "approve"
	if blocking > 0 || carried > 0 {
		verdict = "request_changes"
	}
	cc := c.CarriedCount()
	summary := fmt.Sprintf("Codex（GitHub 連携）が %s をレビューした。指摘 %d 件（うち重大 %d 件）。",
		short(c.SHA), len(c.Findings)-cc, blocking-cc)
	if cc > 0 {
		summary += fmt.Sprintf("要約に未解決の指摘があるので、前の SHA の Codex の重大な指摘 %d 件を open のまま引き継いだ。", cc)
	}
	if carried > 0 {
		summary += fmt.Sprintf("前のレビューの重大な指摘 %d 件は open のまま引き継いだ（Codex 以外のレビューで確かめる）。", carried)
	}
	return &review.Record{
		Schema: review.Schema, PR: pr, HeadSHA: c.SHA, BaseSHA: baseSHA, Round: round,
		Reviewer: config.ReviewerPrimary, ReviewerVersion: codex.ReviewerVersion, FallbackReason: "",
		Verdict:           verdict,
		Summary:           summary,
		ProtectedPathsAck: &review.Ack{Reviewed: boolPtr(false), WeakensGuards: boolPtr(false)},
		Findings:          findings,
		CreatedAt:         now.UTC().Format(time.RFC3339),
	}
}

func boolPtr(b bool) *bool { return &b }

func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}
