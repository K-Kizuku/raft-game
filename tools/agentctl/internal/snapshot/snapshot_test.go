package snapshot

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/github"
)

func TestLoadSample(t *testing.T) {
	s, err := Load("testdata/snapshot.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Issues) == 0 || len(s.Pulls) == 0 || len(s.Branches) == 0 || len(s.Statuses) == 0 || len(s.Worktrees) == 0 {
		t.Errorf("サンプルの欄が空: %+v", s)
	}
}

func TestParseStrict(t *testing.T) {
	for name, data := range map[string]string{
		"未知のキー":           `{"issues":[],"pulls":[],"branches":[],"statuses":{},"extra":1}`,
		"Issue の番号が重複":    `{"issues":[{"number":1,"state":"open"},{"number":1,"state":"open"}]}`,
		"state が不正":       `{"issues":[{"number":1,"state":"draft"}]}`,
		"merged なのに open": `{"pulls":[{"number":2,"state":"open","merged":true}]}`,
		"後ろに余計なデータ":       `{} {}`,
		"worktree の番号":    `{"worktrees":[{"issue":0}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(data)); err == nil {
				t.Error("エラーにならない")
			}
		})
	}
}

func TestHelpers(t *testing.T) {
	s := &Snapshot{
		Issues: []Issue{
			{Number: 1, State: "closed", StateReason: "completed"},
			{Number: 2, State: "closed", StateReason: "not_planned"},
			{Number: 3, State: "open", Body: "## 依存 Issue\n- #1\n- #2\n- #4\n"},
			{Number: 4, State: "open"},
			{Number: 5, State: "open", Body: "依存の節がない"},
			{Number: 6, State: "open", Body: "## 依存 Issue\n- #99\n"},
		},
		Pulls: []Pull{
			{Number: 10, State: "open", Author: "K-Kizuku", HeadRef: "agent/issue-3"},
			{Number: 11, State: "open", Author: "K-Kizuku", HeadRef: "x", Body: "```\nCloses #3\n```\nResolves #4"},
			{Number: 12, State: "closed", Merged: true, Author: "bot", HeadRef: "y", Body: "fixes #3"},
			// 信頼しない作者の PR は、Closes #3 でもブランチ名が同じでも数えない。
			{Number: 13, State: "open", Author: "someone", HeadRef: "agent/issue-3", Body: "Closes #3"},
			{Number: 14, State: "closed", Merged: true, Author: "", HeadRef: "z", Body: "Closes #3"},
		},
	}
	trusted := func(l string) bool { return l == "K-Kizuku" || l == "bot" }
	if got := s.OpenPullsFor(3, "agent/issue-3", trusted); len(got) != 1 || got[0].Number != 10 {
		t.Errorf("open = %+v（コードブロックの中の Closes と、信頼しない作者の PR は数えない）", got)
	}
	if got := s.MergedPullsFor(3, "agent/issue-3", trusted); len(got) != 1 || got[0].Number != 12 {
		t.Errorf("merged = %+v", got)
	}
	if got := s.OpenPullsFor(4, "agent/issue-4", trusted); len(got) != 1 || got[0].Number != 11 {
		t.Errorf("open #4 = %+v", got)
	}
	if got := s.OpenPullsFor(3, "agent/issue-3", nil); len(got) != 0 {
		t.Errorf("trusted が nil: open = %+v", got)
	}
	reasons, err := s.UnmetDeps(s.Issue(3))
	if err != nil || len(reasons) != 2 || !strings.Contains(reasons[0], "#2") || !strings.Contains(reasons[1], "#4") {
		t.Errorf("reasons = %q, err = %v", reasons, err)
	}
	if reasons, err := s.UnmetDeps(s.Issue(5)); err != nil || len(reasons) != 1 {
		t.Errorf("節がない: %q, %v", reasons, err)
	}
	if _, err := s.UnmetDeps(s.Issue(6)); err == nil {
		t.Error("存在しない依存先がエラーにならない")
	}
}

func TestFetch(t *testing.T) {
	const rp = "/repos/K-Kizuku/raft-game"
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.RequestURI())
		switch r.URL.Path {
		case rp + "/issues":
			if r.URL.Query().Get("page") == "" {
				w.Header().Set("Link", fmt.Sprintf(`<%s%s/issues?state=all&per_page=100&page=2>; rel="next"`, "http://"+r.Host, rp))
				_, _ = w.Write([]byte(`[{"number":12,"title":"T","state":"open","state_reason":null,"labels":[{"name":"status:ready"}],"body":"b"},
					{"number":20,"title":"PR","state":"open","pull_request":{"url":"x"}}]`))
				return
			}
			_, _ = w.Write([]byte(`[{"number":3,"title":"done","state":"closed","state_reason":"completed","labels":[{"name":"status:merged"}],"body":null},
				{"number":5,"title":"stale","state":"closed","state_reason":"completed","labels":[{"name":"status:in-review"}],"body":""}]`))
		case rp + "/issues/12/comments", rp + "/issues/5/comments":
			_, _ = w.Write([]byte(`[{"user":{"login":"K-Kizuku"},"body":"hi","created_at":"2026-11-06T00:00:00Z"}]`))
		case rp + "/issues/20/comments":
			_, _ = w.Write([]byte(`[]`))
		case rp + "/pulls":
			_, _ = w.Write([]byte(`[{"number":20,"state":"open","merged_at":null,"body":"Closes #12","user":{"login":"github-actions[bot]"},"head":{"ref":"agent/issue-12","sha":"aaa"},"labels":[]},
				{"number":21,"state":"closed","merged_at":"2026-11-01T00:00:00Z","body":"Closes #3","head":{"ref":"agent/issue-3","sha":"bbb"},"labels":[]}]`))
		case rp + "/commits/aaa/status":
			_, _ = w.Write([]byte(`{"statuses":[{"context":"merge-gate","state":"success"},{"context":"merge-gate","state":"pending"}]}`))
		case rp + "/git/matching-refs/heads/agent/issue-":
			_, _ = w.Write([]byte(`[{"ref":"refs/heads/agent/issue-12"}]`))
		default:
			t.Errorf("想定外の要求 %s", r.URL.RequestURI())
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c, err := github.New(srv.URL, "tok")
	if err != nil {
		t.Fatal(err)
	}
	s, err := Fetch(context.Background(), c, "K-Kizuku/raft-game", "agent/issue-")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Issues) != 3 || s.Issue(20) != nil {
		t.Errorf("issues = %+v（PR は含めない）", s.Issues)
	}
	if is := s.Issue(12); len(is.Comments) != 1 || is.Comments[0].Author != "K-Kizuku" || is.StateReason != "" {
		t.Errorf("#12 = %+v", is)
	}
	if is := s.Issue(3); len(is.Comments) != 0 || is.StateReason != "completed" {
		t.Errorf("#3 = %+v（merged で閉じた Issue のコメントは読まない）", is)
	}
	if is := s.Issue(5); len(is.Comments) != 1 {
		t.Errorf("#5 = %+v（状態のラベルが残る閉じた Issue のコメントは読む）", is)
	}
	if p := s.Pull(21); !p.Merged || p.State != "closed" || p.Author != "" {
		t.Errorf("#21 = %+v", p)
	}
	if p := s.Pull(20); p.Author != "github-actions[bot]" {
		t.Errorf("#20 の作者 = %q", p.Author)
	}
	if s.Status("aaa", "merge-gate") != "success" || !s.HasBranch("agent/issue-12") {
		t.Errorf("statuses = %v, branches = %v", s.Statuses, s.Branches)
	}
}

func TestFetchError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/issues") {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()
	c, _ := github.New(srv.URL, "tok")
	if _, err := Fetch(context.Background(), c, "o/r", "agent/issue-"); err == nil {
		t.Error("途中の失敗がエラーにならない")
	}
}

func TestLocalWorktreesFake(t *testing.T) {
	list := "worktree /repo\nHEAD 1\nbranch refs/heads/main\n\n" +
		"worktree /repo/.claude/worktrees/issue-4\nHEAD 2\nbranch refs/heads/agent/issue-4\n\n" +
		"worktree /tmp/other\nHEAD 3\nbranch refs/heads/agent/issue-7\n\n" +
		"worktree /repo/.claude/worktrees/issue-9\nHEAD 4\ndetached\nprunable gitdir file points to non-existent location\n\n"
	git := func(_ context.Context, dir string, args ...string) (string, error) {
		switch strings.Join(args, " ") {
		case "worktree list --porcelain":
			return list, nil
		case "status --porcelain":
			if dir == "/repo/.claude/worktrees/issue-4" {
				return " M a.go\n", nil
			}
			return "", nil
		case "rev-list --count HEAD --not --remotes":
			if dir == "/tmp/other" {
				return "3\n", nil
			}
			return "0\n", nil
		}
		return "", fmt.Errorf("想定外: %s %v", dir, args)
	}
	ws, err := LocalWorktrees(context.Background(), git, "/repo", "agent/issue-")
	if err != nil {
		t.Fatal(err)
	}
	want := []Worktree{
		{Issue: 4, Path: "/repo/.claude/worktrees/issue-4", Branch: "agent/issue-4", Dirty: true},
		{Issue: 7, Path: "/tmp/other", Branch: "agent/issue-7", Ahead: 3},
	}
	if fmt.Sprint(ws) != fmt.Sprint(want) {
		t.Errorf("worktrees = %+v", ws)
	}
	failing := func(context.Context, string, ...string) (string, error) { return "", errors.New("git がない") }
	if _, err := LocalWorktrees(context.Background(), failing, "/repo", "agent/issue-"); err == nil {
		t.Error("git の失敗がエラーにならない")
	}
}

// TestLocalWorktreesGit は、本物の git で、未コミットの変更と push していないコミットを見分ける。
func TestLocalWorktreesGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git がない")
	}
	ctx := context.Background()
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	repo := filepath.Join(root, "repo")
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run(root, "init", "-q", "--bare", remote)
	run(root, "init", "-q", "-b", "main", repo)
	run(repo, "commit", "-q", "--allow-empty", "-m", "init")
	run(repo, "remote", "add", "origin", remote)
	run(repo, "push", "-q", "origin", "main")
	w4 := filepath.Join(repo, ".claude", "worktrees", "issue-4")
	w5 := filepath.Join(repo, ".claude", "worktrees", "issue-5")
	w6 := filepath.Join(repo, ".claude", "worktrees", "issue-6")
	run(repo, "worktree", "add", "-q", "-b", "agent/issue-4", w4)
	run(repo, "worktree", "add", "-q", "-b", "agent/issue-5", w5)
	run(repo, "worktree", "add", "-q", "-b", "agent/issue-6", w6)
	if err := os.WriteFile(filepath.Join(w4, "new.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(w5, "commit", "-q", "--allow-empty", "-m", "wip")

	ws, err := LocalWorktrees(ctx, RunGit, repo, "agent/issue-")
	if err != nil {
		t.Fatal(err)
	}
	got := map[int]Worktree{}
	for _, w := range ws {
		got[w.Issue] = w
	}
	if len(got) != 3 || !got[4].Dirty || got[4].Ahead != 0 || got[5].Dirty || got[5].Ahead != 1 || got[6].HasUnpushedWork() {
		t.Errorf("worktrees = %+v", ws)
	}
	if got[5].Branch != "agent/issue-5" {
		t.Errorf("branch = %q", got[5].Branch)
	}
}
