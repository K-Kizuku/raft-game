// Package issuebody は、Issue・PR の本文（Markdown）を節に分け、
// チェックボックス・パスの glob・依存 Issue を取り出す。
//
// 書式（`.github/ISSUE_TEMPLATE/agent-task.md`）:
//
//   - 節は `## ` で始まる見出しで分ける（`### ` 以下は親の節の中身）
//   - コードブロック（``` または ~~~）と HTML コメントの中は無視する
//   - `## 変更予定ファイル`・`## 変更禁止範囲`: 箇条書きの各行の最初のインラインコードがパスまたは glob
//   - `## 依存 Issue`: 箇条書きで `#番号`、なければ `- なし`
//   - `## Acceptance Criteria`: `- [ ] AC-1: …` のチェックボックス
package issuebody

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// 節の名前。
const (
	SectionPlannedFiles = "変更予定ファイル"
	SectionForbidden    = "変更禁止範囲"
	SectionDependencies = "依存 Issue"
	SectionAC           = "Acceptance Criteria"
)

// Section は `## ` の見出しで区切った節。
type Section struct {
	Title string
	Lines []string
}

// Checkbox は箇条書きのチェックボックス。
type Checkbox struct {
	Checked bool
	Text    string
}

// Doc は解析した本文。
type Doc struct {
	Sections []Section
}

var (
	headingRe  = regexp.MustCompile(`^ {0,3}##[ \t]+(.*?)[ \t]*#*[ \t]*$`)
	fenceRe    = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})")
	checkboxRe = regexp.MustCompile(`^[ \t]*[-*+][ \t]+\[([ xX])\](?:[ \t]+(.*))?$`)
	listItemRe = regexp.MustCompile(`^[ \t]*[-*+][ \t]+(.*)$`)
	inlineRe   = regexp.MustCompile("`([^`]+)`")
	issueRefRe = regexp.MustCompile(`#(\d+)\b`)
)

// VisibleLines は、改行を LF にそろえ、コードブロックと HTML コメントを除いた行を返す。
func VisibleLines(body string) []string {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	body = strings.ReplaceAll(body, "\r", "\n")
	var out []string
	inComment := false
	fence := "" // 開いているコードブロックの目印（空なら外）
	for _, line := range strings.Split(body, "\n") {
		if fence != "" {
			if m := fenceRe.FindStringSubmatch(line); m != nil &&
				m[1][0] == fence[0] && len(m[1]) >= len(fence) &&
				strings.TrimSpace(line[strings.Index(line, m[1])+len(m[1]):]) == "" {
				fence = ""
			}
			continue
		}
		if !inComment {
			if m := fenceRe.FindStringSubmatch(line); m != nil {
				fence = m[1]
				continue
			}
		}
		var b strings.Builder
		rest := line
		for {
			if inComment {
				i := strings.Index(rest, "-->")
				if i < 0 {
					rest = ""
					break
				}
				rest = rest[i+3:]
				inComment = false
				continue
			}
			i := strings.Index(rest, "<!--")
			if i < 0 {
				b.WriteString(rest)
				break
			}
			b.WriteString(rest[:i])
			rest = rest[i+4:]
			inComment = true
		}
		out = append(out, b.String())
	}
	return out
}

// Parse は本文を節に分ける。最初の見出しより前の行は Title が空の節に入る。
func Parse(body string) *Doc {
	d := &Doc{}
	cur := Section{}
	for _, line := range VisibleLines(body) {
		if m := headingRe.FindStringSubmatch(line); m != nil {
			d.Sections = append(d.Sections, cur)
			cur = Section{Title: strings.TrimSpace(m[1])}
			continue
		}
		cur.Lines = append(cur.Lines, line)
	}
	d.Sections = append(d.Sections, cur)
	return d
}

// Text は、見出しより前の部分も含め、見える行をすべてつないだもの。
func (d *Doc) Text() string {
	var b strings.Builder
	for _, s := range d.Sections {
		for _, l := range s.Lines {
			b.WriteString(l)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// Section は、title の節を返す。同じ名前の節が複数あるときは最初のもの。
func (d *Doc) Section(title string) (Section, bool) {
	for _, s := range d.Sections {
		if s.Title != "" && s.Title == title {
			return s, true
		}
	}
	return Section{}, false
}

// Checkboxes は節の中のチェックボックスを返す。
func (s Section) Checkboxes() []Checkbox {
	var out []Checkbox
	for _, l := range s.Lines {
		if m := checkboxRe.FindStringSubmatch(l); m != nil {
			out = append(out, Checkbox{Checked: m[1] != " ", Text: strings.TrimSpace(m[2])})
		}
	}
	return out
}

// Patterns は、箇条書きの各行の最初のインラインコードを、パスまたは glob として返す。
// インラインコードのない行は無視する。
func (s Section) Patterns() []string {
	var out []string
	for _, l := range s.Lines {
		m := listItemRe.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		if c := inlineRe.FindStringSubmatch(m[1]); c != nil {
			out = append(out, NormalizePath(strings.TrimSpace(c[1])))
		}
	}
	return out
}

// AcceptanceCriteria は `## Acceptance Criteria` のチェックボックスを返す。節がなければ nil。
func (d *Doc) AcceptanceCriteria() []Checkbox {
	s, ok := d.Section(SectionAC)
	if !ok {
		return nil
	}
	return s.Checkboxes()
}

// PlannedFiles は `## 変更予定ファイル` の glob を返す。
func (d *Doc) PlannedFiles() []string {
	s, _ := d.Section(SectionPlannedFiles)
	return s.Patterns()
}

// ForbiddenFiles は `## 変更禁止範囲` の glob を返す。
func (d *Doc) ForbiddenFiles() []string {
	s, _ := d.Section(SectionForbidden)
	return s.Patterns()
}

// Dependencies は `## 依存 Issue` の Issue 番号を返す。`- なし` なら空。
// 節がない、または `#番号` も `なし` もない項目があればエラー。
func (d *Doc) Dependencies() ([]int, error) {
	s, ok := d.Section(SectionDependencies)
	if !ok {
		return nil, fmt.Errorf("issuebody: 「%s」の節がない", SectionDependencies)
	}
	var out []int
	seen := map[int]bool{}
	items := 0
	for _, l := range s.Lines {
		m := listItemRe.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		items++
		text := strings.TrimSpace(m[1])
		refs := issueRefRe.FindAllStringSubmatch(text, -1)
		if len(refs) == 0 {
			if text == "なし" {
				continue
			}
			return nil, fmt.Errorf("issuebody: 「%s」の項目 %q に #番号 がない", SectionDependencies, text)
		}
		for _, r := range refs {
			n, err := strconv.Atoi(r[1])
			if err != nil || n <= 0 {
				return nil, fmt.Errorf("issuebody: 不正な Issue 番号 %q", r[0])
			}
			if !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	if items == 0 {
		return nil, fmt.Errorf("issuebody: 「%s」の節に項目がない（なければ `- なし` と書く）", SectionDependencies)
	}
	return out, nil
}
