package snapshot

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/github"
)

type restUser struct {
	Login string `json:"login"`
}

type restLabel struct {
	Name string `json:"name"`
}

type restIssue struct {
	Number      int             `json:"number"`
	Title       string          `json:"title"`
	State       string          `json:"state"`
	StateReason *string         `json:"state_reason"`
	Labels      []restLabel     `json:"labels"`
	Body        *string         `json:"body"`
	PullRequest json.RawMessage `json:"pull_request"`
}

type restPull struct {
	Number   int     `json:"number"`
	State    string  `json:"state"`
	MergedAt *string `json:"merged_at"`
	Body     *string `json:"body"`
	Head     struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"head"`
	Labels []restLabel `json:"labels"`
}

type restComment struct {
	User      restUser `json:"user"`
	Body      string   `json:"body"`
	CreatedAt string   `json:"created_at"`
}

type restRef struct {
	Ref string `json:"ref"`
}

type restStatus struct {
	Context string `json:"context"`
	State   string `json:"state"`
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func names(ls []restLabel) []string {
	out := make([]string, 0, len(ls))
	for _, l := range ls {
		out = append(out, l.Name)
	}
	return out
}

func (c restComment) toComment() Comment {
	return Comment{Author: c.User.Login, Body: c.Body, CreatedAt: c.CreatedAt}
}

// Fetch は REST API からスナップショットを組み立てる。どれかの取得に失敗したらエラー（部分的な情報で判断しない）。
//
//   - issues: すべての Issue（閉じたものも含む。PR は除く）。コメントは、開いた Issue と、
//     `status:merged` 以外の状態のラベルが残っている閉じた Issue のものだけを読む
//   - pulls: すべての PR。コメントは開いた PR のものだけを読む
//   - branches: branchPrefix で始まるブランチ
//   - statuses: 開いた PR の HEAD のコミットステータス
func Fetch(ctx context.Context, c *github.Client, repo, branchPrefix string) (*Snapshot, error) {
	rp := github.RepoPath(repo)
	s := &Snapshot{Issues: []Issue{}, Pulls: []Pull{}, Branches: []string{}, Statuses: map[string]map[string]string{}}

	comments := func(n int) ([]Comment, error) {
		cs, err := github.GetAll[restComment](ctx, c, fmt.Sprintf("%s/issues/%d/comments?per_page=100", rp, n))
		if err != nil {
			return nil, err
		}
		out := make([]Comment, 0, len(cs))
		for _, cm := range cs {
			out = append(out, cm.toComment())
		}
		return out, nil
	}

	issues, err := github.GetAll[restIssue](ctx, c, rp+"/issues?state=all&per_page=100")
	if err != nil {
		return nil, err
	}
	for _, ri := range issues {
		if len(ri.PullRequest) > 0 && string(ri.PullRequest) != "null" {
			continue
		}
		is := Issue{
			Number: ri.Number, Title: ri.Title, State: ri.State, StateReason: str(ri.StateReason),
			Labels: names(ri.Labels), Body: str(ri.Body), Comments: []Comment{},
		}
		sl := is.StatusLabels()
		if is.State == "open" || (len(sl) > 0 && !(len(sl) == 1 && sl[0] == StatusLabelPrefix+"merged")) {
			if is.Comments, err = comments(is.Number); err != nil {
				return nil, err
			}
		}
		s.Issues = append(s.Issues, is)
	}

	pulls, err := github.GetAll[restPull](ctx, c, rp+"/pulls?state=all&per_page=100")
	if err != nil {
		return nil, err
	}
	for _, rpull := range pulls {
		p := Pull{
			Number: rpull.Number, State: rpull.State, Merged: rpull.MergedAt != nil,
			HeadRef: rpull.Head.Ref, HeadSHA: rpull.Head.SHA, Body: str(rpull.Body), Labels: names(rpull.Labels),
		}
		if p.State == "open" {
			if p.Comments, err = comments(p.Number); err != nil {
				return nil, err
			}
			sts, err := github.GetPages(ctx, c, fmt.Sprintf("%s/commits/%s/status?per_page=100", rp, url.PathEscape(p.HeadSHA)), func(raw []byte) ([]restStatus, error) {
				var v struct {
					Statuses []restStatus `json:"statuses"`
				}
				err := json.Unmarshal(raw, &v)
				return v.Statuses, err
			})
			if err != nil {
				return nil, err
			}
			m := map[string]string{}
			for _, st := range sts {
				// 一覧は新しい順。最初に出てきたものを採る。
				if _, ok := m[st.Context]; !ok {
					m[st.Context] = st.State
				}
			}
			s.Statuses[p.HeadSHA] = m
		}
		s.Pulls = append(s.Pulls, p)
	}

	segs := strings.Split("heads/"+branchPrefix, "/")
	for i := range segs {
		segs[i] = url.PathEscape(segs[i])
	}
	refs, err := github.GetAll[restRef](ctx, c, rp+"/git/matching-refs/"+strings.Join(segs, "/")+"?per_page=100")
	if err != nil {
		return nil, err
	}
	for _, r := range refs {
		if b, ok := strings.CutPrefix(r.Ref, "refs/heads/"); ok {
			s.Branches = append(s.Branches, b)
		}
	}
	if err := s.Validate(); err != nil {
		return nil, err
	}
	return s, nil
}
