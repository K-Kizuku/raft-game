package prbody

import (
	"reflect"
	"testing"
)

func TestClosingIssues(t *testing.T) {
	tests := []struct {
		body string
		want []int
	}{
		{"Closes #3\n", []int{3}},
		{"closes #3 and fixes #3\n", []int{3}},
		{"Fixes #3\nResolved: #4\n", []int{3, 4}},
		{"FIXED #10", []int{10}},
		{"Refs #3\n", nil},
		{"Closes #<Issue 番号>\n", nil},
		{"Encloses #3\n", nil},
		{"```\nCloses #3\n```\n", nil},
		{"<!-- Closes #3 -->\n", nil},
		{"(closes #7)", []int{7}},
	}
	for _, tt := range tests {
		if got := Parse(tt.body).ClosingIssues(); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%q: got %v, want %v", tt.body, got, tt.want)
		}
	}
}

func TestSections(t *testing.T) {
	p := Parse("Closes #3\n\n## Acceptance Criteria\n<!-- コメント -->\n- [x] AC-1: a\n- [ ] AC-2: b\n## テスト\n- [x] t\n## ドキュメント\n")
	if got := p.AcceptanceCriteria(); len(got) != 2 || !got[0].Checked || got[1].Checked {
		t.Errorf("AC = %+v", got)
	}
	if got := p.Checkboxes(SectionTests); len(got) != 1 {
		t.Errorf("テスト = %+v", got)
	}
	if got := p.Checkboxes(SectionDocs); len(got) != 0 {
		t.Errorf("ドキュメント = %+v", got)
	}
	if got := p.Checkboxes("ない節"); got != nil {
		t.Errorf("ない節 = %+v", got)
	}
}
