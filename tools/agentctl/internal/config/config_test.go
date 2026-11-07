package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repoConfig はリポジトリの `.agents/config.json`。
const repoConfig = "../../../../.agents/config.json"

func TestRepoConfigIsValid(t *testing.T) {
	c, err := Load(repoConfig)
	if err != nil {
		t.Fatal(err)
	}
	if c.DefaultBranch != "main" || c.RequiredChecks[0] != "ci-ok" || !c.IsBlocking("high") || c.IsBlocking("medium") {
		t.Errorf("config = %+v", c)
	}
	if !c.IsTrustedAuthor("k-kizuku") || c.IsTrustedAuthor("other") {
		t.Error("IsTrustedAuthor")
	}
	if !c.IsCodexBot("chatgpt-codex-connector[bot]") || c.IsCodexBot("chatgpt-codex-connector") || c.IsCodexBot("K-Kizuku") {
		t.Error("IsCodexBot")
	}
	if !c.IsFallbackReason("rate_limited") || c.IsFallbackReason("") || c.IsFallbackReason("x") {
		t.Error("IsFallbackReason")
	}
}

// review.carry_over_on_base_merge（Issue #120）は、リポジトリの設定では true、書かなければ false。
func TestCarryOverOnBaseMerge(t *testing.T) {
	c, err := Load(repoConfig)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Review.CarryOverOnBaseMerge {
		t.Error("リポジトリの設定の carry_over_on_base_merge が true ではない")
	}
	c, err = Parse(mutate(t, func(m map[string]any) { delete(review(m), "carry_over_on_base_merge") }))
	if err != nil {
		t.Fatal(err)
	}
	if c.Review.CarryOverOnBaseMerge {
		t.Error("既定が false ではない")
	}
}

// mutate はリポジトリの設定を map として書き換え、JSON にする。
func mutate(t *testing.T, f func(m map[string]any)) []byte {
	t.Helper()
	data, err := os.ReadFile(repoConfig)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	f(m)
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func review(m map[string]any) map[string]any { return m["review"].(map[string]any) }

func TestInvalid(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(m map[string]any)
		want   string
	}{
		{"required_checks が空", func(m map[string]any) { m["required_checks"] = []any{} }, "required_checks"},
		{"required_checks がない", func(m map[string]any) { delete(m, "required_checks") }, "required_checks"},
		{"required_checks に merge-gate", func(m map[string]any) { m["required_checks"] = []any{"merge-gate"} }, "merge-gate"},
		{"blocking に critical がない", func(m map[string]any) { review(m)["blocking_severities"] = []any{"high"} }, `"critical"`},
		{"blocking に high がない", func(m map[string]any) { review(m)["blocking_severities"] = []any{"critical"} }, `"high"`},
		{"blocking に未知の値", func(m map[string]any) {
			review(m)["blocking_severities"] = []any{"critical", "high", "urgent"}
		}, "urgent"},
		{"trusted_authors が空", func(m map[string]any) { review(m)["trusted_authors"] = []any{} }, "trusted_authors"},
		{"codex_bot_authors が空", func(m map[string]any) { review(m)["codex_bot_authors"] = []any{} }, "codex_bot_authors"},
		{"codex_bot_authors がない", func(m map[string]any) { delete(review(m), "codex_bot_authors") }, "codex_bot_authors"},
		{"codex_bot_authors に空の値", func(m map[string]any) { review(m)["codex_bot_authors"] = []any{" "} }, "codex_bot_authors"},
		{"max_workers が 0", func(m map[string]any) { m["max_workers"] = 0 }, "max_workers"},
		{"max_workers が 9", func(m map[string]any) { m["max_workers"] = 9 }, "max_workers"},
		{"retry_limit が 0", func(m map[string]any) { m["retry_limit"] = 0 }, "retry_limit"},
		{"retry_limit が 6", func(m map[string]any) { m["retry_limit"] = 6 }, "retry_limit"},
		{"dev_slots が 0", func(m map[string]any) { m["dev_slots"] = 0 }, "dev_slots"},
		{"dev_slots が 21", func(m map[string]any) { m["dev_slots"] = 21 }, "dev_slots"},
		{"version が 2", func(m map[string]any) { m["version"] = 2 }, "version"},
		{"primary が別", func(m map[string]any) { review(m)["primary"] = "anyone" }, "primary"},
		{"未知のキー", func(m map[string]any) { m["skip_review"] = true }, "unknown field"},
		{"review の未知のキー", func(m map[string]any) { review(m)["disabled"] = true }, "unknown field"},
		{"不正な glob", func(m map[string]any) { m["protected_paths"] = []any{"a/[b"} }, "protected_paths"},
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

func TestTrailingData(t *testing.T) {
	data, _ := os.ReadFile(repoConfig)
	if _, err := Parse(append(data, []byte(`{}`)...)); err == nil {
		t.Fatal("後ろのデータを受け入れた")
	}
}

func TestFind(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, ".agents", "config.json")
	if err := os.WriteFile(want, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "tools", "agentctl")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := Find(sub)
	if err != nil || got != want {
		t.Fatalf("Find = %q, %v", got, err)
	}
}
