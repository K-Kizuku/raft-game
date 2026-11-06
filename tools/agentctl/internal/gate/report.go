package gate

import (
	"fmt"
	"strings"
)

// CommentMarker は Merge Gate のコメントの目印。PR ごとに 1 つだけ作り、以後は更新する。
const CommentMarker = "<!-- merge-gate:v1 -->"

// MaxDescription はコミットステータスの説明の上限（文字数）。
const MaxDescription = 140

// StatusState は、コミットステータスの state（success・pending・failure）。
func (r *Report) StatusState() string {
	switch r.Verdict {
	case Pass:
		return "success"
	case Pending:
		return "pending"
	default:
		return "failure"
	}
}

// Description は、コミットステータスの説明（140 文字以内）。
func (r *Report) Description() string {
	fails, pendings := r.Failed()
	var s string
	switch r.Verdict {
	case Pass:
		s = "pass: すべての条件を満たした"
	case Pending:
		s = "pending: " + strings.Join(pendings, ", ")
	default:
		s = "fail: " + strings.Join(fails, ", ")
		if len(pendings) > 0 {
			s += "（待ち: " + strings.Join(pendings, ", ") + "）"
		}
	}
	return truncate(s, MaxDescription)
}

func truncate(s string, n int) string {
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	return string(rs[:n-1]) + "…"
}

var icons = map[Verdict]string{Pass: "✅", Pending: "⏳", Fail: "❌"}

// RenderComment は Merge Gate のコメントの本文を返す。
func (r *Report) RenderComment(targetURL string) string {
	var b strings.Builder
	b.WriteString(CommentMarker + "\n")
	fmt.Fprintf(&b, "## Merge Gate: %s %s\n\n", icons[r.Verdict], r.Verdict)
	fmt.Fprintf(&b, "判定した SHA: `%s`\n\n", r.HeadSHA)
	b.WriteString("| ID | 条件 | 結果 | 詳細 |\n|---|---|---|---|\n")
	for _, res := range r.Results {
		fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", res.ID, cell(res.Title), icons[res.Status], cell(res.Detail))
	}
	if len(r.Notes) > 0 {
		b.WriteString("\n### 無視したレビュー記録など\n\n")
		for _, n := range r.Notes {
			fmt.Fprintf(&b, "- %s\n", cell(n))
		}
	}
	if targetURL != "" {
		fmt.Fprintf(&b, "\n[実行の記録](%s)\n", targetURL)
	}
	return b.String()
}

func cell(s string) string {
	return strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ", "|", `\|`, "<", "&lt;", ">", "&gt;").Replace(s)
}
