package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/gate"
	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/issuebody"
	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/prbody"
)

const (
	sampleInput  = "internal/gate/testdata/pass.json"
	sampleConfig = "internal/gate/testdata/config.json"
	sampleReview = "internal/review/testdata/valid.json"
	repoName     = "K-Kizuku/raft-game"
	headSHA      = "1111111111111111111111111111111111111111"
)

func runCmd(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := run(args, strings.NewReader(""), &out, &errOut)
	return code, out.String(), errOut.String()
}

// writeInput は、サンプルの入力を f で書き換えて一時ファイルに書く。
func writeInput(t *testing.T, f func(in *gate.Input)) string {
	t.Helper()
	data, err := os.ReadFile(sampleInput)
	if err != nil {
		t.Fatal(err)
	}
	var in gate.Input
	if err := json.Unmarshal(data, &in); err != nil {
		t.Fatal(err)
	}
	f(&in)
	out, _ := json.Marshal(in)
	p := filepath.Join(t.TempDir(), "input.json")
	if err := os.WriteFile(p, out, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestGateInput(t *testing.T) {
	tests := []struct {
		name     string
		input    func(t *testing.T) string
		args     []string
		wantCode int
		verdict  gate.Verdict
	}{
		{"pass", func(*testing.T) string { return sampleInput }, []string{"--pr", "12"}, 0, gate.Pass},
		{"pass（--sha）", func(*testing.T) string { return sampleInput }, []string{"--sha", headSHA}, 0, gate.Pass},
		{"fail（遅れている）", func(t *testing.T) string {
			return writeInput(t, func(in *gate.Input) { in.Compare.BehindBy = 3 })
		}, []string{"--pr", "12"}, 1, gate.Fail},
		{"pending（CI が実行中）", func(t *testing.T) string {
			return writeInput(t, func(in *gate.Input) { in.CheckRuns[0].Status = "in_progress"; in.CheckRuns[0].Conclusion = "" })
		}, []string{"--pr", "12"}, 2, gate.Pending},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := append([]string{"gate", "--repo", repoName, "--config", sampleConfig, "--input", tt.input(t), "--json"}, tt.args...)
			code, out, errOut := runCmd(t, args...)
			if code != tt.wantCode {
				t.Fatalf("exit = %d, want %d\nstdout: %s\nstderr: %s", code, tt.wantCode, out, errOut)
			}
			var rep gate.Report
			if err := json.Unmarshal([]byte(out), &rep); err != nil {
				t.Fatalf("JSON を読めない: %v\n%s", err, out)
			}
			if rep.Verdict != tt.verdict || rep.HeadSHA != headSHA || len(rep.Results) != 13 {
				t.Errorf("report = %+v", rep)
			}
		})
	}
}

func TestGateTextOutput(t *testing.T) {
	code, out, _ := runCmd(t, "gate", "--repo", repoName, "--pr", "12", "--config", sampleConfig, "--input", sampleInput)
	if code != 0 || !strings.Contains(out, "merge-gate: pass") || !strings.Contains(out, "C12") {
		t.Fatalf("exit = %d\n%s", code, out)
	}
}

func TestGateInvalidConfigFailsClosed(t *testing.T) {
	dir := t.TempDir()
	data, _ := os.ReadFile(sampleConfig)
	for name, mutated := range map[string]string{
		"必須チェックが空": strings.Replace(string(data), `"required_checks": ["ci-ok"]`, `"required_checks": []`, 1),
		"未知のキー":    strings.Replace(string(data), `"version": 1,`, `"version": 1, "disable_gate": true,`, 1),
		"壊れた JSON": "{",
	} {
		t.Run(name, func(t *testing.T) {
			if mutated == string(data) {
				t.Fatal("書き換えられていない")
			}
			p := filepath.Join(dir, "config.json")
			if err := os.WriteFile(p, []byte(mutated), 0o644); err != nil {
				t.Fatal(err)
			}
			code, out, errOut := runCmd(t, "gate", "--repo", repoName, "--pr", "12", "--config", p, "--input", sampleInput, "--json")
			if code != 1 {
				t.Fatalf("exit = %d\n%s\n%s", code, out, errOut)
			}
			var rep gate.Report
			if err := json.Unmarshal([]byte(out), &rep); err != nil {
				t.Fatal(err)
			}
			if rep.Verdict != gate.Fail || rep.Results[0].ID != "G0" || rep.Results[0].Status != gate.Fail {
				t.Errorf("report = %+v", rep)
			}
		})
	}
}

func TestGateUsageErrors(t *testing.T) {
	base := []string{"gate", "--config", sampleConfig, "--input", sampleInput}
	for name, args := range map[string][]string{
		"--repo がない":       {"--pr", "12"},
		"--repo が不正":       {"--repo", "nope", "--pr", "12"},
		"--pr と --sha の両方": {"--repo", repoName, "--pr", "12", "--sha", headSHA},
		"どちらもない":           {"--repo", repoName},
		"--pr が入力と違う":      {"--repo", repoName, "--pr", "13"},
		"--sha が入力と違う":     {"--repo", repoName, "--sha", strings.Repeat("9", 40)},
		"--sha が不正":        {"--repo", repoName, "--sha", "abc"},
		"--repo が入力と違う":    {"--repo", "other/repo", "--pr", "12"},
		"--input のファイルがない": {"--repo", repoName, "--pr", "12", "--input", filepath.Join(t.TempDir(), "none.json")},
	} {
		t.Run(name, func(t *testing.T) {
			if code, out, errOut := runCmd(t, append(append([]string{}, base...), args...)...); code != 3 {
				t.Errorf("exit = %d\n%s\n%s", code, out, errOut)
			}
		})
	}
	if code, _, _ := runCmd(t); code != 3 {
		t.Error("引数なし")
	}
	if code, _, _ := runCmd(t, "nope"); code != 3 {
		t.Error("未知のコマンド")
	}
}

func TestGateInputUnknownField(t *testing.T) {
	p := filepath.Join(t.TempDir(), "in.json")
	data, _ := os.ReadFile(sampleInput)
	if err := os.WriteFile(p, bytes.Replace(data, []byte(`"repo":`), []byte(`"verdict": "pass", "repo":`), 1), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := runCmd(t, "gate", "--repo", repoName, "--pr", "12", "--config", sampleConfig, "--input", p); code != 3 {
		t.Errorf("exit = %d", code)
	}
}

func TestGateAPIErrorIsInternal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()
	t.Setenv("GITHUB_API_URL", srv.URL)
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "tok")
	code, out, errOut := runCmd(t, "gate", "--repo", repoName, "--pr", "12", "--config", sampleConfig, "--json")
	if code != 3 || out != "" {
		t.Fatalf("exit = %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
}

func TestGateNoToken(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	if code, _, _ := runCmd(t, "gate", "--repo", repoName, "--pr", "12", "--config", sampleConfig); code != 3 {
		t.Errorf("exit = %d", code)
	}
}

func TestGateSHAWithoutOpenPR(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/"+repoName+"/commits/"+headSHA+"/pulls" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()
	t.Setenv("GITHUB_API_URL", srv.URL)
	t.Setenv("GITHUB_TOKEN", "tok")
	code, _, errOut := runCmd(t, "gate", "--repo", repoName, "--sha", headSHA, "--config", sampleConfig, "--set-status")
	if code != 0 || !strings.Contains(errOut, "開いている PR がない") {
		t.Fatalf("exit = %d, stderr = %s", code, errOut)
	}
}

func TestGateSetStatusAndComment(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b bytes.Buffer
		_, _ = b.ReadFrom(r.Body)
		got = append(got, r.Method+" "+r.URL.Path+" "+b.String())
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	t.Setenv("GITHUB_API_URL", srv.URL)
	t.Setenv("GITHUB_TOKEN", "tok")
	t.Setenv("AGENTCTL_BOT_LOGIN", "")
	input := writeInput(t, func(in *gate.Input) { in.Compare.BehindBy = 1 })
	code, _, errOut := runCmd(t, "gate", "--repo", repoName, "--pr", "12", "--config", sampleConfig, "--input", input,
		"--set-status", "--comment", "--target-url", "https://example.invalid/run/1")
	if code != 1 {
		t.Fatalf("exit = %d, stderr = %s", code, errOut)
	}
	if len(got) != 2 {
		t.Fatalf("requests = %q", got)
	}
	if !strings.HasPrefix(got[0], "POST /repos/"+repoName+"/statuses/"+headSHA+" ") ||
		!strings.Contains(got[0], `"state":"failure"`) || !strings.Contains(got[0], `"context":"merge-gate"`) {
		t.Errorf("status = %s", got[0])
	}
	// サンプルには github-actions[bot] の Merge Gate のコメント（ID 1004）があるので、それを更新する。
	if !strings.HasPrefix(got[1], "PATCH /repos/"+repoName+"/issues/comments/1004 ") || !strings.Contains(got[1], "merge-gate:v1") {
		t.Errorf("comment = %s", got[1])
	}
}

func TestGateClosedPRHasNoStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("呼ばれてはいけない: %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()
	t.Setenv("GITHUB_API_URL", srv.URL)
	t.Setenv("GITHUB_TOKEN", "tok")
	input := writeInput(t, func(in *gate.Input) { in.PullRequest.State = "closed"; in.PullRequest.Merged = true })
	if code, _, _ := runCmd(t, "gate", "--repo", repoName, "--pr", "12", "--config", sampleConfig, "--input", input, "--set-status", "--comment"); code != 1 {
		t.Errorf("exit = %d", code)
	}
}

func TestReviewCommands(t *testing.T) {
	cfg := "../../.agents/config.json"
	if code, out, errOut := runCmd(t, "review", "validate", "--config", cfg, sampleReview); code != 0 || !strings.HasPrefix(out, "OK") {
		t.Fatalf("validate: exit = %d\n%s\n%s", code, out, errOut)
	}
	code, out, errOut := runCmd(t, "review", "render", "--config", cfg, sampleReview)
	if code != 0 || !strings.HasPrefix(out, "<!-- agent-review:v1 -->\n") || !strings.Contains(out, "```json\n") || !strings.Contains(out, "| F1 |") {
		t.Fatalf("render: exit = %d\n%s\n%s", code, out, errOut)
	}
	// render の出力（コメントの本文）も validate できる。
	p := filepath.Join(t.TempDir(), "comment.md")
	if err := os.WriteFile(p, []byte(out), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := runCmd(t, "review", "validate", "--config", cfg, p); code != 0 {
		t.Errorf("validate(comment): exit = %d, %s", code, errOut)
	}

	bad := filepath.Join(t.TempDir(), "bad.json")
	data, _ := os.ReadFile(sampleReview)
	if err := os.WriteFile(bad, bytes.Replace(data, []byte(`"reviewer": "codex"`), []byte(`"reviewer": "claude-fallback"`), 1), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := runCmd(t, "review", "validate", "--config", cfg, bad); code != 1 || !strings.Contains(errOut, "fallback_reason") {
		t.Errorf("フォールバックの理由なし: exit = %d, %s", code, errOut)
	}
	if code, _, _ := runCmd(t, "review", "render", "--config", cfg, bad); code != 1 {
		t.Errorf("render(bad): exit = %d", code)
	}
	if code, _, _ := runCmd(t, "review", "validate", "--config", cfg); code != 3 {
		t.Errorf("ファイルなし: exit = %d", code)
	}
	if code, _, _ := runCmd(t, "review", "nope"); code != 3 {
		t.Errorf("未知のサブコマンド: exit = %d", code)
	}
}

// TestTemplates は、Issue と PR のテンプレートが Merge Gate の読む書式どおりかを確かめる。
func TestTemplates(t *testing.T) {
	data, err := os.ReadFile("../../.github/ISSUE_TEMPLATE/agent-task.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := issuebody.Parse(string(data))
	var titles []string
	for _, s := range doc.Sections[1:] {
		titles = append(titles, s.Title)
	}
	want := []string{"背景・目的", "スコープ", "実装仕様", "技術方針・インターフェース", "変更予定ファイル", "変更禁止範囲",
		"依存 Issue", "エッジケースとエラー処理", "テストケース", "ドキュメント更新", "Acceptance Criteria", "Definition of Done"}
	if strings.Join(titles, "|") != strings.Join(want, "|") {
		t.Errorf("Issue テンプレートの節 = %q", titles)
	}
	if deps, err := doc.Dependencies(); err != nil || len(deps) != 0 {
		t.Errorf("依存 Issue = %v, %v", deps, err)
	}
	if len(doc.AcceptanceCriteria()) != 1 || len(doc.PlannedFiles()) != 1 || len(doc.ForbiddenFiles()) != 1 {
		t.Errorf("AC・変更予定ファイル・変更禁止範囲がテンプレートにない")
	}

	data, err = os.ReadFile("../../.github/pull_request_template.md")
	if err != nil {
		t.Fatal(err)
	}
	pr := prbody.Parse(strings.Replace(string(data), "#<Issue 番号>", "#3", 1))
	if refs := pr.ClosingIssues(); len(refs) != 1 || refs[0] != 3 {
		t.Errorf("Closes = %v", refs)
	}
	for _, sec := range []string{prbody.SectionAC, prbody.SectionTests, prbody.SectionDocs, "チェックリスト"} {
		if len(pr.Checkboxes(sec)) == 0 {
			t.Errorf("PR テンプレートの「%s」にチェックボックスがない", sec)
		}
	}
}

func TestReviewStdinAndDefaultConfig(t *testing.T) {
	data, _ := os.ReadFile(sampleReview)
	var out, errOut bytes.Buffer
	// --config を省略すると、親のディレクトリへたどって .agents/config.json を探す。
	if code := run([]string{"review", "validate", "-"}, bytes.NewReader(data), &out, &errOut); code != 0 {
		t.Fatalf("exit = %d, %s", code, errOut.String())
	}
}
