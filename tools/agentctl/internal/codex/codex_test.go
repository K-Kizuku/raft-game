package codex

import (
	"errors"
	"strings"
	"testing"
)

const sha = "0123456789abcdef0123456789abcdef01234567"

func TestParseState(t *testing.T) {
	body := SummaryMarker + "\n### Codex Review\n" +
		`<!-- codex-review-state:v1 {"code_review":{"commit_sha":"` + sha + `","has_any_findings":true,"has_unresolved_findings":true,"status":"completed"},"security_review":{"status":"not_requested","extra":1}} -->`
	st, err := ParseState(body)
	if err != nil {
		t.Fatal(err)
	}
	if cr := st.CodeReview; cr.CommitSHA != sha || !cr.HasAnyFindings || !cr.HasUnresolvedFindings || cr.Status != StatusCompleted {
		t.Errorf("state = %+v", cr)
	}

	if _, err := ParseState("no state"); !errors.Is(err, ErrNoState) {
		t.Errorf("err = %v", err)
	}
	for name, b := range map[string]string{
		"2 つ":            body + "\n" + body,
		"閉じていない":         StateTag + ` {"code_review":{}}`,
		"壊れた JSON":       StateTag + ` {"code_review": -->`,
		"code_review なし": StateTag + ` {"security_review":{}} -->`,
		"SHA が短い":        StateTag + ` {"code_review":{"commit_sha":"0123456","status":"completed"}} -->`,
	} {
		if _, err := ParseState(b); err == nil || errors.Is(err, ErrNoState) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	// 未完了なら commit_sha は確かめない。
	if st, err := ParseState(StateTag + ` {"code_review":{"commit_sha":null,"status":"in_progress"}} -->`); err != nil || st.CodeReview.Status != "in_progress" {
		t.Errorf("in_progress: %+v, %v", st, err)
	}
}

func TestSeverityAndTitle(t *testing.T) {
	body := "**<sub><sub>![P1 Badge](https://img.shields.io/badge/P1-orange?style=flat)</sub></sub>  Guard against nil config**\n\n" +
		"Line 1 of detail.\nLine 2.\n\nUseful? React with 👍 / 👎.\n\n" + FindingMarker
	if !IsFinding(body) || Priority(body) != "P1" {
		t.Fatalf("priority = %q", Priority(body))
	}
	if s, ok := Severity(body); !ok || s != "high" {
		t.Errorf("severity = %q, %t", s, ok)
	}
	if got := Title(body); got != "Guard against nil config" {
		t.Errorf("title = %q", got)
	}
	if got := Detail(body, 0); got != "Line 1 of detail.\nLine 2." {
		t.Errorf("detail = %q", got)
	}
	if got := Detail(body, 4); got != "Line…" {
		t.Errorf("detail(4) = %q", got)
	}
	for p, want := range map[string]string{"P0": "critical", "P1": "high", "P2": "medium", "P3": "low"} {
		if s, _ := Severity("![" + p + " Badge](x)"); s != want {
			t.Errorf("%s → %s", p, s)
		}
		// 代替テキストがなくても、URL のバッジで分かる。
		if s, _ := Severity("![](https://img.shields.io/badge/" + p + "-red)"); s != want {
			t.Errorf("%s（URL）→ %s", p, s)
		}
	}
	if _, ok := Severity("**no badge**"); ok {
		t.Error("バッジがないのに重大度がある")
	}
	if got := Title("\n\n" + FindingMarker); got != "" {
		t.Errorf("title = %q", got)
	}
	if !strings.Contains(ReviewerVersion, "GitHub") {
		t.Error(ReviewerVersion)
	}
}
