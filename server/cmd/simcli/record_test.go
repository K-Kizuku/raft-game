package main

import (
	"crypto/sha256"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// parsedRecord は記録を読んだもの（テストで見るキーだけ）。
type parsedRecord struct {
	Format     int    `json:"format"`
	Kind       string `json:"kind"`
	Build      string `json:"build"`
	StageID    string `json:"stageId"`
	StageHash  string `json:"stageHash"`
	Seed       uint64 `json:"seed"`
	Bot        string `json:"bot"`
	DurationMs int64  `json:"durationMs"`
	EventCount int    `json:"eventCount"`
	TraceHash  string `json:"traceHash"`
	Snapshot   struct {
		Phase   string `json:"phase"`
		T       int64  `json:"t"`
		LastSeq uint64 `json:"lastSeq"`
	} `json:"snapshot"`
	Events []struct {
		Seq   uint64 `json:"seq"`
		T     int64  `json:"t"`
		Type  string `json:"type"`
		Phase string `json:"phase"`
	} `json:"events"`
}

// recordArgs は record の引数（--data testdata --stage tiny --bot greedy --seed 1）。
func recordArgs(extra ...string) []string {
	return append([]string{"record", "--data", testData, "--stage", testStage, "--bot", "greedy", "--seed", "1"}, extra...)
}

// recordOK は record を呼び、成功を確かめて、標準出力と読んだ記録を返す。
func recordOK(t *testing.T, extra ...string) (cliResult, parsedRecord) {
	t.Helper()
	r := runCLI(t, nil, recordArgs(extra...)...)
	r.wantCode(t, exitOK)
	if !json.Valid([]byte(r.stdout)) {
		t.Fatalf("記録が JSON でない:\n%s", r.stdout)
	}
	var rec parsedRecord
	if err := json.Unmarshal([]byte(r.stdout), &rec); err != nil {
		t.Fatal(err)
	}
	return r, rec
}

// RC-1・RC-2・RC-3: ヘッダー・スナップショット・イベントの形。
func TestRecordShape(t *testing.T) {
	t.Parallel()
	r, rec := recordOK(t)
	st, err := loadStage(testData, testStage)
	if err != nil {
		t.Fatal(err)
	}
	// RC-1
	if rec.Format != 1 || rec.Kind != "simcli.record" || rec.StageID != testStage || rec.StageHash != st.Hash ||
		rec.Seed != 1 || rec.Bot != "greedy" || rec.DurationMs != testDurationMs || rec.Build == "" {
		t.Errorf("header = %+v", rec)
	}
	if rec.EventCount != len(rec.Events) || rec.EventCount == 0 {
		t.Errorf("eventCount = %d, events = %d", rec.EventCount, len(rec.Events))
	}
	if !traceHashRE.MatchString(rec.TraceHash) {
		t.Errorf("traceHash = %q", rec.TraceHash)
	}
	// 1 行目がヘッダーで、キーの順が決まっている。
	lines := strings.Split(r.stdout, "\n")
	if want := `{"format":1,"kind":"simcli.record","build":`; !strings.HasPrefix(lines[0], want) ||
		!strings.HasSuffix(lines[0], `"traceHash":"`+rec.TraceHash+`",`) {
		t.Errorf("1 行目 = %s", lines[0])
	}
	if !strings.HasPrefix(lines[1], `"snapshot":{`) || !strings.HasSuffix(lines[1], "},") || lines[2] != `"events":[` {
		t.Errorf("2・3 行目 = %s / %s", lines[1], lines[2])
	}
	if !strings.HasSuffix(r.stdout, "\n]}\n") || strings.HasSuffix(r.stdout, "\n\n") {
		t.Errorf("末尾 = %q", r.stdout[len(r.stdout)-10:])
	}
	if want := "simcli record: " + testStage + " bot=greedy seed=1 events="; !strings.Contains(r.stderr, want) ||
		!strings.Contains(r.stderr, "traceHash="+rec.TraceHash) {
		t.Errorf("stderr = %s", r.stderr)
	}
	// RC-2
	if rec.Snapshot.Phase != "intro" || rec.Snapshot.LastSeq != 0 || rec.Snapshot.T != 0 {
		t.Errorf("snapshot = %+v", rec.Snapshot)
	}
	// RC-3
	var prevT int64
	for k, ev := range rec.Events {
		if ev.Seq != uint64(k+1) {
			t.Fatalf("events[%d].seq = %d", k, ev.Seq)
		}
		if ev.T < prevT {
			t.Fatalf("events[%d].t = %d < %d", k, ev.T, prevT)
		}
		prevT = ev.T
	}
	first, last := rec.Events[0], rec.Events[len(rec.Events)-1]
	if first.Type != "phase.changed" || first.Phase != "opening" {
		t.Errorf("first = %+v", first)
	}
	if last.Type != "phase.changed" || last.Phase != "ended" || last.T != testDurationMs {
		t.Errorf("last = %+v", last)
	}
}

// RC-4: §10.2。イベントの各行（末尾の , を除く）に改行を付けて連結した SHA-256 が traceHash。
func TestRecordTraceHash(t *testing.T) {
	t.Parallel()
	r, rec := recordOK(t)
	lines := strings.Split(r.stdout, "\n")
	start, end := -1, -1
	for i, l := range lines {
		if l == `"events":[` && start < 0 {
			start = i + 1
		}
		if l == "]}" {
			end = i
		}
	}
	if start < 0 || end < start {
		t.Fatalf("events の範囲が見つからない: %d %d", start, end)
	}
	h := sha256.New()
	for _, l := range lines[start:end] {
		h.Write([]byte(strings.TrimSuffix(l, ",")))
		h.Write([]byte{'\n'})
	}
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	if got := formatHash(sum); got != rec.TraceHash {
		t.Errorf("hash = %s, traceHash = %s", got, rec.TraceHash)
	}
	if end-start != rec.EventCount {
		t.Errorf("lines = %d, eventCount = %d", end-start, rec.EventCount)
	}
}

// RC-5: 同じ引数なら同じバイト列。
func TestRecordSameBytes(t *testing.T) {
	t.Parallel()
	a, _ := recordOK(t)
	b, _ := recordOK(t)
	if a.stdout != b.stdout {
		t.Error("stdout differs")
	}
}

// RC-6: run の行と traceHash・events が一致する。
func TestRecordMatchesRun(t *testing.T) {
	t.Parallel()
	_, rec := recordOK(t)
	r := runCLI(t, nil, "run", "--data", testData, "--stage", testStage, "--bot", "greedy", "--seeds", "1")
	r.wantCode(t, exitOK)
	line := oneResult(t, r.stdout)
	if line.TraceHash != rec.TraceHash || *line.Events != rec.EventCount {
		t.Errorf("run %+v, record traceHash %s eventCount %d", line, rec.TraceHash, rec.EventCount)
	}
}

// RC-7: --inputs-dir の入力ログを replay すると、記録と同じ traceHash になる。
func TestRecordInputsDir(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "in")
	_, rec := recordOK(t, "--inputs-dir", dir)
	r := replayFile(t, inputLogPath(dir, testStage, "greedy", 1), "--expect", rec.TraceHash)
	r.wantCode(t, exitOK)
	if got := oneResult(t, r.stdout); got.TraceHash != rec.TraceHash || *got.Events != rec.EventCount {
		t.Errorf("replay %+v, record %s", got, rec.TraceHash)
	}
}

// RC-8: 失敗したら標準出力は空。
func TestRecordFailureWritesNothing(t *testing.T) {
	t.Parallel()
	r := runCLI(t, nil, "record", "--data", testData, "--stage", "nope", "--seed", "1")
	r.wantCode(t, exitFail)
	if r.stdout != "" {
		t.Errorf("stdout = %q", r.stdout)
	}
}
