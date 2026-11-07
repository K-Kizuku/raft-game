package plan

import (
	"fmt"
	"sort"

	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/config"
	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/gate"
	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/review"
	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/snapshot"
	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/state"
)

// recover の行動。
const (
	ActionResetToReady  = "reset-to-ready"
	ActionResumeWorker  = "resume-worker"
	ActionSetState      = "set-state"
	ActionRequestReview = "request-review"
	ActionMerge         = "merge"
	ActionEscalate      = "escalate"
	ActionFixLabels     = "fix-labels"
)

// Action は、1 つの Issue に対する次の行動。
type Action struct {
	Issue  int    `json:"issue"`
	Status string `json:"status"`
	Action string `json:"action"`
	// To は set-state の行き先。
	To string `json:"to,omitempty"`
	PR int    `json:"pr,omitempty"`
	// Labels は escalate で付けるラベル。
	Labels []string `json:"labels,omitempty"`
	// Worker・Session は、resume-worker のときの、状態の記録にある最新の Worker と session（再開に使う）。
	Worker  string `json:"worker,omitempty"`
	Session string `json:"session,omitempty"`
	// Worktree は、resume-worker のときの、ローカルの Worktree のパス（あれば）。
	Worktree string `json:"worktree,omitempty"`
	Reason   string `json:"reason"`
}

// latestSession は、状態の記録のうち、session のある最新のものの worker と session を返す。
func latestSession(h state.History) (worker, session string) {
	for i := len(h.Records) - 1; i >= 0; i-- {
		if r := h.Records[i]; r.Session != "" {
			return r.Worker, r.Session
		}
	}
	if c := h.LatestClaim(); c != nil {
		return c.Worker, ""
	}
	return "", ""
}

// RecoverResult は recover の出力。
type RecoverResult struct {
	Actions  []Action `json:"actions"`
	Warnings []string `json:"-"`
}

// Recover は、作業中の状態の Issue ごとに、次の行動を 1 つ決める。
// trusted は、状態の記録とレビュー記録を信頼するコメントの作者かを返す。
func Recover(snap *snapshot.Snapshot, cfg *config.Config, trusted func(login string) bool) *RecoverResult {
	res := &RecoverResult{Actions: []Action{}}
	issues := make([]*snapshot.Issue, 0, len(snap.Issues))
	for i := range snap.Issues {
		issues = append(issues, &snap.Issues[i])
	}
	sort.Slice(issues, func(i, j int) bool { return issues[i].Number < issues[j].Number })

	for _, is := range issues {
		n := is.Number
		branch := snapshot.BranchFor(cfg.BranchPrefix, n)
		labels := is.StatusLabels()
		h, warns := state.ReadHistory(is, trusted)
		res.Warnings = append(res.Warnings, warns...)
		hasBranch := snap.HasBranch(branch)
		open := snap.OpenPullsFor(n, branch, trusted)
		merged := snap.MergedPullsFor(n, branch, trusted)

		// エージェントの作業の対象ではない Issue（ラベルも記録もブランチも PR もない）は見ない。
		if len(labels) == 0 && len(h.Records) == 0 && !hasBranch && len(open) == 0 {
			continue
		}
		statusText := ""
		cur, curErr := state.Current(is)
		if curErr == nil {
			statusText = string(cur)
		}
		if is.State == "closed" && len(open) == 0 && (len(labels) == 0 || (curErr == nil && cur == state.Merged)) {
			continue // 閉じていて、直すべき状態のラベルが残っていない
		}
		wt := snap.WorktreeFor(n)
		add := func(a Action) {
			a.Issue, a.Status = n, statusText
			if a.Action == ActionResumeWorker {
				a.Worker, a.Session = latestSession(h)
				if wt != nil {
					a.Worktree = wt.Path
				}
			}
			res.Actions = append(res.Actions, a)
		}

		if len(open) >= 2 {
			nums := make([]int, len(open))
			for i, p := range open {
				nums[i] = p.Number
			}
			add(Action{Action: ActionEscalate, Labels: []string{state.NeedsHumanLabel},
				Reason: fmt.Sprintf("開いた PR が %d 個ある: %v", len(open), nums)})
			continue
		}
		if curErr != nil {
			add(Action{Action: ActionFixLabels, Reason: fmt.Sprintf("status:* がちょうど 1 つではない（%v）", labels)})
			continue
		}
		if len(merged) > 0 && cur != state.Merged {
			if state.CanTransition(cur, state.Merged) {
				add(Action{Action: ActionSetState, To: string(state.Merged), PR: merged[0].Number,
					Reason: fmt.Sprintf("PR #%d がマージ済み", merged[0].Number)})
			} else {
				// 遷移表では merged に行けない（例: 人が in-review のままマージした）。set-state を出しても拒否されるので人に任せる。
				add(Action{Action: ActionEscalate, PR: merged[0].Number, Labels: []string{state.NeedsHumanLabel},
					Reason: fmt.Sprintf("PR #%d がマージ済みだが、状態が %s なので merged に遷移できない（merged へは %s からだけ）",
						merged[0].Number, cur, state.ReadyToMerge)})
			}
			continue
		}
		if is.State != "open" {
			continue
		}
		var pr *snapshot.Pull
		if len(open) == 1 {
			pr = open[0]
		}
		switch cur {
		case state.InProgress:
			switch {
			case pr != nil:
				add(Action{Action: ActionSetState, To: string(state.InReview), PR: pr.Number, Reason: fmt.Sprintf("PR #%d がある", pr.Number)})
			case hasBranch:
				add(Action{Action: ActionResumeWorker, Reason: fmt.Sprintf("ブランチ %s があり、PR がない", branch)})
			case wt != nil && wt.HasUnpushedWork():
				add(Action{Action: ActionResumeWorker, Reason: fmt.Sprintf(
					"ブランチも PR もないが、ローカルの Worktree %s に未コミットの変更（%t）か push していないコミット（%d 個）がある",
					wt.Path, wt.Dirty, wt.Ahead)})
			default:
				add(Action{Action: ActionResetToReady, Reason: "ブランチも PR もない（Worker が push する前に止まった）"})
			}
		case state.InReview:
			if pr != nil && !hasHeadReview(pr, cfg) && !carriesOver(pr, cfg) {
				add(Action{Action: ActionRequestReview, PR: pr.Number, Reason: fmt.Sprintf("PR #%d の HEAD %s のレビュー記録がない", pr.Number, short(pr.HeadSHA))})
			}
		case state.ChangesRequested:
			a := Action{Action: ActionResumeWorker, Reason: "修正を求められている"}
			if pr != nil {
				a.PR = pr.Number
			}
			add(a)
		case state.ReadyToMerge:
			if pr != nil && snap.Status(pr.HeadSHA, config.SelfContext) == "success" {
				add(Action{Action: ActionMerge, PR: pr.Number, Reason: fmt.Sprintf("PR #%d の Merge Gate が pass", pr.Number)})
			}
		}
	}
	return res
}

// hasHeadReview は、PR に、HEAD の SHA に対する信頼できる作者のレビュー記録があるかを返す。
func hasHeadReview(p *snapshot.Pull, cfg *config.Config) bool {
	for _, c := range p.Comments {
		if !cfg.IsTrustedAuthor(c.Author) {
			continue
		}
		rec, err := review.FromComment(c.Body)
		if err != nil {
			continue
		}
		if rec.PR == p.Number && rec.HeadSHA == p.HeadSHA {
			return true
		}
	}
	return false
}

// carriesOver は、PR の前の SHA のレビューを、Merge Gate の C6 と同じ条件で HEAD に引き継げるかを返す
// （main の取り込みだけで PR の自分の変更が同じ。Issue #120）。判定の材料（p.Gate）がなければ false。
func carriesOver(p *snapshot.Pull, cfg *config.Config) bool {
	if p.Gate == nil || p.Gate.PullRequest.Number != p.Number || p.Gate.PullRequest.HeadSHA != p.HeadSHA {
		return false
	}
	return gate.CarriesOver(cfg, *p.Gate).OK
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
