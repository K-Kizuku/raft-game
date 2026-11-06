package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/gate"
	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/prbody"
)

type user struct {
	Login string `json:"login"`
}

type label struct {
	Name string `json:"name"`
}

type ref struct {
	Ref string `json:"ref"`
	SHA string `json:"sha"`
}

type pull struct {
	Number         int     `json:"number"`
	State          string  `json:"state"`
	Merged         bool    `json:"merged"`
	Draft          bool    `json:"draft"`
	Title          string  `json:"title"`
	Body           *string `json:"body"`
	User           user    `json:"user"`
	Labels         []label `json:"labels"`
	Base           ref     `json:"base"`
	Head           ref     `json:"head"`
	Mergeable      *bool   `json:"mergeable"`
	MergeableState string  `json:"mergeable_state"`
	ChangedFiles   int     `json:"changed_files"`
}

type issue struct {
	Number      int             `json:"number"`
	State       string          `json:"state"`
	Title       string          `json:"title"`
	Body        *string         `json:"body"`
	Labels      []label         `json:"labels"`
	PullRequest json.RawMessage `json:"pull_request"`
}

type comment struct {
	ID        int64  `json:"id"`
	User      user   `json:"user"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func labelNames(ls []label) []string {
	out := make([]string, 0, len(ls))
	for _, l := range ls {
		out = append(out, l.Name)
	}
	return out
}

func repoPath(repo string) string {
	owner, name, _ := strings.Cut(repo, "/")
	return "/repos/" + esc(owner) + "/" + esc(name)
}

func (c *Client) getPull(ctx context.Context, repo string, n int) (*pull, error) {
	var p pull
	if _, err := c.do(ctx, http.MethodGet, fmt.Sprintf("%s/pulls/%d", repoPath(repo), n), nil, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// OpenPullsForSHA は、HEAD が sha である開いている PR の番号を返す。
func (c *Client) OpenPullsForSHA(ctx context.Context, repo, sha string) ([]int, error) {
	ps, err := getPages(ctx, c, fmt.Sprintf("%s/commits/%s/pulls?per_page=100", repoPath(repo), esc(sha)), listOf[pull])
	if err != nil {
		return nil, err
	}
	var out []int
	for _, p := range ps {
		if p.State == "open" && p.Head.SHA == sha {
			out = append(out, p.Number)
		}
	}
	return out, nil
}

// mergeableRetries は、mergeable が未計算（null）のときに読み直す回数。
const mergeableRetries = 3

// FetchInput は、PR の判定に使う状態を集める。どれかの取得に失敗したらエラー。
func (c *Client) FetchInput(ctx context.Context, repo string, n int) (gate.Input, error) {
	in := gate.Input{Repo: repo}
	rp := repoPath(repo)

	p, err := c.getPull(ctx, repo, n)
	if err != nil {
		return in, err
	}
	for i := 0; i < mergeableRetries && p.Mergeable == nil && p.State == "open"; i++ {
		c.Sleep(2 * time.Second)
		if p, err = c.getPull(ctx, repo, n); err != nil {
			return in, err
		}
	}
	in.PullRequest = gate.PullRequest{
		Number: p.Number, State: p.State, Merged: p.Merged, Draft: p.Draft,
		Title: p.Title, Body: str(p.Body), Author: p.User.Login, Labels: labelNames(p.Labels),
		BaseRef: p.Base.Ref, BaseSHA: p.Base.SHA, HeadRef: p.Head.Ref, HeadSHA: p.Head.SHA,
		Mergeable: p.Mergeable, MergeableState: p.MergeableState, ChangedFiles: p.ChangedFiles,
	}
	if p.State != "open" {
		// closed・merged は fail にするだけなので、ほかの情報は集めない。
		return in, nil
	}
	head := p.Head.SHA

	if in.Files, err = getPages(ctx, c, fmt.Sprintf("%s/pulls/%d/files?per_page=100", rp, n), listOf[gate.File]); err != nil {
		return in, err
	}

	if refs := prbody.Parse(str(p.Body)).ClosingIssues(); len(refs) == 1 {
		var is issue
		_, err := c.do(ctx, http.MethodGet, fmt.Sprintf("%s/issues/%d", rp, refs[0]), nil, &is)
		switch {
		case errors.Is(err, ErrNotFound):
		case err != nil:
			return in, err
		default:
			in.Issue = &gate.Issue{
				Number: is.Number, State: is.State, Title: is.Title, Body: str(is.Body),
				Labels: labelNames(is.Labels), IsPullRequest: len(is.PullRequest) > 0 && string(is.PullRequest) != "null",
			}
		}
	}

	type checkRun struct {
		Name       string  `json:"name"`
		HeadSHA    string  `json:"head_sha"`
		Status     string  `json:"status"`
		Conclusion *string `json:"conclusion"`
		App        *struct {
			Slug string `json:"slug"`
		} `json:"app"`
	}
	runs, err := getPages(ctx, c, fmt.Sprintf("%s/commits/%s/check-runs?per_page=100", rp, esc(head)), func(raw []byte) ([]checkRun, error) {
		var v struct {
			CheckRuns []checkRun `json:"check_runs"`
		}
		err := json.Unmarshal(raw, &v)
		return v.CheckRuns, err
	})
	if err != nil {
		return in, err
	}
	for _, r := range runs {
		cr := gate.CheckRun{Name: r.Name, HeadSHA: r.HeadSHA, Status: r.Status, Conclusion: str(r.Conclusion)}
		if r.App != nil {
			cr.App = r.App.Slug
		}
		in.CheckRuns = append(in.CheckRuns, cr)
	}

	if in.Statuses, err = getPages(ctx, c, fmt.Sprintf("%s/commits/%s/status?per_page=100", rp, esc(head)), func(raw []byte) ([]gate.Status, error) {
		var v struct {
			Statuses []gate.Status `json:"statuses"`
		}
		err := json.Unmarshal(raw, &v)
		return v.Statuses, err
	}); err != nil {
		return in, err
	}

	cs, err := getPages(ctx, c, fmt.Sprintf("%s/issues/%d/comments?per_page=100", rp, n), listOf[comment])
	if err != nil {
		return in, err
	}
	for _, cm := range cs {
		in.Comments = append(in.Comments, gate.Comment{ID: cm.ID, Author: cm.User.Login, Body: cm.Body, CreatedAt: cm.CreatedAt, UpdatedAt: cm.UpdatedAt})
	}

	var cmp gate.Compare
	if _, err := c.do(ctx, http.MethodGet, fmt.Sprintf("%s/compare/%s...%s?per_page=1", rp, esc(p.Base.Ref), esc(head)), nil, &cmp); err != nil {
		return in, err
	}
	in.Compare = &cmp

	// 集めている間に HEAD が変わっていたら、混ざった情報で判定しない。
	again, err := c.getPull(ctx, repo, n)
	if err != nil {
		return in, err
	}
	if again.Head.SHA != head {
		return in, fmt.Errorf("github: 判定中に PR #%d の HEAD が %s から %s に変わった", n, head, again.Head.SHA)
	}
	return in, nil
}

// SetStatus は sha にコミットステータスを付ける。
func (c *Client) SetStatus(ctx context.Context, repo, sha, state, contextName, description, targetURL string) error {
	body := map[string]string{"state": state, "context": contextName, "description": description}
	if targetURL != "" {
		body["target_url"] = targetURL
	}
	_, err := c.do(ctx, http.MethodPost, fmt.Sprintf("%s/statuses/%s", repoPath(repo), esc(sha)), body, nil)
	return err
}

// UpsertComment は、author が書いた marker を含むコメントがあれば更新し、なければ作る。
func (c *Client) UpsertComment(ctx context.Context, repo string, n int, existing []gate.Comment, marker, author, body string) error {
	for _, cm := range existing {
		if strings.EqualFold(cm.Author, author) && strings.Contains(cm.Body, marker) {
			_, err := c.do(ctx, http.MethodPatch, fmt.Sprintf("%s/issues/comments/%d", repoPath(repo), cm.ID), map[string]string{"body": body}, nil)
			return err
		}
	}
	_, err := c.do(ctx, http.MethodPost, fmt.Sprintf("%s/issues/%d/comments", repoPath(repo), n), map[string]string{"body": body}, nil)
	return err
}
