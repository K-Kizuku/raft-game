package gate

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strconv"
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
//     一覧（名前・前の名前・状態）でも、正規化した patch-id（PatchID。文脈の行とハンクの区切りを含め、行番号と空白は無視する）でも、
//     ファイルのモード（差分の両端）でも一致する。patch がない・ハンクの形が読めない・モードを確かめられないファイルがあれば引き継がない
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
	if err := sameChanges(m.Files, e.in.Files, m.Modes); err != "" {
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
// 一覧と patch-id とファイルのモードで一致するかを確かめる。一致しなければ理由を返す。
func sameChanges(a, b []File, modes *CarryOverModes) string {
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
		px, err := PatchID(x.Patch)
		if err != nil {
			return fmt.Sprintf("%s の S の patch が読めない（%v）", x.Filename, err)
		}
		py, err := PatchID(y.Patch)
		if err != nil {
			return fmt.Sprintf("%s の HEAD の patch が読めない（%v）", y.Filename, err)
		}
		if px != py {
			return fmt.Sprintf("%s の差分が違う（patch-id が一致しない）", x.Filename)
		}
		if err := sameMode(x, modes); err != "" {
			return err
		}
	}
	return ""
}

// sameMode は、ファイル f のモード（実行権限など）の変更が、S の差分と HEAD の差分で同じかを確かめる。
// 差分の両端（マージベースと先端）のモードがそれぞれ一致すること。確かめられなければ理由を返す。
// f は S と HEAD で一覧（名前・前の名前・状態）が一致したファイル。
func sameMode(f File, modes *CarryOverModes) string {
	if modes == nil {
		return "ファイルのモードの材料がない"
	}
	old := f.Filename
	if f.PreviousFilename != "" {
		old = f.PreviousFilename
	}
	type end struct {
		name     string
		s, head  map[string]string
		path     string
		required bool
	}
	for _, e := range []end{
		{"マージベース", modes.S.Base, modes.Head.Base, old, f.Status != "added"},
		{"先端", modes.S.Tip, modes.Head.Tip, f.Filename, f.Status != "removed"},
	} {
		ms, okS := e.s[e.path]
		mh, okH := e.head[e.path]
		switch {
		case e.required && (!okS || !okH):
			return fmt.Sprintf("%s の%sのモードを確かめられない", e.path, e.name)
		case okS != okH || ms != mh:
			return fmt.Sprintf("%s のモードが違う（%sで S は %q、HEAD は %q）", f.Filename, e.name, ms, mh)
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
// `git patch-id --stable` と同じ考え方で、ハンクの見出しの行番号と空白の違いだけを無視する。
// ハンクの区切りと見出しの残り（関数名など）、文脈の行（先頭が空白）、変更行（+・-）、「\ No newline at end of file」を順に含める
// （変更行が同じでも、変更の場所（文脈）が違えば別の ID になる。git patch-id --stable より厳しい）。
// ハンクの形が読めない（見出しがない・見出しの行数と中身の行数が合わない・知らない形の行がある）ときはエラーを返す。
func PatchID(patch string) (string, error) {
	lines := strings.Split(patch, "\n")
	h := sha256.New()
	inHunk := false
	oldLeft, newLeft := 0, 0
	prevLine := false // 直前がハンクの中身の行（「\」の行を置ける）
	for i, line := range lines {
		if m := hunkHeaderRe.FindStringSubmatch(line); m != nil {
			if inHunk && (oldLeft != 0 || newLeft != 0) {
				return "", fmt.Errorf("%d 行目: 前のハンクの行数が見出しと合わない", i+1)
			}
			oldLeft, newLeft = hunkCount(m[2]), hunkCount(m[4])
			if oldLeft < 0 || newLeft < 0 {
				return "", fmt.Errorf("%d 行目: ハンクの見出しが読めない", i+1)
			}
			inHunk, prevLine = true, false
			h.Write([]byte("@@"))
			h.Write([]byte(removeSpace(m[5])))
			h.Write([]byte{'\n'})
			continue
		}
		if !inHunk {
			return "", fmt.Errorf("%d 行目: ハンクの見出しの前に行がある", i+1)
		}
		if line == "" && oldLeft == 0 && newLeft == 0 && i == len(lines)-1 {
			break // 末尾の改行
		}
		var kind byte
		switch {
		case line == "", line[0] == ' ':
			kind, oldLeft, newLeft = ' ', oldLeft-1, newLeft-1
		case line[0] == '-':
			kind, oldLeft = '-', oldLeft-1
		case line[0] == '+':
			kind, newLeft = '+', newLeft-1
		case line[0] == '\\':
			if !prevLine {
				return "", fmt.Errorf("%d 行目: 「\\」の行の前に中身の行がない", i+1)
			}
			h.Write([]byte("\\\n"))
			prevLine = false
			continue
		default:
			return "", fmt.Errorf("%d 行目: 知らない形の行", i+1)
		}
		if oldLeft < 0 || newLeft < 0 {
			return "", fmt.Errorf("%d 行目: ハンクの行数が見出しより多い", i+1)
		}
		body := ""
		if line != "" {
			body = line[1:]
		}
		h.Write([]byte{kind})
		h.Write([]byte(removeSpace(body)))
		h.Write([]byte{'\n'})
		prevLine = true
	}
	switch {
	case !inHunk:
		return "", fmt.Errorf("ハンクがない")
	case oldLeft != 0 || newLeft != 0:
		return "", fmt.Errorf("最後のハンクの行数が見出しと合わない")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// hunkHeaderRe は、ハンクの見出し（@@ -a[,b] +c[,d] @@ 残り）。
var hunkHeaderRe = regexp.MustCompile(`^@@ -([0-9]+)(?:,([0-9]+))? \+([0-9]+)(?:,([0-9]+))? @@(.*)$`)

// hunkCount は、見出しの行数（省略なら 1）を返す。読めなければ -1。
func hunkCount(s string) int {
	if s == "" {
		return 1
	}
	n, err := strconv.Atoi(s)
	if err != nil || n > 1<<20 {
		return -1
	}
	return n
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
