package plan

import (
	"fmt"
	"sort"
	"strings"

	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/snapshot"
)

// mermaidClasses は状態ごとの色。
var mermaidClasses = []struct{ class, style string }{
	{"backlog", "fill:#eeeeee,stroke:#999999"},
	{"ready", "fill:#d0ebff,stroke:#1c7ed6"},
	{"in-progress", "fill:#fff3bf,stroke:#f08c00"},
	{"in-review", "fill:#e5dbff,stroke:#7048e8"},
	{"changes-requested", "fill:#ffe8cc,stroke:#e8590c"},
	{"ci-pending", "fill:#f3d9fa,stroke:#ae3ec9"},
	{"ready-to-merge", "fill:#d3f9d8,stroke:#2f9e44"},
	{"merged", "fill:#b2f2bb,stroke:#2b8a3e"},
	{"blocked", "fill:#ffc9c9,stroke:#e03131"},
	{"open", "fill:#ffffff,stroke:#868e96"},
	{"closed", "fill:#f8f9fa,stroke:#adb5bd,color:#868e96"},
	{"not-planned", "fill:#f8f9fa,stroke:#e03131,color:#868e96"},
	{"invalid", "fill:#ffffff,stroke:#e03131,stroke-dasharray:4"},
}

func classID(c string) string { return strings.ReplaceAll(c, "-", "_") }

func mermaidText(s string) string {
	r := strings.NewReplacer(`"`, "#quot;", "<", "#lt;", ">", "#gt;", "\n", " ")
	return r.Replace(s)
}

// Mermaid は、状態で色分けした DAG を Mermaid の flowchart で返す（依存先 → 依存している Issue）。
func (g *Graph) Mermaid() string {
	var b strings.Builder
	b.WriteString("flowchart TD\n")
	for _, c := range mermaidClasses {
		fmt.Fprintf(&b, "  classDef %s %s\n", classID(c.class), c.style)
	}
	nodes := map[int]string{}
	for _, n := range g.order {
		nd := g.nodes[n]
		cls := nd.status
		if cls == "" {
			cls = "invalid"
		}
		nodes[n] = cls
		for _, d := range nd.deps {
			if _, ok := g.nodes[d]; ok {
				continue
			}
			dep := g.snap.Issue(d)
			switch {
			case dep.Done():
				nodes[d] = "closed"
			case dep.State == "closed":
				nodes[d] = "not-planned"
			default:
				nodes[d] = "open" // status:* のない開いた Issue
			}
		}
	}
	nums := make([]int, 0, len(nodes))
	for n := range nodes {
		nums = append(nums, n)
	}
	sort.Ints(nums)
	for _, n := range nums {
		title := ""
		if is := g.snap.Issue(n); is != nil {
			title = is.Title
		}
		fmt.Fprintf(&b, "  i%d[\"#%d %s\"]:::%s\n", n, n, mermaidText(title), classID(nodes[n]))
	}
	for _, n := range g.order {
		deps := append([]int(nil), g.nodes[n].deps...)
		sort.Ints(deps)
		for _, d := range deps {
			fmt.Fprintf(&b, "  i%d --> i%d\n", d, n)
		}
	}
	return b.String()
}

// Mermaid は、スナップショットから DAG を作り、Mermaid の flowchart で返す。
func Mermaid(snap *snapshot.Snapshot) (string, error) {
	g, err := Build(snap)
	if err != nil {
		return "", err
	}
	return g.Mermaid(), nil
}
