package gate

import (
	"strings"
	"testing"

	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/config"
)

// smallIssueBody は、小さな PR の判定のテストで使う Issue の本文（docs・テスト・コードを変更予定ファイルに含む）。
const smallIssueBody = "## 背景・目的\n\n小さな PR。\n\n## 変更予定ファイル\n\n- `docs/**`\n- `tools/agentctl/**`\n\n" +
	"## 変更禁止範囲\n\n- `server/**`\n\n## Acceptance Criteria\n\n- [ ] AC-1: gate が判定する\n- [ ] AC-2: 設定で無効にできない\n"

// smallInput は、docs の 1 ファイルだけ・50 行の PR で、レビュー記録がない入力を返す。
func smallInput(t *testing.T) Input {
	t.Helper()
	in := loadInput(t)
	in.Issue.Body = smallIssueBody
	in.Comments = nil
	in.Reviews = nil
	in.ReviewComments = nil
	setFiles(&in, 40, 10, File{Filename: "docs/agents/workflow.md", Status: "modified"})
	return in
}

func setFiles(in *Input, add, del int, files ...File) {
	in.Files = files
	in.PullRequest.ChangedFiles = len(files)
	in.PullRequest.Additions = ptr(add)
	in.PullRequest.Deletions = ptr(del)
}

// smallConfig は、review.skip_small_prs を true にした設定（skip_paths・skip_max_lines は既定）。
func smallConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg := loadConfig(t)
	cfg.Review.SkipSmallPRs = true
	return cfg
}

var reviewIDs = []string{"C5", "C6", "C7", "C8"}

func assertSkipped(t *testing.T, rep Report, skipped bool) {
	t.Helper()
	for _, id := range reviewIDs {
		r, ok := resultOf(rep, id)
		if !ok {
			t.Fatalf("%s がない", id)
		}
		got := r.Status == Pass && strings.HasPrefix(r.Detail, "小さな PR なのでレビューを省いた（")
		if got != skipped {
			t.Errorf("%s: skipped = %v, want %v（%s: %s）", id, got, skipped, r.Status, r.Detail)
		}
		if !skipped && r.Status != Pending {
			// レビュー記録がないので、今までどおりレビューを求める（pending）。
			t.Errorf("%s = %s（%s）, want pending", id, r.Status, r.Detail)
		}
	}
}

func TestSmallPRSkipsReview(t *testing.T) {
	rep := Evaluate(smallConfig(t), smallInput(t))
	assertSkipped(t, rep, true)
	if rep.Verdict != Pass {
		t.Errorf("verdict = %s, results = %+v", rep.Verdict, rep.Results)
	}
	r, _ := resultOf(rep, "C6")
	if !strings.Contains(r.Detail, "1 ファイルがすべて review.skip_paths に一致") || !strings.Contains(r.Detail, "50 行（上限 300 行）") {
		t.Errorf("detail = %q", r.Detail)
	}
	// 省いたことが Merge Gate のコメントに残る。
	if c := rep.RenderComment(""); !strings.Contains(c, "小さな PR なのでレビューを省いた") {
		t.Errorf("comment = %s", c)
	}
}

// 小さな PR でも、レビュー以外の条件（C1〜C4・C9〜C12）は今までどおり判定する。
func TestSmallPROtherConditionsUnchanged(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(in *Input)
		id     string
		want   Verdict
	}{
		{name: "C1 draft", id: "C1", want: Fail, mutate: func(in *Input) { in.PullRequest.Draft = true }},
		{name: "C2 AC が未チェック", id: "C2", want: Fail, mutate: func(in *Input) {
			in.PullRequest.Body = strings.Replace(in.PullRequest.Body, "- [x] AC-1", "- [ ] AC-1", 1)
		}},
		{name: "C3 テストが未チェック", id: "C3", want: Fail, mutate: func(in *Input) {
			in.PullRequest.Body = strings.Replace(in.PullRequest.Body, "- [x] 追加・更新したテスト", "- [ ] 追加・更新したテスト", 1)
		}},
		{name: "C4 CI が失敗", id: "C4", want: Fail, mutate: func(in *Input) { in.CheckRuns[0].Conclusion = "failure" }},
		{name: "C4 CI が実行中", id: "C4", want: Pending, mutate: func(in *Input) { in.CheckRuns[0].Status = "in_progress" }},
		{name: "C9 コンフリクト", id: "C9", want: Fail, mutate: func(in *Input) { in.PullRequest.Mergeable = ptr(false) }},
		{name: "C10 遅れている", id: "C10", want: Fail, mutate: func(in *Input) { in.Compare.BehindBy = 2 }},
		{name: "C11 変更予定ファイルの外", id: "C11", want: Fail, mutate: func(in *Input) {
			in.Issue.Body = strings.Replace(in.Issue.Body, "- `docs/**`\n", "", 1)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := smallInput(t)
			tt.mutate(&in)
			rep := Evaluate(smallConfig(t), in)
			assertSkipped(t, rep, true)
			r, _ := resultOf(rep, tt.id)
			if r.Status != tt.want {
				t.Errorf("%s = %s（%s）, want %s", tt.id, r.Status, r.Detail, tt.want)
			}
			if rep.Verdict == Pass {
				t.Errorf("verdict = pass")
			}
		})
	}
}

func TestSmallPRConditions(t *testing.T) {
	tests := []struct {
		name   string
		cfg    func(*config.Config)
		mutate func(in *Input)
		small  bool
		reason string
	}{
		{name: "docs の 1 ファイルだけ・50 行", small: true},
		{name: "テスト 1 ファイルとコード 1 ファイル", reason: "review.skip_paths に一致しない変更ファイルがある: tools/agentctl/internal/gate/gate.go",
			mutate: func(in *Input) {
				setFiles(in, 20, 5, File{Filename: "tools/agentctl/internal/gate/gate_test.go"}, File{Filename: "tools/agentctl/internal/gate/gate.go"})
			}},
		{name: "docs だけで 400 行", reason: "追加と削除が 400 行（review.skip_max_lines の 300 行を超える）",
			mutate: func(in *Input) { setFiles(in, 350, 50, File{Filename: "docs/README.md"}) }},
		{name: "ちょうど上限の 300 行", small: true,
			mutate: func(in *Input) { setFiles(in, 300, 0, File{Filename: "docs/README.md"}) }},
		{name: "docs/adr のファイル", reason: "レビューを省かないパスの変更がある: docs/adr/0020-x.md",
			mutate: func(in *Input) { setFiles(in, 10, 0, File{Filename: "docs/adr/0020-x.md"}) }},
		{name: "skip_paths に docs/adr を書いても docs/adr は省かない", reason: "レビューを省かないパスの変更がある",
			cfg:    func(c *config.Config) { c.Review.SkipPaths = []string{"docs/adr/**"} },
			mutate: func(in *Input) { setFiles(in, 10, 0, File{Filename: "docs/adr/README.md"}) }},
		{name: "保護されたパスの docs のファイル", reason: "保護されたパスの変更がある: docs/agents/workflow.md",
			cfg: func(c *config.Config) { c.ProtectedPaths = append(c.ProtectedPaths, "docs/agents/workflow.md") }},
		{name: "skip_small_prs が false", reason: "review.skip_small_prs が false",
			cfg: func(c *config.Config) { c.Review.SkipSmallPRs = false }},
		{name: "変更ファイルの一覧が不完全", reason: "変更ファイルの一覧が不完全（2 件中 1 件）",
			mutate: func(in *Input) { in.PullRequest.ChangedFiles = 2 }},
		{name: "変更ファイルの一覧が空", reason: "変更ファイルの一覧が不完全（0 件中 0 件）",
			mutate: func(in *Input) { setFiles(in, 0, 0) }},
		{name: "行数が分からない", reason: "追加と削除の行数が分からない",
			mutate: func(in *Input) { in.PullRequest.Additions = nil }},
		{name: "行数が不正", reason: "追加と削除の行数が不正",
			mutate: func(in *Input) { in.PullRequest.Deletions = ptr(-1) }},
		{name: "名前の変更: 前と後が docs", small: true,
			mutate: func(in *Input) {
				setFiles(in, 0, 0, File{Filename: "docs/b.md", PreviousFilename: "docs/a.md", Status: "renamed"})
			}},
		{name: "名前の変更: 前がコード", reason: "review.skip_paths に一致しない変更ファイルがある: tools/agentctl/a.md",
			mutate: func(in *Input) {
				setFiles(in, 0, 0, File{Filename: "docs/a.md", PreviousFilename: "tools/agentctl/a.md", Status: "renamed"})
			}},
		{name: "名前の変更: 前が docs/adr", reason: "レビューを省かないパスの変更がある: docs/adr/a.md",
			mutate: func(in *Input) {
				setFiles(in, 0, 0, File{Filename: "docs/a.md", PreviousFilename: "docs/adr/a.md", Status: "renamed"})
			}},
		{name: "テスト・testdata・fixtures だけ", small: true,
			mutate: func(in *Input) {
				setFiles(in, 100, 20,
					File{Filename: "server/internal/sim/engine_test.go"}, File{Filename: "web/src/app.test.ts"},
					File{Filename: "web/src/view.test.tsx"}, File{Filename: "tools/x/testdata/a.json"},
					File{Filename: "web/test/fixtures/b.json"})
			}},
		{name: "skip_paths を設定で変える", reason: "review.skip_paths に一致しない変更ファイルがある: docs/agents/workflow.md",
			cfg: func(c *config.Config) { c.Review.SkipPaths = []string{"**/*_test.go"} }},
		{name: "skip_max_lines を設定で変える", reason: "追加と削除が 50 行（review.skip_max_lines の 49 行を超える）",
			cfg: func(c *config.Config) { c.Review.SkipMaxLines = ptr(49) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := smallConfig(t)
			if tt.cfg != nil {
				tt.cfg(cfg)
			}
			in := smallInput(t)
			if tt.mutate != nil {
				tt.mutate(&in)
			}
			ok, reason := SmallPR(cfg, in)
			if ok != tt.small {
				t.Fatalf("small = %v（%s）, want %v", ok, reason, tt.small)
			}
			if tt.reason != "" && !strings.Contains(reason, tt.reason) {
				t.Errorf("reason = %q, want %q を含む", reason, tt.reason)
			}
			// 小さな PR でなければ、今までどおりレビューを求める（レビュー記録がないので C5〜C8 が pending）。
			assertSkipped(t, Evaluate(cfg, in), tt.small)
		})
	}
}

// 小さな PR でなければ、レビュー記録があるときの判定（pass）も今までどおり。
func TestNotSmallPRUsesReview(t *testing.T) {
	cfg := smallConfig(t)
	in := loadInput(t) // tools/agentctl/** などの変更（保護されたパスを含む）
	in.PullRequest.Additions, in.PullRequest.Deletions = ptr(1), ptr(1)
	rep := Evaluate(cfg, in)
	if rep.Verdict != Pass {
		t.Fatalf("verdict = %s, results = %+v", rep.Verdict, rep.Results)
	}
	for _, id := range reviewIDs {
		if r, _ := resultOf(rep, id); strings.Contains(r.Detail, "小さな PR") {
			t.Errorf("%s: %s", id, r.Detail)
		}
	}
}

func TestSmallPRNilConfig(t *testing.T) {
	if ok, _ := SmallPR(nil, Input{}); ok {
		t.Error("設定がないのに小さな PR にした")
	}
}
