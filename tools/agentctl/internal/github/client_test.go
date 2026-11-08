package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/gate"
)

const (
	head = "1111111111111111111111111111111111111111"
	repo = "K-Kizuku/raft-game"
)

// fake は GitHub の REST API の偽物。
type fake struct {
	t        *testing.T
	srv      *httptest.Server
	mu       sync.Mutex
	pulls    []map[string]any // GET /pulls/12 の応答を順に返す（最後のものを繰り返す）
	pullGets int
	comments int // コメントの件数（100 件ずつページ送り）
	// reviewComments は行コメントの件数（100 件ずつページ送り）。
	reviewComments int
	fail           map[string]int
	posted         []string // POST・PATCH のパスと本文
	issue404       bool
	external       string // Link に入れる別のホスト
}

func newFake(t *testing.T) *fake {
	f := &fake{t: t, fail: map[string]int{}, comments: 3, reviewComments: 2}
	f.pulls = []map[string]any{pullJSON(head, true)}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	return f
}

func pullJSON(sha string, mergeable any) map[string]any {
	return map[string]any{
		"number": 12, "state": "open", "merged": false, "draft": false, "title": "t",
		"body": "Closes #3\n", "user": map[string]any{"login": "K-Kizuku"},
		"labels":    []any{map[string]any{"name": "agent"}},
		"base":      map[string]any{"ref": "main", "sha": "2222222222222222222222222222222222222222"},
		"head":      map[string]any{"ref": "agent/issue-3", "sha": sha},
		"mergeable": mergeable, "mergeable_state": "blocked", "changed_files": 1,
		"additions": 7, "deletions": 3,
	}
}

func (f *fake) client(t *testing.T) *Client {
	c, err := New(f.srv.URL, "tok")
	if err != nil {
		t.Fatal(err)
	}
	c.Sleep = func(time.Duration) {}
	return c
}

func (f *fake) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer tok" {
		http.Error(w, "no auth", http.StatusUnauthorized)
		return
	}
	p := r.URL.Path
	if code, ok := f.fail[p]; ok {
		if code == http.StatusForbidden {
			w.Header().Set("X-RateLimit-Remaining", "0")
		}
		w.WriteHeader(code)
		fmt.Fprint(w, `{"message":"boom"}`)
		return
	}
	pre := "/repos/" + repo
	write := func(v any) { _ = json.NewEncoder(w).Encode(v) }
	if r.Method != http.MethodGet {
		b, _ := io.ReadAll(r.Body)
		f.posted = append(f.posted, r.Method+" "+p+" "+string(b))
		w.WriteHeader(http.StatusCreated)
		write(map[string]any{})
		return
	}
	switch p {
	case pre + "/pulls/12":
		i := f.pullGets
		if i >= len(f.pulls) {
			i = len(f.pulls) - 1
		}
		f.pullGets++
		write(f.pulls[i])
	case pre + "/pulls/12/files":
		write([]any{map[string]any{"filename": "tools/agentctl/main.go", "status": "added"}})
	case pre + "/issues/3":
		if f.issue404 {
			w.WriteHeader(http.StatusNotFound)
			write(map[string]any{"message": "Not Found"})
			return
		}
		write(map[string]any{"number": 3, "state": "open", "title": "i", "body": "## Acceptance Criteria\n- [ ] AC-1: a\n", "labels": []any{}})
	case pre + "/commits/" + head + "/check-runs":
		write(map[string]any{"total_count": 1, "check_runs": []any{
			map[string]any{"name": "ci-ok", "head_sha": head, "status": "completed", "conclusion": "success", "app": map[string]any{"slug": "github-actions"}},
			map[string]any{"name": "lint", "head_sha": head, "status": "in_progress", "conclusion": nil},
		}})
	case pre + "/commits/" + head + "/status":
		write(map[string]any{"state": "pending", "statuses": []any{map[string]any{"context": "merge-gate", "state": "pending"}}})
	case pre + "/commits/" + head + "/pulls":
		write([]any{
			map[string]any{"number": 12, "state": "open", "head": map[string]any{"sha": head}},
			map[string]any{"number": 13, "state": "closed", "head": map[string]any{"sha": head}},
			map[string]any{"number": 14, "state": "open", "head": map[string]any{"sha": "9999999999999999999999999999999999999999"}},
		})
	case pre + "/issues/12/comments":
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page == 0 {
			page = 1
		}
		start := (page - 1) * 100
		end := min(start+100, f.comments)
		var out []any
		for i := start; i < end; i++ {
			out = append(out, map[string]any{"id": i + 1, "user": map[string]any{"login": "u"}, "body": fmt.Sprintf("c%d", i+1), "created_at": "2026-11-01T00:00:00Z"})
		}
		if end < f.comments {
			host := f.srv.URL
			if f.external != "" {
				host = f.external
			}
			w.Header().Set("Link", fmt.Sprintf(`<%s%s?per_page=100&page=%d>; rel="next", <%s%s?page=99>; rel="last"`, host, p, page+1, host, p))
		}
		if out == nil {
			out = []any{}
		}
		write(out)
	case pre + "/pulls/12/reviews":
		write([]any{map[string]any{"id": 501, "user": map[string]any{"login": "chatgpt-codex-connector[bot]"}, "commit_id": head,
			"body": "<!-- codex-review-receipt:v1 -->", "submitted_at": "2026-11-01T00:00:00Z"}})
	case pre + "/pulls/12/comments":
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page == 0 {
			page = 1
		}
		start := (page - 1) * 100
		end := min(start+100, f.reviewComments)
		out := []any{}
		for i := start; i < end; i++ {
			c := map[string]any{"id": 9000 + i, "user": map[string]any{"login": "chatgpt-codex-connector[bot]"}, "commit_id": head,
				"original_commit_id": head, "path": "a.go", "line": i + 1, "body": fmt.Sprintf("rc%d", i), "created_at": "2026-11-01T00:00:00Z"}
			if i == 0 {
				// 古くなった行コメントは line が null で、original_line だけがある。
				c["line"] = nil
				c["original_line"] = 42
				c["user"] = nil
			}
			out = append(out, c)
		}
		if end < f.reviewComments {
			w.Header().Set("Link", fmt.Sprintf(`<%s%s?per_page=100&page=%d>; rel="next"`, f.srv.URL, p, page+1))
		}
		write(out)
	case pre + "/compare/main..." + head:
		write(map[string]any{"status": "ahead", "ahead_by": 1, "behind_by": 0})
	default:
		w.WriteHeader(http.StatusNotFound)
		write(map[string]any{"message": "Not Found: " + p})
	}
}

func TestFetchInput(t *testing.T) {
	f := newFake(t)
	f.comments = 250
	f.pulls = []map[string]any{pullJSON(head, nil), pullJSON(head, true)}
	in, err := f.client(t).FetchInput(context.Background(), repo, 12)
	if err != nil {
		t.Fatal(err)
	}
	if in.PullRequest.HeadSHA != head || in.PullRequest.Mergeable == nil || !*in.PullRequest.Mergeable {
		t.Errorf("pr = %+v", in.PullRequest)
	}
	if len(in.Comments) != 250 || in.Comments[249].Body != "c250" {
		t.Errorf("comments = %d", len(in.Comments))
	}
	if in.Issue == nil || in.Issue.Number != 3 || in.Issue.IsPullRequest {
		t.Errorf("issue = %+v", in.Issue)
	}
	if len(in.CheckRuns) != 2 || in.CheckRuns[0].App != "github-actions" || in.CheckRuns[1].Conclusion != "" {
		t.Errorf("check runs = %+v", in.CheckRuns)
	}
	if len(in.Statuses) != 1 || len(in.Files) != 1 || in.Compare == nil || in.Compare.AheadBy != 1 {
		t.Errorf("input = %+v", in)
	}
	if in.PullRequest.Labels[0] != "agent" || in.PullRequest.Author != "K-Kizuku" {
		t.Errorf("pr = %+v", in.PullRequest)
	}
	// 小さな PR の判定（Issue #243）に使う追加・削除の行数。
	if a, d := in.PullRequest.Additions, in.PullRequest.Deletions; a == nil || d == nil || *a != 7 || *d != 3 {
		t.Errorf("additions = %v, deletions = %v", a, d)
	}
	if len(in.Reviews) != 1 || in.Reviews[0].ID != 501 || in.Reviews[0].Author != "chatgpt-codex-connector[bot]" || in.Reviews[0].CommitID != head {
		t.Errorf("reviews = %+v", in.Reviews)
	}
	if len(in.ReviewComments) != 2 || in.ReviewComments[1].Line != 2 || in.ReviewComments[1].Path != "a.go" || in.ReviewComments[1].ReviewedSHA() != head {
		t.Errorf("review comments = %+v", in.ReviewComments)
	}
}

func TestFetchDiscussion(t *testing.T) {
	f := newFake(t)
	f.reviewComments = 230
	in, err := f.client(t).FetchDiscussion(context.Background(), repo, 12)
	if err != nil {
		t.Fatal(err)
	}
	if in.PullRequest.HeadSHA != head || in.PullRequest.BaseSHA != "2222222222222222222222222222222222222222" || len(in.Comments) != 3 {
		t.Errorf("input = %+v", in)
	}
	if len(in.ReviewComments) != 230 || in.ReviewComments[229].ID != 9229 {
		t.Fatalf("review comments = %d", len(in.ReviewComments))
	}
	// line が null なら original_line、user が null なら作者は空。
	if rc := in.ReviewComments[0]; rc.Line != 42 || rc.Author != "" {
		t.Errorf("review comment[0] = %+v", rc)
	}
	// ファイル・Issue・CI は集めない。
	if in.Files != nil || in.Issue != nil || in.CheckRuns != nil || in.Compare != nil {
		t.Errorf("余計な情報を集めた: %+v", in)
	}
	for _, p := range []string{"/repos/" + repo + "/pulls/12/reviews", "/repos/" + repo + "/pulls/12/comments"} {
		f2 := newFake(t)
		f2.fail[p] = http.StatusBadGateway
		if _, err := f2.client(t).FetchDiscussion(context.Background(), repo, 12); err == nil {
			t.Errorf("%s の失敗を無視した", p)
		}
	}
}

func TestCreateComment(t *testing.T) {
	f := newFake(t)
	if _, err := f.client(t).CreateComment(context.Background(), repo, 12, "@codex review"); err != nil {
		t.Fatal(err)
	}
	if len(f.posted) != 1 || f.posted[0] != "POST /repos/"+repo+"/issues/12/comments "+`{"body":"@codex review"}` {
		t.Errorf("posted = %q", f.posted)
	}
}

// 追加・削除の行数がない応答では null のまま（小さな PR と判定しない）。
func TestFetchInputNoLineCounts(t *testing.T) {
	f := newFake(t)
	p := pullJSON(head, true)
	delete(p, "additions")
	delete(p, "deletions")
	f.pulls = []map[string]any{p}
	in, err := f.client(t).FetchInput(context.Background(), repo, 12)
	if err != nil {
		t.Fatal(err)
	}
	if in.PullRequest.Additions != nil || in.PullRequest.Deletions != nil {
		t.Errorf("additions = %v, deletions = %v", in.PullRequest.Additions, in.PullRequest.Deletions)
	}
}

func TestFetchInputIssueNotFound(t *testing.T) {
	f := newFake(t)
	f.issue404 = true
	in, err := f.client(t).FetchInput(context.Background(), repo, 12)
	if err != nil {
		t.Fatal(err)
	}
	if in.Issue != nil {
		t.Errorf("issue = %+v", in.Issue)
	}
}

func TestFetchInputErrors(t *testing.T) {
	pre := "/repos/" + repo
	for _, tc := range []struct {
		path        string
		code        int
		rateLimited bool
	}{
		{pre + "/issues/12/comments", http.StatusBadGateway, false},
		{pre + "/commits/" + head + "/check-runs", http.StatusForbidden, true},
		{pre + "/commits/" + head + "/status", http.StatusTooManyRequests, true},
		{pre + "/compare/main..." + head, http.StatusInternalServerError, false},
		{pre + "/pulls/12/files", http.StatusServiceUnavailable, false},
	} {
		t.Run(tc.path, func(t *testing.T) {
			f := newFake(t)
			f.fail[tc.path] = tc.code
			_, err := f.client(t).FetchInput(context.Background(), repo, 12)
			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("err = %v", err)
			}
			if apiErr.StatusCode != tc.code || apiErr.RateLimited != tc.rateLimited {
				t.Errorf("err = %+v", apiErr)
			}
		})
	}
}

func TestFetchInputHeadChanged(t *testing.T) {
	f := newFake(t)
	f.pulls = []map[string]any{pullJSON(head, true), pullJSON("8888888888888888888888888888888888888888", true)}
	if _, err := f.client(t).FetchInput(context.Background(), repo, 12); err == nil || !strings.Contains(err.Error(), "HEAD") {
		t.Fatalf("err = %v", err)
	}
}

func TestPaginationRefusesOtherHost(t *testing.T) {
	f := newFake(t)
	f.comments = 150
	f.external = "https://evil.example.invalid"
	if _, err := f.client(t).FetchInput(context.Background(), repo, 12); err == nil || !strings.Contains(err.Error(), "別のホスト") {
		t.Fatalf("err = %v", err)
	}
}

func TestOpenPullsForSHA(t *testing.T) {
	f := newFake(t)
	got, err := f.client(t).OpenPullsForSHA(context.Background(), repo, head)
	if err != nil || len(got) != 1 || got[0] != 12 {
		t.Fatalf("got = %v, %v", got, err)
	}
}

func TestSetStatusAndUpsertComment(t *testing.T) {
	f := newFake(t)
	c := f.client(t)
	ctx := context.Background()
	if err := c.SetStatus(ctx, repo, head, "pending", "merge-gate", "d", "https://x.invalid/run"); err != nil {
		t.Fatal(err)
	}
	marker := gate.CommentMarker
	if err := c.UpsertComment(ctx, repo, 12, []gate.Comment{{ID: 5, Author: "someone", Body: marker}}, marker, "github-actions[bot]", "b1"); err != nil {
		t.Fatal(err)
	}
	if err := c.UpsertComment(ctx, repo, 12, []gate.Comment{{ID: 7, Author: "github-actions[bot]", Body: marker}}, marker, "github-actions[bot]", "b2"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"POST /repos/" + repo + "/statuses/" + head,
		"POST /repos/" + repo + "/issues/12/comments",
		"PATCH /repos/" + repo + "/issues/comments/7",
	}
	if len(f.posted) != len(want) {
		t.Fatalf("posted = %q", f.posted)
	}
	for i, w := range want {
		if !strings.HasPrefix(f.posted[i], w+" ") {
			t.Errorf("posted[%d] = %q, want %q", i, f.posted[i], w)
		}
	}
	if !strings.Contains(f.posted[0], `"context":"merge-gate"`) || !strings.Contains(f.posted[0], `"target_url"`) {
		t.Errorf("status = %s", f.posted[0])
	}
}

func TestNew(t *testing.T) {
	if _, err := New("", ""); err == nil {
		t.Error("トークンなしで作れた")
	}
	if _, err := New("ftp://x", "t"); err == nil {
		t.Error("不正な URL で作れた")
	}
	c, err := New("", "t")
	if err != nil || c.base.String() != DefaultBaseURL {
		t.Errorf("c = %v, %v", c, err)
	}
	if !ValidRepo("a/b") || ValidRepo("a") || ValidRepo("a/b/c") || ValidRepo("../x") {
		t.Error("ValidRepo")
	}
}
