// Package codex は、Codex の GitHub 連携（`@codex review`）が PR に書く出力を読む（Issue #82）。
//
// Codex は次のものを書く（作者は設定の review.codex_bot_authors。既定は chatgpt-codex-connector[bot]）:
//
//   - 要約のコメント（Issue のコメント）: SummaryMarker と、
//     `<!-- codex-review-state:v1 {"code_review":{"commit_sha":"…","status":"completed",…},…} -->`。
//     新しいレビューのたびに同じコメントが更新される
//   - 指摘があるとき: PR のレビュー（本文に ReceiptMarker）と、行コメント
//     （本文に `![P2 Badge](…)` のような重大度のバッジと FindingMarker）
//   - 指摘がないとき: Issue のコメント（本文に ReceiptMarker と「Didn't find any major issues」）
//
// このパッケージは本文の解析だけをする。どの作者を信頼するか・どの SHA のものかは呼び出し側が決める。
package codex

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// 目印。
const (
	SummaryMarker = "<!-- codex-pull-request-review-summary -->"
	ReceiptMarker = "<!-- codex-review-receipt:v1 -->"
	FindingMarker = "<!-- codex-code-review-finding:v1 -->"
	// StateTag は、状態の JSON を持つコメントの目印の先頭。
	StateTag = "<!-- codex-review-state:v1"
)

// StatusCompleted は、レビューが終わったことを表す code_review.status の値。
const StatusCompleted = "completed"

// ReviewerVersion は、Codex の GitHub 連携のレビューを agent-review/v1 に変換したときの reviewer_version。
const ReviewerVersion = "chatgpt-codex-connector (GitHub)"

// CodeReview は codex-review-state の code_review。
type CodeReview struct {
	CommitSHA             string `json:"commit_sha"`
	HasAnyFindings        bool   `json:"has_any_findings"`
	HasUnresolvedFindings bool   `json:"has_unresolved_findings"`
	Status                string `json:"status"`
}

// State は codex-review-state:v1 の JSON。
type State struct {
	CodeReview     *CodeReview     `json:"code_review"`
	SecurityReview json.RawMessage `json:"security_review,omitempty"`
}

// ErrNoState は、本文に codex-review-state がないことを表す。
var ErrNoState = errors.New("codex: codex-review-state がない")

var sha40Re = regexp.MustCompile(`^[0-9a-f]{40}$`)

// HasState は、本文に codex-review-state の目印があるかを返す。
func HasState(body string) bool { return strings.Contains(body, StateTag) }

// ParseState は本文から codex-review-state を読む。
// 目印がなければ ErrNoState。目印が複数ある・JSON が壊れている・code_review がない・
// completed なのに commit_sha が 40 桁の 16 進ではないときはエラー。
func ParseState(body string) (*State, error) {
	n := strings.Count(body, StateTag)
	if n == 0 {
		return nil, ErrNoState
	}
	if n > 1 {
		return nil, fmt.Errorf("codex: codex-review-state が %d 個ある", n)
	}
	rest := body[strings.Index(body, StateTag)+len(StateTag):]
	end := strings.Index(rest, "-->")
	if end < 0 {
		return nil, errors.New("codex: codex-review-state が閉じていない")
	}
	raw := strings.TrimSpace(rest[:end])
	var st State
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		return nil, fmt.Errorf("codex: codex-review-state の JSON を読めない: %w", err)
	}
	if st.CodeReview == nil {
		return nil, errors.New("codex: codex-review-state に code_review がない")
	}
	st.CodeReview.CommitSHA = strings.ToLower(strings.TrimSpace(st.CodeReview.CommitSHA))
	if st.CodeReview.Status == StatusCompleted && !sha40Re.MatchString(st.CodeReview.CommitSHA) {
		return nil, fmt.Errorf("codex: code_review.commit_sha が 40 桁の 16 進ではない（%q）", st.CodeReview.CommitSHA)
	}
	return &st, nil
}

// IsFinding は、本文が Codex の指摘（行コメント）かを返す。
func IsFinding(body string) bool { return strings.Contains(body, FindingMarker) }

var (
	badgeAltRe = regexp.MustCompile(`!\[P([0-3]) Badge\]`)
	badgeURLRe = regexp.MustCompile(`/badge/P([0-3])-`)
)

// Priorities は、Codex の重大度のバッジ（P0〜P3）と agent-review/v1 の重大度の対応。
var Priorities = map[string]string{"P0": "critical", "P1": "high", "P2": "medium", "P3": "low"}

// Priority は本文の重大度のバッジ（P0〜P3）を返す。見つからなければ ""。
func Priority(body string) string {
	if m := badgeAltRe.FindStringSubmatch(body); m != nil {
		return "P" + m[1]
	}
	if m := badgeURLRe.FindStringSubmatch(body); m != nil {
		return "P" + m[1]
	}
	return ""
}

// Severity は本文のバッジを agent-review/v1 の重大度にする。バッジがなければ ok=false。
func Severity(body string) (severity string, ok bool) {
	s, ok := Priorities[Priority(body)]
	return s, ok
}

var (
	imageRe = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
	tagRe   = regexp.MustCompile(`</?sub>`)
)

// Title は指摘の本文の 1 行目（バッジ・<sub>・** を除いたもの）を返す。
func Title(body string) string {
	for _, line := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		t := imageRe.ReplaceAllString(line, "")
		t = tagRe.ReplaceAllString(t, "")
		t = strings.TrimSpace(strings.ReplaceAll(t, "**", ""))
		if t != "" && !strings.HasPrefix(t, "<!--") {
			return t
		}
	}
	return ""
}

// Detail は指摘の本文から、1 行目・目印・リアクションの案内を除いたものを返す（最大 max 文字）。
func Detail(body string, max int) string {
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	var out []string
	skippedTitle := false
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if !skippedTitle {
			if Title(line) != "" {
				skippedTitle = true
			}
			continue
		}
		if strings.HasPrefix(t, "<!--") || strings.HasPrefix(t, "Useful? React with") {
			continue
		}
		out = append(out, line)
	}
	d := strings.TrimSpace(strings.Join(out, "\n"))
	if r := []rune(d); max > 0 && len(r) > max {
		d = string(r[:max]) + "…"
	}
	return d
}
