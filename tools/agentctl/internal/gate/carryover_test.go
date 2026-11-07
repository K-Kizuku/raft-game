package gate

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/config"
	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/review"
)

// carry-over.json の SHA。HEAD（1111…）は S（3333…）に main の先頭（4444…）をマージしたコミット。
const (
	carrySHA = "3333333333333333333333333333333333333333"
	mainSHA  = "4444444444444444444444444444444444444444"
	otherSHA = "5555555555555555555555555555555555555555"
	authSHA  = "6666666666666666666666666666666666666666"
)

// loadCarry は、main の取り込みだけで差分が変わらない PR の入力（testdata/carry-over.json）を返す。
func loadCarry(t *testing.T) Input {
	t.Helper()
	data, err := os.ReadFile("testdata/carry-over.json")
	if err != nil {
		t.Fatal(err)
	}
	var in Input
	if err := json.Unmarshal(data, &in); err != nil {
		t.Fatal(err)
	}
	return in
}

func notesOf(rep Report) string { return strings.Join(rep.Notes, "\n") }

// (a) main の取り込みだけ → C6 が pass（引き継ぎの detail が出る）。
func TestCarryOverBaseMergeOnly(t *testing.T) {
	rep := Evaluate(loadConfig(t), loadCarry(t))
	if rep.Verdict != Pass {
		t.Fatalf("verdict = %s, results = %+v, notes = %v", rep.Verdict, rep.Results, rep.Notes)
	}
	c6 := mustResult(t, rep, "C6", Pass)
	for _, want := range []string{"round 2", "3333333", "引き継いだ", "main の取り込みだけ", "1111111"} {
		if !strings.Contains(c6.Detail, want) {
			t.Errorf("C6 の detail %q に %q がない", c6.Detail, want)
		}
	}
	mustResult(t, rep, "C7", Pass)
	mustResult(t, rep, "C12", Pass)
	// コメントにも、引き継いだことと S の SHA が出る。
	if body := rep.RenderComment(""); !strings.Contains(body, "round 2（3333333）を引き継いだ") {
		t.Errorf("comment = %s", body)
	}
	if co := CarriesOver(loadConfig(t), loadCarry(t)); !co.OK || co.SHA != carrySHA || co.Round != 2 || co.Codex {
		t.Errorf("CarriesOver = %+v", co)
	}
}

func TestCarryOverRejected(t *testing.T) {
	tests := []struct {
		name   string
		cfg    func(*config.Config)
		mutate func(t *testing.T, in *Input)
		// reason は、引き継がない理由（notes）に含まれる文字列。
		reason string
		// c6 は C6 の結果（既定は pending）。
		c6 Verdict
	}{
		// (b) S のあとに作者のコミット
		{name: "(b) S のあとに作者のコミット", reason: "マージではないコミット 6666666",
			mutate: func(t *testing.T, in *Input) {
				in.CarryOver.Commits = []Commit{
					{SHA: headSHA, Parents: []string{authSHA, mainSHA}},
					{SHA: authSHA, Parents: []string{carrySHA}},
				}
			}},
		{name: "(b) main の取り込みと作者のコミットが混ざっている", reason: "マージではないコミット 6666666",
			mutate: func(t *testing.T, in *Input) {
				in.CarryOver.Commits = []Commit{
					{SHA: headSHA, Parents: []string{otherSHA, mainSHA}},
					{SHA: otherSHA, Parents: []string{authSHA, mainSHA}},
					{SHA: authSHA, Parents: []string{carrySHA}},
				}
			}},
		{name: "マージの 2 番目の親が main の履歴にない", reason: "親 4444444 が main の履歴にない",
			mutate: func(t *testing.T, in *Input) { in.CarryOver.InBase = map[string]bool{mainSHA: false} }},
		{name: "マージの最初の親が main（逆向きのマージ）", reason: "親 3333333 が main の履歴にない",
			mutate: func(t *testing.T, in *Input) {
				in.CarryOver.Commits = []Commit{{SHA: headSHA, Parents: []string{mainSHA, carrySHA}}}
			}},
		{name: "たどっても S に着かない", reason: "3333333 に着かない",
			mutate: func(t *testing.T, in *Input) {
				in.CarryOver.Commits = []Commit{{SHA: headSHA, Parents: []string{otherSHA, mainSHA}}}
			}},
		{name: "コミットの並びが HEAD からではない", reason: "HEAD から最初の親をたどった",
			mutate: func(t *testing.T, in *Input) {
				in.CarryOver.Commits = []Commit{{SHA: otherSHA, Parents: []string{carrySHA, mainSHA}}}
			}},
		{name: "コミットがない", reason: "コミットがない",
			mutate: func(t *testing.T, in *Input) { in.CarryOver.Commits = nil }},
		{name: "コミットが多すぎる", reason: "多すぎる",
			mutate: func(t *testing.T, in *Input) {
				in.CarryOver.Commits = make([]Commit, MaxCarryOverCommits+1)
			}},
		// (c) コンフリクトの解消で差分が変わった
		{name: "(c) コンフリクトの解消で差分が変わった", reason: "docs/guide.md の差分が違う",
			mutate: func(t *testing.T, in *Input) {
				in.Files[0].Patch = strings.Replace(in.Files[0].Patch, "+PR が足した行", "+PR が足した行（解消で変えた）", 1)
			}},
		{name: "(c) コンフリクトの解消で変更行が増えた", reason: "docs/guide.md の差分が違う",
			mutate: func(t *testing.T, in *Input) { in.Files[0].Patch += "\n+足した行" }},
		{name: "変更ファイルの数が違う", reason: "変更ファイルの数が違う",
			mutate: func(t *testing.T, in *Input) {
				in.Files = append(in.Files, File{Filename: "docs/more.md", Status: "added", Patch: "@@ -0,0 +1 @@\n+x"})
				in.PullRequest.ChangedFiles++
			}},
		{name: "名前の変更の前の名前が違う", reason: "変更ファイルの一覧が違う",
			mutate: func(t *testing.T, in *Input) { in.Files[2].PreviousFilename = "docs/older.md" }},
		{name: "変更の状態が違う", reason: "変更ファイルの一覧が違う",
			mutate: func(t *testing.T, in *Input) { in.Files[1].Status = "modified" }},
		{name: "HEAD の変更ファイルの一覧が不完全", reason: "不完全",
			mutate: func(t *testing.T, in *Input) { in.PullRequest.ChangedFiles = 4 }},
		{name: "S の変更ファイルが上限", reason: "件以上",
			mutate: func(t *testing.T, in *Input) {
				in.CarryOver.Files = make([]File, MaxCompareFiles)
			}},
		// (d) 保護されたパスを変える PR
		{name: "(d) 保護されたパスを変える PR", reason: "保護されたパスを変える（.agents/config.json）",
			mutate: func(t *testing.T, in *Input) {
				f := File{Filename: ".agents/config.json", Status: "modified", Patch: "@@ -1 +1 @@\n-a\n+b"}
				in.Files = append(in.Files, f)
				in.CarryOver.Files = append(in.CarryOver.Files, f)
				in.PullRequest.ChangedFiles++
			}},
		{name: "(d) 保護されたパスの名前を変える PR", reason: "保護されたパスを変える（scripts/agents/x.sh）",
			mutate: func(t *testing.T, in *Input) {
				f := File{Filename: "docs/x.sh", PreviousFilename: "scripts/agents/x.sh", Status: "renamed", Patch: "@@ -1 +1 @@\n-a\n+b"}
				in.Files = append(in.Files, f)
				in.CarryOver.Files = append(in.CarryOver.Files, f)
				in.PullRequest.ChangedFiles++
			}},
		{name: "(d) S で保護されたパスを変えていた", reason: "3333333 で保護されたパスを変えている",
			mutate: func(t *testing.T, in *Input) {
				in.CarryOver.Files[0].Filename = "tools/agentctl/main.go"
			}},
		// (e) 設定が false
		{name: "(e) 設定が false", reason: "review.carry_over_on_base_merge が false",
			cfg: func(c *config.Config) { c.Review.CarryOverOnBaseMerge = false }},
		// (f) patch がないファイル
		{name: "(f) HEAD に patch がないファイル", reason: "server/internal/foo/foo.go の patch がない",
			mutate: func(t *testing.T, in *Input) { in.Files[1].Patch = "" }},
		{name: "(f) S に patch がないファイル", reason: "docs/new.md の patch がない",
			mutate: func(t *testing.T, in *Input) { in.CarryOver.Files[1].Patch = "" }},
		// (g) S が request_changes
		{name: "(g) S の記録が request_changes", reason: "verdict が request_changes",
			mutate: func(t *testing.T, in *Input) {
				in.Comments = []Comment{commentOf(t, 1003, "K-Kizuku", makeRecord(2, carrySHA, "request_changes",
					[]review.Finding{finding("F2", "medium", "open")}))}
			}},
		{name: "S の記録が approve でも重大な指摘が open（C7）", reason: "C7 を満たさない",
			mutate: func(t *testing.T, in *Input) {
				in.Comments = []Comment{commentOf(t, 1003, "K-Kizuku", makeRecord(2, carrySHA, "approve",
					[]review.Finding{finding("F2", "high", "open")}))}
			}},
		{name: "S の記録が前の重大な指摘を resolved にしていない（C7）", reason: "C7 を満たさない",
			mutate: func(t *testing.T, in *Input) {
				in.Comments = []Comment{
					in.Comments[0],
					commentOf(t, 1003, "K-Kizuku", makeRecord(2, carrySHA, "approve", nil)),
				}
			}},
		{name: "S の記録のレビュー役が不正（C8）", reason: "C8 を満たさない",
			cfg: func(c *config.Config) { c.Review.AllowFallback = false },
			mutate: func(t *testing.T, in *Input) {
				in.Comments = []Comment{commentOf(t, 1003, "K-Kizuku", makeRecord(2, carrySHA, "approve", nil,
					func(r *review.Record) { r.Reviewer = "claude-fallback"; r.FallbackReason = "rate_limited" }))}
			}},
		// 記録が複数の SHA にある: 新しい request_changes が優先する（古い approve は使わない）。
		{name: "approve より新しい request_changes（別の SHA）", reason: "verdict が request_changes",
			mutate: func(t *testing.T, in *Input) {
				in.Comments = append(in.Comments, commentOf(t, 1005, "K-Kizuku", makeRecord(3, otherSHA, "request_changes",
					[]review.Finding{finding("F1", "high", "resolved"), finding("F3", "high", "open")},
					func(r *review.Record) { r.CreatedAt = "2026-11-01T13:00:00Z" })))
			}},
		{name: "approve より新しい request_changes（HEAD）", c6: Fail,
			mutate: func(t *testing.T, in *Input) {
				in.Comments = append(in.Comments, commentOf(t, 1005, "K-Kizuku", makeRecord(3, headSHA, "request_changes",
					[]review.Finding{finding("F1", "high", "resolved"), finding("F3", "high", "open")},
					func(r *review.Record) { r.CreatedAt = "2026-11-01T13:00:00Z" })))
			}},
		// 材料がない・集められなかった
		{name: "判定の材料がない", reason: "判定の材料がない",
			mutate: func(t *testing.T, in *Input) { in.CarryOver = nil }},
		{name: "判定の材料を集められなかった", reason: "集められなかった（github: 500）",
			mutate: func(t *testing.T, in *Input) { in.CarryOver.Error = "github: 500" }},
		{name: "材料の SHA が最新のレビューと違う", reason: "最新のレビューの SHA 3333333 と違う",
			mutate: func(t *testing.T, in *Input) { in.CarryOver.SHA = otherSHA }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := loadConfig(t)
			if tt.cfg != nil {
				tt.cfg(cfg)
			}
			in := loadCarry(t)
			if tt.mutate != nil {
				tt.mutate(t, &in)
			}
			rep := Evaluate(cfg, in)
			want := tt.c6
			if want == "" {
				want = Pending
			}
			c6 := mustResult(t, rep, "C6", want)
			if strings.Contains(c6.Detail, "引き継いだ") {
				t.Errorf("C6 = %+v（引き継がない）", c6)
			}
			if rep.Verdict == Pass {
				t.Errorf("verdict = pass")
			}
			if tt.reason != "" && !strings.Contains(notesOf(rep), tt.reason) {
				t.Errorf("notes = %v, want %q", rep.Notes, tt.reason)
			}
			if co := CarriesOver(cfg, in); co.OK {
				t.Errorf("CarriesOver = %+v", co)
			}
		})
	}
}

// main の取り込みで、PR が変えたファイルに main の変更が入った（文脈と行番号が変わった）が、patch-id が同じなら引き継ぐ。
// 変更行の空白の違い（git patch-id と同じ考え方）も無視する。
func TestCarryOverContextChanged(t *testing.T) {
	in := loadCarry(t)
	in.Files[0].Patch = "@@ -40,9 +40,10 @@ func x() {\n main の行\n main の行\n 文脈 B\n+PR  が足した行\t\n 別の文脈"
	in.Files[2].Patch = "@@ -3,4 +3,4 @@\n 文脈\n-古い説明\n+新しい説明\n"
	rep := Evaluate(loadConfig(t), in)
	mustResult(t, rep, "C6", Pass)
}

// 2 回の main の取り込み（マージコミットが 2 つ）でも引き継ぐ。
func TestCarryOverTwoBaseMerges(t *testing.T) {
	in := loadCarry(t)
	in.CarryOver.Commits = []Commit{
		{SHA: headSHA, Parents: []string{otherSHA, mainSHA}},
		{SHA: otherSHA, Parents: []string{carrySHA, baseSHA}},
	}
	in.CarryOver.InBase = map[string]bool{mainSHA: true, baseSHA: true}
	rep := Evaluate(loadConfig(t), in)
	mustResult(t, rep, "C6", Pass)

	// 2 つ目のマージの親がベースにないなら引き継がない。
	in.CarryOver.InBase[baseSHA] = false
	rep = Evaluate(loadConfig(t), in)
	mustResult(t, rep, "C6", Pending)
}

// 最新の記録が HEAD のものなら、引き継ぎを使わない（今までどおり）。
func TestCarryOverNotNeededForHead(t *testing.T) {
	in := loadCarry(t)
	in.Comments = append(in.Comments, commentOf(t, 1005, "K-Kizuku", makeRecord(3, headSHA, "approve",
		[]review.Finding{finding("F1", "high", "resolved")}, func(r *review.Record) { r.CreatedAt = "2026-11-01T13:00:00Z" })))
	rep := Evaluate(loadConfig(t), in)
	c6 := mustResult(t, rep, "C6", Pass)
	if strings.Contains(c6.Detail, "引き継いだ") || strings.Contains(notesOf(rep), "引き継がない") {
		t.Errorf("C6 = %+v, notes = %v", c6, rep.Notes)
	}
	if got := CarryOverCandidate(loadConfig(t), in); got != "" {
		t.Errorf("CarryOverCandidate = %q", got)
	}
}

// Codex の GitHub 連携のレビューも、同じ条件で引き継ぐ。
func TestCarryOverCodexReview(t *testing.T) {
	codexSummary := func(sha string, findings bool) Comment {
		f := "false"
		if findings {
			f = "true"
		}
		return Comment{ID: 2301, Author: codexBot, CreatedAt: "2026-11-02T09:00:00Z", UpdatedAt: "2026-11-02T09:00:00Z",
			Body: "<!-- codex-pull-request-review-summary -->\n### Codex Review\n<!-- codex-review-state:v1 " +
				`{"code_review":{"commit_sha":"` + sha + `","has_any_findings":` + f + `,"has_unresolved_findings":` + f +
				`,"status":"completed"},"security_review":{"commit_sha":null,"has_any_findings":null,"has_unresolved_findings":null,"status":null}} -->`}
	}
	cfg := loadConfig(t)

	in := loadCarry(t)
	in.Comments = append(in.Comments, codexSummary(carrySHA, false))
	if got := CarryOverCandidate(cfg, in); got != carrySHA {
		t.Errorf("CarryOverCandidate = %q", got)
	}
	rep := Evaluate(cfg, in)
	if rep.Verdict != Pass {
		t.Fatalf("verdict = %s, results = %+v, notes = %v", rep.Verdict, rep.Results, rep.Notes)
	}
	c6 := mustResult(t, rep, "C6", Pass)
	if !strings.Contains(c6.Detail, "Codex の GitHub 連携（コメント 2301）の 3333333 のレビューを引き継いだ") {
		t.Errorf("C6 = %+v", c6)
	}
	mustResult(t, rep, "C7", Pass)
	if co := CarriesOver(cfg, in); !co.OK || !co.Codex || co.SHA != carrySHA {
		t.Errorf("CarriesOver = %+v", co)
	}

	// 重大な指摘があれば引き継がない（C6・C7 は今までどおり pending）。
	in = loadCarry(t)
	in.Comments = append(in.Comments, codexSummary(carrySHA, true))
	in.ReviewComments = []ReviewComment{codexFinding(7001, codexBot, carrySHA, "P1", "壊れる")}
	rep = Evaluate(cfg, in)
	mustResult(t, rep, "C6", Pending)
	mustResult(t, rep, "C7", Pending)
	if !strings.Contains(notesOf(rep), "重大な指摘が 1 件ある") {
		t.Errorf("notes = %v", rep.Notes)
	}

	// 材料がなければ引き継がない。
	in = loadCarry(t)
	in.Comments = append(in.Comments, codexSummary(carrySHA, false))
	in.CarryOver = nil
	rep = Evaluate(cfg, in)
	mustResult(t, rep, "C6", Pending)
	mustResult(t, rep, "C7", Pending)
}

func TestCarryOverCandidate(t *testing.T) {
	cfg := loadConfig(t)
	if got := CarryOverCandidate(cfg, loadCarry(t)); got != carrySHA {
		t.Errorf("CarryOverCandidate = %q, want %s", got, carrySHA)
	}
	off := loadConfig(t)
	off.Review.CarryOverOnBaseMerge = false
	if got := CarryOverCandidate(off, loadCarry(t)); got != "" {
		t.Errorf("設定が false: %q", got)
	}
	if got := CarryOverCandidate(nil, loadCarry(t)); got != "" {
		t.Errorf("設定がない: %q", got)
	}
	in := loadCarry(t)
	in.Comments = nil
	if got := CarryOverCandidate(cfg, in); got != "" {
		t.Errorf("レビューがない: %q", got)
	}
	in = loadCarry(t)
	in.Files = append(in.Files, File{Filename: "tools/agentctl/main.go", Status: "modified"})
	if got := CarryOverCandidate(cfg, in); got != "" {
		t.Errorf("保護されたパス: %q", got)
	}
}

func TestPatchID(t *testing.T) {
	base := "@@ -1,4 +1,5 @@ func a() {\n ctx\n-old\n+new\n+more\n ctx"
	same := []struct{ name, patch string }{
		{"行番号が違う", "@@ -10,4 +12,5 @@ func b() {\n ctx\n-old\n+new\n+more\n ctx"},
		{"文脈が違う", "@@ -1,4 +1,5 @@\n other\n-old\n+new\n+more\n other2\n other3"},
		{"空白が違う", "@@ -1,4 +1,5 @@ func a() {\n ctx\n- old\t\n+n e w\n+more  \r\n ctx"},
		{"ハンクの分かれ方が違う", "@@ -1,2 +1,2 @@\n ctx\n-old\n+new\n@@ -5,1 +5,2 @@\n+more"},
	}
	for _, tt := range same {
		if PatchID(tt.patch) != PatchID(base) {
			t.Errorf("%s: patch-id が違う", tt.name)
		}
	}
	differ := []struct{ name, patch string }{
		{"変更行が違う", "@@ -1,4 +1,5 @@\n ctx\n-old\n+new2\n+more\n ctx"},
		{"追加と削除が入れ替わった", "@@ -1,4 +1,5 @@\n ctx\n+old\n-new\n-more\n ctx"},
		{"変更行の順番が違う", "@@ -1,4 +1,5 @@\n ctx\n-old\n+more\n+new\n ctx"},
		{"変更行が減った", "@@ -1,4 +1,5 @@\n ctx\n-old\n+new\n ctx"},
		{"末尾の改行がない", "@@ -1,4 +1,5 @@\n ctx\n-old\n+new\n+more\n\\ No newline at end of file"},
		{"変更行が文脈になった", "@@ -1,4 +1,5 @@\n ctx\n old\n+new\n+more\n ctx"},
	}
	for _, tt := range differ {
		if PatchID(tt.patch) == PatchID(base) {
			t.Errorf("%s: patch-id が同じ", tt.name)
		}
	}
	// 文脈の行の「\ No newline at end of file」は数えない。
	if PatchID(base+"\n\\ No newline at end of file") != PatchID(base) {
		t.Errorf("文脈の行の末尾の改行で patch-id が変わった")
	}
}
