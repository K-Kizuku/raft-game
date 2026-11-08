package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/gate"
	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/review"
)

const codexBot = "chatgpt-codex-connector[bot]"

// codexView は、pass.json に testdata/codex-pr80.json（Codex の出力）を加えた gate.Input を返す。
func codexView(t *testing.T) gate.Input {
	t.Helper()
	data, err := os.ReadFile(sampleInput)
	if err != nil {
		t.Fatal(err)
	}
	var in gate.Input
	if err := json.Unmarshal(data, &in); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile("internal/gate/testdata/codex-pr80.json")
	if err != nil {
		t.Fatal(err)
	}
	var cd struct {
		Comments       []gate.Comment       `json:"comments"`
		Reviews        []gate.PullReview    `json:"reviews"`
		ReviewComments []gate.ReviewComment `json:"review_comments"`
	}
	if err := json.Unmarshal(data, &cd); err != nil {
		t.Fatal(err)
	}
	in.Comments = append(in.Comments, cd.Comments...)
	in.Reviews, in.ReviewComments = cd.Reviews, cd.ReviewComments
	return in
}

func writeViewJSON(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "view.json")
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func fixedNow(t *testing.T) {
	t.Helper()
	old := nowFunc
	nowFunc = func() time.Time { return time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { nowFunc = old })
}

func TestPRCodexRecordFromInput(t *testing.T) {
	fixedNow(t)
	in := codexView(t)
	// HEAD のレビューで指摘を出したので、要約は has_any_findings: true（実際の Codex の出力と同じ）。
	for i := range in.Comments {
		in.Comments[i].Body = strings.Replace(in.Comments[i].Body,
			`"has_any_findings":false,"has_unresolved_findings":false`, `"has_any_findings":true,"has_unresolved_findings":true`, 1)
	}
	in.ReviewComments = append(in.ReviewComments, gate.ReviewComment{
		ID: 4700, Author: codexBot, CommitID: headSHA, OriginalCommitID: headSHA, Path: "a.go", Line: 3,
		Body: "**<sub><sub>![P2 Badge](https://img.shields.io/badge/P2-yellow?style=flat)</sub></sub>  軽い指摘**\n\n説明\n\n<!-- codex-code-review-finding:v1 -->",
	})
	view := writeViewJSON(t, in)
	code, out, errOut := runCmd(t, "pr", "codex-record", "--repo", repoName, "--pr", "12", "--config", sampleConfig, "--input", view)
	if code != 0 {
		t.Fatalf("exit = %d\n%s\n%s", code, out, errOut)
	}
	rec, err := review.Parse([]byte(out))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	// 前回は pass.json の round 2 の記録。
	if rec.Reviewer != "codex" || rec.ReviewerVersion != "chatgpt-codex-connector (GitHub)" || rec.Round != 3 ||
		rec.HeadSHA != headSHA || rec.Verdict != "approve" || rec.CreatedAt != "2026-11-06T00:00:00Z" {
		t.Errorf("record = %+v", rec)
	}
	found := false
	for _, f := range rec.Findings {
		if f.ID == "C4700" && f.Severity == "medium" && f.Status == "open" {
			found = true
		}
		// 古い SHA の P2（C4101）は、要約に未解決の指摘があり、最新の receipt が重大な指摘なしなので、
		// 重大でない指摘として open のまま引き継ぐ（Issue #213）。重大な指摘にはしない。
		if f.ID == "C4101" && (f.Severity != "medium" || f.Status != "open" || !strings.Contains(f.Title, "引き継いだ")) {
			t.Errorf("古い SHA の指摘 = %+v", f)
		}
	}
	if !found {
		t.Errorf("findings = %+v", rec.Findings)
	}

	// --previous を渡すと、その round の次になる。
	prev := makePrev(t, 7)
	code, out, errOut = runCmd(t, "pr", "codex-record", "--repo", repoName, "--pr", "12", "--config", sampleConfig, "--input", view, "--previous", prev)
	if code != 0 || !strings.Contains(out, `"round": 8`) {
		t.Fatalf("exit = %d\n%s\n%s", code, out, errOut)
	}
}

func makePrev(t *testing.T, round int) string {
	t.Helper()
	data, err := os.ReadFile(sampleReview)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := review.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	rec.Round = round
	body, err := review.Render(rec)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "prev.md")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPRCodexRecordPending(t *testing.T) {
	in := codexView(t)
	view := writeViewJSON(t, in)
	// 別の SHA（HEAD が進んだ）を待つ。
	code, _, errOut := runCmd(t, "pr", "codex-record", "--repo", repoName, "--pr", "12", "--config", sampleConfig, "--input", view,
		"--sha", strings.Repeat("4", 40))
	if code != 2 || !strings.Contains(errOut, "1111111") {
		t.Errorf("exit = %d, %s", code, errOut)
	}
	// 要約のコメントがない（receipt だけ）。
	var cs []gate.Comment
	for _, c := range in.Comments {
		if !strings.Contains(c.Body, "codex-review-state") {
			cs = append(cs, c)
		}
	}
	in.Comments = cs
	if code, _, errOut := runCmd(t, "pr", "codex-record", "--repo", repoName, "--pr", "12", "--config", sampleConfig, "--input", writeViewJSON(t, in)); code != 2 {
		t.Errorf("exit = %d, %s", code, errOut)
	}
}

func TestPRCodexRecordAfter(t *testing.T) {
	// 依頼する前の要約のコメントの updated_at（--after）より新しいものだけを完了とみなす。
	fixedNow(t)
	view := writeViewJSON(t, codexView(t))
	base := []string{"pr", "codex-record", "--repo", repoName, "--pr", "12", "--config", sampleConfig, "--input", view}
	if code, _, errOut := runCmd(t, append(base, "--after", "2026-11-05T10:30:00Z")...); code != 2 || !strings.Contains(errOut, "2101") {
		t.Errorf("同じ updated_at: exit = %d, %s", code, errOut)
	}
	if code, _, errOut := runCmd(t, append(base, "--after", "2026-11-05T11:00:00Z")...); code != 2 {
		t.Errorf("後の時刻: exit = %d, %s", code, errOut)
	}
	if code, out, errOut := runCmd(t, append(base, "--after", "2026-11-05T10:29:59Z")...); code != 0 || !strings.Contains(out, `"reviewer": "codex"`) {
		t.Errorf("前の時刻: exit = %d\n%s\n%s", code, out, errOut)
	}
	if code, _, _ := runCmd(t, append(base, "--after", "yesterday")...); code != 3 {
		t.Errorf("不正な --after: exit = %d", code)
	}
}

func TestPRProtectedFiles(t *testing.T) {
	in := codexView(t)
	base := []string{"pr", "protected-files", "--repo", repoName, "--pr", "12", "--config", sampleConfig}
	code, out, errOut := runCmd(t, append(base, "--input", writeViewJSON(t, in))...)
	if code != 0 || out != "tools/agentctl/main.go\ntools/agentctl/internal/gate/gate.go\n" {
		t.Errorf("exit = %d\n%q\n%s", code, out, errOut)
	}
	// 名前を変えたファイルは前の名前も見る。
	in.Files = []gate.File{{Filename: "docs/x.md", PreviousFilename: "scripts/agents/x.md", Status: "renamed"}, {Filename: "README.md"}}
	if code, out, _ := runCmd(t, append(base, "--input", writeViewJSON(t, in))...); code != 0 || out != "scripts/agents/x.md\n" {
		t.Errorf("renamed: exit = %d, %q", code, out)
	}
	in.Files = []gate.File{{Filename: "README.md"}}
	if code, out, _ := runCmd(t, append(base, "--input", writeViewJSON(t, in))...); code != 0 || out != "" {
		t.Errorf("なし: exit = %d, %q", code, out)
	}
	// GitHub の REST（GET /pulls/{n}/files）から読む。
	githubFake(t, codexView(t))
	if code, out, errOut := runCmd(t, base...); code != 0 || !strings.HasPrefix(out, "tools/agentctl/main.go\n") {
		t.Errorf("API: exit = %d\n%q\n%s", code, out, errOut)
	}
	if code, _, _ := runCmd(t, "pr", "protected-files", "--repo", repoName, "--pr", "13", "--config", sampleConfig); code != 3 {
		t.Errorf("存在しない PR: exit = %d", code)
	}
	if code, _, _ := runCmd(t, append(base, "x")...); code != 3 {
		t.Errorf("余計な引数: exit = %d", code)
	}
}

func TestPRCodexRecordErrors(t *testing.T) {
	view := writeViewJSON(t, codexView(t))
	for name, args := range map[string][]string{
		"--repo がない":     {"--pr", "12", "--input", view},
		"--pr がない":       {"--repo", repoName, "--input", view},
		"--sha が不正":      {"--repo", repoName, "--pr", "12", "--input", view, "--sha", "abc"},
		"--pr が入力と違う":    {"--repo", repoName, "--pr", "13", "--input", view},
		"--previous がない": {"--repo", repoName, "--pr", "12", "--input", view, "--previous", filepath.Join(t.TempDir(), "x")},
		"余計な引数":          {"--repo", repoName, "--pr", "12", "--input", view, "x"},
	} {
		t.Run(name, func(t *testing.T) {
			args = append([]string{"pr", "codex-record", "--config", sampleConfig}, args...)
			if code, out, errOut := runCmd(t, args...); code != 3 {
				t.Errorf("exit = %d\n%s\n%s", code, out, errOut)
			}
		})
	}
	if code, _, _ := runCmd(t, "pr"); code != 3 {
		t.Error("pr だけ")
	}
	if code, _, _ := runCmd(t, "pr", "nope"); code != 3 {
		t.Error("未知のサブコマンド")
	}
}

// githubFake は、PR #12 の REST API（読み取りとコメントの作成）の偽物。
func githubFake(t *testing.T, in gate.Input) (*httptest.Server, *[]string) {
	t.Helper()
	var posted []string
	pre := "/repos/" + repoName
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		enc := json.NewEncoder(w)
		if r.Method == http.MethodPost && r.URL.Path == pre+"/issues/12/comments" {
			b, _ := io.ReadAll(r.Body)
			posted = append(posted, string(b))
			w.WriteHeader(http.StatusCreated)
			_ = enc.Encode(map[string]any{"id": 777})
			return
		}
		user := func(l string) map[string]any { return map[string]any{"login": l} }
		switch r.URL.Path {
		case pre + "/pulls/12":
			p := in.PullRequest
			_ = enc.Encode(map[string]any{"number": p.Number, "state": p.State, "body": p.Body, "user": user(p.Author),
				"base": map[string]any{"ref": p.BaseRef, "sha": p.BaseSHA}, "head": map[string]any{"ref": p.HeadRef, "sha": p.HeadSHA}})
		case pre + "/issues/12/comments":
			var out []any
			for _, c := range in.Comments {
				out = append(out, map[string]any{"id": c.ID, "user": user(c.Author), "body": c.Body, "created_at": c.CreatedAt, "updated_at": c.UpdatedAt})
			}
			_ = enc.Encode(out)
		case pre + "/pulls/12/reviews":
			var out []any
			for _, c := range in.Reviews {
				out = append(out, map[string]any{"id": c.ID, "user": user(c.Author), "commit_id": c.CommitID, "body": c.Body, "submitted_at": c.SubmittedAt})
			}
			_ = enc.Encode(out)
		case pre + "/pulls/12/comments":
			var out []any
			for _, c := range in.ReviewComments {
				out = append(out, map[string]any{"id": c.ID, "user": user(c.Author), "commit_id": c.CommitID, "original_commit_id": c.OriginalCommitID,
					"path": c.Path, "line": c.Line, "body": c.Body, "created_at": c.CreatedAt})
			}
			_ = enc.Encode(out)
		case pre + "/pulls/12/files":
			_ = enc.Encode(in.Files)
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"Not Found"}`))
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("GITHUB_API_URL", srv.URL)
	t.Setenv("GITHUB_TOKEN", "tok")
	return srv, &posted
}

func TestPRViewAndCodexRecordFromAPI(t *testing.T) {
	fixedNow(t)
	in := codexView(t)
	githubFake(t, in)
	code, out, errOut := runCmd(t, "pr", "view", "--repo", repoName, "--pr", "12")
	if code != 0 {
		t.Fatalf("exit = %d\n%s", code, errOut)
	}
	var got gate.Input
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if got.PullRequest.HeadSHA != headSHA || len(got.Comments) != len(in.Comments) || len(got.Reviews) != 1 ||
		len(got.ReviewComments) != 1 || got.ReviewComments[0].Author != codexBot || got.ReviewComments[0].Line != 30 {
		t.Errorf("view = %+v", got)
	}
	// pr view の出力は、そのまま codex-record の --input に使える。
	view := filepath.Join(t.TempDir(), "view.json")
	if err := os.WriteFile(view, []byte(out), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := runCmd(t, "pr", "codex-record", "--repo", repoName, "--pr", "12", "--config", sampleConfig, "--input", view); code != 0 {
		t.Errorf("codex-record(--input view): exit = %d, %s", code, errOut)
	}
	code, out, errOut = runCmd(t, "pr", "codex-record", "--repo", repoName, "--pr", "12", "--config", sampleConfig)
	if code != 0 || !strings.Contains(out, `"reviewer": "codex"`) {
		t.Fatalf("codex-record: exit = %d\n%s\n%s", code, out, errOut)
	}
	if code, _, _ := runCmd(t, "pr", "view", "--repo", repoName, "--pr", "13"); code != 3 {
		t.Errorf("存在しない PR: exit = %d", code)
	}
}

func TestComment(t *testing.T) {
	_, posted := githubFake(t, codexView(t))
	code, out, errOut := runCmd(t, "comment", "--repo", repoName, "--pr", "12", "--body", "@codex review")
	if code != 0 || strings.TrimSpace(out) != `{"id":777}` {
		t.Fatalf("exit = %d\n%s\n%s", code, out, errOut)
	}
	p := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(p, []byte("本文\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := runCmd(t, "comment", "--repo", repoName, "--pr", "12", "--body-file", p); code != 0 {
		t.Fatalf("--body-file: exit = %d, %s", code, errOut)
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"comment", "--repo", repoName, "--pr", "12", "--body-file", "-"}, strings.NewReader("標準入力"), &stdout, &stderr); code != 0 {
		t.Fatalf("--body-file -: exit = %d, %s", code, stderr.String())
	}
	want := []string{`{"body":"@codex review"}`, `{"body":"本文\n"}`, `{"body":"標準入力"}`}
	if len(*posted) != len(want) {
		t.Fatalf("posted = %q", *posted)
	}
	for i, w := range want {
		if strings.TrimSpace((*posted)[i]) != w {
			t.Errorf("posted[%d] = %s, want %s", i, (*posted)[i], w)
		}
	}
	for name, args := range map[string][]string{
		"本文がない":      {"--repo", repoName, "--pr", "12"},
		"両方":         {"--repo", repoName, "--pr", "12", "--body", "a", "--body-file", p},
		"空の本文":       {"--repo", repoName, "--pr", "12", "--body", "  "},
		"--pr がない":   {"--repo", repoName, "--body", "a"},
		"--repo が不正": {"--repo", "x", "--pr", "12", "--body", "a"},
	} {
		if code, _, _ := runCmd(t, append([]string{"comment"}, args...)...); code != 3 {
			t.Errorf("%s: exit = %d", name, code)
		}
	}
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	if code, _, _ := runCmd(t, "comment", "--repo", repoName, "--pr", "12", "--body", "a"); code != 3 {
		t.Errorf("トークンなし: exit = %d", code)
	}
}
