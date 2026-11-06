package state

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/github"
	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/snapshot"
)

const repo = "K-Kizuku/raft-game"

var fixedNow = time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

func env() Env {
	return Env{Repo: repo, BranchPrefix: "agent/issue-", RetryLimit: 3, Now: fixedNow,
		Trusted: func(l string) bool { return l == "K-Kizuku" }}
}

func body(deps ...int) string {
	var b strings.Builder
	b.WriteString("## 変更予定ファイル\n\n- `tools/x/**`\n\n## 依存 Issue\n\n")
	if len(deps) == 0 {
		b.WriteString("- なし\n")
	}
	for _, d := range deps {
		fmt.Fprintf(&b, "- #%d\n", d)
	}
	return b.String()
}

func recComment(author string, r Record) snapshot.Comment {
	if r.At == "" {
		r.At = FormatTime(fixedNow)
	}
	if r.Actor == "" {
		r.Actor = DefaultActor
	}
	return snapshot.Comment{Author: author, Body: r.Comment(), CreatedAt: r.At}
}

func snap(issues ...snapshot.Issue) *snapshot.Snapshot {
	return &snapshot.Snapshot{Issues: issues}
}

// applyLabels は、ops のラベルの操作を labels に当てた結果を返す。
func applyLabels(t *testing.T, labels []string, ops []github.Op) []string {
	t.Helper()
	out := append([]string(nil), labels...)
	for _, op := range ops {
		switch {
		case op.Method == "POST" && strings.HasSuffix(op.Path, "/labels"):
			out = append(out, op.Body.(map[string][]string)["labels"]...)
		case op.Method == "DELETE":
			i := strings.LastIndex(op.Path, "/labels/")
			name, _ := url.PathUnescape(op.Path[i+len("/labels/"):])
			found := false
			for j, l := range out {
				if l == name {
					out = append(out[:j], out[j+1:]...)
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("付いていないラベル %q を外そうとした", name)
			}
		}
	}
	return out
}

func commentRecord(t *testing.T, op github.Op) *Record {
	t.Helper()
	if op.Method != "POST" || !strings.HasSuffix(op.Path, "/comments") {
		t.Fatalf("コメントの操作ではない: %+v", op)
	}
	r, err := ParseComment(op.Body.(map[string]string)["body"])
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestTransitionTable(t *testing.T) {
	allowed := map[Status][]Status{
		Backlog:          {Ready, Blocked},
		Ready:            {InProgress, Backlog, Blocked},
		InProgress:       {InReview, Ready, Blocked},
		InReview:         {ChangesRequested, CIPending, ReadyToMerge, Blocked},
		ChangesRequested: {InReview, Blocked},
		CIPending:        {ReadyToMerge, ChangesRequested, InReview, Blocked},
		ReadyToMerge:     {Merged, InReview, ChangesRequested, Blocked},
		Blocked:          {Ready, Backlog, InProgress},
		Merged:           {},
	}
	n := 0
	for _, from := range All {
		for _, to := range All {
			want := false
			for _, a := range allowed[from] {
				want = want || a == to
			}
			if want {
				n++
			}
			if got := CanTransition(from, to); got != want {
				t.Errorf("%s → %s = %v, want %v", from, to, got, want)
			}
		}
	}
	if n != 25 {
		t.Errorf("許す遷移の数 = %d", n)
	}
	if _, err := Parse("status:in-review"); err != nil {
		t.Error(err)
	}
	if _, err := Parse("done"); err == nil {
		t.Error("未知の状態を受け入れた")
	}
}

func TestRecordRoundTrip(t *testing.T) {
	r := Record{From: Ready, To: InProgress, Actor: "orchestrator", Worker: "issue-12", Branch: "agent/issue-12",
		Session: "0b6f3c4e-0000-4000-8000-000000000000", Attempt: 2, Cause: "ci:ci-ok",
		Reason: "<b>危ない --> 文字</b> & \"引用\"\n改行", PR: 34, At: "2026-11-06T12:00:00Z"}
	c := r.Comment()
	first := strings.SplitN(c, "\n", 2)[0]
	if !strings.HasPrefix(first, "<!-- agent-state:v1 {\"from\":\"ready\",\"to\":\"in-progress\",\"actor\":\"orchestrator\"") || !strings.HasSuffix(first, " -->") {
		t.Fatalf("1 行目 = %s", first)
	}
	if strings.Count(first, "-->") != 1 {
		t.Errorf("JSON の中に --> がある: %s", first)
	}
	got, err := ParseComment(c)
	if err != nil {
		t.Fatal(err)
	}
	if *got != r {
		t.Errorf("往復 = %+v, want %+v", *got, r)
	}
}

func TestParseCommentBroken(t *testing.T) {
	for name, b := range map[string]string{
		"JSON が壊れている": "<!-- agent-state:v1 {\"from\": -->",
		"未知のキー":       `<!-- agent-state:v1 {"from":"ready","to":"in-progress","x":1,"at":"2026-11-06T12:00:00Z"} -->`,
		"未知の状態":       `<!-- agent-state:v1 {"from":"ready","to":"done","at":"2026-11-06T12:00:00Z"} -->`,
		"時刻が不正":       `<!-- agent-state:v1 {"from":"ready","to":"in-progress","at":"昨日"} -->`,
		"閉じていない":      `<!-- agent-state:v1 {"from":"ready"}`,
		"目印が 2 つ":     "<!-- agent-state:v1 {} -->\n<!-- agent-state:v1 {} -->",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseComment(b); err == nil || errors.Is(err, ErrNoRecord) {
				t.Errorf("err = %v", err)
			}
		})
	}
	if _, err := ParseComment("ふつうのコメント"); !errors.Is(err, ErrNoRecord) {
		t.Errorf("err = %v", err)
	}
	is := &snapshot.Issue{Number: 5, Comments: []snapshot.Comment{
		{Author: "K-Kizuku", Body: "<!-- agent-state:v1 {\"from\": -->"},
		recComment("someone", Record{From: Ready, To: InProgress, Worker: "evil"}),
		recComment("K-Kizuku", Record{From: Ready, To: InProgress, Worker: "issue-5", Attempt: 1}),
	}}
	h, warns := ReadHistory(is, env().Trusted)
	if len(h.Records) != 1 || h.Records[0].Worker != "issue-5" || len(warns) != 2 {
		t.Errorf("records = %+v, warns = %q", h.Records, warns)
	}
}

func TestTransition(t *testing.T) {
	mk := func(labels ...string) *snapshot.Snapshot {
		return snap(snapshot.Issue{Number: 7, State: "open", Labels: append([]string{"agent"}, labels...), Body: body()})
	}
	t.Run("許す遷移", func(t *testing.T) {
		s := mk("status:in-review")
		res, err := Transition(s, env(), 7, CIPending, TransitionOpts{Reason: "CI 待ち", PR: 0, Cause: "ci:ci-ok"})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Ops) != 3 {
			t.Fatalf("ops = %+v", res.Ops)
		}
		if res.Ops[0].Path != "/repos/"+repo+"/issues/7/labels" || res.Ops[1].Method != "DELETE" ||
			res.Ops[1].Path != "/repos/"+repo+"/issues/7/labels/status:in-review" {
			t.Errorf("ops = %+v", res.Ops)
		}
		got := applyLabels(t, s.Issues[0].Labels, res.Ops)
		s.Issues[0].Labels = got
		if st, err := Current(&s.Issues[0]); err != nil || st != CIPending {
			t.Errorf("付け替えた後 = %v, %v（%v）", st, err, got)
		}
		r := commentRecord(t, res.Ops[2])
		if r.From != InReview || r.To != CIPending || r.Reason != "CI 待ち" || r.Cause != "ci:ci-ok" ||
			r.Actor != "orchestrator" || r.At != "2026-11-06T12:00:00Z" || r.Branch != "agent/issue-7" {
			t.Errorf("record = %+v", r)
		}
	})
	t.Run("許さない遷移", func(t *testing.T) {
		_, err := Transition(mk("status:backlog"), env(), 7, InProgress, TransitionOpts{})
		if r, ok := IsReject(err); !ok || r.Precondition {
			t.Errorf("err = %v", err)
		}
		_, err = Transition(mk("status:merged"), env(), 7, Ready, TransitionOpts{})
		if _, ok := IsReject(err); !ok {
			t.Errorf("終端から: err = %v", err)
		}
	})
	t.Run("ラベルが 0 個・2 個", func(t *testing.T) {
		for _, s := range []*snapshot.Snapshot{mk(), mk("status:ready", "status:in-progress"), mk("status:wip")} {
			_, err := Transition(s, env(), 7, Ready, TransitionOpts{})
			if r, ok := IsReject(err); !ok || r.Precondition || !strings.Contains(err.Error(), "ちょうど 1 つではない") {
				t.Errorf("err = %v", err)
			}
		}
	})
	t.Run("Issue がない", func(t *testing.T) {
		if _, err := Transition(mk("status:ready"), env(), 99, Backlog, TransitionOpts{}); err == nil {
			t.Error("エラーにならない")
		} else if _, ok := IsReject(err); ok {
			t.Error("拒否ではなく内部エラーにする")
		}
	})
	t.Run("merged は PR がマージ済みのときだけ", func(t *testing.T) {
		s := mk("status:ready-to-merge")
		s.Pulls = []snapshot.Pull{{Number: 30, State: "open", HeadRef: "agent/issue-7", Body: "Closes #7"}}
		if _, err := Transition(s, env(), 7, Merged, TransitionOpts{}); err == nil {
			t.Error("マージされていないのに merged にした")
		}
		if _, err := Transition(s, env(), 7, Merged, TransitionOpts{PR: 30}); err == nil {
			t.Error("--pr がマージされていないのに merged にした")
		}
		s.Pulls = append(s.Pulls, snapshot.Pull{Number: 31, State: "closed", Merged: true, HeadRef: "other", Body: "Closes #8"})
		if _, err := Transition(s, env(), 7, Merged, TransitionOpts{PR: 31}); err == nil {
			t.Error("関係のない PR で merged にした")
		}
		s.Pulls[0].State, s.Pulls[0].Merged = "closed", true
		res, err := Transition(s, env(), 7, Merged, TransitionOpts{})
		if err != nil {
			t.Fatal(err)
		}
		if r := commentRecord(t, res.Ops[2]); r.PR != 30 || r.To != Merged {
			t.Errorf("record = %+v", r)
		}
	})
	t.Run("着手の記録を引き継ぐ", func(t *testing.T) {
		s := mk("status:in-progress")
		s.Issues[0].Comments = []snapshot.Comment{recComment("K-Kizuku", Record{From: Ready, To: InProgress, Worker: "issue-7", Branch: "agent/issue-7", Session: "s1", Attempt: 2})}
		res, err := Transition(s, env(), 7, InReview, TransitionOpts{PR: 0})
		if err != nil {
			t.Fatal(err)
		}
		if r := commentRecord(t, res.Ops[2]); r.Worker != "issue-7" || r.Session != "s1" || r.Attempt != 2 {
			t.Errorf("record = %+v", r)
		}
	})
}

func readyIssue(n int, deps ...int) snapshot.Issue {
	return snapshot.Issue{Number: n, State: "open", Labels: []string{"status:ready"}, Body: body(deps...)}
}

func TestClaim(t *testing.T) {
	t.Run("正常", func(t *testing.T) {
		s := snap(readyIssue(12, 3), snapshot.Issue{Number: 3, State: "closed", StateReason: "completed", Body: body()})
		s.Issues[0].Comments = []snapshot.Comment{
			recComment("K-Kizuku", Record{From: Ready, To: InProgress, Worker: "issue-12", Session: "old", Attempt: 1}),
			recComment("K-Kizuku", Record{From: InProgress, To: Ready, Attempt: 1}),
		}
		res, err := Claim(s, env(), 12, "issue-12", "sess-2", false)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Ops) != 3 {
			t.Fatalf("ops = %+v", res.Ops)
		}
		if got := applyLabels(t, s.Issues[0].Labels, res.Ops); len(got) != 1 || got[0] != "status:in-progress" {
			t.Errorf("labels = %v", got)
		}
		r := commentRecord(t, res.Ops[2])
		if r.From != Ready || r.To != InProgress || r.Worker != "issue-12" || r.Branch != "agent/issue-12" || r.Session != "sess-2" || r.Attempt != 2 {
			t.Errorf("record = %+v", r)
		}
	})
	pre := func(t *testing.T, s *snapshot.Snapshot, resume bool) {
		t.Helper()
		_, err := Claim(s, env(), 12, "issue-12", "sess", resume)
		if r, ok := IsReject(err); !ok || !r.Precondition {
			t.Errorf("err = %v（前提を満たさないので拒否する）", err)
		}
	}
	t.Run("ブランチあり", func(t *testing.T) {
		s := snap(readyIssue(12))
		s.Branches = []string{"agent/issue-12"}
		pre(t, s, false)
	})
	t.Run("PR あり", func(t *testing.T) {
		s := snap(readyIssue(12))
		s.Pulls = []snapshot.Pull{{Number: 40, State: "open", HeadRef: "someone/fix", Body: "Fixes #12"}}
		pre(t, s, false)
	})
	t.Run("依存が未完了", func(t *testing.T) {
		pre(t, snap(readyIssue(12, 3), snapshot.Issue{Number: 3, State: "open", Labels: []string{"status:in-review"}, Body: body()}), false)
		pre(t, snap(readyIssue(12, 3), snapshot.Issue{Number: 3, State: "closed", StateReason: "not_planned", Body: body()}), false)
	})
	t.Run("ready でない", func(t *testing.T) {
		is := readyIssue(12)
		is.Labels = []string{"status:backlog"}
		pre(t, snap(is), false)
		is.Labels = []string{"status:ready", "status:backlog"}
		pre(t, snap(is), false)
	})
	t.Run("依存先が存在しない", func(t *testing.T) {
		_, err := Claim(snap(readyIssue(12, 99)), env(), 12, "issue-12", "s", false)
		if _, ok := IsReject(err); err == nil || ok {
			t.Errorf("err = %v（内部エラーにする）", err)
		}
	})
	t.Run("冪等", func(t *testing.T) {
		is := readyIssue(12)
		is.Labels = []string{"status:in-progress"}
		is.Comments = []snapshot.Comment{recComment("K-Kizuku", Record{From: Ready, To: InProgress, Worker: "issue-12", Session: "sess", Attempt: 1})}
		s := snap(is)
		s.Branches = []string{"agent/issue-12"}
		res, err := Claim(s, env(), 12, "issue-12", "sess", false)
		if err != nil || len(res.Ops) != 0 {
			t.Fatalf("res = %+v, err = %v", res, err)
		}
		// 別のセッション・別の Worker は拒否する。
		for _, w := range [][2]string{{"issue-12", "other"}, {"issue-99", "sess"}} {
			_, err := Claim(s, env(), 12, w[0], w[1], false)
			if r, ok := IsReject(err); !ok || !r.Precondition {
				t.Errorf("%v: err = %v", w, err)
			}
		}
	})
	t.Run("--resume", func(t *testing.T) {
		is := readyIssue(12)
		is.Labels = []string{"status:in-progress"}
		is.Comments = []snapshot.Comment{recComment("K-Kizuku", Record{From: Ready, To: InProgress, Worker: "issue-12", Session: "old", Attempt: 1})}
		s := snap(is)
		pre(t, s, true) // ブランチがない
		s.Branches = []string{"agent/issue-12"}
		res, err := Claim(s, env(), 12, "issue-12", "new", true)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Ops) != 1 {
			t.Fatalf("ops = %+v（記録だけを付ける）", res.Ops)
		}
		if r := commentRecord(t, res.Ops[0]); r.From != InProgress || r.To != InProgress || r.Attempt != 2 || r.Session != "new" {
			t.Errorf("record = %+v", r)
		}
		s.Pulls = []snapshot.Pull{{Number: 41, State: "open", HeadRef: "agent/issue-12"}}
		pre(t, s, true) // PR がある
		s.Pulls = nil
		s.Issues[0].Labels = []string{"status:ready"}
		pre(t, s, true) // in-progress でない
	})
}

func TestAttempt(t *testing.T) {
	mk := func(n int, labels ...string) *snapshot.Snapshot {
		is := snapshot.Issue{Number: 9, State: "open", Labels: labels, Body: body()}
		for i := 0; i < n; i++ {
			is.Comments = append(is.Comments, recComment("K-Kizuku", Record{From: InReview, To: InReview, Cause: "ci:ci-ok", Attempt: 1}))
		}
		// ほかの原因は数えない。
		is.Comments = append(is.Comments, recComment("K-Kizuku", Record{From: InReview, To: InReview, Cause: "review:F3", Attempt: 1}))
		return snap(is)
	}
	t.Run("上限の手前", func(t *testing.T) {
		for n := 0; n < 3; n++ {
			res, err := Attempt(mk(n, "status:in-review"), env(), 9, "ci:ci-ok")
			if err != nil || res.LimitReached || len(res.Ops) != 1 {
				t.Fatalf("n=%d: res = %+v, err = %v", n, res, err)
			}
			if r := commentRecord(t, res.Ops[0]); r.Cause != "ci:ci-ok" || r.From != InReview || r.To != InReview || r.Reason != fmt.Sprintf("再試行 %d/3", n+1) {
				t.Errorf("record = %+v", r)
			}
		}
	})
	t.Run("上限で blocked", func(t *testing.T) {
		s := mk(3, "status:in-review")
		res, err := Attempt(s, env(), 9, "ci:ci-ok")
		if err != nil || !res.LimitReached {
			t.Fatalf("res = %+v, err = %v", res, err)
		}
		got := applyLabels(t, s.Issues[0].Labels, res.Ops)
		if strings.Join(got, ",") != "status:blocked,needs-human" {
			t.Errorf("labels = %v", got)
		}
		if r := commentRecord(t, res.Ops[len(res.Ops)-1]); r.From != InReview || r.To != Blocked || r.Cause != "ci:ci-ok" {
			t.Errorf("record = %+v", r)
		}
	})
	t.Run("blocked・needs-human 済み", func(t *testing.T) {
		res, err := Attempt(mk(4, "status:blocked", "needs-human"), env(), 9, "ci:ci-ok")
		if err != nil || !res.LimitReached || len(res.Ops) != 0 {
			t.Fatalf("res = %+v, err = %v", res, err)
		}
	})
	t.Run("人が blocked から戻したら数え直す", func(t *testing.T) {
		s := mk(3, "status:in-review")
		s.Issues[0].Comments = append(s.Issues[0].Comments,
			recComment("K-Kizuku", Record{From: InReview, To: Blocked, Cause: "ci:ci-ok"}),
			recComment("K-Kizuku", Record{From: Blocked, To: InProgress}))
		res, err := Attempt(s, env(), 9, "ci:ci-ok")
		if err != nil || res.LimitReached {
			t.Fatalf("res = %+v, err = %v", res, err)
		}
	})
	t.Run("merged", func(t *testing.T) {
		if _, err := Attempt(mk(0, "status:merged"), env(), 9, "conflict"); err == nil {
			t.Error("merged で記録した")
		}
	})
}
