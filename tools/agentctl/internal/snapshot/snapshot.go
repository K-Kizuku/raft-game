// Package snapshot は、計画（plan）・状態の更新（state・claim・attempt）・復旧（recover）が読む
// GitHub と Git の状態の写し（スナップショット）を扱う。
//
// JSON の形（`--input` で渡す）:
//
//	{
//	  "issues":   [{"number", "title", "state", "state_reason", "labels", "body",
//	                "comments": [{"author", "body", "created_at"}]}],
//	  "pulls":    [{"number", "state", "merged", "author", "head_ref", "head_sha", "body", "labels",
//	                "comments": [...]}],   // comments は省略できる（in-review のレビュー記録の確認に使う）
//	                                       // author（PR の作者）が信頼する作者でない PR は、Issue との関係を数えない
//	                "gate": {...}},        // gate は省略できる（gate.Input。レビュー記録の引き継ぎの確認に使う）
//	  "branches": ["agent/issue-12", ...],
//	  "statuses": {"<sha>": {"merge-gate": "success"}}
//	}
//
// issues には、依存先を判定できるよう、閉じた Issue も含める（PR は含めない）。
package snapshot

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/gate"
	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/issuebody"
	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/prbody"
)

// StatusLabelPrefix は状態のラベルの接頭辞。
const StatusLabelPrefix = "status:"

// Comment は Issue・PR のコメント。
type Comment struct {
	Author    string `json:"author"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at"`
}

// Issue は Issue（PR は含まない）。
type Issue struct {
	Number      int       `json:"number"`
	Title       string    `json:"title"`
	State       string    `json:"state"`        // open | closed
	StateReason string    `json:"state_reason"` // completed | not_planned | reopened | ""
	Labels      []string  `json:"labels"`
	Body        string    `json:"body"`
	Comments    []Comment `json:"comments"`
}

// Pull は PR。
type Pull struct {
	Number   int       `json:"number"`
	State    string    `json:"state"` // open | closed
	Merged   bool      `json:"merged"`
	Author   string    `json:"author"`
	HeadRef  string    `json:"head_ref"`
	HeadSHA  string    `json:"head_sha"`
	Body     string    `json:"body"`
	Labels   []string  `json:"labels"`
	Comments []Comment `json:"comments,omitempty"`
	// Gate は、この PR の Merge Gate の判定の材料（gate.Input）。recover が、HEAD のレビュー記録のない
	// in-review の PR について、前の SHA のレビューを引き継げるか（gate.CarriesOver）を確かめるのに使う。省略できる。
	Gate *gate.Input `json:"gate,omitempty"`
}

// Worktree はローカルの作業ディレクトリ（`git worktree`）。
type Worktree struct {
	Issue  int    `json:"issue"`
	Path   string `json:"path"`
	Branch string `json:"branch"`
	// Dirty は、未コミットの変更（追跡していないファイルを含む）があること。
	Dirty bool `json:"dirty"`
	// Ahead は、どのリモートのブランチにもないコミットの数（push していないコミット）。
	Ahead int `json:"ahead"`
}

// HasUnpushedWork は、未コミットの変更か push していないコミットがあるかを返す。
func (w Worktree) HasUnpushedWork() bool { return w.Dirty || w.Ahead > 0 }

// Snapshot は GitHub と Git の状態の写し。
type Snapshot struct {
	Issues   []Issue                      `json:"issues"`
	Pulls    []Pull                       `json:"pulls"`
	Branches []string                     `json:"branches"`
	Statuses map[string]map[string]string `json:"statuses"`
	// Worktrees はローカルの Worktree（recover が見る）。
	Worktrees []Worktree `json:"worktrees,omitempty"`
}

// WorktreeFor は Issue n のローカルの Worktree を返す。なければ nil。
func (s *Snapshot) WorktreeFor(n int) *Worktree {
	for i := range s.Worktrees {
		if s.Worktrees[i].Issue == n {
			return &s.Worktrees[i]
		}
	}
	return nil
}

// Parse は JSON を読み、検証する。未知のキーはエラーにする。
func Parse(data []byte) (*Snapshot, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var s Snapshot
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("snapshot: JSON を読めない: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("snapshot: JSON の後ろに余計なデータがある")
	}
	if err := s.Validate(); err != nil {
		return nil, err
	}
	return &s, nil
}

// Load はファイルを読み、Parse する。
func Load(path string) (*Snapshot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("snapshot: %w", err)
	}
	return Parse(data)
}

// Validate は番号の重複と状態の値を確かめる。
func (s *Snapshot) Validate() error {
	var errs []string
	add := func(format string, a ...any) { errs = append(errs, fmt.Sprintf(format, a...)) }
	seen := map[int]bool{}
	for _, is := range s.Issues {
		if is.Number <= 0 {
			add("Issue の番号が不正（%d）", is.Number)
		}
		if seen[is.Number] {
			add("Issue #%d が重複している", is.Number)
		}
		seen[is.Number] = true
		if is.State != "open" && is.State != "closed" {
			add("Issue #%d の state が不正（%q）", is.Number, is.State)
		}
	}
	seenPR := map[int]bool{}
	for _, p := range s.Pulls {
		if p.Number <= 0 {
			add("PR の番号が不正（%d）", p.Number)
		}
		if seenPR[p.Number] || seen[p.Number] {
			add("PR #%d が重複している", p.Number)
		}
		seenPR[p.Number] = true
		if p.State != "open" && p.State != "closed" {
			add("PR #%d の state が不正（%q）", p.Number, p.State)
		}
		if p.Merged && p.State != "closed" {
			add("PR #%d は merged なのに state が %q", p.Number, p.State)
		}
	}
	for _, w := range s.Worktrees {
		if w.Issue <= 0 {
			add("worktrees の issue が不正（%d）", w.Issue)
		}
		if w.Ahead < 0 {
			add("worktrees の #%d の ahead が負", w.Issue)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("snapshot: 不正: %s", strings.Join(errs, "; "))
	}
	return nil
}

// Issue は番号 n の Issue を返す。なければ nil。
func (s *Snapshot) Issue(n int) *Issue {
	for i := range s.Issues {
		if s.Issues[i].Number == n {
			return &s.Issues[i]
		}
	}
	return nil
}

// HasBranch はブランチ name があるかを返す。
func (s *Snapshot) HasBranch(name string) bool {
	for _, b := range s.Branches {
		if b == name {
			return true
		}
	}
	return false
}

// BranchFor は Issue n の作業ブランチの名前。
func BranchFor(prefix string, n int) string { return prefix + strconv.Itoa(n) }

// RelatesTo は、PR が Issue n を閉じる（本文に `Closes #n` がある）か、作業ブランチ branch から出ているかを返す。
// 公開リポジトリでは誰でも PR を開けるので、作者が信頼する作者（trusted）でない PR は関係しないとみなす
// （trusted が nil なら、どの PR も関係しない）。
func (p *Pull) RelatesTo(n int, branch string, trusted func(login string) bool) bool {
	if trusted == nil || !trusted(p.Author) {
		return false
	}
	if p.HeadRef == branch {
		return true
	}
	for _, c := range prbody.Parse(p.Body).ClosingIssues() {
		if c == n {
			return true
		}
	}
	return false
}

// OpenPullsFor は、Issue n に関係する開いた PR（作者が trusted のもの）を返す。
func (s *Snapshot) OpenPullsFor(n int, branch string, trusted func(login string) bool) []*Pull {
	var out []*Pull
	for i := range s.Pulls {
		if p := &s.Pulls[i]; p.State == "open" && p.RelatesTo(n, branch, trusted) {
			out = append(out, p)
		}
	}
	return out
}

// MergedPullsFor は、Issue n に関係するマージ済みの PR（作者が trusted のもの）を返す。
func (s *Snapshot) MergedPullsFor(n int, branch string, trusted func(login string) bool) []*Pull {
	var out []*Pull
	for i := range s.Pulls {
		if p := &s.Pulls[i]; p.Merged && p.RelatesTo(n, branch, trusted) {
			out = append(out, p)
		}
	}
	return out
}

// Pull は番号 n の PR を返す。なければ nil。
func (s *Snapshot) Pull(n int) *Pull {
	for i := range s.Pulls {
		if s.Pulls[i].Number == n {
			return &s.Pulls[i]
		}
	}
	return nil
}

// Status は、sha のコミットステータス context の state を返す（なければ空）。
func (s *Snapshot) Status(sha, context string) string {
	return s.Statuses[sha][context]
}

// Done は、Issue が完了として閉じられている（state_reason が completed）かを返す。
func (is *Issue) Done() bool { return is.State == "closed" && is.StateReason == "completed" }

// HasLabel はラベル name が付いているかを返す。
func (is *Issue) HasLabel(name string) bool {
	for _, l := range is.Labels {
		if l == name {
			return true
		}
	}
	return false
}

// StatusLabels は `status:` で始まるラベルを返す。
func (is *Issue) StatusLabels() []string {
	var out []string
	for _, l := range is.Labels {
		if strings.HasPrefix(l, StatusLabelPrefix) {
			out = append(out, l)
		}
	}
	return out
}

// PlannedFiles は本文の「変更予定ファイル」の glob を返す。
func (is *Issue) PlannedFiles() []string {
	return issuebody.Parse(is.Body).PlannedFiles()
}

// UnmetDeps は、Issue の依存先のうち、完了していないものの理由を返す。
// 本文の「依存 Issue」の節を読めないときは、その理由を 1 つ返す（満たしていない扱い）。
// 依存先の番号がスナップショットにない（存在しない）ときはエラーを返す。
func (s *Snapshot) UnmetDeps(is *Issue) (reasons []string, err error) {
	deps, perr := issuebody.Parse(is.Body).Dependencies()
	if perr != nil {
		return []string{fmt.Sprintf("依存 Issue の節を読めない: %v", perr)}, nil
	}
	for _, d := range deps {
		if d == is.Number {
			return nil, fmt.Errorf("snapshot: Issue #%d が自分自身に依存している", d)
		}
		dep := s.Issue(d)
		if dep == nil {
			return nil, fmt.Errorf("snapshot: Issue #%d の依存先 #%d が存在しない", is.Number, d)
		}
		switch {
		case dep.Done():
		case dep.State == "closed":
			reason := dep.StateReason
			if reason == "" {
				reason = "理由なし"
			}
			reasons = append(reasons, fmt.Sprintf("依存先 #%d が完了せずに閉じている（%s）", d, reason))
		default:
			reasons = append(reasons, fmt.Sprintf("依存先 #%d がまだ開いている", d))
		}
	}
	return reasons, nil
}

// Dependencies は依存先の番号を返す（読めなければ nil）。
func (is *Issue) Dependencies() []int {
	deps, err := issuebody.Parse(is.Body).Dependencies()
	if err != nil {
		return nil
	}
	return deps
}
