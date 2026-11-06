package review

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/config"
)

func loadConfig(t *testing.T) *config.Config {
	t.Helper()
	c, err := config.Load("../../../../.agents/config.json")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func validJSON(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/valid.json")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestValid(t *testing.T) {
	r, err := Parse(validJSON(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Validate(loadConfig(t)); err != nil {
		t.Fatal(err)
	}
	if r.Round != 1 || len(r.Findings) != 1 || r.CreatedTime().IsZero() {
		t.Errorf("record = %+v", r)
	}
}

func mutate(t *testing.T, f func(m map[string]any)) []byte {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(validJSON(t), &m); err != nil {
		t.Fatal(err)
	}
	f(m)
	out, _ := json.Marshal(m)
	return out
}

func finding0(m map[string]any) map[string]any {
	return m["findings"].([]any)[0].(map[string]any)
}

func TestInvalid(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(m map[string]any)
		want   string
	}{
		{"schema がない", func(m map[string]any) { delete(m, "schema") }, "schema"},
		{"head_sha がない", func(m map[string]any) { delete(m, "head_sha") }, "head_sha"},
		{"head_sha が短い", func(m map[string]any) { m["head_sha"] = "abc123" }, "head_sha"},
		{"head_sha が大文字", func(m map[string]any) { m["head_sha"] = strings.Repeat("A", 40) }, "head_sha"},
		{"base_sha が 16 進ではない", func(m map[string]any) { m["base_sha"] = strings.Repeat("g", 40) }, "base_sha"},
		{"pr が 0", func(m map[string]any) { m["pr"] = 0 }, "pr"},
		{"round が 0", func(m map[string]any) { m["round"] = 0 }, "round"},
		{"reviewer が不正", func(m map[string]any) { m["reviewer"] = "human" }, "reviewer"},
		{"reviewer_version がない", func(m map[string]any) { delete(m, "reviewer_version") }, "reviewer_version"},
		{"verdict が不正", func(m map[string]any) { m["verdict"] = "lgtm" }, "verdict"},
		{"summary がない", func(m map[string]any) { delete(m, "summary") }, "summary"},
		{"protected_paths_ack がない", func(m map[string]any) { delete(m, "protected_paths_ack") }, "protected_paths_ack"},
		{"weakens_guards がない", func(m map[string]any) {
			delete(m["protected_paths_ack"].(map[string]any), "weakens_guards")
		}, "weakens_guards"},
		{"findings がない", func(m map[string]any) { delete(m, "findings") }, "findings"},
		{"severity が不正", func(m map[string]any) { finding0(m)["severity"] = "blocker" }, "severity"},
		{"category が不正", func(m map[string]any) { finding0(m)["category"] = "style" }, "category"},
		{"status が不正", func(m map[string]any) { finding0(m)["status"] = "fixed" }, "status"},
		{"id がない", func(m map[string]any) { delete(finding0(m), "id") }, "id"},
		{"id が重複", func(m map[string]any) {
			m["findings"] = append(m["findings"].([]any), finding0(m))
		}, "重複"},
		{"created_at が RFC 3339 ではない", func(m map[string]any) { m["created_at"] = "2026/11/01" }, "created_at"},
		{"フォールバックの理由なし", func(m map[string]any) { m["reviewer"] = "claude-fallback" }, "fallback_reason"},
		{"codex で理由がある", func(m map[string]any) { m["fallback_reason"] = "rate_limited" }, "fallback_reason"},
		{"未知のキー", func(m map[string]any) { m["force_approve"] = true }, "unknown field"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(mutate(t, tt.mutate))
			if err == nil {
				t.Fatal("エラーにならない")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want %q を含む", err, tt.want)
			}
		})
	}
}

func TestFallbackReasonFromConfig(t *testing.T) {
	cfg := loadConfig(t)
	ok, err := Parse(mutate(t, func(m map[string]any) {
		m["reviewer"] = "claude-fallback"
		m["fallback_reason"] = "rate_limited"
	}))
	if err != nil {
		t.Fatal(err)
	}
	if err := ok.Validate(cfg); err != nil {
		t.Errorf("正しいフォールバック: %v", err)
	}
	bad, err := Parse(mutate(t, func(m map[string]any) {
		m["reviewer"] = "claude-fallback"
		m["fallback_reason"] = "bored"
	}))
	if err != nil {
		t.Fatal(err)
	}
	if err := bad.Validate(cfg); err == nil || !strings.Contains(err.Error(), "fallback_reasons") {
		t.Errorf("設定にない理由: %v", err)
	}
	cfg.Review.AllowFallback = false
	if err := ok.Validate(cfg); err == nil {
		t.Error("allow_fallback=false なのに通った")
	}
}

func TestRenderRoundTrip(t *testing.T) {
	r, err := Parse(validJSON(t))
	if err != nil {
		t.Fatal(err)
	}
	r.Summary = "目印 " + Marker + " と | と\n改行と ``` を含む"
	r.Findings[0].Title = "<b>|x|</b>"
	body, err := Render(r)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(body, Marker); n != 1 {
		t.Fatalf("目印が %d 個:\n%s", n, body)
	}
	if !strings.Contains(body, "| F1 |") || !strings.Contains(body, "```json\n") {
		t.Errorf("表または JSON がない:\n%s", body)
	}
	got, err := FromComment(body)
	if err != nil {
		t.Fatalf("FromComment: %v\n%s", err, body)
	}
	if got.Summary != r.Summary || got.Findings[0].Title != r.Findings[0].Title {
		t.Errorf("往復で変わった: %+v", got)
	}
	if _, err := ParseDocument([]byte(body)); err != nil {
		t.Errorf("ParseDocument(comment): %v", err)
	}
	if _, err := ParseDocument(validJSON(t)); err != nil {
		t.Errorf("ParseDocument(json): %v", err)
	}
}

func TestFromCommentErrors(t *testing.T) {
	js := string(validJSON(t))
	tests := map[string]string{
		"目印がない":         "```json\n" + js + "\n```\n",
		"目印が 2 つ":       Marker + Marker + "\n```json\n" + js + "\n```\n",
		"JSON がない":      Marker + "\nLGTM\n",
		"JSON が 2 つ":    Marker + "\n```json\n" + js + "\n```\n```json\n" + js + "\n```\n",
		"JSON が壊れている":   Marker + "\n```json\n{\"schema\":\n```\n",
		"言語が json ではない": Marker + "\n```yaml\n" + js + "\n```\n",
	}
	for name, body := range tests {
		if _, err := FromComment(body); err == nil {
			t.Errorf("%s: エラーにならない", name)
		}
	}
	if _, err := FromComment("hello"); err != ErrNoMarker {
		t.Errorf("目印なし: %v", err)
	}
	// CRLF とほかのコードブロックがあっても読める。
	body := strings.ReplaceAll(Marker+"\n```text\nlog\n```\n```json\n"+js+"\n```\n", "\n", "\r\n")
	if _, err := FromComment(body); err != nil {
		t.Errorf("CRLF: %v", err)
	}
}
