package issuebody

import (
	"reflect"
	"testing"
)

const body = "親: #1\r\n" +
	"\r\n" +
	"## 背景・目的\r\n" +
	"\r\n" +
	"説明。\r\n" +
	"\r\n" +
	"### 小見出し（親の節の中身）\r\n" +
	"\r\n" +
	"```markdown\r\n" +
	"## Acceptance Criteria\r\n" +
	"- [x] AC-9: コードブロックの中は数えない\r\n" +
	"```\r\n" +
	"\r\n" +
	"## 変更予定ファイル\r\n" +
	"\r\n" +
	"- `tools/agentctl/**`\r\n" +
	"- `./.github/workflows/merge-gate.yml`（説明）\r\n" +
	"- 説明だけの行\r\n" +
	"<!-- - `secret/**` -->\r\n" +
	"\r\n" +
	"## 変更禁止範囲\r\n" +
	"\r\n" +
	"- `docs/**`\r\n" +
	"\r\n" +
	"## 依存 Issue\r\n" +
	"\r\n" +
	"- #2（CI）\r\n" +
	"- #4, #2\r\n" +
	"\r\n" +
	"## Acceptance Criteria ##\r\n" +
	"\r\n" +
	"- [ ] AC-1: 一つ目\r\n" +
	"- [x] AC-2: 二つ目\r\n" +
	"  - [X] AC-3: 入れ子\r\n" +
	"<!--\r\n" +
	"- [ ] AC-4: コメントの中は数えない\r\n" +
	"-->\r\n" +
	"~~~\r\n" +
	"- [ ] AC-5: ~~~ のコードブロックも数えない\r\n" +
	"~~~\r\n" +
	"- 普通の行\r\n"

func TestSections(t *testing.T) {
	d := Parse(body)
	var titles []string
	for _, s := range d.Sections {
		titles = append(titles, s.Title)
	}
	want := []string{"", "背景・目的", "変更予定ファイル", "変更禁止範囲", "依存 Issue", "Acceptance Criteria"}
	if !reflect.DeepEqual(titles, want) {
		t.Fatalf("titles = %q", titles)
	}
	if _, ok := d.Section("小見出し（親の節の中身）"); ok {
		t.Error("### を節として扱った")
	}
}

func TestAcceptanceCriteria(t *testing.T) {
	got := Parse(body).AcceptanceCriteria()
	want := []Checkbox{{false, "AC-1: 一つ目"}, {true, "AC-2: 二つ目"}, {true, "AC-3: 入れ子"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("AC = %+v", got)
	}
	if got := Parse("## 概要\n- [ ] x\n").AcceptanceCriteria(); got != nil {
		t.Errorf("節がないのに %+v", got)
	}
}

func TestPatterns(t *testing.T) {
	d := Parse(body)
	if got, want := d.PlannedFiles(), []string{"tools/agentctl/**", ".github/workflows/merge-gate.yml"}; !reflect.DeepEqual(got, want) {
		t.Errorf("planned = %q", got)
	}
	if got, want := d.ForbiddenFiles(), []string{"docs/**"}; !reflect.DeepEqual(got, want) {
		t.Errorf("forbidden = %q", got)
	}
}

func TestDependencies(t *testing.T) {
	got, err := Parse(body).Dependencies()
	if err != nil || !reflect.DeepEqual(got, []int{2, 4}) {
		t.Fatalf("deps = %v, %v", got, err)
	}
	got, err = Parse("## 依存 Issue\n\n- なし\n").Dependencies()
	if err != nil || len(got) != 0 {
		t.Fatalf("なし = %v, %v", got, err)
	}
	for _, b := range []string{"## 概要\n", "## 依存 Issue\n\n", "## 依存 Issue\n- 未定\n"} {
		if _, err := Parse(b).Dependencies(); err == nil {
			t.Errorf("%q がエラーにならない", b)
		}
	}
}

func TestMatch(t *testing.T) {
	tests := []struct {
		pattern, name string
		want          bool
	}{
		{"tools/agentctl/**", "tools/agentctl/main.go", true},
		{"tools/agentctl/**", "tools/agentctl/internal/gate/gate.go", true},
		{"tools/agentctl/**", "tools/agentctl", true},
		{"tools/agentctl/**", "tools/agentctl2/main.go", false},
		{"tools/agentctl/**", "tools/main.go", false},
		{".github/workflows/**", ".github/workflows/ci.yml", true},
		{".github/workflows/*.yml", ".github/workflows/ci.yml", true},
		{".github/workflows/*.yml", ".github/workflows/sub/ci.yml", false},
		{"**/*.go", "main.go", true},
		{"**/*.go", "a/b/c.go", true},
		{"**/*.go", "a/b/c.ts", false},
		{"a/**/z.txt", "a/z.txt", true},
		{"a/**/z.txt", "a/b/c/z.txt", true},
		{"a/**/z.txt", "a/b/c/y.txt", false},
		{"a/**/**/z", "a/b/z", true},
		{"CLAUDE.md", "CLAUDE.md", true},
		{"CLAUDE.md", "docs/CLAUDE.md", false},
		{"./CLAUDE.md", "CLAUDE.md", true},
		{"docs/**", "docs", true},
		{"docs/**", "docsx/a.md", false},
		{"*", "a/b", false},
	}
	for _, tt := range tests {
		got, err := Match(tt.pattern, tt.name)
		if err != nil {
			t.Errorf("Match(%q, %q): %v", tt.pattern, tt.name, err)
			continue
		}
		if got != tt.want {
			t.Errorf("Match(%q, %q) = %t, want %t", tt.pattern, tt.name, got, tt.want)
		}
	}
}

func TestBadPattern(t *testing.T) {
	for _, p := range []string{"", "a/[b", "a//b", "a/", "a\\"} {
		if err := ValidatePattern(p); err == nil {
			t.Errorf("ValidatePattern(%q) がエラーにならない", p)
		}
		if _, err := Match(p, "a/b"); err == nil {
			t.Errorf("Match(%q) がエラーにならない", p)
		}
	}
	if ok, err := MatchAny([]string{"x/**", "a/*"}, "a/b"); !ok || err != nil {
		t.Errorf("MatchAny = %t, %v", ok, err)
	}
}
