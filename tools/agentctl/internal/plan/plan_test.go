package plan

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/config"
	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/snapshot"
	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/state"
)

func cfg(maxWorkers int) *config.Config {
	return &config.Config{
		BranchPrefix: "agent/issue-", MaxWorkers: maxWorkers, RetryLimit: 3,
		SharedAppendFiles: []string{"CLAUDE.md", "docs/README.md"},
		Review:            config.Review{TrustedAuthors: []string{"K-Kizuku"}},
	}
}

// iss は、状態・依存・変更予定ファイルを持つ開いた Issue を作る。
func iss(n int, status string, deps []int, files ...string) snapshot.Issue {
	var b strings.Builder
	b.WriteString("## 変更予定ファイル\n\n")
	for _, f := range files {
		fmt.Fprintf(&b, "- `%s`\n", f)
	}
	b.WriteString("\n## 依存 Issue\n\n")
	if len(deps) == 0 {
		b.WriteString("- なし\n")
	}
	for _, d := range deps {
		fmt.Fprintf(&b, "- #%d\n", d)
	}
	is := snapshot.Issue{Number: n, Title: fmt.Sprintf("T%d", n), State: "open", Body: b.String()}
	if status != "" {
		is.Labels = []string{"status:" + status}
	}
	return is
}

func closed(n int, reason string) snapshot.Issue {
	return snapshot.Issue{Number: n, Title: fmt.Sprintf("T%d", n), State: "closed", StateReason: reason, Labels: []string{"status:merged"}, Body: "## 依存 Issue\n- なし\n"}
}

func nums(items []Item) []int {
	out := []int{}
	for _, it := range items {
		out = append(out, it.Number)
	}
	return out
}

func mustPlan(t *testing.T, s *snapshot.Snapshot, c *config.Config) *Result {
	t.Helper()
	res, err := Plan(s, c, trusted)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestPlanDependencies(t *testing.T) {
	t.Run("鎖", func(t *testing.T) {
		// 1(完了) → 2(ready) → 3(ready) → 4(ready)
		s := &snapshot.Snapshot{Issues: []snapshot.Issue{
			closed(1, "completed"), iss(2, "ready", []int{1}, "a/**"), iss(3, "ready", []int{2}, "b/**"), iss(4, "ready", []int{3}, "c/**"),
		}}
		res := mustPlan(t, s, cfg(2))
		if !reflect.DeepEqual(nums(res.Ready), []int{2}) || !reflect.DeepEqual(nums(res.BlockedByDeps), []int{3, 4}) {
			t.Errorf("ready = %v, blocked = %v", nums(res.Ready), nums(res.BlockedByDeps))
		}
		if res.Ready[0].CriticalPath != 3 {
			t.Errorf("critical_path = %d", res.Ready[0].CriticalPath)
		}
	})
	t.Run("菱形", func(t *testing.T) {
		// 1 → {2, 3} → 4
		s := &snapshot.Snapshot{Issues: []snapshot.Issue{
			iss(1, "ready", nil, "a/**"), iss(2, "backlog", []int{1}, "b/**"), iss(3, "backlog", []int{1}, "c/**"), iss(4, "backlog", []int{2, 3}, "d/**"),
		}}
		g, err := Build(s)
		if err != nil {
			t.Fatal(err)
		}
		if g.CriticalPath(1) != 3 || g.CriticalPath(2) != 2 || g.CriticalPath(4) != 1 {
			t.Errorf("cp = %d %d %d", g.CriticalPath(1), g.CriticalPath(2), g.CriticalPath(4))
		}
		if res := mustPlan(t, s, cfg(2)); !reflect.DeepEqual(nums(res.Ready), []int{1}) {
			t.Errorf("ready = %v", nums(res.Ready))
		}
		// 1 と 2 が完了すると、4 は 3 を待つ。
		s.Issues[0] = closed(1, "completed")
		s.Issues[1] = closed(2, "completed")
		s.Issues[2].Labels = []string{"status:ready"}
		s.Issues[3].Labels = []string{"status:ready"}
		res := mustPlan(t, s, cfg(2))
		if !reflect.DeepEqual(nums(res.Ready), []int{3}) || !reflect.DeepEqual(nums(res.BlockedByDeps), []int{4}) {
			t.Errorf("ready = %v, blocked = %v", nums(res.Ready), nums(res.BlockedByDeps))
		}
	})
	t.Run("循環", func(t *testing.T) {
		s := &snapshot.Snapshot{Issues: []snapshot.Issue{
			iss(1, "ready", nil, "a"), iss(2, "backlog", []int{4}, "b"), iss(3, "backlog", []int{2}, "c"), iss(4, "backlog", []int{3}, "d"),
		}}
		_, err := Plan(s, cfg(2), trusted)
		var ce *CycleError
		if !errors.As(err, &ce) {
			t.Fatalf("err = %v", err)
		}
		if !reflect.DeepEqual(ce.Cycle, []int{2, 4, 3, 2}) || !strings.Contains(err.Error(), "#2 → #4 → #3 → #2") {
			t.Errorf("cycle = %v (%v)", ce.Cycle, err)
		}
		if _, err := Mermaid(s); !errors.As(err, &ce) {
			t.Errorf("mermaid: err = %v", err)
		}
		// 自分自身への依存も循環。
		s = &snapshot.Snapshot{Issues: []snapshot.Issue{iss(5, "ready", []int{5}, "a")}}
		if _, err := Plan(s, cfg(2), trusted); !errors.As(err, &ce) {
			t.Errorf("自己ループ: err = %v", err)
		}
	})
	t.Run("依存先が not_planned", func(t *testing.T) {
		s := &snapshot.Snapshot{Issues: []snapshot.Issue{closed(1, "not_planned"), iss(2, "ready", []int{1}, "a/**")}}
		res := mustPlan(t, s, cfg(2))
		if len(res.Ready) != 0 || len(res.BlockedByDeps) != 1 || !strings.Contains(strings.Join(res.BlockedByDeps[0].Reasons, ""), "not_planned") {
			t.Errorf("res = %+v", res)
		}
	})
	t.Run("依存先が存在しない", func(t *testing.T) {
		s := &snapshot.Snapshot{Issues: []snapshot.Issue{iss(2, "ready", []int{77}, "a/**")}}
		if _, err := Plan(s, cfg(2), trusted); err == nil || !strings.Contains(err.Error(), "#77") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("ブランチや PR がある", func(t *testing.T) {
		s := &snapshot.Snapshot{
			Issues:   []snapshot.Issue{iss(2, "ready", nil, "a/**"), iss(3, "ready", nil, "b/**"), iss(4, "ready", nil, "c/**")},
			Branches: []string{"agent/issue-2"},
			Pulls: []snapshot.Pull{{Number: 50, State: "open", Author: "K-Kizuku", HeadRef: "x", Body: "Closes #3"}, {Number: 51, State: "closed", Author: "K-Kizuku", HeadRef: "agent/issue-4"},
				// 信頼しない作者の PR は、Issue との関係を数えない。
				{Number: 52, State: "open", Author: "someone", HeadRef: "agent/issue-4", Body: "Closes #4"}},
		}
		res := mustPlan(t, s, cfg(3))
		if !reflect.DeepEqual(nums(res.Ready), []int{4}) || !reflect.DeepEqual(nums(res.BlockedByDeps), []int{2, 3}) {
			t.Errorf("ready = %v, blocked = %v", nums(res.Ready), nums(res.BlockedByDeps))
		}
	})
}

func TestPlanCapacity(t *testing.T) {
	base := func() *snapshot.Snapshot {
		return &snapshot.Snapshot{Issues: []snapshot.Issue{
			iss(10, "in-progress", nil, "x/**"), iss(11, "in-review", nil, "y/**"),
			iss(1, "ready", nil, "a/**"), iss(2, "ready", nil, "b/**"), iss(3, "ready", nil, "c/**"),
		}}
	}
	for _, tt := range []struct {
		max, capacity   int
		ready, deferred []int
	}{
		{1, 0, []int{}, []int{1, 2, 3}}, // in-progress が 1 つ → 容量 0
		{2, 1, []int{1}, []int{2, 3}},   // 容量 1
		{3, 2, []int{1, 2}, []int{3}},   // 容量 2
		{4, 3, []int{1, 2, 3}, []int{}}, // すべて選べる
	} {
		t.Run(fmt.Sprint(tt.max), func(t *testing.T) {
			res := mustPlan(t, base(), cfg(tt.max))
			if res.Capacity != tt.capacity || !reflect.DeepEqual(nums(res.Ready), tt.ready) {
				t.Errorf("capacity = %d, ready = %v", res.Capacity, nums(res.Ready))
			}
			// 容量で外れたものは deferred に、理由つきで入る。
			if !reflect.DeepEqual(nums(res.Deferred), tt.deferred) {
				t.Errorf("deferred = %v, want %v", nums(res.Deferred), tt.deferred)
			}
			for _, it := range res.Deferred {
				if len(it.Reasons) != 1 || !strings.Contains(it.Reasons[0], "容量") {
					t.Errorf("#%d の理由 = %q", it.Number, it.Reasons)
				}
			}
			if !reflect.DeepEqual(nums(res.Active), []int{10, 11}) {
				t.Errorf("active = %v", nums(res.Active))
			}
		})
	}
	// changes-requested も数える。
	s := base()
	s.Issues[1].Labels = []string{"status:changes-requested"}
	if res := mustPlan(t, s, cfg(2)); res.Capacity != 0 || len(res.Ready) != 0 {
		t.Errorf("capacity = %d", res.Capacity)
	}
}

func TestPlanConflicts(t *testing.T) {
	t.Run("実装中と重なる", func(t *testing.T) {
		s := &snapshot.Snapshot{Issues: []snapshot.Issue{
			iss(10, "in-review", nil, "tools/agentctl/**"),
			iss(1, "ready", nil, "tools/agentctl/internal/plan/plan.go"),
			iss(2, "ready", nil, "tools/agentctl2/x.go", "server/**"),
		}}
		res := mustPlan(t, s, cfg(3))
		if !reflect.DeepEqual(nums(res.Ready), []int{2}) || !reflect.DeepEqual(nums(res.Conflicts), []int{1}) ||
			!reflect.DeepEqual(res.Conflicts[0].ConflictsWith, []int{10}) {
			t.Errorf("ready = %v, conflicts = %+v", nums(res.Ready), res.Conflicts)
		}
	})
	t.Run("同時に選ぶものと重なる", func(t *testing.T) {
		s := &snapshot.Snapshot{Issues: []snapshot.Issue{
			iss(1, "ready", nil, "web/src/*.ts"), iss(2, "ready", nil, "web/src/app/main.ts"), iss(3, "ready", nil, "server/a.go"),
		}}
		res := mustPlan(t, s, cfg(3))
		if !reflect.DeepEqual(nums(res.Ready), []int{1, 3}) || !reflect.DeepEqual(nums(res.Conflicts), []int{2}) {
			t.Errorf("ready = %v, conflicts = %v", nums(res.Ready), nums(res.Conflicts))
		}
	})
	t.Run("重ならない", func(t *testing.T) {
		s := &snapshot.Snapshot{Issues: []snapshot.Issue{
			iss(10, "in-progress", nil, "server/**"), iss(1, "ready", nil, "web/**"), iss(2, "ready", nil, "docs/adr/0020.md"),
		}}
		res := mustPlan(t, s, cfg(3))
		if !reflect.DeepEqual(nums(res.Ready), []int{1, 2}) || len(res.Conflicts) != 0 {
			t.Errorf("ready = %v, conflicts = %v", nums(res.Ready), nums(res.Conflicts))
		}
	})
	t.Run("shared_append_files だけ", func(t *testing.T) {
		s := &snapshot.Snapshot{Issues: []snapshot.Issue{
			iss(10, "in-progress", nil, "server/**", "CLAUDE.md"), iss(1, "ready", nil, "web/**", "CLAUDE.md", "docs/README.md"),
			iss(2, "ready", nil, "scripts/**", "./docs/README.md"),
		}}
		res := mustPlan(t, s, cfg(3))
		if !reflect.DeepEqual(nums(res.Ready), []int{1, 2}) || len(res.Conflicts) != 0 {
			t.Fatalf("ready = %v, conflicts = %+v", nums(res.Ready), res.Conflicts)
		}
		if len(res.Ready[0].Warnings) == 0 || len(res.Ready[1].Warnings) == 0 {
			t.Errorf("警告がない: %+v", res.Ready)
		}
	})
	t.Run("shared でも glob との重なりは競合", func(t *testing.T) {
		s := &snapshot.Snapshot{Issues: []snapshot.Issue{iss(10, "in-progress", nil, "docs/**"), iss(1, "ready", nil, "docs/README.md")}}
		if res := mustPlan(t, s, cfg(3)); len(res.Ready) != 0 || len(res.Conflicts) != 1 {
			t.Errorf("res = %+v", res)
		}
	})
	t.Run("変更予定ファイルがない", func(t *testing.T) {
		s := &snapshot.Snapshot{Issues: []snapshot.Issue{iss(10, "in-progress", nil, "server/**"), iss(1, "ready", nil)}}
		if res := mustPlan(t, s, cfg(3)); len(res.Ready) != 0 || len(res.Conflicts) != 1 {
			t.Errorf("res = %+v", res)
		}
	})
}

func TestOverlap(t *testing.T) {
	for _, tt := range []struct {
		a, b string
		want bool
	}{
		{"tools/agentctl/**", "tools/agentctl/main.go", true},
		{"tools/agentctl/**", "tools/agentctl2/main.go", false},
		{"web/src/*.ts", "web/src/app/x.ts", true},
		{"web/**", "server/**", false},
		{"docs/a.md", "docs/a.md", true},
		{"docs", "docs/a.md", true},
		{"**", "anything", true},
	} {
		h, _ := Overlap([]string{tt.a}, []string{tt.b}, nil)
		if (len(h) > 0) != tt.want {
			t.Errorf("Overlap(%q, %q) = %v", tt.a, tt.b, h)
		}
	}
}

func TestPlanCriticalPathOrder(t *testing.T) {
	// 5 の後ろには 2 つ続き、1 と 3 には続かない。容量 2 なら 5 と 1。
	s := &snapshot.Snapshot{Issues: []snapshot.Issue{
		iss(1, "ready", nil, "a/**"), iss(3, "ready", nil, "c/**"), iss(5, "ready", nil, "e/**"),
		iss(6, "backlog", []int{5}, "f/**"), iss(7, "backlog", []int{6}, "g/**"),
	}}
	res := mustPlan(t, s, cfg(2))
	if !reflect.DeepEqual(nums(res.Ready), []int{5, 1}) {
		t.Errorf("ready = %v", nums(res.Ready))
	}
}

func TestPlanBrokenLabels(t *testing.T) {
	is := iss(1, "ready", nil, "a/**")
	is.Labels = append(is.Labels, "status:in-progress")
	res := mustPlan(t, &snapshot.Snapshot{Issues: []snapshot.Issue{is}}, cfg(2))
	if len(res.Ready) != 0 || len(res.Warnings) != 1 {
		t.Errorf("res = %+v", res)
	}
}

func TestMermaid(t *testing.T) {
	s := &snapshot.Snapshot{Issues: []snapshot.Issue{
		closed(1, "completed"), iss(2, "in-progress", []int{1}, "a"), iss(3, "ready", []int{2}, "b"),
	}}
	s.Issues[2].Title = `"引用" <b>`
	out, err := Mermaid(s)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"flowchart TD\n", "classDef in_progress ", `i1["#1 T1"]:::closed`, `i2["#2 T2"]:::in_progress`,
		`i3["#3 #quot;引用#quot; #lt;b#gt;"]:::ready`, "i1 --> i2\n", "i2 --> i3\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("%q がない:\n%s", want, out)
		}
	}
}

// --- recover ---

const sha1 = "1111111111111111111111111111111111111111"

func trusted(l string) bool { return l == "K-Kizuku" }

func claimComment(worker, session string) snapshot.Comment {
	r := state.Record{From: state.Ready, To: state.InProgress, Actor: "orchestrator", Worker: worker, Branch: "agent/issue-" + strings.TrimPrefix(worker, "issue-"),
		Session: session, Attempt: 1, At: "2026-11-06T12:00:00Z"}
	return snapshot.Comment{Author: "K-Kizuku", Body: r.Comment()}
}

func reviewComment(pr int, head string) snapshot.Comment {
	return snapshot.Comment{Author: "K-Kizuku", Body: fmt.Sprintf("<!-- agent-review:v1 -->\n```json\n"+
		`{"schema":"agent-review/v1","pr":%d,"head_sha":"%s","base_sha":"%s","round":1,"reviewer":"codex","reviewer_version":"codex-cli 1",`+
		`"fallback_reason":"","verdict":"approve","summary":"ok","protected_paths_ack":{"reviewed":true,"weakens_guards":false},"findings":[],`+
		`"created_at":"2026-11-06T12:00:00Z"}`+"\n```\n", pr, head, strings.Repeat("2", 40))}
}

func recoverOne(t *testing.T, s *snapshot.Snapshot, n int) *Action {
	t.Helper()
	res := Recover(s, cfg(2), trusted)
	var found *Action
	for i := range res.Actions {
		if res.Actions[i].Issue == n {
			if found != nil {
				t.Fatalf("#%d に行動が 2 つある: %+v", n, res.Actions)
			}
			found = &res.Actions[i]
		}
	}
	return found
}

func TestRecover(t *testing.T) {
	pull := func(n int, issue int, open bool) snapshot.Pull {
		p := snapshot.Pull{Number: n, State: "open", Author: "K-Kizuku", HeadRef: fmt.Sprintf("agent/issue-%d", issue), HeadSHA: sha1, Body: fmt.Sprintf("Closes #%d", issue)}
		if !open {
			p.State, p.Merged = "closed", true
		}
		return p
	}
	withClaim := func(is snapshot.Issue) snapshot.Issue {
		is.Comments = []snapshot.Comment{claimComment(fmt.Sprintf("issue-%d", is.Number), "sess-old"), claimComment(fmt.Sprintf("issue-%d", is.Number), "sess-new")}
		return is
	}
	tests := []struct {
		name   string
		snap   *snapshot.Snapshot
		action string
		to     string
		pr     int
	}{
		{"in-progress、ブランチも PR もない", &snapshot.Snapshot{Issues: []snapshot.Issue{iss(4, "in-progress", nil, "a")}}, ActionResetToReady, "", 0},
		{"in-progress、ブランチも PR もなく、Worktree は綺麗", &snapshot.Snapshot{Issues: []snapshot.Issue{iss(4, "in-progress", nil, "a")},
			Worktrees: []snapshot.Worktree{{Issue: 4, Path: "/w/.claude/worktrees/issue-4", Branch: "agent/issue-4"}}}, ActionResetToReady, "", 0},
		{"in-progress、ブランチも PR もないが、未コミットの変更がある", &snapshot.Snapshot{Issues: []snapshot.Issue{withClaim(iss(4, "in-progress", nil, "a"))},
			Worktrees: []snapshot.Worktree{{Issue: 4, Path: "/w/.claude/worktrees/issue-4", Dirty: true}}}, ActionResumeWorker, "", 0},
		{"in-progress、ブランチも PR もないが、push していないコミットがある", &snapshot.Snapshot{Issues: []snapshot.Issue{withClaim(iss(4, "in-progress", nil, "a"))},
			Worktrees: []snapshot.Worktree{{Issue: 4, Path: "/w/.claude/worktrees/issue-4", Ahead: 2}}}, ActionResumeWorker, "", 0},
		{"in-progress、ブランチあり、PR なし", &snapshot.Snapshot{Issues: []snapshot.Issue{withClaim(iss(4, "in-progress", nil, "a"))}, Branches: []string{"agent/issue-4"}}, ActionResumeWorker, "", 0},
		{"in-progress、PR あり", &snapshot.Snapshot{Issues: []snapshot.Issue{iss(4, "in-progress", nil, "a")}, Branches: []string{"agent/issue-4"},
			Pulls: []snapshot.Pull{pull(20, 4, true)}}, ActionSetState, "in-review", 20},
		{"in-review、HEAD のレビュー記録がない", &snapshot.Snapshot{Issues: []snapshot.Issue{iss(4, "in-review", nil, "a")},
			Pulls: []snapshot.Pull{func() snapshot.Pull {
				p := pull(20, 4, true)
				p.Comments = []snapshot.Comment{reviewComment(20, strings.Repeat("3", 40))} // 古い HEAD のレビュー
				return p
			}()}}, ActionRequestReview, "", 20},
		{"changes-requested", &snapshot.Snapshot{Issues: []snapshot.Issue{withClaim(iss(4, "changes-requested", nil, "a"))}, Pulls: []snapshot.Pull{pull(20, 4, true)}}, ActionResumeWorker, "", 20},
		{"ready-to-merge、Merge Gate が pass", &snapshot.Snapshot{Issues: []snapshot.Issue{iss(4, "ready-to-merge", nil, "a")}, Pulls: []snapshot.Pull{pull(20, 4, true)},
			Statuses: map[string]map[string]string{sha1: {"merge-gate": "success"}}}, ActionMerge, "", 20},
		{"PR がマージ済みなのに merged ではない", &snapshot.Snapshot{Issues: []snapshot.Issue{func() snapshot.Issue {
			is := iss(4, "ready-to-merge", nil, "a")
			is.State, is.StateReason = "closed", "completed"
			return is
		}()}, Pulls: []snapshot.Pull{pull(20, 4, false)}}, ActionSetState, "merged", 20},
		// merged へは ready-to-merge からだけ。ほかの状態のままマージされたら（例: 人が手でマージした）人に任せる。
		{"PR がマージ済みだが in-review のまま", &snapshot.Snapshot{Issues: []snapshot.Issue{func() snapshot.Issue {
			is := iss(4, "in-review", nil, "a")
			is.State, is.StateReason = "closed", "completed"
			return is
		}()}, Pulls: []snapshot.Pull{pull(20, 4, false)}}, ActionEscalate, "", 20},
		{"PR がマージ済みだが ci-pending で開いたまま", &snapshot.Snapshot{Issues: []snapshot.Issue{iss(4, "ci-pending", nil, "a")},
			Pulls: []snapshot.Pull{pull(20, 4, false)}}, ActionEscalate, "", 20},
		{"開いた PR が 2 つ", &snapshot.Snapshot{Issues: []snapshot.Issue{iss(4, "in-review", nil, "a")},
			Pulls: []snapshot.Pull{pull(20, 4, true), {Number: 21, State: "open", Author: "K-Kizuku", HeadRef: "other", HeadSHA: sha1, Body: "Fixes #4"}}}, ActionEscalate, "", 0},
		{"信頼しない作者の PR は数えない", &snapshot.Snapshot{Issues: []snapshot.Issue{withClaim(iss(4, "in-progress", nil, "a"))}, Branches: []string{"agent/issue-4"},
			Pulls: []snapshot.Pull{{Number: 21, State: "open", Author: "someone", HeadRef: "agent/issue-4", HeadSHA: sha1, Body: "Closes #4"},
				{Number: 22, State: "closed", Merged: true, Author: "someone", HeadRef: "x", HeadSHA: sha1, Body: "Closes #4"}}}, ActionResumeWorker, "", 0},
		{"status:* が 2 つ", &snapshot.Snapshot{Issues: []snapshot.Issue{func() snapshot.Issue {
			is := iss(4, "ready", nil, "a")
			is.Labels = append(is.Labels, "status:in-progress")
			return is
		}()}}, ActionFixLabels, "", 0},
		{"status:* が 0 個（記録はある）", &snapshot.Snapshot{Issues: []snapshot.Issue{withClaim(iss(4, "", nil, "a"))}}, ActionFixLabels, "", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := recoverOne(t, tt.snap, 4)
			if a == nil {
				t.Fatal("行動がない")
			}
			if a.Action != tt.action || a.To != tt.to || a.PR != tt.pr {
				t.Errorf("action = %+v", *a)
			}
			if a.Action == ActionResumeWorker && (a.Session != "sess-new" || a.Worker != "issue-4") {
				t.Errorf("resume-worker に最新の session がない: %+v", *a)
			}
			if a.Action == ActionEscalate && !reflect.DeepEqual(a.Labels, []string{"needs-human"}) {
				t.Errorf("labels = %v", a.Labels)
			}
			if a.Action == ActionEscalate && a.PR != 0 && !strings.Contains(a.Reason, "merged に遷移できない") {
				t.Errorf("reason = %q", a.Reason)
			}
		})
	}
}

func TestRecoverNoAction(t *testing.T) {
	p := snapshot.Pull{Number: 20, State: "open", Author: "K-Kizuku", HeadRef: "agent/issue-4", HeadSHA: sha1, Body: "Closes #4",
		Comments: []snapshot.Comment{reviewComment(20, sha1)}}
	s := &snapshot.Snapshot{
		Issues: []snapshot.Issue{
			iss(4, "in-review", nil, "a"),         // HEAD のレビュー記録がある
			iss(5, "ready", nil, "a"),             // 作業中ではない
			{Number: 6, State: "open", Body: "x"}, // エージェントの対象ではない
			closed(7, "completed"),                // 完了
		},
		Pulls: []snapshot.Pull{p},
	}
	if res := Recover(s, cfg(2), trusted); len(res.Actions) != 0 {
		t.Errorf("actions = %+v", res.Actions)
	}
	// 信頼しない作者のレビュー記録は数えない。
	s.Pulls[0].Comments[0].Author = "someone"
	if a := recoverOne(t, s, 4); a == nil || a.Action != ActionRequestReview {
		t.Errorf("action = %+v", a)
	}
}
