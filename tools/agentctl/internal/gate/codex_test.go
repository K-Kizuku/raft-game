package gate

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/review"
)

const (
	codexBot = "chatgpt-codex-connector[bot]"
	firstSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
)

type codexData struct {
	Comments       []Comment       `json:"comments"`
	Reviews        []PullReview    `json:"reviews"`
	ReviewComments []ReviewComment `json:"review_comments"`
}

// codexInput は、pass.json に Codex の出力（testdata/codex-*.json）を加えた入力を返す。
// keepRecords が false なら、pass.json の agent-review/v1 の記録を除く。
// 変更ファイルは保護されたパスを含まないものにする（Codex のレビューは保護されたパスを確認しないため）。
func codexInput(t *testing.T, name string, keepRecords bool) Input {
	t.Helper()
	in := loadInput(t)
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	var cd codexData
	if err := json.Unmarshal(data, &cd); err != nil {
		t.Fatal(err)
	}
	if !keepRecords {
		var cs []Comment
		for _, c := range in.Comments {
			if !strings.Contains(c.Body, review.Marker) {
				cs = append(cs, c)
			}
		}
		in.Comments = cs
	}
	in.Comments = append(in.Comments, cd.Comments...)
	in.Reviews = cd.Reviews
	in.ReviewComments = cd.ReviewComments
	in.Files = []File{{Filename: ".github/pull_request_template.md", PreviousFilename: ".github/PULL_REQUEST_TEMPLATE.md", Status: "renamed"}}
	in.PullRequest.ChangedFiles = 1
	return in
}

func codexFinding(id int64, author, sha, priority, title string) ReviewComment {
	body := "**<sub><sub>![" + priority + " Badge](https://img.shields.io/badge/" + priority + "-red?style=flat)</sub></sub>  " + title +
		"**\n\n詳しい説明。\n\nUseful? React with 👍 / 👎.\n\n<!-- codex-code-review-finding:v1 -->"
	if priority == "" {
		body = "**" + title + "**\n\n<!-- codex-code-review-finding:v1 -->"
	}
	return ReviewComment{ID: id, Author: author, CommitID: sha, OriginalCommitID: sha, Path: "tools/agentctl/main.go", Line: 7,
		Body: body, CreatedAt: "2026-11-05T10:30:00Z"}
}

// withCodexFindings は、Codex の要約（codex-review-state）を、最新のレビューに指摘があるもの
// （has_any_findings・has_unresolved_findings が true）にする。対象の SHA に行コメントの指摘を足すテストで使う
// （実際の Codex は、指摘を出したレビューの要約を has_any_findings: true にする）。
func withCodexFindings(in Input) Input {
	for i := range in.Comments {
		in.Comments[i].Body = strings.Replace(in.Comments[i].Body,
			`"has_any_findings":false,"has_unresolved_findings":false`, `"has_any_findings":true,"has_unresolved_findings":true`, 1)
	}
	return in
}

func mustResult(t *testing.T, rep Report, id string, want Verdict) Result {
	t.Helper()
	r, ok := resultOf(rep, id)
	if !ok {
		t.Fatalf("%s がない", id)
	}
	if r.Status != want {
		t.Errorf("%s = %s（%s）, want %s", id, r.Status, r.Detail, want)
	}
	return r
}

func notes(rep Report) string { return strings.Join(rep.Notes, "\n") }

// withoutComment は、ID が id のコメントを除いた入力を返す。
func withoutComment(in Input, id int64) Input {
	var cs []Comment
	for _, c := range in.Comments {
		if c.ID != id {
			cs = append(cs, c)
		}
	}
	in.Comments = cs
	return in
}

func TestCodexPR84NoFindingsPasses(t *testing.T) {
	rep := Evaluate(loadConfig(t), codexInput(t, "codex-pr84.json", false))
	if rep.Verdict != Pass {
		t.Fatalf("verdict = %s; %+v", rep.Verdict, rep.Results)
	}
	if l := rep.LatestReview; l == nil || l.CommentID != 2201 || l.Reviewer != "codex" || l.HeadSHA != headSHA || l.Verdict != "approve" {
		t.Errorf("latest = %+v", rep.LatestReview)
	}
	if r := mustResult(t, rep, "C8", Pass); !strings.Contains(r.Detail, "GitHub") {
		t.Errorf("C8 = %+v", r)
	}
}

func TestCodexPR80OldP2IgnoredAfterHeadChanged(t *testing.T) {
	// 古い HEAD（aaaa…）の P2 の指摘は、新しい HEAD のレビューの判定に使わない。
	rep := Evaluate(loadConfig(t), codexInput(t, "codex-pr80.json", true))
	if rep.Verdict != Pass {
		t.Fatalf("verdict = %s; %+v", rep.Verdict, rep.Results)
	}
	if rep.LatestReview == nil || rep.LatestReview.CommentID != 2101 {
		t.Errorf("latest = %+v（Codex の要約のコメントが agent-review/v1 の記録より新しい）", rep.LatestReview)
	}
	mustResult(t, rep, "C5", Pass)
}

func TestCodexOnlyP2Passes(t *testing.T) {
	in := withCodexFindings(codexInput(t, "codex-pr80.json", false))
	in.ReviewComments[0].CommitID, in.ReviewComments[0].OriginalCommitID = headSHA, headSHA
	in.ReviewComments = append(in.ReviewComments, codexFinding(4102, codexBot, headSHA, "P3", "名前"))
	rep := Evaluate(loadConfig(t), in)
	if rep.Verdict != Pass {
		t.Fatalf("verdict = %s; %+v", rep.Verdict, rep.Results)
	}
	if r := mustResult(t, rep, "C7", Pass); !strings.Contains(r.Detail, "2 件") {
		t.Errorf("C7 = %+v", r)
	}
}

func TestCodexP1Fails(t *testing.T) {
	for _, p := range []string{"P0", "P1"} {
		t.Run(p, func(t *testing.T) {
			in := withCodexFindings(codexInput(t, "codex-pr80.json", false))
			in.ReviewComments = append(in.ReviewComments, codexFinding(4200, codexBot, headSHA, p, "設定がないと panic する"))
			rep := Evaluate(loadConfig(t), in)
			if rep.Verdict != Fail {
				t.Fatalf("verdict = %s; %+v", rep.Verdict, rep.Results)
			}
			r := mustResult(t, rep, "C7", Fail)
			for _, want := range []string{"C4200", p, "設定がないと panic する", "tools/agentctl/main.go:7"} {
				if !strings.Contains(r.Detail, want) {
					t.Errorf("C7 の detail に %q がない: %s", want, r.Detail)
				}
			}
			if strings.Contains(r.Detail, "Badge") || strings.Contains(r.Detail, "<sub>") {
				t.Errorf("C7 の detail にバッジが残っている: %s", r.Detail)
			}
			mustResult(t, rep, "C6", Pass)
			if rep.LatestReview.Verdict != "request_changes" {
				t.Errorf("latest = %+v", rep.LatestReview)
			}
		})
	}
}

func TestCodexMissingBadgeIsBlocking(t *testing.T) {
	in := withCodexFindings(codexInput(t, "codex-pr84.json", false))
	in.ReviewComments = append(in.ReviewComments, codexFinding(4300, codexBot, headSHA, "", "バッジのない指摘"))
	rep := Evaluate(loadConfig(t), in)
	mustResult(t, rep, "C7", Fail)
	if !strings.Contains(notes(rep), "4300") {
		t.Errorf("notes = %v", rep.Notes)
	}
}

func TestCodexStaleSHAIsPending(t *testing.T) {
	// Codex の最新のレビューが古い HEAD のもの（HEAD が変わった後、まだ再レビューしていない）。
	in := codexInput(t, "codex-pr80.json", false)
	in.Comments[len(in.Comments)-3].Body = strings.Replace(in.Comments[len(in.Comments)-3].Body, headSHA, firstSHA, 1)
	rep := Evaluate(loadConfig(t), in)
	if rep.Verdict != Pending {
		t.Fatalf("verdict = %s; %+v", rep.Verdict, rep.Results)
	}
	mustResult(t, rep, "C6", Pending)
	mustResult(t, rep, "C7", Pending)
}

func TestCodexUntrustedAuthorIgnored(t *testing.T) {
	t.Run("要約のコメント", func(t *testing.T) {
		in := codexInput(t, "codex-pr84.json", false)
		for i := range in.Comments {
			if strings.Contains(in.Comments[i].Body, "codex-review-state") {
				in.Comments[i].Author = "chatgpt-codex-connector"
			}
		}
		rep := Evaluate(loadConfig(t), in)
		if rep.Verdict != Pending {
			t.Fatalf("verdict = %s; %+v", rep.Verdict, rep.Results)
		}
		mustResult(t, rep, "C5", Pending)
		if !strings.Contains(notes(rep), "信頼しない作者") {
			t.Errorf("notes = %v", rep.Notes)
		}
	})
	t.Run("行コメント", func(t *testing.T) {
		in := withCodexFindings(codexInput(t, "codex-pr84.json", false))
		in.ReviewComments = append(in.ReviewComments, codexFinding(4400, "someone-else", headSHA, "P0", "偽の指摘"))
		rep := Evaluate(loadConfig(t), in)
		if rep.Verdict != Pass {
			t.Fatalf("verdict = %s; %+v", rep.Verdict, rep.Results)
		}
		if !strings.Contains(notes(rep), "4400") {
			t.Errorf("notes = %v", rep.Notes)
		}
	})
}

func TestCodexNewerAgentRecordWins(t *testing.T) {
	// Codex の要約のコメントより新しい agent-review/v1 の記録があれば、そちらで判定する。
	in := withCodexFindings(codexInput(t, "codex-pr80.json", true))
	in.ReviewComments = append(in.ReviewComments, codexFinding(4500, codexBot, headSHA, "P1", "重大"))
	rec := makeRecord(3, headSHA, "approve", resolvedF1())
	rec.CreatedAt = "2026-11-06T00:00:00Z"
	in.Comments = append(in.Comments, commentOf(t, 5000, "K-Kizuku", rec))
	rep := Evaluate(loadConfig(t), in)
	if rep.Verdict != Pass {
		t.Fatalf("verdict = %s; %+v", rep.Verdict, rep.Results)
	}
	if rep.LatestReview == nil || rep.LatestReview.CommentID != 5000 {
		t.Errorf("latest = %+v", rep.LatestReview)
	}

	// 逆に、Codex の方が新しければ Codex の P1 で fail。
	rec.CreatedAt = "2026-11-05T10:00:00Z"
	in.Comments[len(in.Comments)-1] = commentOf(t, 5000, "K-Kizuku", rec)
	rep = Evaluate(loadConfig(t), in)
	if rep.Verdict != Fail || rep.LatestReview.CommentID != 2101 {
		t.Fatalf("verdict = %s, latest = %+v", rep.Verdict, rep.LatestReview)
	}
}

func TestCodexBrokenOrIncompleteStateIgnored(t *testing.T) {
	for name, repl := range map[string][2]string{
		"壊れた JSON":       {`{"code_review":{`, `{"code_review":{,`},
		"未完了":            {`"status":"completed"`, `"status":"in_progress"`},
		"SHA が不正":        {headSHA, "1111111"},
		"code_review なし": {`"code_review"`, `"codeReview"`},
	} {
		t.Run(name, func(t *testing.T) {
			in := codexInput(t, "codex-pr84.json", false)
			for i := range in.Comments {
				in.Comments[i].Body = strings.Replace(in.Comments[i].Body, repl[0], repl[1], 1)
			}
			rep := Evaluate(loadConfig(t), in)
			mustResult(t, rep, "C5", Pending)
			if !strings.Contains(notes(rep), "2201") {
				t.Errorf("notes = %v", rep.Notes)
			}
		})
	}
}

func TestCodexReceiptOnlyWaitsForSummary(t *testing.T) {
	// 指摘がないときの receipt のコメントだけでは、レビュー記録に数えない（要約を待つ）。
	in := codexInput(t, "codex-pr84.json", false)
	var cs []Comment
	for _, c := range in.Comments {
		if !strings.Contains(c.Body, "codex-review-state") {
			cs = append(cs, c)
		}
	}
	in.Comments = cs
	rep := Evaluate(loadConfig(t), in)
	mustResult(t, rep, "C5", Pending)
	mustResult(t, rep, "C6", Pending)
}

func TestCodexProtectedPathsNeedAck(t *testing.T) {
	in := codexInput(t, "codex-pr84.json", false)
	in.Files = append(in.Files, File{Filename: "tools/agentctl/main.go", Status: "modified"})
	in.PullRequest.ChangedFiles = len(in.Files)
	rep := Evaluate(loadConfig(t), in)
	if r := mustResult(t, rep, "C12", Fail); !strings.Contains(r.Detail, "Codex") {
		t.Errorf("C12 = %+v", r)
	}
}

func TestCodexNewestSummaryWins(t *testing.T) {
	// 要約のコメントが 2 つあれば、updated_at が新しい方を使う。
	in := codexInput(t, "codex-pr84.json", false)
	var old Comment
	for _, c := range in.Comments {
		if c.ID == 2201 {
			old = c
		}
	}
	old.ID = 2190
	old.UpdatedAt = "2026-11-04T00:00:00Z"
	old.Body = strings.Replace(old.Body, headSHA, firstSHA, 1)
	in.Comments = append(in.Comments, old)
	rep := Evaluate(loadConfig(t), in)
	if rep.Verdict != Pass || rep.LatestReview.CommentID != 2201 {
		t.Fatalf("verdict = %s, latest = %+v", rep.Verdict, rep.LatestReview)
	}
}

func TestCodexRecord(t *testing.T) {
	cfg := loadConfig(t)
	in := withCodexFindings(codexInput(t, "codex-pr80.json", true))
	in.ReviewComments = append(in.ReviewComments,
		codexFinding(4600, codexBot, headSHA, "P1", "重大な指摘"),
		codexFinding(4601, codexBot, headSHA, "P2", "軽い指摘"))
	cr, _ := FindCodexReview(cfg, in)
	if cr == nil || cr.SHA != headSHA || len(cr.Findings) != 2 {
		t.Fatalf("codex = %+v", cr)
	}
	prev := LatestRecord(cfg, in)
	if prev == nil || prev.Round != 2 {
		t.Fatalf("prev = %+v", prev)
	}
	// C4101 は Codex の行コメント（古い SHA の P2）に対応する。C4000 は C<数字> の形だが、対応する Codex の行コメントがない
	// （Codex 以外のレビュー役が付けた ID）。
	prev.Findings = append(prev.Findings, finding("F9", "high", "open"), finding("F8", "low", "open"),
		finding("C4101", "high", "open"), finding("C4000", "high", "open"))
	now := time.Date(2026, 11, 6, 1, 2, 3, 0, time.UTC)
	rec := cr.Record(cfg, 12, baseSHA, prev, now)
	if err := rec.Validate(cfg); err != nil {
		t.Fatal(err)
	}
	if rec.Reviewer != "codex" || rec.ReviewerVersion != "chatgpt-codex-connector (GitHub)" || rec.FallbackReason != "" ||
		rec.Round != 3 || rec.HeadSHA != headSHA || rec.BaseSHA != baseSHA || rec.CreatedAt != "2026-11-06T01:02:03Z" {
		t.Errorf("record = %+v", rec)
	}
	if rec.Verdict != "request_changes" || *rec.ProtectedPathsAck.Reviewed {
		t.Errorf("verdict = %s, ack = %+v", rec.Verdict, rec.ProtectedPathsAck)
	}
	byID := map[string]review.Finding{}
	for _, f := range rec.Findings {
		byID[f.ID] = f
	}
	if f := byID["C4600"]; f.Severity != "high" || f.Status != "open" || f.File != "tools/agentctl/main.go" || f.Line != 7 ||
		f.Title != "P1: 重大な指摘" || f.Detail != "詳しい説明。" {
		t.Errorf("C4600 = %+v", f)
	}
	if f := byID["C4601"]; f.Severity != "medium" {
		t.Errorf("C4601 = %+v", f)
	}
	// 前回の Codex 以外の重大な指摘は、Codex のレビューでは resolved にせず open のまま引き継ぐ。
	// ID が C<数字> の形でも、対応する Codex の行コメントがなければ Codex 以外の指摘として扱う。
	for _, id := range []string{"F9", "C4000"} {
		if f := byID[id]; f.Status != "open" || f.Resolution != "" {
			t.Errorf("%s = %+v", id, f)
		}
	}
	if !strings.Contains(rec.Summary, "open のまま引き継いだ") {
		t.Errorf("summary = %s", rec.Summary)
	}
	// 重大でない指摘と、前の Codex の指摘は、今回指摘されなければ resolved で引き継ぐ。resolved のものはそのまま。
	for _, id := range []string{"F8", "C4101"} {
		if f := byID[id]; f.Status != "resolved" || !strings.Contains(f.Resolution, "1111111") {
			t.Errorf("%s = %+v", id, f)
		}
	}
	if f := byID["F1"]; f.Status != "resolved" || f.Resolution != "内部エラーにした" {
		t.Errorf("F1 = %+v", f)
	}

	// 変換した記録を載せると、それが最新の記録になり、同じ判定（C7 で fail）になる。
	body, err := review.Render(rec)
	if err != nil {
		t.Fatal(err)
	}
	in.Comments = append(in.Comments, Comment{ID: 6000, Author: "K-Kizuku", Body: body, CreatedAt: rec.CreatedAt})
	rep := Evaluate(cfg, in)
	if rep.LatestReview.CommentID != 6000 {
		t.Errorf("latest = %+v", rep.LatestReview)
	}
	mustResult(t, rep, "C7", Fail)

	// 指摘がなければ approve。
	cr.Findings = nil
	if rec := cr.Record(cfg, 12, baseSHA, nil, now); rec.Verdict != "approve" || rec.Round != 1 || len(rec.Findings) != 0 {
		t.Errorf("record = %+v", rec)
	}
	// 指摘がなくても、前の重大な指摘を open のまま引き継いだら request_changes。
	if rec := cr.Record(cfg, 12, baseSHA, prev, now); rec.Verdict != "request_changes" {
		t.Errorf("record = %+v", rec)
	}
}

// codexAfterOpenHigh は、round 1 の記録（Claude のフォールバック）で F1（high）が open のあと、
// Codex の GitHub 連携が HEAD を指摘なしでレビューした入力を返す。
func codexAfterOpenHigh(t *testing.T) Input {
	t.Helper()
	in := codexInput(t, "codex-pr84.json", false)
	rec := makeRecord(1, oldSHA, "request_changes", []review.Finding{finding("F1", "high", "open")}, fallback("service_unavailable"))
	rec.CreatedAt = "2026-11-01T00:00:00Z"
	in.Comments = append(in.Comments, commentOf(t, 7000, "K-Kizuku", rec))
	return in
}

func TestCodexCannotResolvePriorBlocking(t *testing.T) {
	cfg := loadConfig(t)

	t.Run("前の high が open のまま Codex が指摘なし", func(t *testing.T) {
		rep := Evaluate(cfg, codexAfterOpenHigh(t))
		if rep.Verdict == Pass {
			t.Fatalf("verdict = %s; %+v", rep.Verdict, rep.Results)
		}
		if rep.LatestReview == nil || rep.LatestReview.CommentID != 2201 {
			t.Errorf("latest = %+v（Codex の要約のコメントが最新）", rep.LatestReview)
		}
		mustResult(t, rep, "C6", Pass)
		if r := mustResult(t, rep, "C7", Fail); !strings.Contains(r.Detail, "F1") {
			t.Errorf("C7 = %+v", r)
		}
	})

	t.Run("変換した記録を載せても resolved にならない", func(t *testing.T) {
		in := codexAfterOpenHigh(t)
		cr, _ := FindCodexReview(cfg, in)
		prev := LatestRecord(cfg, in)
		if cr == nil || prev == nil || prev.Round != 1 {
			t.Fatalf("codex = %+v, prev = %+v", cr, prev)
		}
		rec := cr.Record(cfg, 12, baseSHA, prev, time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC))
		if rec.Verdict != "request_changes" {
			t.Errorf("verdict = %s", rec.Verdict)
		}
		in.Comments = append(in.Comments, commentOf(t, 7001, "K-Kizuku", rec))
		rep := Evaluate(cfg, in)
		if rep.LatestReview == nil || rep.LatestReview.CommentID != 7001 {
			t.Errorf("latest = %+v", rep.LatestReview)
		}
		if r := mustResult(t, rep, "C7", Fail); !strings.Contains(r.Detail, "F1") {
			t.Errorf("C7 = %+v", r)
		}

		// 変換した記録で F1 を resolved に書き換えても（古い版の変換など）、Codex 以外の最新の記録で判定する。
		rec.Findings[0].Status, rec.Findings[0].Resolution = "resolved", "Codex が指摘しなかった"
		rec.Verdict = "approve"
		in.Comments[len(in.Comments)-1] = commentOf(t, 7001, "K-Kizuku", rec)
		rep = Evaluate(cfg, in)
		if r := mustResult(t, rep, "C7", Fail); !strings.Contains(r.Detail, "F1") {
			t.Errorf("C7 = %+v", r)
		}
	})

	t.Run("Codex 以外のレビューで resolved にすれば pass", func(t *testing.T) {
		in := codexAfterOpenHigh(t)
		rec := makeRecord(2, headSHA, "approve", resolvedF1())
		rec.CreatedAt = "2026-11-01T01:00:00Z"
		in.Comments = append(in.Comments, commentOf(t, 7002, "K-Kizuku", rec))
		rep := Evaluate(cfg, in)
		if rep.Verdict != Pass {
			t.Fatalf("verdict = %s; %+v", rep.Verdict, rep.Results)
		}
		if rep.LatestReview == nil || rep.LatestReview.CommentID != 2201 {
			t.Errorf("latest = %+v", rep.LatestReview)
		}
	})
}

// codexPR80Unresolved は、codex-pr80.json の古い SHA（aaaa…）の P2 の指摘を priority に変え、
// 最新の要約（HEAD のレビュー）の has_unresolved_findings を unresolved にした入力を返す。
// Codex は、HEAD のレビューで前の指摘を行コメントとして出し直していない。
// 「Didn't find any major issues」の receipt（コメント 2103）は除く（重大な指摘なしと判断できないときの扱いを確かめる。
// receipt があるときは TestCodexPR194NoMajorIssuesResolvesOldP1）。
func codexPR80Unresolved(t *testing.T, priority string, unresolved bool) Input {
	t.Helper()
	in := withoutComment(codexInput(t, "codex-pr80.json", false), 2103)
	rc := &in.ReviewComments[0]
	if rc.ID != 4101 || rc.ReviewedSHA() != firstSHA {
		t.Fatalf("review_comments[0] = %+v", rc)
	}
	rc.Body = strings.Replace(rc.Body, "P2 Badge](https://img.shields.io/badge/P2-yellow", priority+" Badge](https://img.shields.io/badge/"+priority+"-red", 1)
	if unresolved {
		for i := range in.Comments {
			in.Comments[i].Body = strings.Replace(in.Comments[i].Body, `"has_unresolved_findings":false`, `"has_unresolved_findings":true`, 1)
		}
	}
	cr, _ := FindCodexReview(loadConfig(t), in)
	if cr == nil || cr.SHA != headSHA || cr.Unresolved != unresolved {
		t.Fatalf("codex = %+v", cr)
	}
	return in
}

func TestCodexOldP1CarriedWhileUnresolved(t *testing.T) {
	cfg := loadConfig(t)
	now := time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC)

	t.Run("古い SHA の P1 ＋ unresolved=true → fail", func(t *testing.T) {
		in := codexPR80Unresolved(t, "P1", true)
		rep := Evaluate(cfg, in)
		if rep.Verdict != Fail {
			t.Fatalf("verdict = %s; %+v", rep.Verdict, rep.Results)
		}
		mustResult(t, rep, "C6", Pass)
		r := mustResult(t, rep, "C7", Fail)
		for _, want := range []string{"C4101", "P1", "aaaaaaa から未解決"} {
			if !strings.Contains(r.Detail, want) {
				t.Errorf("C7 の detail に %q がない: %s", want, r.Detail)
			}
		}
		if rep.LatestReview.Verdict != "request_changes" {
			t.Errorf("latest = %+v", rep.LatestReview)
		}

		// codex-record の変換でも open のまま引き継ぐ。
		cr, _ := FindCodexReview(cfg, in)
		rec := cr.Record(cfg, 12, baseSHA, nil, now)
		if err := rec.Validate(cfg); err != nil {
			t.Fatal(err)
		}
		if rec.Verdict != "request_changes" || len(rec.Findings) != 1 || rec.Findings[0].ID != "C4101" ||
			rec.Findings[0].Status != "open" || rec.Findings[0].Severity != "high" || !strings.Contains(rec.Summary, "引き継いだ") {
			t.Errorf("record = %+v", rec)
		}
		// 前回の記録に C4101 が open で残っていても、resolved にしない。
		prev := makeRecord(1, firstSHA, "request_changes", []review.Finding{finding("C4101", "high", "open")})
		rec = cr.Record(cfg, 12, baseSHA, prev, now)
		if rec.Verdict != "request_changes" || len(rec.Findings) != 1 || rec.Findings[0].Status != "open" {
			t.Errorf("record = %+v", rec)
		}
		// 変換した記録を載せても、C7 は fail のまま。
		in.Comments = append(in.Comments, commentOf(t, 8000, "K-Kizuku", rec))
		mustResult(t, Evaluate(cfg, in), "C7", Fail)
	})

	t.Run("古い SHA の P1 ＋ unresolved=false → pass", func(t *testing.T) {
		in := codexPR80Unresolved(t, "P1", false)
		rep := Evaluate(cfg, in)
		if rep.Verdict != Pass {
			t.Fatalf("verdict = %s; %+v", rep.Verdict, rep.Results)
		}
		cr, _ := FindCodexReview(cfg, in)
		if len(cr.Findings) != 0 {
			t.Errorf("findings = %+v", cr.Findings)
		}
		prev := makeRecord(1, firstSHA, "request_changes", []review.Finding{finding("C4101", "high", "open")})
		rec := cr.Record(cfg, 12, baseSHA, prev, now)
		if rec.Verdict != "approve" || len(rec.Findings) != 1 || rec.Findings[0].Status != "resolved" {
			t.Errorf("record = %+v", rec)
		}
	})

	t.Run("古い P1 を agent-review/v1 が resolved → pass", func(t *testing.T) {
		in := codexPR80Unresolved(t, "P1", true)
		res := finding("C4101", "high", "resolved")
		res.Resolution = "PATH の偽のディレクトリを飛ばすようにした"
		rec := makeRecord(1, headSHA, "approve", []review.Finding{res})
		rec.CreatedAt = "2026-11-05T10:00:00Z" // 行コメント（09:05）より後、Codex の要約（10:30）より前
		in.Comments = append(in.Comments, commentOf(t, 8100, "K-Kizuku", rec))
		rep := Evaluate(cfg, in)
		if rep.Verdict != Pass {
			t.Fatalf("verdict = %s; %+v", rep.Verdict, rep.Results)
		}
		if rep.LatestReview == nil || rep.LatestReview.CommentID != 2101 {
			t.Errorf("latest = %+v（Codex の要約のコメントが最新）", rep.LatestReview)
		}
		if !strings.Contains(notes(rep), "4101") {
			t.Errorf("notes = %v", rep.Notes)
		}

		// 行コメントより前の記録で resolved にしても、引き継ぐ。
		rec.CreatedAt = "2026-11-05T09:00:00Z"
		in.Comments[len(in.Comments)-1] = commentOf(t, 8100, "K-Kizuku", rec)
		mustResult(t, Evaluate(cfg, in), "C7", Fail)

		// Codex の GitHub 連携の変換した記録では resolved にできない。
		rec.CreatedAt = "2026-11-05T10:00:00Z"
		rec.Reviewer, rec.ReviewerVersion = "codex", "chatgpt-codex-connector (GitHub)"
		in.Comments[len(in.Comments)-1] = commentOf(t, 8100, "K-Kizuku", rec)
		mustResult(t, Evaluate(cfg, in), "C7", Fail)
	})

	t.Run("P2 だけ ＋ unresolved=true → pass", func(t *testing.T) {
		in := codexPR80Unresolved(t, "P2", true)
		rep := Evaluate(cfg, in)
		if rep.Verdict != Pass {
			t.Fatalf("verdict = %s; %+v", rep.Verdict, rep.Results)
		}
		cr, _ := FindCodexReview(cfg, in)
		if len(cr.Findings) != 0 {
			t.Errorf("findings = %+v", cr.Findings)
		}
	})
}

// codexAfterLocalC123 は、round 1 の記録（Claude のフォールバック）が C123（high）を open で記録したあと、
// Codex の GitHub 連携が HEAD を指摘なしでレビューした入力を返す。C123 は C<数字> の形だが、Codex の行コメントはない。
func codexAfterLocalC123(t *testing.T) Input {
	t.Helper()
	in := codexInput(t, "codex-pr84.json", false)
	rec := makeRecord(1, oldSHA, "request_changes", []review.Finding{finding("C123", "high", "open")}, fallback("service_unavailable"))
	rec.CreatedAt = "2026-11-01T00:00:00Z"
	in.Comments = append(in.Comments, commentOf(t, 7100, "K-Kizuku", rec))
	return in
}

func TestCodexIDShapeIsNotProvenance(t *testing.T) {
	cfg := loadConfig(t)
	now := time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC)

	t.Run("ローカルの記録の C123 high open → Codex の指摘なしが最新 → C7 fail", func(t *testing.T) {
		rep := Evaluate(cfg, codexAfterLocalC123(t))
		if rep.Verdict != Fail {
			t.Fatalf("verdict = %s; %+v", rep.Verdict, rep.Results)
		}
		if rep.LatestReview == nil || rep.LatestReview.CommentID != 2201 {
			t.Errorf("latest = %+v（Codex の要約のコメントが最新）", rep.LatestReview)
		}
		if r := mustResult(t, rep, "C7", Fail); !strings.Contains(r.Detail, "C123") {
			t.Errorf("C7 = %+v", r)
		}
	})

	t.Run("変換でも C123 を resolved にしない", func(t *testing.T) {
		in := codexAfterLocalC123(t)
		cr, _ := FindCodexReview(cfg, in)
		prev := LatestRecord(cfg, in)
		if cr == nil || prev == nil || cr.IsCodexFinding("C123") {
			t.Fatalf("codex = %+v, prev = %+v", cr, prev)
		}
		rec := cr.Record(cfg, 12, baseSHA, prev, now)
		if err := rec.Validate(cfg); err != nil {
			t.Fatal(err)
		}
		if rec.Verdict != "request_changes" || len(rec.Findings) != 1 || rec.Findings[0].ID != "C123" ||
			rec.Findings[0].Status != "open" || rec.Findings[0].Resolution != "" {
			t.Errorf("record = %+v", rec)
		}
		// 変換した記録を載せても、C7 は fail のまま。
		in.Comments = append(in.Comments, commentOf(t, 7101, "K-Kizuku", rec))
		rep := Evaluate(cfg, in)
		if rep.LatestReview == nil || rep.LatestReview.CommentID != 7101 {
			t.Errorf("latest = %+v", rep.LatestReview)
		}
		if r := mustResult(t, rep, "C7", Fail); !strings.Contains(r.Detail, "C123") {
			t.Errorf("C7 = %+v", r)
		}
	})

	t.Run("信頼しない作者の行コメントは出所にならない", func(t *testing.T) {
		in := codexAfterLocalC123(t)
		in.ReviewComments = append(in.ReviewComments, codexFinding(123, "someone-else", oldSHA, "P1", "偽の指摘"))
		if r := mustResult(t, Evaluate(cfg, in), "C7", Fail); !strings.Contains(r.Detail, "C123") {
			t.Errorf("C7 = %+v", r)
		}
		cr, _ := FindCodexReview(cfg, in)
		if rec := cr.Record(cfg, 12, baseSHA, LatestRecord(cfg, in), now); rec.Findings[0].Status != "open" {
			t.Errorf("record = %+v", rec)
		}
	})

	t.Run("信頼する Codex の行コメントに対応する C123 は、Codex のレビューで判定し直す", func(t *testing.T) {
		// 前の SHA の Codex の P1（C123）を、ローカルの記録が写した。最新の要約は未解決の指摘なし。
		in := codexAfterLocalC123(t)
		in.ReviewComments = append(in.ReviewComments, codexFinding(123, codexBot, oldSHA, "P1", "Codex の指摘"))
		rep := Evaluate(cfg, in)
		if rep.Verdict != Pass {
			t.Fatalf("verdict = %s; %+v", rep.Verdict, rep.Results)
		}
		cr, _ := FindCodexReview(cfg, in)
		rec := cr.Record(cfg, 12, baseSHA, LatestRecord(cfg, in), now)
		if rec.Verdict != "approve" || rec.Findings[0].ID != "C123" || rec.Findings[0].Status != "resolved" {
			t.Errorf("record = %+v", rec)
		}
	})
}

func TestCodexSameSHAReReviewWithdrawsFindings(t *testing.T) {
	cfg := loadConfig(t)
	now := time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC)

	t.Run("最新の要約が指摘なし → 前のレビューの P1 は解消", func(t *testing.T) {
		in := codexInput(t, "codex-same-sha.json", false)
		rep := Evaluate(cfg, in)
		if rep.Verdict != Pass {
			t.Fatalf("verdict = %s; %+v", rep.Verdict, rep.Results)
		}
		mustResult(t, rep, "C6", Pass)
		mustResult(t, rep, "C7", Pass)
		if !strings.Contains(notes(rep), "4801") {
			t.Errorf("notes = %v", rep.Notes)
		}

		cr, _ := FindCodexReview(cfg, in)
		if cr == nil || cr.SHA != headSHA || len(cr.Findings) != 0 || !cr.IsCodexFinding("C4801") {
			t.Fatalf("codex = %+v", cr)
		}
		// pr codex-record も、取り下げた指摘を open で出さない。前回の記録の open の C4801 は resolved にする。
		if rec := cr.Record(cfg, 12, baseSHA, nil, now); rec.Verdict != "approve" || len(rec.Findings) != 0 {
			t.Errorf("record = %+v", rec)
		}
		prev := makeRecord(1, headSHA, "request_changes", []review.Finding{finding("C4801", "high", "open")})
		rec := cr.Record(cfg, 12, baseSHA, prev, now)
		if err := rec.Validate(cfg); err != nil {
			t.Fatal(err)
		}
		if rec.Verdict != "approve" || len(rec.Findings) != 1 || rec.Findings[0].Status != "resolved" {
			t.Errorf("record = %+v", rec)
		}
	})

	t.Run("最新の要約に未解決の指摘がある → fail", func(t *testing.T) {
		in := codexInput(t, "codex-same-sha.json", false)
		for i := range in.Comments {
			in.Comments[i].Body = strings.Replace(in.Comments[i].Body, `"has_unresolved_findings":false`, `"has_unresolved_findings":true`, 1)
		}
		if r := mustResult(t, Evaluate(cfg, in), "C7", Fail); !strings.Contains(r.Detail, "C4801") {
			t.Errorf("C7 = %+v", r)
		}
	})

	t.Run("最新の要約が has_any_findings: true → fail", func(t *testing.T) {
		in := codexInput(t, "codex-same-sha.json", false)
		for i := range in.Comments {
			in.Comments[i].Body = strings.Replace(in.Comments[i].Body, `"has_any_findings":false`, `"has_any_findings":true`, 1)
		}
		mustResult(t, Evaluate(cfg, in), "C7", Fail)
	})

	t.Run("要約の更新より後の行コメント・作成日時が分からない行コメントは解消とみなさない", func(t *testing.T) {
		for _, created := range []string{"2026-11-05T10:31:00Z", ""} {
			in := codexInput(t, "codex-same-sha.json", false)
			in.ReviewComments[0].CreatedAt = created
			if r := mustResult(t, Evaluate(cfg, in), "C7", Fail); !strings.Contains(r.Detail, "C4801") {
				t.Errorf("created_at %q: C7 = %+v", created, r)
			}
		}
	})
}

// codexPR194 は、PR #194 の形（testdata/codex-pr194.json）の入力を返す。古い SHA（aaaa…）に P1（C4501850982）と
// P2（C4501850984）があり、HEAD のレビューは「Didn't find any major issues」、要約は has_unresolved_findings: true。
func codexPR194(t *testing.T) Input {
	t.Helper()
	in := codexInput(t, "codex-pr194.json", false)
	cr, _ := FindCodexReview(loadConfig(t), in)
	if cr == nil || cr.SHA != headSHA || !cr.Unresolved {
		t.Fatalf("codex = %+v", cr)
	}
	return in
}

func TestCodexPR194NoMajorIssuesResolvesOldP1(t *testing.T) {
	cfg := loadConfig(t)
	now := time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC)

	t.Run("古い SHA の P1 は解消・P2 は引き継ぎ", func(t *testing.T) {
		in := codexPR194(t)
		rep := Evaluate(cfg, in)
		if rep.Verdict != Pass {
			t.Fatalf("verdict = %s; %+v", rep.Verdict, rep.Results)
		}
		mustResult(t, rep, "C6", Pass)
		mustResult(t, rep, "C7", Pass)
		if !strings.Contains(notes(rep), "4501850982") || !strings.Contains(notes(rep), "解消とみなした") {
			t.Errorf("notes = %v", rep.Notes)
		}

		cr, _ := FindCodexReview(cfg, in)
		if len(cr.Findings) != 1 || cr.Findings[0].ID() != "C4501850984" || !cr.Findings[0].Carried || cr.Findings[0].Severity != "medium" {
			t.Errorf("findings = %+v", cr.Findings)
		}
		if len(cr.Resolved) != 1 || cr.Resolved[0].ID() != "C4501850982" || cr.Resolved[0].Severity != "high" {
			t.Fatalf("resolved = %+v", cr.Resolved)
		}
		for _, want := range []string{"Didn't find any major issues", "2403", "1111111", "aaaaaaa"} {
			if !strings.Contains(cr.Resolved[0].Resolution, want) {
				t.Errorf("resolution に %q がない: %s", want, cr.Resolved[0].Resolution)
			}
		}

		// round 4 の記録（P1・P2 が open）の後の変換（#194 の round 5 と同じ）。
		prev := makeRecord(4, firstSHA, "request_changes", []review.Finding{
			finding("C4501850982", "high", "open"), finding("C4501850984", "medium", "open")})
		for _, p := range []*review.Record{nil, prev} {
			rec := cr.Record(cfg, 12, baseSHA, p, now)
			if err := rec.Validate(cfg); err != nil {
				t.Fatal(err)
			}
			byID := map[string]review.Finding{}
			for _, f := range rec.Findings {
				byID[f.ID] = f
			}
			if rec.Verdict != "approve" || len(rec.Findings) != 2 {
				t.Errorf("record = %+v", rec)
			}
			if f := byID["C4501850982"]; f.Status != "resolved" || f.Severity != "high" ||
				!strings.Contains(f.Resolution, "Didn't find any major issues") || !strings.Contains(f.Title, "P1: ") {
				t.Errorf("C4501850982 = %+v", f)
			}
			if f := byID["C4501850984"]; f.Status != "open" || f.Severity != "medium" || !strings.Contains(f.Title, "引き継いだ") {
				t.Errorf("C4501850984 = %+v", f)
			}
			if !strings.Contains(rec.Summary, "解消とみなした") || !strings.Contains(rec.Summary, "重大でない指摘 1 件") {
				t.Errorf("summary = %s", rec.Summary)
			}

			// 変換した記録を載せても、C7 は pass。
			in2 := in
			in2.Comments = append(append([]Comment{}, in.Comments...), commentOf(t, 8200, "K-Kizuku", rec))
			rep := Evaluate(cfg, in2)
			if rep.LatestReview == nil || rep.LatestReview.CommentID != 8200 {
				t.Errorf("latest = %+v", rep.LatestReview)
			}
			mustResult(t, rep, "C7", Pass)
		}
	})

	t.Run("Codex が新しい SHA で同じ P1 を再び出した → open のまま", func(t *testing.T) {
		for _, p := range []string{"P1", "P0", ""} {
			in := codexPR194(t)
			again := codexFinding(4502000001, codexBot, headSHA, p, "★1 の確認のテストが必ず落ちる")
			again.Path, again.Line, again.CreatedAt = "server/internal/stage/stage_test.go", 120, "2026-11-05T10:30:00Z"
			in.ReviewComments = append(in.ReviewComments, again)
			rep := Evaluate(cfg, in)
			r := mustResult(t, rep, "C7", Fail)
			for _, want := range []string{"C4501850982", "aaaaaaa から未解決", "C4502000001"} {
				if !strings.Contains(r.Detail, want) {
					t.Errorf("%q: C7 の detail に %q がない: %s", p, want, r.Detail)
				}
			}
			cr, _ := FindCodexReview(cfg, in)
			if len(cr.Resolved) != 0 {
				t.Errorf("%q: resolved = %+v", p, cr.Resolved)
			}
			rec := cr.Record(cfg, 12, baseSHA, nil, now)
			for _, f := range rec.Findings {
				if f.ID == "C4501850982" && (f.Status != "open" || f.Resolution != "") {
					t.Errorf("%q: C4501850982 = %+v", p, f)
				}
			}
			if rec.Verdict != "request_changes" {
				t.Errorf("%q: verdict = %s", p, rec.Verdict)
			}
		}
	})

	t.Run("重大な指摘なしと判断できない → open のまま", func(t *testing.T) {
		for name, edit := range map[string]func(in *Input){
			"receipt がない": func(in *Input) { *in = withoutComment(*in, 2403) },
			"文言が英語以外": func(in *Input) {
				in.Comments[len(in.Comments)-1].Body = "<!-- codex-review-receipt:v1 -->\nCodex のレビュー: 大きな問題は見つかりませんでした。\n\n**Reviewed commit:** `1111111`\n"
			},
			"文言が変わった": func(in *Input) {
				in.Comments[len(in.Comments)-1].Body = strings.Replace(in.Comments[len(in.Comments)-1].Body, "Didn't find any major issues", "No major issues found", 1)
			},
			"Reviewed commit が古い SHA": func(in *Input) {
				in.Comments[len(in.Comments)-1].Body = strings.Replace(in.Comments[len(in.Comments)-1].Body, "`1111111`", "`aaaaaaa`", 1)
			},
			"Reviewed commit がない": func(in *Input) {
				in.Comments[len(in.Comments)-1].Body = strings.Replace(in.Comments[len(in.Comments)-1].Body, "**Reviewed commit:** `1111111`", "", 1)
			},
			"receipt の作者を信頼しない":   func(in *Input) { in.Comments[len(in.Comments)-1].Author = "someone-else" },
			"receipt の作成日時が分からない": func(in *Input) { in.Comments[len(in.Comments)-1].CreatedAt = "" },
			"receipt より新しいレビューの receipt がある": func(in *Input) {
				in.Reviews = append(in.Reviews, PullReview{ID: 3402, Author: codexBot, CommitID: headSHA, SubmittedAt: "2026-11-05T10:40:00Z",
					Body: "<!-- codex-review-receipt:v1 -->\n### 💡 Codex Review\n\n**Reviewed commit:** `1111111`\n"})
			},
			"同じ日時のレビューの receipt がある": func(in *Input) {
				in.Reviews = append(in.Reviews, PullReview{ID: 3402, Author: codexBot, CommitID: headSHA, SubmittedAt: "2026-11-05T10:30:00Z",
					Body: "<!-- codex-review-receipt:v1 -->\n### 💡 Codex Review\n\n**Reviewed commit:** `1111111`\n"})
			},
		} {
			t.Run(name, func(t *testing.T) {
				in := codexPR194(t)
				if in.Comments[len(in.Comments)-1].ID != 2403 {
					t.Fatalf("comments = %+v", in.Comments)
				}
				edit(&in)
				rep := Evaluate(cfg, in)
				if r := mustResult(t, rep, "C7", Fail); !strings.Contains(r.Detail, "C4501850982") || strings.Contains(r.Detail, "C4501850984") {
					t.Errorf("C7 = %+v", r)
				}
				if !strings.Contains(notes(rep), "解消とみなさなかった") {
					t.Errorf("notes = %v", rep.Notes)
				}
				cr, _ := FindCodexReview(cfg, in)
				if len(cr.Resolved) != 0 || len(cr.Findings) != 1 || cr.Findings[0].ID() != "C4501850982" {
					t.Errorf("codex = %+v", cr)
				}
				rec := cr.Record(cfg, 12, baseSHA, nil, now)
				if rec.Verdict != "request_changes" || len(rec.Findings) != 1 || rec.Findings[0].Status != "open" || rec.Findings[0].Resolution != "" {
					t.Errorf("record = %+v", rec)
				}
			})
		}
	})
}

// receipt（重大な指摘なし）より後に作られた指摘や、前後関係を確かめられない指摘は、解消とみなさない（Issue #213 のレビュー C4502464035）。
// main の取り込み前の SHA（1111…）に 10:30 の receipt があり、取り込み後の SHA（4444…）に 11:00 の P1 があるとき、
// receipt はその P1 を見ていないので、P1 は open のまま（古い SHA の P1 は receipt より前なので解消）。
func TestCodexPR194ReceiptDoesNotResolveLaterFindings(t *testing.T) {
	cfg := loadConfig(t)
	now := time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC)
	const mergedSHA = "4444444444444444444444444444444444444444"

	for name, createdAt := range map[string]string{
		"receipt より後（11:00）": "2026-11-05T11:00:00Z",
		"receipt と同じ時刻":      "2026-11-05T10:30:00Z",
		"作成日時が分からない":         "",
		"作成日時が壊れている":         "yesterday",
	} {
		t.Run(name, func(t *testing.T) {
			in := codexPR194(t)
			later := codexFinding(4502100001, codexBot, mergedSHA, "P1", "取り込み後に壊れた")
			later.CreatedAt = createdAt
			in.ReviewComments = append(in.ReviewComments, later)

			cr, _ := FindCodexReview(cfg, in)
			if len(cr.Resolved) != 1 || cr.Resolved[0].ID() != "C4501850982" {
				t.Errorf("resolved = %+v", cr.Resolved)
			}
			var got *CodexFinding
			for i := range cr.Findings {
				if cr.Findings[i].ID() == "C4502100001" {
					got = &cr.Findings[i]
				}
			}
			if got == nil || !got.Carried || got.Severity != "high" || got.Resolution != "" {
				t.Fatalf("findings = %+v", cr.Findings)
			}

			rep := Evaluate(cfg, in)
			if r := mustResult(t, rep, "C7", Fail); !strings.Contains(r.Detail, "C4502100001") || strings.Contains(r.Detail, "C4501850982") {
				t.Errorf("C7 = %+v", r)
			}
			if !strings.Contains(notes(rep), "4502100001") || !strings.Contains(notes(rep), "より前に作られたと確かめられない") {
				t.Errorf("notes = %v", rep.Notes)
			}

			rec := cr.Record(cfg, 12, baseSHA, nil, now)
			if rec.Verdict != "request_changes" {
				t.Errorf("verdict = %s", rec.Verdict)
			}
			for _, f := range rec.Findings {
				switch f.ID {
				case "C4502100001":
					if f.Status != "open" || f.Resolution != "" {
						t.Errorf("C4502100001 = %+v", f)
					}
				case "C4501850982":
					if f.Status != "resolved" || !strings.Contains(f.Resolution, "2026-11-05T10:30:00Z") {
						t.Errorf("C4501850982 = %+v", f)
					}
				}
			}
		})
	}

	t.Run("古い SHA の P1 でも receipt より後に作られたら open", func(t *testing.T) {
		in := codexPR194(t)
		in.ReviewComments[0].CreatedAt = "2026-11-05T10:31:00Z"
		if in.ReviewComments[0].ID != 4501850982 {
			t.Fatalf("review_comments[0] = %+v", in.ReviewComments[0])
		}
		cr, _ := FindCodexReview(cfg, in)
		if len(cr.Resolved) != 0 {
			t.Errorf("resolved = %+v", cr.Resolved)
		}
		mustResult(t, Evaluate(cfg, in), "C7", Fail)
	})
}

// 設定の blocking_severities に medium を足しても、重大な指摘なしの receipt で解消とみなすのは P0・P1 だけで、
// 古い SHA の P2 は open のまま引き継ぐ（Issue #213 のレビュー C4502464027）。
func TestCodexPR194ReceiptClearsOnlyP0P1(t *testing.T) {
	cfg := loadConfig(t)
	cfg.Review.BlockingSeverities = []string{"critical", "high", "medium"}
	in := codexPR194(t)

	cr, _ := FindCodexReview(cfg, in)
	if len(cr.Resolved) != 1 || cr.Resolved[0].ID() != "C4501850982" {
		t.Errorf("resolved = %+v", cr.Resolved)
	}
	if len(cr.Findings) != 1 || cr.Findings[0].ID() != "C4501850984" || !cr.Findings[0].Carried || cr.Findings[0].Resolution != "" {
		t.Errorf("findings = %+v", cr.Findings)
	}
	rep := Evaluate(cfg, in)
	if r := mustResult(t, rep, "C7", Fail); !strings.Contains(r.Detail, "C4501850984") || strings.Contains(r.Detail, "C4501850982") {
		t.Errorf("C7 = %+v", r)
	}
	if !strings.Contains(notes(rep), "P0・P1 ではない") {
		t.Errorf("notes = %v", rep.Notes)
	}
	rec := cr.Record(cfg, 12, baseSHA, nil, time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC))
	if rec.Verdict != "request_changes" {
		t.Errorf("verdict = %s", rec.Verdict)
	}
}
