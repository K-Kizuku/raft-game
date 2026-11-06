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
}

// Config は `.agents/config.json` の内容。
type Config struct {
	Version           int      `json:"version"`
	DefaultBranch     string   `json:"default_branch"`
	BranchPrefix      string   `json:"branch_prefix"`
	MaxWorkers        int      `json:"max_workers"`
	RetryLimit        int      `json:"retry_limit"`
	RequiredChecks    []string `json:"required_checks"`
	Review            Review   `json:"review"`
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
