package github

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/gate"
)

const (
	carryS    = "3333333333333333333333333333333333333333"
	carryMain = "4444444444444444444444444444444444444444"
	carryMid  = "5555555555555555555555555555555555555555"
	carryAuth = "6666666666666666666666666666666666666666"
)

// carryFake は、引き継ぎの材料の API（git/commits・compare）の偽物。
type carryFake struct {
	mu      sync.Mutex
	commits map[string][]string // SHA → 親
	// compare は GET /compare/main...{sha} の応答（sha ごと）。
	compare map[string]map[string]any
	fail    map[string]bool
	paths   []string
}

func (f *carryFake) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paths = append(f.paths, r.URL.Path)
	if f.fail[r.URL.Path] {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"message":"boom"}`))
		return
	}
	pre := "/repos/" + repo
	if sha, ok := strings.CutPrefix(r.URL.Path, pre+"/git/commits/"); ok {
		ps, ok := f.commits[sha]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"Not Found"}`))
			return
		}
		var parents []any
		for _, p := range ps {
			parents = append(parents, map[string]any{"sha": p})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"sha": sha, "parents": parents})
		return
	}
	if sha, ok := strings.CutPrefix(r.URL.Path, pre+"/compare/main..."); ok {
		if v, ok := f.compare[sha]; ok {
			_ = json.NewEncoder(w).Encode(v)
			return
		}
	}
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte(`{"message":"Not Found"}`))
}

func newCarryFake(t *testing.T) (*carryFake, *Client) {
	f := &carryFake{
		commits: map[string][]string{
			head:     {carryMid, carryMain},
			carryMid: {carryS, carryMain},
		},
		compare: map[string]map[string]any{
			carryMain: {"status": "behind", "ahead_by": 0, "behind_by": 2},
			carryS: {"status": "diverged", "ahead_by": 1, "behind_by": 3, "files": []any{
				map[string]any{"filename": "docs/a.md", "status": "modified", "patch": "@@ -1 +1 @@\n-a\n+b"},
			}},
		},
		fail: map[string]bool{},
	}
	srv := httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(srv.Close)
	c, err := New(srv.URL, "tok")
	if err != nil {
		t.Fatal(err)
	}
	c.Sleep = func(time.Duration) {}
	return f, c
}

func carryInput() gate.Input {
	return gate.Input{PullRequest: gate.PullRequest{Number: 12, BaseRef: "main", HeadSHA: head}}
}

func TestFetchCarryOver(t *testing.T) {
	f, c := newCarryFake(t)
	in := carryInput()
	c.FetchCarryOver(context.Background(), repo, &in, carryS)
	co := in.CarryOver
	if co == nil || co.Error != "" {
		t.Fatalf("carry_over = %+v", co)
	}
	if co.SHA != carryS || len(co.Commits) != 2 || co.Commits[0].SHA != head || co.Commits[1].SHA != carryMid ||
		len(co.Commits[1].Parents) != 2 || co.Commits[1].Parents[0] != carryS {
		t.Errorf("commits = %+v", co.Commits)
	}
	if !co.InBase[carryMain] || len(co.InBase) != 1 {
		t.Errorf("in_base = %+v", co.InBase)
	}
	if len(co.Files) != 1 || co.Files[0].Filename != "docs/a.md" || co.Files[0].Patch == "" {
		t.Errorf("files = %+v", co.Files)
	}
	// 同じ親の compare は 1 回だけ。
	n := 0
	for _, p := range f.paths {
		if strings.HasSuffix(p, "/compare/main..."+carryMain) {
			n++
		}
	}
	if n != 1 {
		t.Errorf("compare main...%s を %d 回呼んだ", carryMain, n)
	}
}

func TestFetchCarryOverStopsAtAuthorCommit(t *testing.T) {
	f, c := newCarryFake(t)
	f.commits[carryMid] = []string{carryAuth}
	in := carryInput()
	c.FetchCarryOver(context.Background(), repo, &in, carryS)
	co := in.CarryOver
	if co.Error != "" || len(co.Commits) != 2 || co.Commits[1].SHA != carryMid || len(co.Commits[1].Parents) != 1 {
		t.Errorf("carry_over = %+v", co)
	}
	if len(co.Files) != 0 {
		t.Errorf("files = %+v（作者のコミットに当たったら集めない）", co.Files)
	}
}

func TestFetchCarryOverParentNotInBase(t *testing.T) {
	f, c := newCarryFake(t)
	f.compare[carryMain] = map[string]any{"status": "diverged", "ahead_by": 1, "behind_by": 2}
	in := carryInput()
	c.FetchCarryOver(context.Background(), repo, &in, carryS)
	if v, ok := in.CarryOver.InBase[carryMain]; !ok || v {
		t.Errorf("in_base = %+v", in.CarryOver.InBase)
	}
}

func TestFetchCarryOverErrors(t *testing.T) {
	for name, mutate := range map[string]func(f *carryFake){
		"git/commits が失敗":   func(f *carryFake) { f.fail["/repos/"+repo+"/git/commits/"+carryMid] = true },
		"親の compare が失敗":    func(f *carryFake) { f.fail["/repos/"+repo+"/compare/main..."+carryMain] = true },
		"S の compare が失敗":   func(f *carryFake) { f.fail["/repos/"+repo+"/compare/main..."+carryS] = true },
		"コミットが見つからない":       func(f *carryFake) { delete(f.commits, carryMid) },
		"コミットが多すぎる（循環している）": func(f *carryFake) { f.commits[carryMid] = []string{head, carryMain} },
	} {
		t.Run(name, func(t *testing.T) {
			f, c := newCarryFake(t)
			mutate(f)
			in := carryInput()
			c.FetchCarryOver(context.Background(), repo, &in, carryS)
			if in.CarryOver == nil || in.CarryOver.Error == "" {
				t.Errorf("carry_over = %+v（error が要る）", in.CarryOver)
			}
		})
	}
}
