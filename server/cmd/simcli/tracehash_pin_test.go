package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"testing"
)

// tracePinFile は、リポジトリのステージ × ボット × seed 1..200 のトレースのハッシュの期待値（DTN-00 #363）。
// sim.Engine の組み替え（段階 A）で振る舞いが変わらないことを確かめるために、組み替えの前に測って固定した。
// 期待値を変えてよいのは、ゲームの振る舞い（イベントの列）をわざと変えたときだけ。そのときは
// TRACEHASH_PIN_UPDATE=1 で書き直し、変えた理由をコミットのメッセージに書く。
const tracePinFile = "testdata/tracehash-pin.json"

// tracePinStages・tracePinBots・tracePinSeeds は期待値を持つ組み合わせ。
var (
	tracePinStages = []string{"stage1", "stage2", "stage3", "demo-goto", "demo-izu"}
	tracePinBots   = []string{"greedy", "novice"}
)

const tracePinSeeds = 200

// tracePinDefaultSeeds は、ふつうの go test で確かめる seed の数（1..n）。全部（1..200）は TRACEHASH_PIN_ALL=1。
const tracePinDefaultSeeds = 6

// TestTraceHashPinned: 組み替えの前に測ったトレースのハッシュと、今の Engine のハッシュが一致する（DTN-00 AC-1）。
func TestTraceHashPinned(t *testing.T) {
	t.Parallel()
	if _, err := os.Stat(repoData + "/islands.json"); errors.Is(err, fs.ErrNotExist) {
		t.Skip("リポジトリの data/ がない")
	}
	update := os.Getenv("TRACEHASH_PIN_UPDATE") == "1"
	seeds := tracePinDefaultSeeds
	if update || os.Getenv("TRACEHASH_PIN_ALL") == "1" {
		seeds = tracePinSeeds
	}
	if testing.Short() && !update {
		seeds = 2
	}

	want := map[string][]string{}
	if !update {
		b, err := os.ReadFile(tracePinFile)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(b, &want); err != nil {
			t.Fatal(err)
		}
	}

	got := make(map[string][]string, len(tracePinStages)*len(tracePinBots))
	for _, s := range tracePinStages {
		st, err := loadStage(repoData, s)
		if err != nil {
			t.Fatal(err)
		}
		for _, b := range tracePinBots {
			key := s + "/" + b
			hs := make([]string, seeds)
			for i := range hs {
				seed := uint64(i + 1)
				res, _, _, err := playSeed(st, seed, b, false)
				if err != nil {
					t.Fatalf("%s/seed %d: %v", key, seed, err)
				}
				hs[i] = res.TraceHash
				if update {
					continue
				}
				w := want[key]
				if i >= len(w) {
					t.Fatalf("%s/seed %d: %s has no expected value", key, seed, tracePinFile)
				}
				if res.TraceHash != w[i] {
					t.Errorf("%s/seed %d: traceHash = %s, want %s", key, seed, res.TraceHash, w[i])
				}
			}
			got[key] = hs
		}
	}
	if !update {
		return
	}
	b, err := marshalTracePin(got)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tracePinFile, b, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s", tracePinFile)
}

// marshalTracePin は、キーを tracePinStages × tracePinBots の順に、1 行に 1 つのハッシュで書く（差分を読みやすくする）。
func marshalTracePin(m map[string][]string) ([]byte, error) {
	var b []byte
	b = append(b, "{\n"...)
	n := 0
	for _, s := range tracePinStages {
		for _, bot := range tracePinBots {
			key := s + "/" + bot
			hs, ok := m[key]
			if !ok {
				return nil, fmt.Errorf("no hashes for %s", key)
			}
			if n > 0 {
				b = append(b, ",\n"...)
			}
			n++
			b = append(b, "  "+strconv.Quote(key)+": [\n"...)
			for i, h := range hs {
				b = append(b, "    "+strconv.Quote(h)...)
				if i < len(hs)-1 {
					b = append(b, ',')
				}
				b = append(b, '\n')
			}
			b = append(b, "  ]"...)
		}
	}
	b = append(b, "\n}\n"...)
	return b, nil
}
