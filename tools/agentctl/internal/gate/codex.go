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
	// Carried は、前の SHA の指摘を、最新の要約の has_unresolved_findings が true なので open のまま引き継いだこと。
	// 引き継ぐのはふつう重大な指摘（P0・P1）だけ。最新のレビューが重大な指摘なしと答えたとき（Issue #213）は、
	// 重大な指摘を解消（CodexReview.Resolved）とし、重大でない指摘（P2・P3）を引き継ぐ。
	Carried bool `json:"carried,omitempty"`
	// Resolution は、前の SHA の指摘を解消とみなした根拠（CodexReview.Resolved のものだけ）。
	Resolution string `json:"resolution,omitempty"`
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
	// AnyFindings は、最新の要約の code_review.has_any_findings。
	AnyFindings bool
	// Findings は、対象の SHA の指摘と、Unresolved のときに前の SHA から引き継いだ指摘（Carried）。
	Findings []CodexFinding
	// Resolved は、Unresolved でも、最新のレビューが重大な指摘なしと答えたので解消とみなした、前の SHA の重大な指摘
	// （根拠は Resolution。Issue #213）。
	Resolved []CodexFinding
	// codexIDs は、信頼する Codex の作者の指摘の行コメントの ID（C<コメントの ID>）の集合（CodexFindingIDs）。
	codexIDs map[string]bool
}

// IsCodexFinding は、id の指摘が Codex の GitHub 連携のもの（信頼する Codex の作者の指摘の行コメントに対応する）かどうかを返す。
func (c *CodexReview) IsCodexFinding(id string) bool { return c.codexIDs[id] }

// CodexFindingIDs は、信頼する Codex の作者（review.codex_bot_authors）の指摘の行コメント
// （codex-code-review-finding:v1）の ID を、変換したときの ID（C<コメントの ID>）の集合で返す。
// 指摘が Codex のものかどうかは、ID の形ではなく、この集合（出所が確かめられるもの）で判断する。
// 別のレビュー役の記録が C<数字> の形の ID を付けても、対応する行コメントがなければ Codex の指摘として扱わない。
func CodexFindingIDs(cfg *config.Config, in Input) map[string]bool {
	ids := map[string]bool{}
	for _, rc := range in.ReviewComments {
		if codex.IsFinding(rc.Body) && cfg.IsCodexBot(rc.Author) {
			ids[fmt.Sprintf("C%d", rc.ID)] = true
		}
	}
	return ids
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

// codexClearance は、最新のレビューが重大な指摘なしと答えたこと（前の SHA の重大な指摘を解消とみなす根拠）。
type codexClearance struct {
	receiptID int64
}

// resolution は、前の SHA の指摘 f を解消とみなした根拠（レビュー記録の resolution）を返す。
func (cl *codexClearance) resolution(best *CodexReview, f CodexFinding) string {
	return fmt.Sprintf("Codex（GitHub 連携）の %s のレビューが「%s」（コメント %d）と答え、%s に P0・P1 の指摘を出し直していないので、%s の指摘を解消とみなした",
		short(best.SHA), codex.NoMajorIssuesPhrase, cl.receiptID, short(best.SHA), short(f.SHA))
}

// codexNoMajorIssues は、最新のレビュー（best の SHA）が重大な指摘なしと答えたかを確かめる（Issue #213）。
// 次のすべてを満たすときだけ根拠を返し、満たさないときは nil と理由を返す（解消とみなさない。安全側）:
//   - 信頼する Codex の作者の receipt（コメントとレビュー）のうち、最も新しいものが Issue のコメントで、
//     「Didn't find any major issues」の文言を持ち、Reviewed commit が best の SHA を指す
//   - best の SHA に、信頼する Codex の作者の P0・P1（バッジのないものを含む）の行コメントがない（同じ指摘を出し直していない）
//
// 作成日時が分からない receipt や、最も新しい receipt が同じ日時で複数あるときも、判断しない。
func codexNoMajorIssues(cfg *config.Config, in Input, best *CodexReview) (*codexClearance, string) {
	for _, rc := range in.ReviewComments {
		if !codex.IsFinding(rc.Body) || !cfg.IsCodexBot(rc.Author) || !strings.EqualFold(rc.ReviewedSHA(), best.SHA) {
			continue
		}
		if sev, ok := codex.Severity(rc.Body); !ok || cfg.IsBlocking(sev) {
			return nil, fmt.Sprintf("%s に Codex の重大な指摘（行コメント %d）がある", short(best.SHA), rc.ID)
		}
	}
	type receipt struct {
		id      int64
		body    string
		at      time.Time
		comment bool
	}
	var rs []receipt
	for _, c := range in.Comments {
		if codex.IsReceipt(c.Body) && cfg.IsCodexBot(c.Author) {
			rs = append(rs, receipt{id: c.ID, body: c.Body, at: parseTime(c.CreatedAt), comment: true})
		}
	}
	for _, r := range in.Reviews {
		if codex.IsReceipt(r.Body) && cfg.IsCodexBot(r.Author) {
			rs = append(rs, receipt{id: r.ID, body: r.Body, at: parseTime(r.SubmittedAt)})
		}
	}
	if len(rs) == 0 {
		return nil, "Codex の receipt がない"
	}
	var newest *receipt
	tie := false
	for i := range rs {
		r := &rs[i]
		if r.at.IsZero() {
			return nil, fmt.Sprintf("Codex の receipt %d の作成日時が分からない", r.id)
		}
		switch {
		case newest == nil || r.at.After(newest.at):
			newest, tie = r, false
		case r.at.Equal(newest.at):
			tie = true
		}
	}
	if tie {
		return nil, "最も新しい Codex の receipt が同じ日時で複数ある"
	}
	if !newest.comment || !codex.NoMajorIssues(newest.body) {
		return nil, fmt.Sprintf("最も新しい Codex の receipt %d が「%s」ではない", newest.id, codex.NoMajorIssuesPhrase)
	}
	if sha := codex.ReviewedCommit(newest.body); sha == "" || !strings.HasPrefix(best.SHA, sha) {
		return nil, fmt.Sprintf("最も新しい Codex の receipt %d の Reviewed commit が %s ではない", newest.id, short(best.SHA))
	}
	return &codexClearance{receiptID: newest.id}, ""
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
//   - ただし true でも、最新のレビューが「Didn't find any major issues」（重大な指摘なし）と答え、その SHA に
//     P0・P1 の指摘を出し直していなければ（codexNoMajorIssues）、前の SHA の重大な指摘は解消（Resolved。根拠を
//     Resolution に書く）とし、重大でない指摘（P2・P3。has_unresolved_findings が残っている理由）を open のまま引き継ぐ。
//     そう判断できないとき（receipt がない・文言が違う・SHA が違う・同じ SHA に P0・P1 がある）は、上のとおり引き継ぐ（Issue #213）
//
// 同じ SHA の再レビューで、最新の要約の has_any_findings と has_unresolved_findings がどちらも false なら、
// 要約の更新より前に作られたその SHA の行コメントは前のレビューのもの（今回のレビューは指摘を出していない）なので、
// resolved とみなす（Codex が取り下げた指摘）。
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
		cand := &CodexReview{Comment: c, SHA: st.CodeReview.CommitSHA, Updated: updated,
			Unresolved: st.CodeReview.HasUnresolvedFindings, AnyFindings: st.CodeReview.HasAnyFindings}
		if best == nil || cand.Updated.After(best.Updated) || (cand.Updated.Equal(best.Updated) && cand.Comment.ID > best.Comment.ID) {
			best = cand
		}
	}
	if best == nil {
		return nil, notes
	}
	best.codexIDs = CodexFindingIDs(cfg, in)

	var clearance *codexClearance
	unclearReason := ""
	if best.Unresolved {
		clearance, unclearReason = codexNoMajorIssues(cfg, in, best)
	}
	notedUnclear := false

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
		if created := parseTime(rc.CreatedAt); !carried && !best.Unresolved && !best.AnyFindings &&
			!created.IsZero() && !created.After(best.Updated) {
			// 同じ SHA の再レビューの要約が指摘なしと言っているので、要約の更新より前の行コメントは前のレビューのもの（取り下げた指摘）。
			// 要約の更新より後の行コメントと、作成日時が分からない行コメントは、要約が数えていないかもしれないので解消とみなさない。
			note("行コメント %d: 同じ SHA %s の再レビューの要約が指摘なしなので、前のレビューの Codex の指摘を解消とみなした", rc.ID, short(sha))
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
		if f.Title == "" {
			f.Title = "（本文なし）"
		}
		if f.Line < 0 {
			f.Line = 0
		}
		if carried {
			blocking := cfg.IsBlocking(sev)
			if !blocking && clearance == nil {
				continue
			}
			if resolvedLater == nil {
				resolvedLater = nonCodexResolver(cfg, in)
			}
			if resolvedLater(f.ID(), parseTime(rc.CreatedAt)) {
				note("行コメント %d: 前の SHA %s の Codex の指摘は、後の記録で resolved になっている", rc.ID, short(sha))
				continue
			}
			if blocking && clearance != nil {
				f.Resolution = clearance.resolution(best, f)
				best.Resolved = append(best.Resolved, f)
				note("行コメント %d: 前の SHA %s の Codex の重大な指摘は、%s のレビューが重大な指摘なし（receipt %d）なので解消とみなした",
					rc.ID, short(sha), short(best.SHA), clearance.receiptID)
				continue
			}
			if blocking && !notedUnclear {
				notedUnclear = true
				note("前の SHA の Codex の重大な指摘を解消とみなさなかった（%s）", unclearReason)
			}
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
//     Codex の指摘（FindCodexReview を参照）も open で含める。前の SHA の重大な指摘を、最新のレビューが重大な指摘なしと
//     答えたので解消とみなしたもの（Resolved）は、根拠を resolution に書いて resolved で含める
//   - prev（前回の記録）にあって今回の指摘にない open の指摘は引き継ぐ。Codex の指摘（信頼する Codex の作者の
//     行コメントに対応する C<コメントの ID>。IsCodexFinding）と重大でない指摘は、今回の SHA の Codex のレビューで
//     指摘されなかったものとして resolved にする。Codex 以外の重大な指摘（C<数字> の形でも、対応する行コメントが
//     ないもの）は、Codex のレビューでは resolved にできないので open のまま（C7 で fail になる）
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
	// own・ownBlocking は対象の SHA の指摘、carriedBlocking・carriedOther は前の SHA から open のまま引き継いだ指摘、
	// cleared は前の SHA の重大な指摘を解消とみなしたものの数。
	own, ownBlocking, carriedBlocking, carriedOther, cleared := 0, 0, 0, 0, 0
	for _, f := range c.Findings {
		id := f.ID()
		if seen[id] {
			continue
		}
		seen[id] = true
		isBlocking := cfg.IsBlocking(f.Severity)
		if isBlocking {
			blocking++
		}
		title := f.Title
		if f.Priority != "" {
			title = f.Priority + ": " + title
		}
		switch {
		case f.Carried:
			title += "（" + short(f.SHA) + " の指摘を引き継いだ）"
			if isBlocking {
				carriedBlocking++
			} else {
				carriedOther++
			}
		default:
			own++
			if isBlocking {
				ownBlocking++
			}
		}
		findings = append(findings, review.Finding{
			ID: id, Severity: f.Severity, Category: CodexCategory, File: f.Path, Line: f.Line,
			Title: title, Detail: codex.Detail(f.Body, codexDetailMax), Status: "open",
		})
	}
	for _, f := range c.Resolved {
		id := f.ID()
		if seen[id] {
			continue
		}
		seen[id] = true
		cleared++
		title := f.Title
		if f.Priority != "" {
			title = f.Priority + ": " + title
		}
		findings = append(findings, review.Finding{
			ID: id, Severity: f.Severity, Category: CodexCategory, File: f.Path, Line: f.Line,
			Title: title + "（" + short(f.SHA) + " の指摘）", Detail: codex.Detail(f.Body, codexDetailMax),
			Status: "resolved", Resolution: f.Resolution,
		})
	}
	if prev != nil {
		for _, f := range prev.Findings {
			if seen[f.ID] {
				continue
			}
			seen[f.ID] = true
			if f.Status == "open" {
				if cfg.IsBlocking(f.Severity) && !c.IsCodexFinding(f.ID) {
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
	summary := fmt.Sprintf("Codex（GitHub 連携）が %s をレビューした。指摘 %d 件（うち重大 %d 件）。",
		short(c.SHA), own, ownBlocking)
	if carriedBlocking > 0 {
		summary += fmt.Sprintf("要約に未解決の指摘があるので、前の SHA の Codex の重大な指摘 %d 件を open のまま引き継いだ。", carriedBlocking)
	}
	if cleared > 0 {
		summary += fmt.Sprintf("%s のレビューが重大な指摘なし（%s）と答えたので、前の SHA の Codex の重大な指摘 %d 件を解消とみなした（根拠は resolution）。",
			short(c.SHA), codex.NoMajorIssuesPhrase, cleared)
	}
	if carriedOther > 0 {
		summary += fmt.Sprintf("要約に未解決の指摘があるので、前の SHA の Codex の重大でない指摘 %d 件を open のまま引き継いだ。", carriedOther)
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
