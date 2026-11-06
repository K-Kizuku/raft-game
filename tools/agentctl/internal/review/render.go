package review

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// Render は、コメントの本文（目印＋人の読める表＋JSON）を返す。
// JSON は HTML の特殊文字をエスケープするので、目印が JSON の中に現れることはない。
func Render(r *Record) (string, error) {
	var js bytes.Buffer
	enc := json.NewEncoder(&js)
	enc.SetEscapeHTML(true)
	enc.SetIndent("", "  ")
	if err := enc.Encode(r); err != nil {
		return "", err
	}

	var b strings.Builder
	b.WriteString(Marker + "\n")
	fmt.Fprintf(&b, "## レビュー記録（round %d）\n\n", r.Round)
	b.WriteString("| 項目 | 値 |\n|---|---|\n")
	row := func(k, v string) { fmt.Fprintf(&b, "| %s | %s |\n", k, cell(v)) }
	row("判定", r.Verdict)
	reviewer := r.Reviewer + "（" + r.ReviewerVersion + "）"
	if r.FallbackReason != "" {
		reviewer += "／理由: " + r.FallbackReason
	}
	row("レビュー役", reviewer)
	row("HEAD", "`"+r.HeadSHA+"`")
	row("ベース", "`"+r.BaseSHA+"`")
	if a := r.ProtectedPathsAck; a != nil && a.Reviewed != nil && a.WeakensGuards != nil {
		row("保護されたパス", fmt.Sprintf("確認した: %t／ガードを弱める: %t", *a.Reviewed, *a.WeakensGuards))
	}
	row("日時", r.CreatedAt)
	row("要約", r.Summary)
	b.WriteString("\n")

	if len(r.Findings) == 0 {
		b.WriteString("指摘はありません。\n\n")
	} else {
		b.WriteString("| ID | 重大度 | 種類 | 場所 | 指摘 | 状態 |\n|---|---|---|---|---|---|\n")
		for _, f := range r.Findings {
			loc := f.File
			if f.Line > 0 {
				loc = fmt.Sprintf("%s:%d", f.File, f.Line)
			}
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s |\n",
				cell(f.ID), cell(f.Severity), cell(f.Category), cell(loc), cell(f.Title), cell(f.Status))
		}
		b.WriteString("\n")
	}

	b.WriteString("```json\n")
	b.Write(js.Bytes())
	b.WriteString("```\n")
	return b.String(), nil
}

// cell は表のセルに入れる文字列をエスケープする（目印や表の崩れを防ぐ）。
func cell(s string) string {
	r := strings.NewReplacer(
		"\r\n", " ", "\n", " ", "\r", " ",
		"|", `\|`, "<", "&lt;", ">", "&gt;", "`", "'",
	)
	if strings.HasPrefix(s, "`") && strings.HasSuffix(s, "`") && len(s) > 2 && !strings.ContainsAny(s[1:len(s)-1], "`|<>\n") {
		return s
	}
	return r.Replace(s)
}
