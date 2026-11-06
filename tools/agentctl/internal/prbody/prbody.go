// Package prbody は PR の本文（`.github/pull_request_template.md` の書式）を解析する。
//
//	Closes #<Issue 番号>
//
//	## 概要
//	## 変更内容
//	## Acceptance Criteria   … Issue の AC をすべて転記し、満たしたものにチェックする
//	## テスト                … チェックボックス 1 つ以上、すべてチェック済み
//	## ドキュメント          … チェックボックス 1 つ以上、すべてチェック済み
//	## チェックリスト
package prbody

import (
	"regexp"
	"strconv"

	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/issuebody"
)

// 節の名前。
const (
	SectionAC    = issuebody.SectionAC
	SectionTests = "テスト"
	SectionDocs  = "ドキュメント"
)

// closingRe は GitHub の「Issue を閉じるキーワード」（大文字と小文字を区別しない）。
var closingRe = regexp.MustCompile(`(?i)(?:^|[^\w])(close[sd]?|fix(?:e[sd])?|resolve[sd]?)[ \t]*:?[ \t]+#(\d+)\b`)

// PR は解析した PR の本文。
type PR struct {
	Doc *issuebody.Doc
}

// Parse は PR の本文を解析する。
func Parse(body string) *PR {
	return &PR{Doc: issuebody.Parse(body)}
}

// ClosingIssues は、`Closes`/`Fixes`/`Resolves #N` で参照している Issue の番号を、重複を除いて出てきた順に返す。
// コードブロックと HTML コメントの中は数えない。
func (p *PR) ClosingIssues() []int {
	var out []int
	seen := map[int]bool{}
	for _, m := range closingRe.FindAllStringSubmatch(p.Doc.Text(), -1) {
		n, err := strconv.Atoi(m[2])
		if err != nil || n <= 0 || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
}

// AcceptanceCriteria は PR の `## Acceptance Criteria` のチェックボックスを返す。
func (p *PR) AcceptanceCriteria() []issuebody.Checkbox {
	return p.Doc.AcceptanceCriteria()
}

// Checkboxes は title の節のチェックボックスを返す。節がなければ nil。
func (p *PR) Checkboxes(title string) []issuebody.Checkbox {
	s, ok := p.Doc.Section(title)
	if !ok {
		return nil
	}
	return s.Checkboxes()
}
