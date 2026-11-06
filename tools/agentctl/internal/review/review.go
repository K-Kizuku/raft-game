// Package review は、PR のコメントに載せるレビュー記録（agent-review/v1）を扱う。
//
// コメントの本文には、目印 `<!-- agent-review:v1 -->` を 1 つと、言語 `json` のコードブロックを 1 つ含める。
// JSON の形:
//
//	{
//	  "schema": "agent-review/v1",
//	  "pr": 12,
//	  "head_sha": "40 桁の 16 進", "base_sha": "40 桁の 16 進",
//	  "round": 1,
//	  "reviewer": "codex | claude-fallback",
//	  "reviewer_version": "codex-cli 0.167.0",
//	  "fallback_reason": "",              // reviewer が claude-fallback のときだけ。設定の fallback_reasons のいずれか
//	  "verdict": "approve | request_changes",
//	  "summary": "…",
//	  "protected_paths_ack": { "reviewed": true, "weakens_guards": false },
//	  "findings": [ { "id": "F1", "severity": "…", "category": "…", "file": "path", "line": 10,
//	                  "title": "…", "detail": "…", "status": "open | resolved | wont_fix", "resolution": "…" } ],
//	  "created_at": "RFC 3339"
//	}
package review

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/config"
)

// Marker はレビュー記録のコメントの目印。
const Marker = "<!-- agent-review:v1 -->"

// Schema は JSON の schema の値。
const Schema = "agent-review/v1"

// 列挙値。
var (
	Verdicts   = []string{"approve", "request_changes"}
	Categories = []string{"correctness", "security", "performance", "concurrency", "test-coverage", "spec", "maintainability"}
	Statuses   = []string{"open", "resolved", "wont_fix"}
	Reviewers  = []string{config.ReviewerPrimary, config.ReviewerFallback}
)

// ErrNoMarker は、コメントがレビュー記録ではない（目印がない）ことを表す。
var ErrNoMarker = errors.New("review: 目印がない")

var shaRe = regexp.MustCompile(`^[0-9a-f]{40}$`)

// Ack は保護されたパスの確認の結果。
type Ack struct {
	Reviewed      *bool `json:"reviewed"`
	WeakensGuards *bool `json:"weakens_guards"`
}

// Finding はレビューの指摘。
type Finding struct {
	ID         string `json:"id"`
	Severity   string `json:"severity"`
	Category   string `json:"category"`
	File       string `json:"file"`
	Line       int    `json:"line"`
	Title      string `json:"title"`
	Detail     string `json:"detail"`
	Status     string `json:"status"`
	Resolution string `json:"resolution"`
}

// Record はレビュー記録。
type Record struct {
	Schema            string    `json:"schema"`
	PR                int       `json:"pr"`
	HeadSHA           string    `json:"head_sha"`
	BaseSHA           string    `json:"base_sha"`
	Round             int       `json:"round"`
	Reviewer          string    `json:"reviewer"`
	ReviewerVersion   string    `json:"reviewer_version"`
	FallbackReason    string    `json:"fallback_reason"`
	Verdict           string    `json:"verdict"`
	Summary           string    `json:"summary"`
	ProtectedPathsAck *Ack      `json:"protected_paths_ack"`
	Findings          []Finding `json:"findings"`
	CreatedAt         string    `json:"created_at"`
}

// Parse は JSON を読み、スキーマを検証する（設定に依存する確認はしない）。
func Parse(data []byte) (*Record, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var r Record
	if err := dec.Decode(&r); err != nil {
		return nil, fmt.Errorf("review: JSON を読めない: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("review: JSON の後ろに余計なデータがある")
	}
	if err := r.ValidateSchema(); err != nil {
		return nil, err
	}
	return &r, nil
}

// ParseDocument は、レビュー記録の JSON か、レビュー記録のコメントの本文のどちらかを読む。
func ParseDocument(data []byte) (*Record, error) {
	if bytes.Contains(data, []byte(Marker)) {
		return FromComment(string(data))
	}
	return Parse(data)
}

// FromComment はコメントの本文からレビュー記録を取り出す。
// 目印がなければ ErrNoMarker。目印や JSON のコードブロックが複数あればエラー。
func FromComment(body string) (*Record, error) {
	n := strings.Count(body, Marker)
	if n == 0 {
		return nil, ErrNoMarker
	}
	if n > 1 {
		return nil, fmt.Errorf("review: 目印が %d 個ある", n)
	}
	blocks := jsonBlocks(body)
	switch len(blocks) {
	case 0:
		return nil, errors.New("review: json のコードブロックがない")
	case 1:
		return Parse([]byte(blocks[0]))
	default:
		return nil, fmt.Errorf("review: json のコードブロックが %d 個ある", len(blocks))
	}
}

var openFenceRe = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})[ \t]*([^ \t`]*)")

// jsonBlocks は、言語 json のコードブロックの中身を返す。
func jsonBlocks(body string) []string {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	var out []string
	var cur []string
	fence, isJSON := "", false
	for _, line := range strings.Split(body, "\n") {
		if fence == "" {
			if m := openFenceRe.FindStringSubmatch(line); m != nil {
				fence, isJSON, cur = m[1], strings.EqualFold(m[2], "json"), nil
			}
			continue
		}
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, fence[:1]) && strings.Trim(t, fence[:1]) == "" && len(t) >= len(fence) {
			if isJSON {
				out = append(out, strings.Join(cur, "\n"))
			}
			fence = ""
			continue
		}
		cur = append(cur, line)
	}
	return out
}

// ValidateSchema は、必須項目・列挙値・SHA の形式・日時の形式を確かめる。
func (r *Record) ValidateSchema() error {
	var errs []string
	add := func(format string, a ...any) { errs = append(errs, fmt.Sprintf(format, a...)) }

	if r.Schema != Schema {
		add("schema は %q にする（%q）", Schema, r.Schema)
	}
	if r.PR <= 0 {
		add("pr は 1 以上にする")
	}
	if !shaRe.MatchString(r.HeadSHA) {
		add("head_sha が 40 桁の 16 進（小文字）ではない（%q）", r.HeadSHA)
	}
	if !shaRe.MatchString(r.BaseSHA) {
		add("base_sha が 40 桁の 16 進（小文字）ではない（%q）", r.BaseSHA)
	}
	if r.Round < 1 {
		add("round は 1 以上にする")
	}
	if !contains(Reviewers, r.Reviewer) {
		add("reviewer が不正（%q）", r.Reviewer)
	}
	if strings.TrimSpace(r.ReviewerVersion) == "" {
		add("reviewer_version がない")
	}
	if r.Reviewer == config.ReviewerFallback && r.FallbackReason == "" {
		add("reviewer が %s のときは fallback_reason が必要", config.ReviewerFallback)
	}
	if r.Reviewer == config.ReviewerPrimary && r.FallbackReason != "" {
		add("reviewer が %s のときは fallback_reason を空にする", config.ReviewerPrimary)
	}
	if !contains(Verdicts, r.Verdict) {
		add("verdict が不正（%q）", r.Verdict)
	}
	if strings.TrimSpace(r.Summary) == "" {
		add("summary がない")
	}
	if r.ProtectedPathsAck == nil {
		add("protected_paths_ack がない")
	} else {
		if r.ProtectedPathsAck.Reviewed == nil {
			add("protected_paths_ack.reviewed がない")
		}
		if r.ProtectedPathsAck.WeakensGuards == nil {
			add("protected_paths_ack.weakens_guards がない")
		}
	}
	if r.Findings == nil {
		add("findings がない（指摘がなければ [] にする）")
	}
	ids := map[string]bool{}
	for i, f := range r.Findings {
		where := fmt.Sprintf("findings[%d]", i)
		if strings.TrimSpace(f.ID) == "" {
			add("%s.id がない", where)
		} else if ids[f.ID] {
			add("%s.id %q が重複している", where, f.ID)
		}
		ids[f.ID] = true
		if !contains(config.Severities, f.Severity) {
			add("%s.severity が不正（%q）", where, f.Severity)
		}
		if !contains(Categories, f.Category) {
			add("%s.category が不正（%q）", where, f.Category)
		}
		if f.Line < 0 {
			add("%s.line は 0 以上にする", where)
		}
		if strings.TrimSpace(f.Title) == "" {
			add("%s.title がない", where)
		}
		if !contains(Statuses, f.Status) {
			add("%s.status が不正（%q）", where, f.Status)
		}
	}
	if _, err := time.Parse(time.RFC3339, r.CreatedAt); err != nil {
		add("created_at が RFC 3339 ではない（%q）", r.CreatedAt)
	}
	if len(errs) > 0 {
		return fmt.Errorf("review: 記録が不正: %s", strings.Join(errs, "; "))
	}
	return nil
}

// Validate は、スキーマに加えて、設定に依存する確認（フォールバックの理由）をする。
func (r *Record) Validate(cfg *config.Config) error {
	if err := r.ValidateSchema(); err != nil {
		return err
	}
	if err := r.CheckReviewer(cfg); err != nil {
		return fmt.Errorf("review: 記録が不正: %w", err)
	}
	return nil
}

// CheckReviewer は、reviewer が codex か、フォールバックが許されていて理由が正しいかを確かめる。
func (r *Record) CheckReviewer(cfg *config.Config) error {
	switch r.Reviewer {
	case cfg.Review.Primary:
		return nil
	case cfg.Review.Fallback:
		if !cfg.Review.AllowFallback {
			return fmt.Errorf("設定がフォールバック（%s）を許していない", cfg.Review.Fallback)
		}
		if !cfg.IsFallbackReason(r.FallbackReason) {
			return fmt.Errorf("fallback_reason %q が設定の fallback_reasons %v にない", r.FallbackReason, cfg.Review.FallbackReasons)
		}
		return nil
	default:
		return fmt.Errorf("reviewer %q は認めない", r.Reviewer)
	}
}

// CreatedTime は created_at を時刻にしたもの（ValidateSchema を通っていれば失敗しない）。
func (r *Record) CreatedTime() time.Time {
	t, _ := time.Parse(time.RFC3339, r.CreatedAt)
	return t
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
