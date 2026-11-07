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
	in := codexInput(t, "codex-pr80.json", false)
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
			in := codexInput(t, "codex-pr80.json", false)
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
	in := codexInput(t, "codex-pr84.json", false)
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
		in := codexInput(t, "codex-pr84.json", false)
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
	in := codexInput(t, "codex-pr80.json", true)
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
	in := codexInput(t, "codex-pr80.json", true)
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
	prev.Findings = append(prev.Findings, finding("F9", "high", "open"), finding("F8", "low", "open"), finding("C4000", "high", "open"))
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
	if f := byID["F9"]; f.Status != "open" || f.Resolution != "" {
		t.Errorf("F9 = %+v", f)
	}
	if !strings.Contains(rec.Summary, "open のまま引き継いだ") {
		t.Errorf("summary = %s", rec.Summary)
	}
	// 重大でない指摘と、前の Codex の指摘は、今回指摘されなければ resolved で引き継ぐ。resolved のものはそのまま。
	for _, id := range []string{"F8", "C4000"} {
		if f := byID[id]; f.Status != "resolved" || !strings.Contains(f.Resolution, "1111111") {
			t.Errorf("%s = %+v", id, f)
		}
	}
	if f := byID["F1"]; f.Status != "resolved" || f.Resolution != "内部エラーにした" {
		t.Errorf("F1 = %+v", f)
	}
	if _, ok := byID["C4101"]; ok {
		t.Error("古い SHA の指摘を含めた")
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
func codexPR80Unresolved(t *testing.T, priority string, unresolved bool) Input {
	t.Helper()
	in := codexInput(t, "codex-pr80.json", false)
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
