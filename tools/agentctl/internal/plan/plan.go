// Package plan は、開いている Issue の依存関係の DAG から、次に着手してよい Issue を選ぶ（agentctl plan）。
// また、作業中の Issue ごとに、GitHub と Git の状態から次の行動を 1 つ決める（agentctl recover）。
//
// どちらもスナップショットだけを読む純粋な関数で、GitHub には接続しない。
package plan

import (
	"fmt"
	"sort"
	"strings"

	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/config"
	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/issuebody"
	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/snapshot"
	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/state"
)

// Item は plan の出力の 1 件。
type Item struct {
	Number        int      `json:"number"`
	Title         string   `json:"title"`
	Status        string   `json:"status"`
	CriticalPath  int      `json:"critical_path"`
	Reasons       []string `json:"reasons,omitempty"`
	ConflictsWith []int    `json:"conflicts_with,omitempty"`
	Warnings      []string `json:"warnings,omitempty"`
}

// Result は plan の出力。
type Result struct {
	Ready         []Item `json:"ready"`
	BlockedByDeps []Item `json:"blocked_by_deps"`
	Conflicts     []Item `json:"conflicts"`
	Active        []Item `json:"active"`
	Capacity      int    `json:"capacity"`
	// Warnings は全体の警告（JSON には含めず、標準エラーに出す）。
	Warnings []string `json:"-"`
}

// CycleError は依存の循環。
type CycleError struct {
	Cycle []int
}

func (e *CycleError) Error() string {
	parts := make([]string, len(e.Cycle))
	for i, n := range e.Cycle {
		parts[i] = fmt.Sprintf("#%d", n)
	}
	return "plan: 依存が循環している: " + strings.Join(parts, " → ")
}

// node は DAG の頂点（`status:*` のラベルを持つ開いた Issue）。
type node struct {
	is     *snapshot.Issue
	status string // ラベルが壊れていれば空
	deps   []int
}

// Graph は依存関係の DAG。
type Graph struct {
	snap       *snapshot.Snapshot
	nodes      map[int]*node
	order      []int         // 番号の小さい順
	dependents map[int][]int // 依存先 → 依存している Issue
	cp         map[int]int
}

// Build は、開いている Issue（`status:*` のラベルを持つもの）から DAG を作る。
// 循環があれば CycleError、依存先が存在しなければエラーを返す。
func Build(snap *snapshot.Snapshot) (*Graph, error) {
	g := &Graph{snap: snap, nodes: map[int]*node{}, dependents: map[int][]int{}, cp: map[int]int{}}
	for i := range snap.Issues {
		is := &snap.Issues[i]
		if is.State != "open" || len(is.StatusLabels()) == 0 {
			continue
		}
		nd := &node{is: is, deps: is.Dependencies()}
		if st, err := state.Current(is); err == nil {
			nd.status = string(st)
		}
		g.nodes[is.Number] = nd
		g.order = append(g.order, is.Number)
	}
	sort.Ints(g.order)
	for _, n := range g.order {
		for _, d := range g.nodes[n].deps {
			if snap.Issue(d) == nil {
				return nil, fmt.Errorf("plan: Issue #%d の依存先 #%d が存在しない", n, d)
			}
			if _, ok := g.nodes[d]; ok {
				g.dependents[d] = append(g.dependents[d], n)
			}
		}
	}
	if c := g.findCycle(); c != nil {
		return nil, &CycleError{Cycle: c}
	}
	return g, nil
}

// findCycle は、循環があれば、その番号の列（最初と最後が同じ）を返す。
func (g *Graph) findCycle() []int {
	const (
		white = iota
		gray
		black
	)
	color := map[int]int{}
	var stack []int
	var found []int
	var visit func(n int) bool
	visit = func(n int) bool {
		color[n] = gray
		stack = append(stack, n)
		for _, d := range g.nodes[n].deps {
			if _, ok := g.nodes[d]; !ok {
				continue
			}
			switch color[d] {
			case gray:
				for i, s := range stack {
					if s == d {
						found = append(append([]int{}, stack[i:]...), d)
						return true
					}
				}
			case white:
				if visit(d) {
					return true
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[n] = black
		return false
	}
	for _, n := range g.order {
		if color[n] == white && visit(n) {
			return found
		}
	}
	return nil
}

// CriticalPath は、n から後ろに続く依存の鎖のうち最も長いものの Issue の数（n 自身を含む）。
func (g *Graph) CriticalPath(n int) int {
	if v, ok := g.cp[n]; ok {
		return v
	}
	best := 0
	for _, d := range g.dependents[n] {
		if v := g.CriticalPath(d); v > best {
			best = v
		}
	}
	g.cp[n] = best + 1
	return best + 1
}

func (g *Graph) item(n int) Item {
	nd := g.nodes[n]
	st := nd.status
	if st == "" {
		st = strings.Join(nd.is.StatusLabels(), ",")
	}
	return Item{Number: n, Title: nd.is.Title, Status: st, CriticalPath: g.CriticalPath(n)}
}

// Plan は、着手してよい Issue を選ぶ。
func Plan(snap *snapshot.Snapshot, cfg *config.Config) (*Result, error) {
	g, err := Build(snap)
	if err != nil {
		return nil, err
	}
	res := &Result{Ready: []Item{}, BlockedByDeps: []Item{}, Conflicts: []Item{}, Active: []Item{}}

	busy := 0
	type locked struct {
		n     int
		files []string
	}
	var active []locked
	var candidates []int
	for _, n := range g.order {
		nd := g.nodes[n]
		if nd.status == "" {
			res.Warnings = append(res.Warnings, fmt.Sprintf("#%d: status:* がちょうど 1 つではない（%v）ので選ばない", n, nd.is.StatusLabels()))
			continue
		}
		st := state.Status(nd.status)
		if st.IsWorking() {
			res.Active = append(res.Active, g.item(n))
			files, w := plannedFiles(nd.is)
			res.Warnings = append(res.Warnings, w...)
			active = append(active, locked{n, files})
			if st == state.InProgress || st == state.ChangesRequested {
				busy++
			}
			continue
		}
		if st != state.Ready {
			continue
		}
		reasons, err := state.Startable(snap, nd.is, cfg.BranchPrefix)
		if err != nil {
			return nil, err
		}
		if len(reasons) > 0 {
			it := g.item(n)
			it.Reasons = reasons
			res.BlockedByDeps = append(res.BlockedByDeps, it)
			continue
		}
		candidates = append(candidates, n)
	}
	res.Capacity = cfg.MaxWorkers - busy
	if res.Capacity < 0 {
		res.Capacity = 0
	}

	// クリティカルパスの長いものを先に、同じなら番号の小さいもの。
	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := g.CriticalPath(candidates[i]), g.CriticalPath(candidates[j])
		if a != b {
			return a > b
		}
		return candidates[i] < candidates[j]
	})

	var selected []locked
	for _, n := range candidates {
		files, w := plannedFiles(g.nodes[n].is)
		it := g.item(n)
		it.Warnings = append(it.Warnings, w...)
		check := func(others []locked, what string) bool {
			hard := false
			for _, o := range others {
				h, soft := Overlap(files, o.files, cfg.SharedAppendFiles)
				if len(h) > 0 {
					hard = true
					it.ConflictsWith = append(it.ConflictsWith, o.n)
					it.Reasons = append(it.Reasons, fmt.Sprintf("%s #%d と変更予定ファイルが重なる: %s", what, o.n, strings.Join(h, ", ")))
				} else if len(soft) > 0 {
					it.Warnings = append(it.Warnings, fmt.Sprintf("#%d と shared_append_files だけが重なる: %s", o.n, strings.Join(soft, ", ")))
				}
			}
			return hard
		}
		if check(active, "実装中の") {
			res.Conflicts = append(res.Conflicts, it)
			continue
		}
		if len(selected) >= res.Capacity {
			continue
		}
		if check(selected, "同時に選ぶ") {
			res.Conflicts = append(res.Conflicts, it)
			continue
		}
		selected = append(selected, locked{n, files})
		res.Ready = append(res.Ready, it)
	}
	return res, nil
}

// plannedFiles は「変更予定ファイル」の glob を返す。書かれていなければ、すべてと重なる `**` とみなす。
func plannedFiles(is *snapshot.Issue) ([]string, []string) {
	fs := is.PlannedFiles()
	if len(fs) == 0 {
		return []string{"**"}, []string{fmt.Sprintf("#%d: 変更予定ファイルがないので、すべてのファイルと重なるとみなす", is.Number)}
	}
	return fs, nil
}

// literalPrefix は glob の、最初のワイルドカードより前の部分。
func literalPrefix(p string) string {
	if i := strings.IndexAny(p, `*?[\`); i >= 0 {
		return p[:i]
	}
	return p
}

func hasWildcard(p string) bool { return literalPrefix(p) != p }

// Overlap は、2 つの glob の一覧の重なりを控えめに判定する。
// glob の最初のワイルドカードより前の部分が、一方が他方の前方一致なら重なりとみなす。
// 同じファイル（ワイルドカードなし）で shared に入るものどうしの重なりは soft、それ以外は hard に入れる。
func Overlap(a, b, shared []string) (hard, soft []string) {
	isShared := func(p string) bool {
		for _, s := range shared {
			if issuebody.NormalizePath(s) == p {
				return true
			}
		}
		return false
	}
	for _, x := range a {
		for _, y := range b {
			px, py := literalPrefix(x), literalPrefix(y)
			if !strings.HasPrefix(px, py) && !strings.HasPrefix(py, px) {
				continue
			}
			desc := x
			if x != y {
				desc = x + " ↔ " + y
			}
			if x == y && !hasWildcard(x) && isShared(x) {
				soft = appendUnique(soft, desc)
			} else {
				hard = appendUnique(hard, desc)
			}
		}
	}
	return hard, soft
}

func appendUnique(list []string, s string) []string {
	for _, v := range list {
		if v == s {
			return list
		}
	}
	return append(list, s)
}
