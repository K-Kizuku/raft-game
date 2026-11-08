package gate

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/config"
	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/review"
)

const (
	headSHA = "1111111111111111111111111111111111111111"
	baseSHA = "2222222222222222222222222222222222222222"
	oldSHA  = "3333333333333333333333333333333333333333"
)

func loadConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.Load("testdata/config.json")
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func loadInput(t *testing.T) Input {
	t.Helper()
	data, err := os.ReadFile("testdata/pass.json")
	if err != nil {
		t.Fatal(err)
	}
	var in Input
	if err := json.Unmarshal(data, &in); err != nil {
		t.Fatal(err)
	}
	return in
}

func ptr[T any](v T) *T { return &v }

type recOpt func(*review.Record)

func makeRecord(round int, sha, verdict string, findings []review.Finding, opts ...recOpt) *review.Record {
	if findings == nil {
		findings = []review.Finding{}
	}
	r := &review.Record{
		Schema: review.Schema, PR: 12, HeadSHA: sha, BaseSHA: baseSHA, Round: round,
		Reviewer: "codex", ReviewerVersion: "codex-cli 0.167.0", Verdict: verdict, Summary: "s",
		ProtectedPathsAck: &review.Ack{Reviewed: ptr(true), WeakensGuards: ptr(false)},
		Findings:          findings,
		CreatedAt:         "2026-11-02T00:00:00Z",
	}
	for _, o := range opts {
		o(r)
	}
	return r
}

func commentOf(t *testing.T, id int64, author string, r *review.Record) Comment {
	t.Helper()
	body, err := review.Render(r)
	if err != nil {
		t.Fatal(err)
	}
	return Comment{ID: id, Author: author, Body: body, CreatedAt: r.CreatedAt}
}

func finding(id, sev, status string) review.Finding {
	return review.Finding{ID: id, Severity: sev, Category: "correctness", File: "a.go", Line: 1, Title: "t", Status: status}
}

func resultOf(rep Report, id string) (Result, bool) {
	for _, r := range rep.Results {
		if r.ID == id {
			return r, true
		}
	}
	return Result{}, false
}

func TestPassSample(t *testing.T) {
	rep := Evaluate(loadConfig(t), loadInput(t))
	if rep.Verdict != Pass {
		t.Fatalf("verdict = %s, results = %+v", rep.Verdict, rep.Results)
	}
	if rep.HeadSHA != headSHA {
		t.Errorf("head_sha = %s", rep.HeadSHA)
	}
	if len(rep.Results) != 13 {
		t.Errorf("results = %d, want 13 (G0, C1..C12)", len(rep.Results))
	}
	if rep.LatestReview == nil || rep.LatestReview.CommentID != 1003 {
		t.Errorf("latest review = %+v, want comment 1003", rep.LatestReview)
	}
	// 信頼しない作者の記録は無視したことが残る。
	if !strings.Contains(strings.Join(rep.Notes, "\n"), "someone-else") {
		t.Errorf("notes = %v", rep.Notes)
	}
}

func TestConditions(t *testing.T) {
	// review-dependent は、レビュー記録がないと pending になる条件。
	reviewDependent := []string{"C5", "C6", "C7", "C8", "C12"}

	tests := []struct {
		name   string
		cfg    func(*config.Config)
		mutate func(t *testing.T, in *Input)
		id     string
		want   Verdict
		// others は、id 以外に pass でなくてよい条件。
		others []string
	}{
		// G0
		{name: "G0 必須チェックが空", id: "G0", want: Fail,
			cfg: func(c *config.Config) { c.RequiredChecks = nil }},
		{name: "G0 blocking に high がない", id: "G0", want: Fail,
			cfg: func(c *config.Config) { c.Review.BlockingSeverities = []string{"critical"} }},
		{name: "G0 信頼する作者が空", id: "G0", want: Fail,
			cfg: func(c *config.Config) { c.Review.TrustedAuthors = nil }},
		{name: "G0 max_workers が範囲外", id: "G0", want: Fail,
			cfg: func(c *config.Config) { c.MaxWorkers = 9 }},

		// C1
		{name: "C1 closed", id: "C1", want: Fail,
			mutate: func(_ *testing.T, in *Input) { in.PullRequest.State = "closed" }},
		{name: "C1 merged", id: "C1", want: Fail,
			mutate: func(_ *testing.T, in *Input) { in.PullRequest.State = "closed"; in.PullRequest.Merged = true }},
		{name: "C1 draft", id: "C1", want: Fail,
			mutate: func(_ *testing.T, in *Input) { in.PullRequest.Draft = true }},
		{name: "C1 ベースが main ではない", id: "C1", want: Fail,
			mutate: func(_ *testing.T, in *Input) { in.PullRequest.BaseRef = "develop" }},
		{name: "C1 status:blocked", id: "C1", want: Fail,
			mutate: func(_ *testing.T, in *Input) { in.PullRequest.Labels = append(in.PullRequest.Labels, "status:blocked") }},
		{name: "C1 needs-human", id: "C1", want: Fail,
			mutate: func(_ *testing.T, in *Input) { in.PullRequest.Labels = append(in.PullRequest.Labels, "needs-human") }},

		// C2
		{name: "C2 Closes がない", id: "C2", want: Fail,
			mutate: func(_ *testing.T, in *Input) {
				in.PullRequest.Body = strings.Replace(in.PullRequest.Body, "Closes #3", "Refs #3", 1)
			}},
		{name: "C2 Closes が 2 つ", id: "C2", want: Fail,
			mutate: func(_ *testing.T, in *Input) { in.PullRequest.Body = "Fixes #4\n" + in.PullRequest.Body }},
		{name: "C2 Issue が閉じている", id: "C2", want: Fail,
			mutate: func(_ *testing.T, in *Input) { in.Issue.State = "closed" }},
		{name: "C2 Issue が見つからない", id: "C2", want: Fail, others: []string{"C11"},
			mutate: func(_ *testing.T, in *Input) { in.Issue = nil }},
		{name: "C2 AC の件数が違う", id: "C2", want: Fail,
			mutate: func(_ *testing.T, in *Input) {
				in.PullRequest.Body = strings.Replace(in.PullRequest.Body, "- [x] AC-2: 設定で無効にできない\r\n", "", 1)
			}},
		{name: "C2 AC が未チェック", id: "C2", want: Fail,
			mutate: func(_ *testing.T, in *Input) {
				in.PullRequest.Body = strings.Replace(in.PullRequest.Body, "- [x] AC-2", "- [ ] AC-2", 1)
			}},
		{name: "C2 Issue に AC がない", id: "C2", want: Fail,
			mutate: func(_ *testing.T, in *Input) {
				in.Issue.Body = strings.Replace(in.Issue.Body, "- [ ] AC-", "- AC-", -1)
			}},

		// C3
		{name: "C3 テストが未チェック", id: "C3", want: Fail,
			mutate: func(_ *testing.T, in *Input) {
				in.PullRequest.Body = strings.Replace(in.PullRequest.Body, "- [x] 追加・更新したテスト", "- [ ] 追加・更新したテスト", 1)
			}},
		{name: "C3 ドキュメントにチェックボックスがない", id: "C3", want: Fail,
			mutate: func(_ *testing.T, in *Input) {
				in.PullRequest.Body = strings.Replace(in.PullRequest.Body, "- [x] 更新した（CLAUDE.md）", "更新した", 1)
			}},

		// C4
		{name: "C4 CI が失敗", id: "C4", want: Fail,
			mutate: func(_ *testing.T, in *Input) { in.CheckRuns[0].Conclusion = "failure" }},
		{name: "C4 CI が実行中", id: "C4", want: Pending,
			mutate: func(_ *testing.T, in *Input) { in.CheckRuns[0].Status = "in_progress"; in.CheckRuns[0].Conclusion = "" }},
		{name: "C4 必須のチェックが未実行", id: "C4", want: Pending,
			mutate: func(_ *testing.T, in *Input) { in.CheckRuns = in.CheckRuns[1:] }},
		{name: "C4 必須のチェックが古い SHA のものだけ", id: "C4", want: Pending,
			mutate: func(_ *testing.T, in *Input) { in.CheckRuns[0].HeadSHA = oldSHA }},
		{name: "C4 必須のチェックが skipped", id: "C4", want: Fail,
			mutate: func(_ *testing.T, in *Input) { in.CheckRuns[0].Conclusion = "skipped" }},
		{name: "C4 必須のチェックのステータスが pending", id: "C4", want: Pending,
			mutate: func(_ *testing.T, in *Input) {
				in.Statuses = append(in.Statuses, Status{Context: "ci-ok", State: "pending"})
			}},
		{name: "C4 ほかの check run が timed_out", id: "C4", want: Fail,
			mutate: func(_ *testing.T, in *Input) { in.CheckRuns[1].Conclusion = "timed_out" }},
		{name: "C4 ほかのステータスが error", id: "C4", want: Fail,
			mutate: func(_ *testing.T, in *Input) {
				in.Statuses = append(in.Statuses, Status{Context: "external", State: "error"})
			}},

		// C5
		{name: "C5 レビュー記録がない", id: "C5", want: Pending, others: reviewDependent,
			mutate: func(_ *testing.T, in *Input) { in.Comments = nil }},
		{name: "C5 信頼しない作者の記録だけ", id: "C5", want: Pending, others: reviewDependent,
			mutate: func(_ *testing.T, in *Input) {
				for i := range in.Comments {
					in.Comments[i].Author = "someone-else"
				}
			}},

		// C6
		{name: "C6 修正後の SHA へのレビューがない", id: "C6", want: Pending,
			mutate: func(_ *testing.T, in *Input) {
				in.PullRequest.HeadSHA = "4444444444444444444444444444444444444444"
				retarget(in)
			}},
		{name: "C6 最新の記録が request_changes", id: "C6", want: Fail,
			mutate: func(t *testing.T, in *Input) {
				in.Comments = append(in.Comments, commentOf(t, 2000, "K-Kizuku", makeRecord(3, headSHA, "request_changes", resolvedF1())))
			}},

		// C7
		{name: "C7 最新の記録に high が open", id: "C7", want: Fail,
			mutate: func(t *testing.T, in *Input) {
				in.Comments = append(in.Comments, commentOf(t, 2000, "K-Kizuku",
					makeRecord(3, headSHA, "approve", append(resolvedF1(), finding("F3", "critical", "open")))))
			}},
		{name: "C7 前の F1 が最新の記録にない", id: "C7", want: Fail,
			mutate: func(t *testing.T, in *Input) {
				in.Comments = append(in.Comments, commentOf(t, 2000, "K-Kizuku", makeRecord(3, headSHA, "approve", nil)))
			}},
		{name: "C7 前の F1 が wont_fix", id: "C7", want: Fail,
			mutate: func(t *testing.T, in *Input) {
				in.Comments = append(in.Comments, commentOf(t, 2000, "K-Kizuku",
					makeRecord(3, headSHA, "approve", []review.Finding{finding("F1", "high", "wont_fix")})))
			}},

		// C8
		{name: "C8 フォールバックが許されていない", id: "C8", want: Fail,
			cfg: func(c *config.Config) { c.Review.AllowFallback = false },
			mutate: func(t *testing.T, in *Input) {
				in.Comments = append(in.Comments, commentOf(t, 2000, "K-Kizuku", makeRecord(3, headSHA, "approve", resolvedF1(), fallback("rate_limited"))))
			}},
		{name: "C8 フォールバックの理由が設定にない", id: "C8", want: Fail,
			mutate: func(t *testing.T, in *Input) {
				in.Comments = append(in.Comments, commentOf(t, 2000, "K-Kizuku", makeRecord(3, headSHA, "approve", resolvedF1(), fallback("bored"))))
			}},

		// C9
		{name: "C9 コンフリクト", id: "C9", want: Fail,
			mutate: func(_ *testing.T, in *Input) {
				in.PullRequest.Mergeable = ptr(false)
				in.PullRequest.MergeableState = "dirty"
			}},
		{name: "C9 mergeable_state が dirty", id: "C9", want: Fail,
			mutate: func(_ *testing.T, in *Input) { in.PullRequest.MergeableState = "dirty" }},
		{name: "C9 mergeable が未計算", id: "C9", want: Pending,
			mutate: func(_ *testing.T, in *Input) {
				in.PullRequest.Mergeable = nil
				in.PullRequest.MergeableState = "unknown"
			}},

		// C10
		{name: "C10 ベースより遅れている", id: "C10", want: Fail,
			mutate: func(_ *testing.T, in *Input) { in.Compare.BehindBy = 1; in.Compare.Status = "diverged" }},
		{name: "C10 比較の結果がない", id: "C10", want: Fail,
			mutate: func(_ *testing.T, in *Input) { in.Compare = nil }},

		// C11
		{name: "C11 変更予定ファイルの外", id: "C11", want: Fail,
			mutate: func(_ *testing.T, in *Input) { addFile(in, "README.md") }},
		{name: "C11 変更禁止範囲", id: "C11", want: Fail,
			mutate: func(_ *testing.T, in *Input) {
				in.Issue.Body = strings.Replace(in.Issue.Body, "- `docs/**`", "- `docs/**`\n- `tools/agentctl/internal/gate/**`", 1)
			}},
		{name: "C11 名前を変える前のファイルが範囲外", id: "C11", want: Fail,
			mutate: func(_ *testing.T, in *Input) { in.Files[2].PreviousFilename = "docs/old.md" }},
		{name: "C11 変更ファイルの一覧が不完全", id: "C11", want: Fail,
			mutate: func(_ *testing.T, in *Input) { in.PullRequest.ChangedFiles = 3001 }},

		// C12
		{name: "C12 ガードを弱める", id: "C12", want: Fail,
			mutate: func(t *testing.T, in *Input) {
				in.Comments = append(in.Comments, commentOf(t, 2000, "K-Kizuku", makeRecord(3, headSHA, "approve", resolvedF1(),
					func(r *review.Record) { r.ProtectedPathsAck.WeakensGuards = ptr(true) })))
			}},
		{name: "C12 保護されたパスを確認していない", id: "C12", want: Fail,
			mutate: func(t *testing.T, in *Input) {
				in.Comments = append(in.Comments, commentOf(t, 2000, "K-Kizuku", makeRecord(3, headSHA, "approve", resolvedF1(),
					func(r *review.Record) { r.ProtectedPathsAck.Reviewed = ptr(false) })))
			}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := loadConfig(t)
			if tt.cfg != nil {
				tt.cfg(cfg)
			}
			in := loadInput(t)
			if tt.mutate != nil {
				tt.mutate(t, &in)
			}
			rep := Evaluate(cfg, in)
			if rep.Verdict != tt.want {
				t.Errorf("verdict = %s, want %s; results = %+v", rep.Verdict, tt.want, rep.Results)
			}
			res, ok := resultOf(rep, tt.id)
			if !ok {
				t.Fatalf("%s の結果がない: %+v", tt.id, rep.Results)
			}
			if res.Status != tt.want {
				t.Errorf("%s = %s (%s), want %s", tt.id, res.Status, res.Detail, tt.want)
			}
			for _, r := range rep.Results {
				if r.ID == tt.id || contains(tt.others, r.ID) {
					continue
				}
				if r.Status != Pass {
					t.Errorf("%s 以外の %s も %s（%s）", tt.id, r.ID, r.Status, r.Detail)
				}
			}
		})
	}
}

// retarget は、check run を新しい HEAD に付け直す（CI は通っていて、レビューだけが古い状態にする）。
func retarget(in *Input) {
	for i := range in.CheckRuns {
		in.CheckRuns[i].HeadSHA = in.PullRequest.HeadSHA
	}
}

func resolvedF1() []review.Finding {
	return []review.Finding{finding("F1", "high", "resolved")}
}

func fallback(reason string) recOpt {
	return func(r *review.Record) {
		r.Reviewer = "claude-fallback"
		r.FallbackReason = reason
		r.ReviewerVersion = "claude"
	}
}

func addFile(in *Input, name string) {
	in.Files = append(in.Files, File{Filename: name, Status: "added"})
	in.PullRequest.ChangedFiles = len(in.Files)
}

func TestNilConfig(t *testing.T) {
	rep := Evaluate(nil, loadInput(t))
	if rep.Verdict != Fail || rep.Results[0].ID != "G0" || rep.Results[0].Status != Fail {
		t.Fatalf("rep = %+v", rep)
	}
}

func TestClosedIsFlagged(t *testing.T) {
	in := loadInput(t)
	in.PullRequest.State = "closed"
	rep := Evaluate(loadConfig(t), in)
	if !rep.Closed || rep.Verdict != Fail {
		t.Fatalf("closed = %t, verdict = %s", rep.Closed, rep.Verdict)
	}
}

func TestRequiredCheckAsCommitStatus(t *testing.T) {
	in := loadInput(t)
	in.CheckRuns = in.CheckRuns[1:]
	in.Statuses = append(in.Statuses, Status{Context: "ci-ok", State: "success"})
	rep := Evaluate(loadConfig(t), in)
	if rep.Verdict != Pass {
		t.Fatalf("verdict = %s; %+v", rep.Verdict, rep.Results)
	}
}

func TestSelfChecksIgnored(t *testing.T) {
	in := loadInput(t)
	in.Statuses = []Status{{Context: "merge-gate", State: "failure"}}
	in.CheckRuns = append(in.CheckRuns, CheckRun{Name: "merge-gate-evaluate", HeadSHA: headSHA, Status: "completed", Conclusion: "failure"})
	rep := Evaluate(loadConfig(t), in)
	if rep.Verdict != Pass {
		t.Fatalf("verdict = %s; %+v", rep.Verdict, rep.Results)
	}
}

func TestFallbackAllowed(t *testing.T) {
	in := loadInput(t)
	in.Comments = append(in.Comments, commentOf(t, 2000, "K-Kizuku", makeRecord(3, headSHA, "approve", resolvedF1(), fallback("rate_limited"))))
	rep := Evaluate(loadConfig(t), in)
	if rep.Verdict != Pass {
		t.Fatalf("verdict = %s; %+v", rep.Verdict, rep.Results)
	}
}

func TestSameRoundNewestWins(t *testing.T) {
	in := loadInput(t)
	older := makeRecord(3, headSHA, "approve", resolvedF1())
	older.CreatedAt = "2026-11-03T00:00:00Z"
	newer := makeRecord(3, headSHA, "request_changes", resolvedF1())
	newer.CreatedAt = "2026-11-04T00:00:00Z"
	// コメントの並びと作成日時の順を逆にしておく。
	in.Comments = append(in.Comments, commentOf(t, 3000, "K-Kizuku", newer), commentOf(t, 3001, "K-Kizuku", older))
	rep := Evaluate(loadConfig(t), in)
	if r, _ := resultOf(rep, "C6"); r.Status != Fail {
		t.Fatalf("C6 = %+v, want fail（新しい request_changes が最新）", r)
	}
	if rep.LatestReview.CommentID != 3000 {
		t.Errorf("latest = %d", rep.LatestReview.CommentID)
	}
}

func TestBrokenRecordsIgnored(t *testing.T) {
	cases := map[string]string{
		"壊れた JSON":  review.Marker + "\n```json\n{\"schema\": \n```\n",
		"目印が 2 つ":   review.Marker + "\n" + review.Marker + "\n```json\n{}\n```\n",
		"JSON がない":  review.Marker + "\nLGTM\n",
		"必須項目の欠落":   review.Marker + "\n```json\n{\"schema\":\"agent-review/v1\"}\n```\n",
		"別の PR の記録": "",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			in := loadInput(t)
			if body == "" {
				r := makeRecord(3, headSHA, "request_changes", nil)
				r.PR = 99
				body = commentOf(t, 0, "", r).Body
			}
			in.Comments = append(in.Comments, Comment{ID: 4000, Author: "K-Kizuku", Body: body, CreatedAt: "2026-11-05T00:00:00Z"})
			rep := Evaluate(loadConfig(t), in)
			if rep.Verdict != Pass {
				t.Fatalf("verdict = %s; %+v", rep.Verdict, rep.Results)
			}
			if !strings.Contains(strings.Join(rep.Notes, "\n"), "4000") {
				t.Errorf("notes に理由がない: %v", rep.Notes)
			}
		})
	}
}

func TestOnlyStaleReviewAfterFix(t *testing.T) {
	// 修正のコミットを積んだ後、CI は新しい HEAD で通っているが、レビューは古い SHA のものしかない。
	in := loadInput(t)
	in.PullRequest.HeadSHA = "5555555555555555555555555555555555555555"
	retarget(&in)
	rep := Evaluate(loadConfig(t), in)
	if rep.Verdict != Pending {
		t.Fatalf("verdict = %s; %+v", rep.Verdict, rep.Results)
	}
	if r, _ := resultOf(rep, "C6"); r.Status != Pending {
		t.Errorf("C6 = %+v", r)
	}
}

func TestDescriptionAndComment(t *testing.T) {
	in := loadInput(t)
	in.Compare.BehindBy = 2
	in.CheckRuns[0].Status = "queued"
	rep := Evaluate(loadConfig(t), in)
	d := rep.Description()
	if !strings.HasPrefix(d, "fail: C10") || !strings.Contains(d, "C4") {
		t.Errorf("description = %q", d)
	}
	if rep.StatusState() != "failure" {
		t.Errorf("state = %s", rep.StatusState())
	}
	body := rep.RenderComment("https://example.invalid/run/1")
	for _, want := range []string{CommentMarker, headSHA, "❌", "⏳", "✅", "| C10 |"} {
		if !strings.Contains(body, want) {
			t.Errorf("comment に %q がない:\n%s", want, body)
		}
	}
	long := Report{Verdict: Fail}
	for i := 0; i < 100; i++ {
		long.Results = append(long.Results, Result{ID: "C99", Status: Fail})
	}
	if n := len([]rune(long.Description())); n > MaxDescription {
		t.Errorf("description の長さ = %d", n)
	}
}

// merge.require_up_to_date（Issue #239）: false なら遅れていても C10 は pass。C9・C4・比較の結果がないときは今までどおり。
func TestC10RequireUpToDate(t *testing.T) {
	behind := func(_ *testing.T, in *Input) { in.Compare.BehindBy = 3; in.Compare.Status = "diverged" }
	setRequire := func(v *bool) func(*config.Config) {
		return func(c *config.Config) { c.Merge = &config.Merge{RequireUpToDate: v} }
	}
	tests := []struct {
		name      string
		cfg       func(*config.Config)
		mutate    func(*testing.T, *Input)
		id        string
		want      Verdict
		detailHas string
	}{
		{name: "true で遅れている", cfg: setRequire(ptr(true)), mutate: behind,
			id: "C10", want: Fail, detailHas: "3 コミット遅れている（最新の main を取り込む）"},
		{name: "設定なしで遅れている", cfg: func(c *config.Config) { c.Merge = nil }, mutate: behind,
			id: "C10", want: Fail, detailHas: "3 コミット遅れている（最新の main を取り込む）"},
		{name: "require_up_to_date の省略で遅れている", cfg: setRequire(nil), mutate: behind,
			id: "C10", want: Fail, detailHas: "3 コミット遅れている（最新の main を取り込む）"},
		{name: "false で遅れている・コンフリクトなし", cfg: setRequire(ptr(false)), mutate: behind,
			id: "C10", want: Pass, detailHas: "main より 3 コミット遅れている"},
		{name: "false で遅れている・コンフリクトあり", cfg: setRequire(ptr(false)),
			mutate: func(t *testing.T, in *Input) {
				behind(t, in)
				in.PullRequest.Mergeable = ptr(false)
				in.PullRequest.MergeableState = "dirty"
			},
			id: "C9", want: Fail, detailHas: "コンフリクトがある"},
		{name: "false で遅れている・CI が失敗", cfg: setRequire(ptr(false)),
			mutate: func(t *testing.T, in *Input) { behind(t, in); in.CheckRuns[0].Conclusion = "failure" },
			id:     "C4", want: Fail},
		{name: "false で比較の結果がない", cfg: setRequire(ptr(false)),
			mutate: func(_ *testing.T, in *Input) { in.Compare = nil },
			id:     "C10", want: Fail, detailHas: "比較の結果がない"},
		{name: "false で behind_by が不正", cfg: setRequire(ptr(false)),
			mutate: func(_ *testing.T, in *Input) { in.Compare.BehindBy = -1 },
			id:     "C10", want: Fail, detailHas: "不正"},
		{name: "false で遅れていない", cfg: setRequire(ptr(false)),
			id: "C10", want: Pass, detailHas: "OK"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := loadConfig(t)
			tt.cfg(cfg)
			in := loadInput(t)
			if tt.mutate != nil {
				tt.mutate(t, &in)
			}
			rep := Evaluate(cfg, in)
			if rep.Verdict != tt.want {
				t.Errorf("verdict = %s, want %s; results = %+v", rep.Verdict, tt.want, rep.Results)
			}
			res, ok := resultOf(rep, tt.id)
			if !ok {
				t.Fatalf("%s の結果がない: %+v", tt.id, rep.Results)
			}
			if res.Status != tt.want || !strings.Contains(res.Detail, tt.detailHas) {
				t.Errorf("%s = %s (%s), want %s (%q を含む)", tt.id, res.Status, res.Detail, tt.want, tt.detailHas)
			}
			// 遅れていても、ほかの条件（C10 も含む）はすべて pass のまま
			for _, r := range rep.Results {
				if r.ID != tt.id && r.Status != Pass {
					t.Errorf("%s 以外の %s も %s（%s）", tt.id, r.ID, r.Status, r.Detail)
				}
			}
		})
	}
}
