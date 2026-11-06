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
	Updated  time.Time
	Findings []CodexFinding
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
		cand := &CodexReview{Comment: c, SHA: st.CodeReview.CommitSHA, Updated: updated}
		if best == nil || cand.Updated.After(best.Updated) || (cand.Updated.Equal(best.Updated) && cand.Comment.ID > best.Comment.ID) {
			best = cand
		}
	}
	if best == nil {
		return nil, notes
	}

	for _, rc := range in.ReviewComments {
		if !codex.IsFinding(rc.Body) {
			continue
		}
		if !cfg.IsCodexBot(rc.Author) {
			note("行コメント %d: 信頼しない作者 %q の Codex の指摘を無視した", rc.ID, rc.Author)
			continue
		}
		if !strings.EqualFold(rc.ReviewedSHA(), best.SHA) {
			continue
		}
		f := CodexFinding{
			CommentID: rc.ID, Priority: codex.Priority(rc.Body), Path: rc.Path, Line: rc.Line,
			Title: codex.Title(rc.Body), Body: rc.Body,
		}
		sev, ok := codex.Severity(rc.Body)
		if !ok {
			// 重大度が分からない指摘は、重大なものとして扱う（fail closed）。
			sev = "high"
			note("警告: 行コメント %d: Codex の指摘に重大度のバッジ（P0〜P3）がないので high とみなした", rc.ID)
		}
		f.Severity = sev
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
//   - findings は行コメントから（id は C<コメントの ID>、status は open）
//   - prev（前回の記録）にあって今回の指摘にない open の指摘は、今回の SHA の Codex のレビューで
//     指摘されなかったものとして resolved にして引き継ぐ（C7 の「前の round の重大な指摘」のため）
//   - verdict は、重大な指摘があれば request_changes、なければ approve
//   - Codex は保護されたパスの確認をしないので、protected_paths_ack は reviewed: false
func (c *CodexReview) Record(cfg *config.Config, pr int, baseSHA string, prev *review.Record, now time.Time) *review.Record {
	round := 1
	if prev != nil {
		round = prev.Round + 1
	}
	findings := []review.Finding{}
	seen := map[string]bool{}
	blocking := 0
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
				f.Status = "resolved"
				f.Resolution = fmt.Sprintf("Codex（GitHub 連携）の %s のレビューで指摘されなかった", short(c.SHA))
			}
			findings = append(findings, f)
		}
	}
	verdict := "approve"
	if blocking > 0 {
		verdict = "request_changes"
	}
	return &review.Record{
		Schema: review.Schema, PR: pr, HeadSHA: c.SHA, BaseSHA: baseSHA, Round: round,
		Reviewer: config.ReviewerPrimary, ReviewerVersion: codex.ReviewerVersion, FallbackReason: "",
		Verdict: verdict,
		Summary: fmt.Sprintf("Codex（GitHub 連携）が %s をレビューした。指摘 %d 件（うち重大 %d 件）。",
			short(c.SHA), len(c.Findings), blocking),
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
