package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/plan"
	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/snapshot"
	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/state"
)

const (
	sampleSnapshot = "internal/snapshot/testdata/snapshot.json"
	repoConfig     = "../../.agents/config.json"
	session4       = "5f0c8a52-9d3e-4c1b-8e2a-0f6b7c1d2e34"
)

func fixClock(t *testing.T) {
	t.Helper()
	old := now
	now = func() time.Time { return time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { now = old })
}

type apiOp struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	Body   any    `json:"body,omitempty"`
}

// fakeGitHub は書き込みの要求を記録する。failAt 番目（1 始まり）の要求は 502 にする。
func fakeGitHub(t *testing.T, failAt int) (*[]apiOp, func()) {
	t.Helper()
	var mu sync.Mutex
	var got []apiOp
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		op := apiOp{Method: r.Method, Path: r.URL.Path}
		data, _ := io.ReadAll(r.Body)
		if len(data) > 0 {
			if err := json.Unmarshal(data, &op.Body); err != nil {
				t.Errorf("本文が JSON ではない: %s", data)
			}
		}
		got = append(got, op)
		if len(got) == failAt {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[]`))
	}))
	t.Setenv("GITHUB_API_URL", srv.URL)
	t.Setenv("GITHUB_TOKEN", "tok")
	return &got, srv.Close
}

func dryRunOps(t *testing.T, out string) []apiOp {
	t.Helper()
	var v struct {
		Operations []apiOp `json:"operations"`
	}
	dec := json.NewDecoder(strings.NewReader(out))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("dry-run の出力を読めない: %v\n%s", err, out)
	}
	return v.Operations
}

// writeSnapshot は、サンプルを f で書き換えて一時ファイルに書く。
func writeSnapshot(t *testing.T, f func(s *snapshot.Snapshot)) string {
	t.Helper()
	s, err := snapshot.Load(sampleSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	f(s)
	data, _ := json.Marshal(s)
	p := filepath.Join(t.TempDir(), "snapshot.json")
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPlanCommand(t *testing.T) {
	code, out, errOut := runCmd(t, "plan", "--config", repoConfig, "--input", sampleSnapshot, "--json")
	if code != 0 {
		t.Fatalf("exit = %d\n%s", code, errOut)
	}
	var res plan.Result
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	ns := func(items []plan.Item) []int {
		o := []int{}
		for _, it := range items {
			o = append(o, it.Number)
		}
		return o
	}
	// max_workers 2 − in-progress 1 = 1。#5 を選び、#6 は依存待ち、#7 は #4 と競合。
	if res.Capacity != 1 || !reflect.DeepEqual(ns(res.Ready), []int{5}) || !reflect.DeepEqual(ns(res.BlockedByDeps), []int{6}) ||
		!reflect.DeepEqual(ns(res.Conflicts), []int{7}) || !reflect.DeepEqual(ns(res.Active), []int{4, 8}) {
		t.Errorf("plan = %s", out)
	}
	for _, key := range []string{`"ready"`, `"blocked_by_deps"`, `"conflicts"`, `"active"`, `"capacity"`} {
		if !strings.Contains(out, key) {
			t.Errorf("%s がない", key)
		}
	}

	code, out, _ = runCmd(t, "plan", "--config", repoConfig, "--input", sampleSnapshot, "--mermaid")
	if code != 0 || !strings.HasPrefix(out, "flowchart TD\n") || !strings.Contains(out, "i3 --> i4") {
		t.Errorf("mermaid: exit = %d\n%s", code, out)
	}
	code, out, _ = runCmd(t, "plan", "--config", repoConfig, "--input", sampleSnapshot)
	if code != 0 || !strings.Contains(out, "capacity: 1") || !strings.Contains(out, "#5 [ready]") {
		t.Errorf("text: exit = %d\n%s", code, out)
	}

	cyc := writeSnapshot(t, func(s *snapshot.Snapshot) {
		s.Issue(5).Body = strings.Replace(s.Issue(5).Body, "- #3", "- #9", 1)
	})
	if code, _, errOut := runCmd(t, "plan", "--config", repoConfig, "--input", cyc, "--json"); code != 3 || !strings.Contains(errOut, "循環") {
		t.Errorf("循環: exit = %d, %s", code, errOut)
	}
	if code, _, _ := runCmd(t, "plan", "--config", repoConfig, "--input", sampleSnapshot, "--json", "--mermaid"); code != 3 {
		t.Errorf("--json と --mermaid: exit = %d", code)
	}
	if code, _, _ := runCmd(t, "plan", "--config", repoConfig); code != 3 {
		t.Errorf("--repo も --input もない: exit = %d", code)
	}
}

func TestRecoverCommand(t *testing.T) {
	code, out, errOut := runCmd(t, "recover", "--config", repoConfig, "--input", sampleSnapshot, "--json")
	if code != 0 {
		t.Fatalf("exit = %d\n%s", code, errOut)
	}
	if !strings.Contains(errOut, "壊れた状態の記録を無視する") {
		t.Errorf("壊れた記録の警告がない: %s", errOut)
	}
	var res struct {
		Actions []plan.Action `json:"actions"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	want := []plan.Action{
		{Issue: 4, Status: "in-progress", Action: "resume-worker", Worker: "issue-4", Session: session4, Worktree: "/home/user/raft-game/.claude/worktrees/issue-4"},
		{Issue: 8, Status: "in-review", Action: "request-review", PR: 30},
	}
	for i := range res.Actions {
		res.Actions[i].Reason = ""
	}
	if !reflect.DeepEqual(res.Actions, want) {
		t.Errorf("actions = %+v", res.Actions)
	}
}

// TestRecoverLocalWorktrees は、--input がないとき、GitHub から読み、ローカルの git の Worktree も見ることを確かめる。
func TestRecoverLocalWorktrees(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/issues"):
			_, _ = w.Write([]byte(`[{"number":4,"title":"t","state":"open","labels":[{"name":"status:in-progress"}],"body":"## 依存 Issue\n- なし\n"},
				{"number":5,"title":"t","state":"open","labels":[{"name":"status:in-progress"}],"body":"## 依存 Issue\n- なし\n"}]`))
		default:
			_, _ = w.Write([]byte(`[]`))
		}
	}))
	defer srv.Close()
	t.Setenv("GITHUB_API_URL", srv.URL)
	t.Setenv("GITHUB_TOKEN", "tok")
	old := gitRunner
	t.Cleanup(func() { gitRunner = old })
	gitRunner = func(_ context.Context, dir string, args ...string) (string, error) {
		switch strings.Join(args, " ") {
		case "worktree list --porcelain":
			return "worktree /r\nbranch refs/heads/main\n\nworktree /r/.claude/worktrees/issue-4\nbranch refs/heads/agent/issue-4\n\n" +
				"worktree /r/.claude/worktrees/issue-5\nbranch refs/heads/agent/issue-5\n", nil
		case "status --porcelain":
			return "", nil
		case "rev-list --count HEAD --not --remotes":
			if strings.HasSuffix(dir, "issue-4") {
				return "2\n", nil
			}
			return "0\n", nil
		}
		t.Errorf("想定外の git %s %v", dir, args)
		return "", nil
	}
	code, out, errOut := runCmd(t, "recover", "--repo", repoName, "--config", repoConfig, "--json")
	if code != 0 {
		t.Fatalf("exit = %d\n%s", code, errOut)
	}
	var res struct {
		Actions []plan.Action `json:"actions"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Actions) != 2 || res.Actions[0].Action != "resume-worker" || res.Actions[0].Worktree != "/r/.claude/worktrees/issue-4" ||
		res.Actions[1].Action != "reset-to-ready" {
		t.Errorf("actions = %s", out)
	}
}

// checkDryRunMatches は、--dry-run の出力と、実際に送った要求が一致することを確かめる。
func checkDryRunMatches(t *testing.T, wantCode int, args ...string) []apiOp {
	t.Helper()
	fixClock(t)
	code, out, errOut := runCmd(t, append(append([]string{}, args...), "--dry-run")...)
	if code != wantCode {
		t.Fatalf("dry-run: exit = %d, want %d\n%s\n%s", code, wantCode, out, errOut)
	}
	planned := dryRunOps(t, out)

	got, stop := fakeGitHub(t, 0)
	defer stop()
	code, out, errOut = runCmd(t, args...)
	if code != wantCode {
		t.Fatalf("exit = %d, want %d\n%s\n%s", code, wantCode, out, errOut)
	}
	if len(planned) == 0 && len(*got) == 0 {
		return planned
	}
	if !reflect.DeepEqual(planned, *got) {
		a, _ := json.MarshalIndent(planned, "", " ")
		b, _ := json.MarshalIndent(*got, "", " ")
		t.Errorf("dry-run と実際の要求が違う:\n%s\n---\n%s", a, b)
	}
	return planned
}

func commentBody(t *testing.T, op apiOp) *state.Record {
	t.Helper()
	m, _ := op.Body.(map[string]any)
	body, _ := m["body"].(string)
	r, err := state.ParseComment(body)
	if err != nil {
		t.Fatalf("記録を読めない: %v\n%+v", err, op)
	}
	return r
}

func TestStateCommand(t *testing.T) {
	base := []string{"--repo", repoName, "--config", repoConfig, "--input", sampleSnapshot}
	ops := checkDryRunMatches(t, 0, append([]string{"state", "8", "ci-pending", "--reason", "CI 待ち", "--pr", "30"}, base...)...)
	prefix := "/repos/" + repoName + "/issues/8"
	if len(ops) != 3 || ops[0].Method != "POST" || ops[0].Path != prefix+"/labels" || ops[1].Method != "DELETE" ||
		ops[1].Path != prefix+"/labels/status:in-review" || ops[2].Path != prefix+"/comments" {
		t.Fatalf("ops = %+v", ops)
	}
	if !reflect.DeepEqual(ops[0].Body, map[string]any{"labels": []any{"status:ci-pending"}}) {
		t.Errorf("labels = %+v", ops[0].Body)
	}
	if r := commentBody(t, ops[2]); r.From != "in-review" || r.To != "ci-pending" || r.PR != 30 || r.Reason != "CI 待ち" || r.At != "2026-11-06T12:00:00Z" {
		t.Errorf("record = %+v", r)
	}

	// 許さない遷移・merged の条件は終了コード 1。
	if code, _, errOut := runCmd(t, append([]string{"state", "9", "in-progress", "--dry-run"}, base...)...); code != 1 {
		t.Errorf("backlog → in-progress: exit = %d, %s", code, errOut)
	}
	if code, _, _ := runCmd(t, append([]string{"state", "8", "merged", "--dry-run"}, base...)...); code != 1 {
		t.Errorf("in-review → merged: exit = %d", code)
	}
	if code, _, _ := runCmd(t, append([]string{"state", "8", "done", "--dry-run"}, base...)...); code != 3 {
		t.Errorf("未知の状態: exit = %d", code)
	}
	if code, _, _ := runCmd(t, "state", "8", "ci-pending", "--config", repoConfig, "--input", sampleSnapshot, "--dry-run"); code != 3 {
		t.Errorf("--repo なし: exit = %d", code)
	}
	if code, _, _ := runCmd(t, append([]string{"state", "8", "--dry-run"}, base...)...); code != 3 {
		t.Errorf("引数が足りない: exit = %d", code)
	}
}

func TestStateLabelFailureMidway(t *testing.T) {
	fixClock(t)
	got, stop := fakeGitHub(t, 2) // DELETE で失敗
	defer stop()
	code, _, errOut := runCmd(t, "state", "8", "ci-pending", "--repo", repoName, "--config", repoConfig, "--input", sampleSnapshot)
	if code != 3 || len(*got) != 2 || !strings.Contains(errOut, "recover") {
		t.Errorf("exit = %d, requests = %+v, %s", code, *got, errOut)
	}
	// 途中で止まると status:* が 2 つ残り、recover が fix-labels を出す。
	p := writeSnapshot(t, func(s *snapshot.Snapshot) {
		s.Issue(8).Labels = append(s.Issue(8).Labels, "status:ci-pending")
	})
	_, out, _ := runCmd(t, "recover", "--config", repoConfig, "--input", p, "--json")
	if !strings.Contains(out, `"issue": 8`) || !strings.Contains(out, `"fix-labels"`) {
		t.Errorf("recover = %s", out)
	}
}

func TestClaimCommand(t *testing.T) {
	base := []string{"--repo", repoName, "--config", repoConfig, "--input", sampleSnapshot}
	ops := checkDryRunMatches(t, 0, append([]string{"claim", "5", "--worker", "issue-5", "--session", "s-5"}, base...)...)
	if len(ops) != 3 {
		t.Fatalf("ops = %+v", ops)
	}
	if r := commentBody(t, ops[2]); r.From != "ready" || r.To != "in-progress" || r.Worker != "issue-5" || r.Branch != "agent/issue-5" ||
		r.Session != "s-5" || r.Attempt != 1 {
		t.Errorf("record = %+v", r)
	}

	// 依存が未完了（#6）・ready ではない（#9）は終了コード 4。
	for _, n := range []string{"6", "9", "4"} {
		if code, _, errOut := runCmd(t, append([]string{"claim", n, "--worker", "issue-" + n, "--dry-run"}, base...)...); code != 4 {
			t.Errorf("#%s: exit = %d, %s", n, code, errOut)
		}
	}
	// ブランチがある。
	p := writeSnapshot(t, func(s *snapshot.Snapshot) { s.Branches = append(s.Branches, "agent/issue-5") })
	if code, _, _ := runCmd(t, "claim", "5", "--worker", "issue-5", "--repo", repoName, "--config", repoConfig, "--input", p, "--dry-run"); code != 4 {
		t.Errorf("ブランチあり: exit = %d", code)
	}

	// 同じ worker と session の 2 回目は何もしない。
	ops = checkDryRunMatches(t, 0, append([]string{"claim", "4", "--worker", "issue-4", "--session", session4}, base...)...)
	if len(ops) != 0 {
		t.Errorf("冪等: ops = %+v", ops)
	}

	// --resume は記録だけを付け、attempt を 1 増やす。
	ops = checkDryRunMatches(t, 0, append([]string{"claim", "4", "--worker", "issue-4", "--session", "s-new", "--resume"}, base...)...)
	if len(ops) != 1 {
		t.Fatalf("resume: ops = %+v", ops)
	}
	if r := commentBody(t, ops[0]); r.From != "in-progress" || r.To != "in-progress" || r.Attempt != 2 || r.Session != "s-new" {
		t.Errorf("resume: record = %+v", r)
	}
	if code, _, _ := runCmd(t, append([]string{"claim", "5", "--dry-run"}, base...)...); code != 3 {
		t.Errorf("--worker なし: exit = %d", code)
	}
}

func TestAttemptCommand(t *testing.T) {
	withRecords := func(n int) string {
		return writeSnapshot(t, func(s *snapshot.Snapshot) {
			is := s.Issue(8)
			for i := 0; i < n; i++ {
				r := state.Record{From: state.InReview, To: state.InReview, Actor: "orchestrator", Cause: "ci:ci-ok", At: "2026-11-06T10:00:00Z"}
				is.Comments = append(is.Comments, snapshot.Comment{Author: "K-Kizuku", Body: r.Comment()})
			}
			// 信頼しない作者の記録は数えない。
			r := state.Record{From: state.InReview, To: state.InReview, Cause: "ci:ci-ok", At: "2026-11-06T10:00:00Z"}
			is.Comments = append(is.Comments, snapshot.Comment{Author: "someone", Body: r.Comment()})
		})
	}
	args := func(p string) []string {
		return []string{"attempt", "8", "--cause", "ci:ci-ok", "--repo", repoName, "--config", repoConfig, "--input", p}
	}
	ops := checkDryRunMatches(t, 0, args(withRecords(2))...)
	if len(ops) != 1 {
		t.Fatalf("ops = %+v", ops)
	}
	if r := commentBody(t, ops[0]); r.Cause != "ci:ci-ok" || r.Reason != "再試行 3/3" {
		t.Errorf("record = %+v", r)
	}

	ops = checkDryRunMatches(t, 5, args(withRecords(3))...)
	if len(ops) != 3 || !reflect.DeepEqual(ops[0].Body, map[string]any{"labels": []any{"status:blocked", "needs-human"}}) ||
		ops[1].Path != "/repos/"+repoName+"/issues/8/labels/status:in-review" {
		t.Fatalf("ops = %+v", ops)
	}
	if r := commentBody(t, ops[2]); r.To != "blocked" || r.Cause != "ci:ci-ok" {
		t.Errorf("record = %+v", r)
	}
	if code, _, _ := runCmd(t, "attempt", "8", "--repo", repoName, "--config", repoConfig, "--input", sampleSnapshot, "--dry-run"); code != 3 {
		t.Errorf("--cause なし: exit = %d", code)
	}
}

func TestTaskHelp(t *testing.T) {
	for _, sub := range []string{"plan", "state", "claim", "attempt", "recover"} {
		code, _, errOut := runCmd(t, sub, "-h")
		if code != 0 || !strings.Contains(errOut, "agentctl "+sub) || !strings.Contains(errOut, "-dry-run") {
			t.Errorf("%s -h: exit = %d\n%s", sub, code, errOut)
		}
	}
	if code, out, _ := runCmd(t, "help"); code != 0 || !strings.Contains(out, "agentctl recover") {
		t.Errorf("help: %d %s", code, out)
	}
}
