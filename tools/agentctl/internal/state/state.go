// Package state は、Issue の状態（`status:*` のラベル）の遷移表と、状態の変化の記録（Issue のコメント）を扱う。
//
// 記録のコメントは、目印と JSON を 1 行で含む:
//
//	<!-- agent-state:v1 {"from":"ready","to":"in-progress","actor":"orchestrator","worker":"issue-12",
//	     "branch":"agent/issue-12","session":"<uuid>","attempt":1,"cause":"","reason":"…","pr":0,"at":"RFC 3339"} -->
//
// コメントの残りには、人が読める 1〜2 行の説明を書く。
package state

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/snapshot"
)

// Status は Issue の状態。
type Status string

// 状態。
const (
	Backlog          Status = "backlog"
	Ready            Status = "ready"
	InProgress       Status = "in-progress"
	InReview         Status = "in-review"
	ChangesRequested Status = "changes-requested"
	CIPending        Status = "ci-pending"
	ReadyToMerge     Status = "ready-to-merge"
	Merged           Status = "merged"
	Blocked          Status = "blocked"
)

// All はすべての状態。
var All = []Status{Backlog, Ready, InProgress, InReview, ChangesRequested, CIPending, ReadyToMerge, Merged, Blocked}

// Working は、実装中（作業ブランチや PR がある、またはこれから作る）の状態。
var Working = []Status{InProgress, InReview, ChangesRequested, CIPending, ReadyToMerge}

// NeedsHumanLabel は、人の判断が必要な Issue に付けるラベル。
const NeedsHumanLabel = "needs-human"

// transitions は許す遷移。これ以外は拒否する。
var transitions = map[Status][]Status{
	Backlog:          {Ready, Blocked},
	Ready:            {InProgress, Backlog, Blocked},
	InProgress:       {InReview, Ready, Blocked},
	InReview:         {ChangesRequested, CIPending, ReadyToMerge, Blocked},
	ChangesRequested: {InReview, Blocked},
	CIPending:        {ReadyToMerge, ChangesRequested, InReview, Blocked},
	ReadyToMerge:     {Merged, InReview, ChangesRequested, Blocked},
	Blocked:          {Ready, Backlog, InProgress},
	Merged:           {},
}

// Label はラベルの名前（`status:…`）。
func (s Status) Label() string { return snapshot.StatusLabelPrefix + string(s) }

// Valid は既知の状態かを返す。
func (s Status) Valid() bool {
	_, ok := transitions[s]
	return ok
}

// IsWorking は実装中の状態かを返す。
func (s Status) IsWorking() bool {
	for _, w := range Working {
		if s == w {
			return true
		}
	}
	return false
}

// Parse は `in-progress` か `status:in-progress` を状態にする。
func Parse(s string) (Status, error) {
	st := Status(strings.TrimPrefix(s, snapshot.StatusLabelPrefix))
	if !st.Valid() {
		return "", fmt.Errorf("state: 未知の状態 %q", s)
	}
	return st, nil
}

// CanTransition は from から to への遷移を許すかを返す。
func CanTransition(from, to Status) bool {
	for _, t := range transitions[from] {
		if t == to {
			return true
		}
	}
	return false
}

// Allowed は from から遷移できる状態を返す。
func Allowed(from Status) []Status { return append([]Status(nil), transitions[from]...) }

// ErrLabels は、`status:*` のラベルがちょうど 1 つではないこと（または未知の状態）を表す。
var ErrLabels = errors.New("state: status:* のラベルがちょうど 1 つではない")

// Current は Issue の今の状態を返す。`status:*` がちょうど 1 つで既知の状態でなければ ErrLabels。
func Current(is *snapshot.Issue) (Status, error) {
	ls := is.StatusLabels()
	if len(ls) != 1 {
		return "", fmt.Errorf("%w（#%d: %v）", ErrLabels, is.Number, ls)
	}
	st, err := Parse(ls[0])
	if err != nil {
		return "", fmt.Errorf("%w（#%d: %v）", ErrLabels, is.Number, err)
	}
	return st, nil
}

// Marker は記録のコメントの目印。
const Marker = "agent-state:v1"

// Record は状態の変化（または再試行・再開）の記録。from と to が同じなら、状態を変えない記録。
type Record struct {
	From    Status `json:"from"`
	To      Status `json:"to"`
	Actor   string `json:"actor"`
	Worker  string `json:"worker"`
	Branch  string `json:"branch"`
	Session string `json:"session"`
	Attempt int    `json:"attempt"`
	Cause   string `json:"cause"`
	Reason  string `json:"reason"`
	PR      int    `json:"pr"`
	At      string `json:"at"`
}

// FormatTime は記録の at の書式（RFC 3339、UTC）。
func FormatTime(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// Validate は記録の値を確かめる。
func (r *Record) Validate() error {
	var errs []string
	if !r.From.Valid() {
		errs = append(errs, fmt.Sprintf("from が不正（%q）", r.From))
	}
	if !r.To.Valid() {
		errs = append(errs, fmt.Sprintf("to が不正（%q）", r.To))
	}
	if r.Attempt < 0 {
		errs = append(errs, "attempt は 0 以上にする")
	}
	if r.PR < 0 {
		errs = append(errs, "pr は 0 以上にする")
	}
	if _, err := time.Parse(time.RFC3339, r.At); err != nil {
		errs = append(errs, fmt.Sprintf("at が RFC 3339 ではない（%q）", r.At))
	}
	if len(errs) > 0 {
		return fmt.Errorf("state: 記録が不正: %s", strings.Join(errs, "; "))
	}
	return nil
}

// Marshal は記録を目印つきの 1 行（`<!-- agent-state:v1 {…} -->`）にする。
// JSON の `<`・`>`・`&` はエスケープされるので、JSON の中に `-->` は現れない。
func (r *Record) Marshal() string {
	b, _ := json.Marshal(r)
	return "<!-- " + Marker + " " + string(b) + " -->"
}

// Comment はコメントの本文（目印の行と、人が読める説明）。
func (r *Record) Comment() string {
	var b strings.Builder
	b.WriteString(r.Marshal())
	b.WriteString("\n")
	if r.From == r.To {
		fmt.Fprintf(&b, "状態は `%s` のまま。", r.To)
	} else {
		fmt.Fprintf(&b, "状態を `%s` から `%s` に変えた。", r.From, r.To)
	}
	var meta []string
	if r.Worker != "" {
		meta = append(meta, "worker: "+r.Worker)
	}
	if r.Attempt > 0 {
		meta = append(meta, fmt.Sprintf("attempt: %d", r.Attempt))
	}
	if r.Cause != "" {
		meta = append(meta, "cause: "+r.Cause)
	}
	if r.PR > 0 {
		meta = append(meta, fmt.Sprintf("PR: #%d", r.PR))
	}
	if len(meta) > 0 {
		b.WriteString("（" + strings.Join(meta, "、") + "）")
	}
	if r.Reason != "" {
		b.WriteString("\n理由: " + strings.ReplaceAll(r.Reason, "\n", " "))
	}
	b.WriteString("\n")
	return b.String()
}

var recordRe = regexp.MustCompile(`<!-- ` + regexp.QuoteMeta(Marker) + `[ \t]+(.*?)[ \t]+-->`)

// ErrNoRecord は、コメントが記録ではない（目印がない）ことを表す。
var ErrNoRecord = errors.New("state: 目印がない")

// ParseComment はコメントの本文から記録を取り出す。目印がなければ ErrNoRecord、壊れていればそのほかのエラー。
func ParseComment(body string) (*Record, error) {
	n := strings.Count(body, "<!-- "+Marker)
	if n == 0 {
		return nil, ErrNoRecord
	}
	if n > 1 {
		return nil, fmt.Errorf("state: 目印が %d 個ある", n)
	}
	m := recordRe.FindStringSubmatch(body)
	if m == nil {
		return nil, errors.New("state: 目印の行が閉じていない")
	}
	dec := json.NewDecoder(bytes.NewReader([]byte(m[1])))
	dec.DisallowUnknownFields()
	var r Record
	if err := dec.Decode(&r); err != nil {
		return nil, fmt.Errorf("state: JSON を読めない: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("state: JSON の後ろに余計なデータがある")
	}
	if err := r.Validate(); err != nil {
		return nil, err
	}
	return &r, nil
}

// History は 1 つの Issue の記録（古い順）。
type History struct {
	Records []Record
}

// ReadHistory は Issue のコメントから記録を集める。trusted が false を返す作者のコメントと、
// 壊れた記録は無視し、警告を返す。
func ReadHistory(is *snapshot.Issue, trusted func(login string) bool) (History, []string) {
	var h History
	var warns []string
	for i, c := range is.Comments {
		r, err := ParseComment(c.Body)
		if errors.Is(err, ErrNoRecord) {
			continue
		}
		if !trusted(c.Author) {
			warns = append(warns, fmt.Sprintf("#%d のコメント %d: 信頼しない作者 %q の状態の記録を無視する", is.Number, i+1, c.Author))
			continue
		}
		if err != nil {
			warns = append(warns, fmt.Sprintf("#%d のコメント %d: 壊れた状態の記録を無視する: %v", is.Number, i+1, err))
			continue
		}
		h.Records = append(h.Records, *r)
	}
	return h, warns
}

// LatestClaim は、最後の着手（to が in-progress の記録。再開も含む）を返す。なければ nil。
func (h History) LatestClaim() *Record {
	for i := len(h.Records) - 1; i >= 0; i-- {
		if h.Records[i].To == InProgress {
			return &h.Records[i]
		}
	}
	return nil
}

// MaxAttempt は記録の attempt の最大値（着手の回数）。
func (h History) MaxAttempt() int {
	m := 0
	for _, r := range h.Records {
		if r.Attempt > m {
			m = r.Attempt
		}
	}
	return m
}

// CauseCount は、cause の記録の数を返す。人が blocked から戻した（from が blocked で to が blocked 以外の）
// 記録があれば、それより後だけを数える。
func (h History) CauseCount(cause string) int {
	n := 0
	for _, r := range h.Records {
		if r.From == Blocked && r.To != Blocked {
			n = 0
			continue
		}
		if r.Cause == cause {
			n++
		}
	}
	return n
}
