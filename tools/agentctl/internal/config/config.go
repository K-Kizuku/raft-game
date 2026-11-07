// Package config は `.agents/config.json` を読み込み、検証する。
//
// 設定でセキュリティの確認を無効にできないよう、下限はコードに直接書く。
// 検証に失敗した設定は使わない（fail closed）。
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/issuebody"
)

// DefaultPath は、リポジトリのルートからの設定ファイルの相対パス。
const DefaultPath = ".agents/config.json"

// レビュー役の名前。Merge Gate の C8 はこの 2 つだけを認める。
const (
	ReviewerPrimary  = "codex"
	ReviewerFallback = "claude-fallback"
)

// SelfContext は Merge Gate 自身のコミットステータスのコンテキスト。
const SelfContext = "merge-gate"

// Severities は、レビューの指摘の重大度として使える値。
var Severities = []string{"critical", "high", "medium", "low", "info"}

// MandatoryBlockingSeverities は、blocking_severities に必ず含める重大度。
var MandatoryBlockingSeverities = []string{"critical", "high"}

// Review はレビューに関する設定。
type Review struct {
	Primary            string   `json:"primary"`
	Fallback           string   `json:"fallback"`
	AllowFallback      bool     `json:"allow_fallback"`
	FallbackReasons    []string `json:"fallback_reasons"`
	BlockingSeverities []string `json:"blocking_severities"`
	TrustedAuthors     []string `json:"trusted_authors"`
	// CodexBotAuthors は、Codex の GitHub 連携の結果（要約のコメント・行コメント）として信頼する作者（Issue #82）。
	CodexBotAuthors []string `json:"codex_bot_authors"`
	// CarryOverOnBaseMerge は、HEAD より前の SHA のレビュー記録を、そのあとが既定のブランチの取り込みだけで
	// PR の自分の変更が同じときに引き継ぐこと（Merge Gate の C6。Issue #120）。既定は false。
	CarryOverOnBaseMerge bool `json:"carry_over_on_base_merge"`
	// MaxRounds は、merge-train.sh がレビューを依頼する回数の上限（Issue #240）。この回数以上のレビューを
	// 受けた PR にはもう依頼せず、ci-ok が pass なら「強制マージ待ち」にする。書かない（0）なら DefaultMaxRounds。
	MaxRounds int `json:"max_rounds"`
	// SkipSmallPRs は、小さな PR（変更ファイルがすべて SkipPaths に一致し、保護されたパスを変えず、
	// 追加と削除の行数の合計が SkipMaxLines 以下）のレビューを省くこと（Merge Gate の C5〜C8。Issue #243）。既定は false。
	SkipSmallPRs bool `json:"skip_small_prs"`
	// SkipPaths は、小さな PR の変更ファイルとして認める glob。省略（null）は DefaultSkipPaths。
	// NeverSkipPaths に一致するファイルは、ここに一致しても小さな PR にしない。
	SkipPaths []string `json:"skip_paths,omitempty"`
	// SkipMaxLines は、小さな PR の追加と削除の行数の合計の上限。省略（null）は DefaultSkipMaxLines。
	SkipMaxLines *int `json:"skip_max_lines,omitempty"`
}

// DefaultMaxRounds は review.max_rounds を書かないときの値。
const DefaultMaxRounds = 2

// MaxReviewRounds は review.max_rounds（書かなければ DefaultMaxRounds）を返す。
func (r Review) MaxReviewRounds() int {
	if r.MaxRounds == 0 {
		return DefaultMaxRounds
	}
	return r.MaxRounds
}

// DefaultSkipPaths は、review.skip_paths を省略したときの glob（Issue #243）。
var DefaultSkipPaths = []string{
	"docs/**",
	"**/*_test.go", "**/*.test.ts", "**/*.test.tsx",
	"**/testdata/**", "**/fixtures/**",
}

// NeverSkipPaths は、review.skip_paths に一致しても小さな PR にしないパス（判断の記録。Issue #243）。
// 設定で外せないよう、コードに直接書く。
var NeverSkipPaths = []string{"docs/adr/**"}

// DefaultSkipMaxLines は、review.skip_max_lines を省略したときの上限。
const DefaultSkipMaxLines = 300

// SmallPRSkipPaths は、小さな PR の変更ファイルとして認める glob を返す（省略なら DefaultSkipPaths）。
func (r Review) SmallPRSkipPaths() []string {
	if r.SkipPaths == nil {
		return DefaultSkipPaths
	}
	return r.SkipPaths
}

// SmallPRMaxLines は、小さな PR の追加と削除の行数の合計の上限を返す（省略なら DefaultSkipMaxLines）。
func (r Review) SmallPRMaxLines() int {
	if r.SkipMaxLines == nil {
		return DefaultSkipMaxLines
	}
	return *r.SkipMaxLines
}

// Merge はマージに関する設定（Issue #239）。
type Merge struct {
	// RequireUpToDate は、Merge Gate の C10 でベース（既定のブランチ）より遅れている PR を fail にすること。
	// 省略（null）は true（今までどおり）。false なら、遅れていても C10 は pass にする（遅れの数を detail に書く）。
	// C9（コンフリクト）と C4（CI）の判定は変わらない。
	RequireUpToDate *bool `json:"require_up_to_date"`
}

// Config は `.agents/config.json` の内容。
type Config struct {
	Version        int      `json:"version"`
	DefaultBranch  string   `json:"default_branch"`
	BranchPrefix   string   `json:"branch_prefix"`
	MaxWorkers     int      `json:"max_workers"`
	RetryLimit     int      `json:"retry_limit"`
	RequiredChecks []string `json:"required_checks"`
	Review         Review   `json:"review"`
	// Merge は省略できる（古い設定は今までどおりに扱う）。
	Merge             *Merge   `json:"merge,omitempty"`
	SharedAppendFiles []string `json:"shared_append_files"`
	ProtectedPaths    []string `json:"protected_paths"`
	DevSlots          int      `json:"dev_slots"`
}

// Parse は JSON を読み、検証する。未知のキーはエラーにする。
func Parse(data []byte) (*Config, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var c Config
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("config: JSON を読めない: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("config: JSON の後ろに余計なデータがある")
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// Load はファイルを読み、Parse する。
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	return Parse(data)
}

// Find は dir から親のディレクトリへたどり、最初に見つかった `.agents/config.json` のパスを返す。
func Find(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for {
		p := filepath.Join(abs, filepath.FromSlash(DefaultPath))
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return "", fmt.Errorf("config: %s が見つからない（--config で指定する）", DefaultPath)
		}
		abs = parent
	}
}

// Validate は下限と値の範囲を確かめる。違反をすべてまとめて返す。
func (c *Config) Validate() error {
	if c == nil {
		return errors.New("config: 設定がない")
	}
	var errs []string
	add := func(format string, a ...any) { errs = append(errs, fmt.Sprintf(format, a...)) }

	if c.Version != 1 {
		add("version は 1 にする（%d）", c.Version)
	}
	if strings.TrimSpace(c.DefaultBranch) == "" {
		add("default_branch が空")
	}
	if strings.TrimSpace(c.BranchPrefix) == "" {
		add("branch_prefix が空")
	}
	checkRange := func(name string, v, lo, hi int) {
		if v < lo || v > hi {
			add("%s は %d〜%d にする（%d）", name, lo, hi, v)
		}
	}
	checkRange("max_workers", c.MaxWorkers, 1, 8)
	checkRange("retry_limit", c.RetryLimit, 1, 5)
	checkRange("dev_slots", c.DevSlots, 1, 20)

	if len(c.RequiredChecks) == 0 {
		add("required_checks は 1 件以上にする")
	}
	for _, rc := range c.RequiredChecks {
		if strings.TrimSpace(rc) == "" {
			add("required_checks に空の名前がある")
		}
		if rc == SelfContext {
			add("required_checks に %q 自身を含めない", SelfContext)
		}
	}

	r := c.Review
	if r.Primary != ReviewerPrimary {
		add("review.primary は %q にする（%q）", ReviewerPrimary, r.Primary)
	}
	if r.Fallback != ReviewerFallback {
		add("review.fallback は %q にする（%q）", ReviewerFallback, r.Fallback)
	}
	for _, fr := range r.FallbackReasons {
		if strings.TrimSpace(fr) == "" {
			add("review.fallback_reasons に空の値がある")
		}
	}
	for _, s := range r.BlockingSeverities {
		if !contains(Severities, s) {
			add("review.blocking_severities に未知の重大度 %q がある", s)
		}
	}
	for _, m := range MandatoryBlockingSeverities {
		if !contains(r.BlockingSeverities, m) {
			add("review.blocking_severities は %q を含める", m)
		}
	}
	if len(r.TrustedAuthors) == 0 {
		add("review.trusted_authors は 1 件以上にする")
	}
	for _, a := range r.TrustedAuthors {
		if strings.TrimSpace(a) == "" {
			add("review.trusted_authors に空の値がある")
		}
	}
	if len(r.CodexBotAuthors) == 0 {
		add("review.codex_bot_authors は 1 件以上にする")
	}
	for _, a := range r.CodexBotAuthors {
		if strings.TrimSpace(a) == "" {
			add("review.codex_bot_authors に空の値がある")
		}
	}
	if r.MaxRounds != 0 {
		checkRange("review.max_rounds", r.MaxRounds, 1, 10)
	}

	for _, p := range r.SkipPaths {
		if err := issuebody.ValidatePattern(p); err != nil {
			add("review.skip_paths の %q: %v", p, err)
		}
	}
	if r.SkipMaxLines != nil && *r.SkipMaxLines < 0 {
		add("review.skip_max_lines は 0 以上にする（%d）", *r.SkipMaxLines)
	}

	for _, p := range c.ProtectedPaths {
		if err := issuebody.ValidatePattern(p); err != nil {
			add("protected_paths の %q: %v", p, err)
		}
	}
	for _, p := range c.SharedAppendFiles {
		if strings.TrimSpace(p) == "" {
			add("shared_append_files に空の値がある")
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("config: 設定が不正: %s", strings.Join(errs, "; "))
	}
	return nil
}

// RequireUpToDate は、Merge Gate の C10 でベースより遅れている PR を fail にするかを返す。
// 設定がない（merge・require_up_to_date の省略）ときは true。
func (c *Config) RequireUpToDate() bool {
	if c == nil || c.Merge == nil || c.Merge.RequireUpToDate == nil {
		return true
	}
	return *c.Merge.RequireUpToDate
}

// IsBlocking は、重大度が「重大な指摘」に当たるかを返す。
func (c *Config) IsBlocking(severity string) bool {
	return contains(c.Review.BlockingSeverities, severity)
}

// IsTrustedAuthor は、login が信頼する作者かを返す（GitHub の login は大文字と小文字を区別しない）。
func (c *Config) IsTrustedAuthor(login string) bool {
	for _, a := range c.Review.TrustedAuthors {
		if strings.EqualFold(a, login) {
			return true
		}
	}
	return false
}

// IsCodexBot は、login が Codex の GitHub 連携の結果として信頼する作者かを返す（大文字と小文字を区別しない）。
func (c *Config) IsCodexBot(login string) bool {
	for _, a := range c.Review.CodexBotAuthors {
		if strings.EqualFold(a, login) {
			return true
		}
	}
	return false
}

// IsFallbackReason は、reason が設定の fallback_reasons に含まれるかを返す。
func (c *Config) IsFallbackReason(reason string) bool {
	return reason != "" && contains(c.Review.FallbackReasons, reason)
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
