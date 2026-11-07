package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/gate"
	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/plan"
	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/snapshot"
)

// carryInput は、main の取り込みだけで差分が変わらない PR のスナップショット（gate のテストの (a)）。
const carryInput = "internal/gate/testdata/carry-over.json"

const (
	carryS    = "3333333333333333333333333333333333333333"
	carryMain = "4444444444444444444444444444444444444444"
)

func loadCarryInput(t *testing.T) gate.Input {
	t.Helper()
	data, err := os.ReadFile(carryInput)
	if err != nil {
		t.Fatal(err)
	}
	var in gate.Input
	if err := json.Unmarshal(data, &in); err != nil {
		t.Fatal(err)
	}
	return in
}

func TestGateCarryOverInput(t *testing.T) {
	code, out, errOut := runCmd(t, "gate", "--repo", repoName, "--pr", "12", "--config", sampleConfig, "--input", carryInput, "--json")
	if code != 0 {
		t.Fatalf("exit = %d\n%s\n%s", code, out, errOut)
	}
	var rep gate.Report
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range rep.Results {
		if r.ID == "C6" {
			found = r.Status == gate.Pass && strings.Contains(r.Detail, "round 2（3333333）を引き継いだ: main の取り込みだけ")
		}
	}
	if !found {
		t.Errorf("C6 が引き継ぎで pass になっていない: %s", out)
	}

	// 設定が false なら引き継がない（pending）。
	cfg := writeSampleConfig(t, func(s string) string {
		return strings.Replace(s, `"carry_over_on_base_merge": true`, `"carry_over_on_base_merge": false`, 1)
	})
	if code, out, _ := runCmd(t, "gate", "--repo", repoName, "--pr", "12", "--config", cfg, "--input", carryInput); code != 2 ||
		!strings.Contains(out, "carry_over_on_base_merge が false") {
		t.Errorf("exit = %d\n%s", code, out)
	}
}

// writeSampleConfig は、サンプルの設定を f で書き換えて一時ファイルに書く。
func writeSampleConfig(t *testing.T, f func(string) string) string {
	t.Helper()
	data, err := os.ReadFile(sampleConfig)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(f(string(data))), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func carrySnapshotFile(t *testing.T, withGate bool) string {
	t.Helper()
	in := loadCarryInput(t)
	p := snapshot.Pull{Number: 12, State: "open", Author: "K-Kizuku", HeadRef: "agent/issue-3", HeadSHA: headSHA, Body: "Closes #3"}
	for _, c := range in.Comments {
		p.Comments = append(p.Comments, snapshot.Comment{Author: c.Author, Body: c.Body, CreatedAt: c.CreatedAt})
	}
	if withGate {
		p.Gate = &in
	}
	s := snapshot.Snapshot{
		Issues: []snapshot.Issue{{Number: 3, Title: "t", State: "open", Labels: []string{"status:in-review"}, Body: in.Issue.Body}},
		Pulls:  []snapshot.Pull{p}, Branches: []string{"agent/issue-3"}, Statuses: map[string]map[string]string{},
	}
	data, _ := json.Marshal(s)
	path := filepath.Join(t.TempDir(), "snapshot.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func recoverJSON(t *testing.T, args ...string) []plan.Action {
	t.Helper()
	code, out, errOut := runCmd(t, append([]string{"recover", "--json"}, args...)...)
	if code != 0 {
		t.Fatalf("exit = %d\n%s", code, errOut)
	}
	var res struct {
		Actions []plan.Action `json:"actions"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	return res.Actions
}

func TestRecoverCarryOverInput(t *testing.T) {
	if acts := recoverJSON(t, "--config", sampleConfig, "--input", carrySnapshotFile(t, true)); len(acts) != 0 {
		t.Errorf("actions = %+v（引き継げる PR に request-review を出さない）", acts)
	}
	acts := recoverJSON(t, "--config", sampleConfig, "--input", carrySnapshotFile(t, false))
	if len(acts) != 1 || acts[0].Action != plan.ActionRequestReview || acts[0].PR != 12 {
		t.Errorf("actions = %+v（材料がなければ request-review）", acts)
	}
}

// carryGitHub は、recover が GitHub から読むときの偽物（Issue #3 が in-review、PR #12 は main の取り込みだけ）。
func carryGitHub(t *testing.T, failCommits bool) {
	t.Helper()
	in := loadCarryInput(t)
	type user struct {
		Login string `json:"login"`
	}
	comments := []map[string]any{}
	for _, c := range in.Comments {
		comments = append(comments, map[string]any{"id": c.ID, "user": user{c.Author}, "body": c.Body, "created_at": c.CreatedAt, "updated_at": c.UpdatedAt})
	}
	pull := map[string]any{
		"number": 12, "state": "open", "merged": false, "draft": false, "title": "t", "body": in.PullRequest.Body,
		"user": user{"K-Kizuku"}, "labels": []any{},
		"base": map[string]any{"ref": "main", "sha": carryMain}, "head": map[string]any{"ref": "agent/issue-3", "sha": headSHA},
		"mergeable": true, "mergeable_state": "blocked", "changed_files": 3,
	}
	issue := map[string]any{"number": 3, "title": "t", "state": "open", "labels": []any{map[string]any{"name": "status:in-review"}}, "body": in.Issue.Body}
	checkRuns := []any{}
	for _, cr := range in.CheckRuns {
		checkRuns = append(checkRuns, map[string]any{"name": cr.Name, "head_sha": cr.HeadSHA, "status": cr.Status, "conclusion": cr.Conclusion})
	}
	// マージベースとツリー（モードは carry-over.json の modes から）。
	const baseS, baseH = "7777777777777777777777777777777777777777", "8888888888888888888888888888888888888888"
	mergeBase := func(sha string) map[string]any {
		return map[string]any{"sha": sha, "commit": map[string]any{"tree": map[string]any{"sha": "tree-" + sha}}}
	}
	tree := func(modes map[string]string) map[string]any {
		entries := []any{}
		for p, m := range modes {
			entries = append(entries, map[string]any{"path": p, "mode": m, "type": "blob"})
		}
		return map[string]any{"truncated": false, "tree": entries}
	}
	m := in.CarryOver.Modes
	trees := map[string]map[string]any{
		"tree-" + baseS: tree(m.S.Base), "tree-" + carryS: tree(m.S.Tip),
		"tree-" + baseH: tree(m.Head.Base), "tree-" + headSHA: tree(m.Head.Tip),
	}
	pre := "/repos/" + repoName
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		write := func(v any) { _ = json.NewEncoder(w).Encode(v) }
		switch p := r.URL.Path; p {
		case pre + "/issues":
			write([]any{issue})
		case pre + "/issues/3":
			write(issue)
		case pre + "/issues/3/comments", pre + "/pulls/12/reviews", pre + "/pulls/12/comments":
			write([]any{})
		case pre + "/pulls":
			write([]any{pull})
		case pre + "/pulls/12":
			write(pull)
		case pre + "/issues/12/comments":
			write(comments)
		case pre + "/pulls/12/files":
			write(in.Files)
		case pre + "/commits/" + headSHA + "/status":
			write(map[string]any{"statuses": []any{}})
		case pre + "/commits/" + headSHA + "/check-runs":
			write(map[string]any{"check_runs": checkRuns})
		case pre + "/git/matching-refs/heads/agent/issue-":
			write([]any{map[string]any{"ref": "refs/heads/agent/issue-3"}})
		case pre + "/compare/main..." + headSHA:
			write(map[string]any{"status": in.Compare.Status, "ahead_by": in.Compare.AheadBy, "behind_by": in.Compare.BehindBy,
				"merge_base_commit": mergeBase(baseH)})
		case pre + "/git/commits/" + headSHA:
			if failCommits {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			write(map[string]any{"sha": headSHA, "tree": map[string]any{"sha": "tree-" + headSHA},
				"parents": []any{map[string]any{"sha": carryS}, map[string]any{"sha": carryMain}}})
		case pre + "/git/commits/" + carryS:
			write(map[string]any{"sha": carryS, "tree": map[string]any{"sha": "tree-" + carryS}, "parents": []any{map[string]any{"sha": baseS}}})
		case pre + "/compare/main..." + carryMain:
			write(map[string]any{"status": "identical", "ahead_by": 0, "behind_by": 0})
		case pre + "/compare/main..." + carryS:
			write(map[string]any{"status": "behind", "ahead_by": 1, "behind_by": 1, "files": in.CarryOver.Files, "merge_base_commit": mergeBase(baseS)})
		default:
			if t, ok := trees[strings.TrimPrefix(p, pre+"/git/trees/")]; ok && r.URL.Query().Get("recursive") == "1" {
				write(t)
				return
			}
			t.Errorf("想定外の要求 %s %s", r.Method, p)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("GITHUB_API_URL", srv.URL)
	t.Setenv("GITHUB_TOKEN", "tok")
	old := gitRunner
	t.Cleanup(func() { gitRunner = old })
	gitRunner = func(_ context.Context, dir string, args ...string) (string, error) {
		return "worktree /r\nbranch refs/heads/main\n", nil
	}
}

// TestRecoverCarryOverFetch は、--input がないとき、request-review の PR について引き継ぎの材料を GitHub から集めることを確かめる。
func TestRecoverCarryOverFetch(t *testing.T) {
	carryGitHub(t, false)
	if acts := recoverJSON(t, "--repo", repoName, "--config", sampleConfig); len(acts) != 0 {
		t.Errorf("actions = %+v（引き継げる PR に request-review を出さない）", acts)
	}
}

func TestRecoverCarryOverFetchError(t *testing.T) {
	carryGitHub(t, true)
	acts := recoverJSON(t, "--repo", repoName, "--config", sampleConfig)
	if len(acts) != 1 || acts[0].Action != plan.ActionRequestReview || acts[0].PR != 12 {
		t.Errorf("actions = %+v（材料を集められなければ request-review）", acts)
	}
}
