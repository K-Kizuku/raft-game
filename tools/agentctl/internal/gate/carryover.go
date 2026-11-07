package gate

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/config"
)

// レビュー記録の引き継ぎ（Issue #120）
//
// main の取り込みで HEAD の SHA が変わっても、PR の自分の変更が前回レビューした SHA（S）と同じなら、
// S の最新のレビューを HEAD のものとして使う（C6・C7・C12）。引き継ぐのは、次をすべて確かめられたときだけ。
// 少しでも確かめられなければ引き継がない（今までどおり HEAD のレビューを求める。C6 は pending）。
//
//  1. HEAD から最初の親をたどると S に着き、その間のコミットはすべてマージコミットで、
//     2 番目以降の親はベースのブランチの履歴に含まれる（S のあとに PR の作者の変更のコミットがない）
//  2. マージベースから S までの変更ファイルと、マージベースから HEAD までの変更ファイルが、
//     一覧（名前・前の名前・状態）でも、正規化した patch-id（PatchID）でも一致する。patch がないファイルがあれば引き継がない
//  3. S の最新のレビューが approve で、C7（重大な指摘が残っていない）と C8 を満たす
//  4. PR が保護されたパスを変えていない
//  5. 設定 review.carry_over_on_base_merge が true

// MaxCarryOverCommits は、HEAD から S までにたどるコミットの上限。超えたら引き継がない。
const MaxCarryOverCommits = 20

// MaxCompareFiles は、GET /compare が返す変更ファイルの上限。この数に達したら一覧が不完全かもしれないので引き継がない。
const MaxCompareFiles = 300

// CarryOver は、HEAD より前の SHA の最新のレビューを引き継ぐかどうかの判定の結果。
type CarryOver struct {
	// OK は、引き継ぐこと。
	OK bool `json:"ok"`
	// SHA は、引き継ぎの候補（最新のレビューの SHA）。レビューがなければ空。
	SHA string `json:"sha"`
	// Round は、最新のレビューが agent-review/v1 の記録のときの round（Codex の GitHub 連携なら 0）。
	Round int `json:"round,omitempty"`
	// Codex は、最新のレビューが Codex の GitHub 連携のものであること。
	Codex bool `json:"codex,omitempty"`
	// Reason は、引き継がない理由。
	Reason string `json:"reason,omitempty"`
}

// CarriesOver は、PR の最新のレビューを HEAD に引き継げるかを、Merge Gate と同じ条件で判定する（`agentctl recover`）。
func CarriesOver(cfg *config.Config, in Input) CarryOver {
	if cfg == nil || cfg.Validate() != nil {
		return CarryOver{Reason: "設定が不正"}
	}
	e := &evaluator{cfg: cfg, in: in}
	e.collectReviews()
	e.collectCodex()
	return e.carryOver()
}

// CarryOverCandidate は、引き継ぎの判定の材料（Input.CarryOver）を集めるべき SHA を返す。
// 設定が false・最新のレビューが HEAD のもの・レビューがない・保護されたパスを変える PR なら空（集めない）。
func CarryOverCandidate(cfg *config.Config, in Input) string {
	if cfg == nil || cfg.Validate() != nil || !cfg.Review.CarryOverOnBaseMerge {
		return ""
	}
	e := &evaluator{cfg: cfg, in: in}
	e.collectReviews()
	e.collectCodex()
	sha := e.latestSHA()
	if sha == "" || sha == in.PullRequest.HeadSHA || !sha40Re.MatchString(sha) || len(ProtectedFiles(cfg, in.Files)) > 0 {
		return ""
	}
	return sha
}

// latestSHA は、最新のレビュー（Codex の GitHub 連携か agent-review/v1 の記録）の SHA を返す（なければ空）。
func (e *evaluator) latestSHA() string {
	switch {
	case e.codexLatest:
		return e.codex.SHA
	case e.latest != nil:
		return e.latest.rec.HeadSHA
	}
	return ""
}

// carryOver は、最新のレビューを HEAD に引き継げるかを判定する。collectReviews・collectCodex のあとに呼ぶ。
func (e *evaluator) carryOver() CarryOver {
	pr := e.in.PullRequest
	co := CarryOver{SHA: e.latestSHA(), Codex: e.codexLatest}
	no := func(format string, a ...any) CarryOver {
		co.Reason = fmt.Sprintf(format, a...)
		return co
	}
	switch {
	case co.SHA == "":
		return no("%s", noReview)
	case co.SHA == pr.HeadSHA:
		return no("最新のレビューは HEAD のもの")
	}
	if !e.codexLatest {
		co.Round = e.latest.rec.Round
	}

	// 条件 5
	if !e.cfg.Review.CarryOverOnBaseMerge {
		return no("設定 review.carry_over_on_base_merge が false")
	}
	// 条件 3
	if e.codexLatest {
		if n := len(e.codex.Blocking(e.cfg)); n > 0 {
			return no("最新のレビュー（Codex の GitHub 連携）に重大な指摘が %d 件ある", n)
		}
	} else if v := e.latest.rec.Verdict; v != "approve" {
		return no("最新の記録（round %d）の verdict が %s", co.Round, v)
	}
	if v, d := e.c7Eval(); v != Pass {
		return no("C7 を満たさない（%s）", d)
	}
	if v, d := e.c8Eval(); v != Pass {
		return no("C8 を満たさない（%s）", d)
	}
	// 条件 4
	if hit := ProtectedFiles(e.cfg, e.in.Files); len(hit) > 0 {
		return no("保護されたパスを変える（%s）", strings.Join(hit, ", "))
	}

	m := e.in.CarryOver
	switch {
	case m == nil:
		return no("判定の材料がない")
	case m.Error != "":
		return no("判定の材料を集められなかった（%s）", m.Error)
	case m.SHA != co.SHA:
		return no("判定の材料の SHA %s が、最新のレビューの SHA %s と違う", short(m.SHA), short(co.SHA))
	case !sha40Re.MatchString(co.SHA) || !sha40Re.MatchString(pr.HeadSHA):
		return no("SHA が不正")
	}
	// 条件 1
	if err := checkMergeChain(pr.HeadSHA, co.SHA, m.Commits, m.InBase, e.cfg.DefaultBranch); err != "" {
		return no("%s", err)
	}
	// 条件 2
	if pr.ChangedFiles != len(e.in.Files) {
		return no("HEAD の変更ファイルの一覧が不完全（%d 件中 %d 件）", pr.ChangedFiles, len(e.in.Files))
	}
	if hit := ProtectedFiles(e.cfg, m.Files); len(hit) > 0 {
		return no("%s で保護されたパスを変えている（%s）", short(co.SHA), strings.Join(hit, ", "))
	}
	if err := sameChanges(m.Files, e.in.Files); err != "" {
		return no("%s", err)
	}
	co.OK = true
	return co
}

// checkMergeChain は、HEAD から最初の親をたどって s に着き、その間がすべてマージコミットで、
// 2 番目以降の親がベースの履歴に含まれるかを確かめる。満たさなければ理由を返す。
func checkMergeChain(head, s string, commits []Commit, inBase map[string]bool, base string) string {
	if len(commits) == 0 {
		return "HEAD から最初の親をたどったコミットがない"
	}
	if len(commits) > MaxCarryOverCommits {
		return fmt.Sprintf("HEAD から %s までのコミットが多すぎる（%d 個より多い）", short(s), MaxCarryOverCommits)
	}
	want := head
	for _, c := range commits {
		if c.SHA != want {
			return fmt.Sprintf("コミット %s が、HEAD から最初の親をたどった %s ではない", short(c.SHA), short(want))
		}
		if len(c.Parents) < 2 {
			return fmt.Sprintf("%s のあとに、マージではないコミット %s がある（PR の作者の変更）", short(s), short(c.SHA))
		}
		for _, p := range c.Parents[1:] {
			if !inBase[p] {
				return fmt.Sprintf("マージコミット %s の親 %s が %s の履歴にない", short(c.SHA), short(p), base)
			}
		}
		want = c.Parents[0]
	}
	if want != s {
		return fmt.Sprintf("HEAD から最初の親をたどっても %s に着かない（%s で止まった）", short(s), short(want))
	}
	return ""
}

// sameChanges は、マージベースから S までの変更ファイル a と、HEAD までの変更ファイル b が、
// 一覧と patch-id で一致するかを確かめる。一致しなければ理由を返す。
func sameChanges(a, b []File) string {
	if len(a) >= MaxCompareFiles {
		return fmt.Sprintf("S の変更ファイルが %d 件以上（一覧が不完全かもしれない）", MaxCompareFiles)
	}
	if len(a) != len(b) {
		return fmt.Sprintf("変更ファイルの数が違う（S %d 件、HEAD %d 件）", len(a), len(b))
	}
	sa, sb := sortedFiles(a), sortedFiles(b)
	for i := range sa {
		x, y := sa[i], sb[i]
		if i > 0 && (sa[i-1].Filename == x.Filename || sb[i-1].Filename == y.Filename) {
			return fmt.Sprintf("変更ファイル %s が重複している", x.Filename)
		}
		if x.Filename != y.Filename || x.PreviousFilename != y.PreviousFilename || x.Status != y.Status {
			return fmt.Sprintf("変更ファイルの一覧が違う（S は %s、HEAD は %s）", describeFile(x), describeFile(y))
		}
		if x.Patch == "" || y.Patch == "" {
			return fmt.Sprintf("%s の patch がない（大きすぎる・バイナリ・中身の変更がない）", x.Filename)
		}
		if PatchID(x.Patch) != PatchID(y.Patch) {
			return fmt.Sprintf("%s の差分が違う（patch-id が一致しない）", x.Filename)
		}
	}
	return ""
}

func sortedFiles(fs []File) []File {
	out := append([]File(nil), fs...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Filename < out[j].Filename })
	return out
}

func describeFile(f File) string {
	s := f.Filename + "（" + f.Status + "）"
	if f.PreviousFilename != "" {
		s = f.PreviousFilename + " → " + s
	}
	return s
}

// PatchID は、差分（GitHub の files[].patch）の正規化した patch-id を返す。
// `git patch-id --stable` と同じ考え方で、ハンクの見出し（@@ の行番号）と空白の違いを無視し、変更行の中身で比べる。
// 文脈の行（先頭が空白）は含めない（main の取り込みで文脈が変わっても、変更行が同じなら同じ ID になる）。
// 変更行の直後の「\ No newline at end of file」は含める。
func PatchID(patch string) string {
	h := sha256.New()
	afterChange := false
	for _, line := range strings.Split(patch, "\n") {
		switch {
		case strings.HasPrefix(line, "@@"):
			afterChange = false
		case strings.HasPrefix(line, "+"), strings.HasPrefix(line, "-"):
			h.Write([]byte{line[0]})
			h.Write([]byte(removeSpace(line[1:])))
			h.Write([]byte{'\n'})
			afterChange = true
		case strings.HasPrefix(line, `\`):
			if afterChange {
				h.Write([]byte("\\\n"))
			}
		default:
			afterChange = false
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// removeSpace は、空白（git の isspace と同じ ASCII の空白）をすべて除く。
func removeSpace(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\n', '\v', '\f', '\r':
			return -1
		}
		return r
	}, s)
}
