package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// テスト用の入力（読むだけ）。server/internal/stage/testdata の複製を小さくしたもの。
var (
	fixtureData   = "testdata"
	fixtureGolden = filepath.Join("testdata", "golden", "stage-catalog.gen.json")
)

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// entries は dir の中のファイル名を返す。
func entries(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range es {
		names = append(names, e.Name())
	}
	return names
}

// copyFixture は testdata の islands.json と stages/*.yaml を t.TempDir() に複製し、そのディレクトリを返す。
func copyFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "islands.json"), readFile(t, filepath.Join(fixtureData, "islands.json")))
	for _, name := range entries(t, filepath.Join(fixtureData, "stages")) {
		writeFile(t, filepath.Join(dir, "stages", name), readFile(t, filepath.Join(fixtureData, "stages", name)))
	}
	return dir
}

// generateTo は data から out を作る。終了コードが 0 でなければ失敗する。
func generateTo(t *testing.T, data, out string) []byte {
	t.Helper()
	var stderr bytes.Buffer
	if code := run([]string{"--data", data, "--out", out}, &stderr); code != 0 {
		t.Fatalf("code = %d, stderr:\n%s", code, &stderr)
	}
	return readFile(t, out)
}

// CMD-1: testdata から作った出力が golden とバイト列で一致する。2 回作っても同じ。末尾は改行 1 つ。
func TestRunGolden(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	out := filepath.Join(dir, "stage-catalog.gen.json")
	var stderr bytes.Buffer
	if code := run([]string{"--data", fixtureData, "--out", out}, &stderr); code != 0 {
		t.Fatalf("code = %d, stderr:\n%s", code, &stderr)
	}
	first := readFile(t, out)
	if want := readFile(t, fixtureGolden); !bytes.Equal(first, want) {
		t.Fatalf("出力が golden と違う。作り直す: go -C server run ./cmd/stagecatalog --data cmd/stagecatalog/testdata --out cmd/stagecatalog/testdata/golden/stage-catalog.gen.json\n%s", first)
	}
	if !bytes.HasSuffix(first, []byte("}\n")) || bytes.HasSuffix(first, []byte("\n\n")) {
		t.Errorf("末尾が改行 1 つでない: %q", first[len(first)-3:])
	}
	if msg := stderr.String(); msg != "stagecatalog: "+out+" を書いた（ステージ 2・うちデモ 1）\n" {
		t.Errorf("標準エラー = %q", msg)
	}
	if second := generateTo(t, fixtureData, out); !bytes.Equal(first, second) {
		t.Error("2 回目の出力が 1 回目と違う")
	}
	if names := entries(t, dir); len(names) != 1 {
		t.Errorf("ディレクトリに余分なファイルがある: %v", names)
	}
}

// CMD-2: --check。
func TestRunCheck(t *testing.T) {
	t.Parallel()

	t.Run("同じ中身", func(t *testing.T) {
		t.Parallel()
		out := filepath.Join(t.TempDir(), "stage-catalog.gen.json")
		want := generateTo(t, fixtureData, out)
		old := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
		if err := os.Chtimes(out, old, old); err != nil {
			t.Fatal(err)
		}
		var stderr bytes.Buffer
		if code := run([]string{"--data", fixtureData, "--out", out, "--check"}, &stderr); code != 0 {
			t.Fatalf("code = %d, stderr:\n%s", code, &stderr)
		}
		if stderr.Len() != 0 {
			t.Errorf("標準エラー = %q, want 空", &stderr)
		}
		if got := readFile(t, out); !bytes.Equal(got, want) {
			t.Error("中身が変わった")
		}
		st, err := os.Stat(out)
		if err != nil {
			t.Fatal(err)
		}
		if !st.ModTime().Equal(old) {
			t.Errorf("更新時刻が変わった: %v", st.ModTime())
		}
	})

	t.Run("違う中身", func(t *testing.T) {
		t.Parallel()
		out := filepath.Join(t.TempDir(), "stage-catalog.gen.json")
		writeFile(t, out, []byte("{\"format\":1,\"stages\":[]}\n"))
		var stderr bytes.Buffer
		if code := run([]string{"--data", fixtureData, "--out", out, "--check"}, &stderr); code != 1 {
			t.Fatalf("code = %d, want 1", code)
		}
		cmd := "go -C server run ./cmd/stagecatalog --data " + fixtureData + " --out " + out
		if msg := stderr.String(); !strings.Contains(msg, cmd) || !strings.Contains(msg, "生成結果と違う") {
			t.Errorf("標準エラーに作り直すコマンドがない: %s", msg)
		}
		if got := readFile(t, out); string(got) != "{\"format\":1,\"stages\":[]}\n" {
			t.Error("--check で書き換えた")
		}
	})

	t.Run("ファイルがない", func(t *testing.T) {
		t.Parallel()
		out := filepath.Join(t.TempDir(), "stage-catalog.gen.json")
		var stderr bytes.Buffer
		if code := run([]string{"--data", fixtureData, "--out", out, "--check"}, &stderr); code != 1 {
			t.Fatalf("code = %d, want 1", code)
		}
		if msg := stderr.String(); !strings.Contains(msg, "読めない") {
			t.Errorf("標準エラーに「読めない」がない: %s", msg)
		}
		if _, err := os.Stat(out); !os.IsNotExist(err) {
			t.Error("--check で --out を作った")
		}
	})
}

// CMD-3: 使い方の誤りは 2。
func TestRunUsage(t *testing.T) {
	t.Parallel()
	out := filepath.Join(t.TempDir(), "stage-catalog.gen.json")
	cases := map[string][]string{
		"--data がない": {"--out", out},
		"--out がない":  {"--data", fixtureData},
		"知らないフラグ":    {"--data", fixtureData, "--out", out, "--verbose"},
		"余分な引数":      {"--data", fixtureData, "--out", out, "extra"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var stderr bytes.Buffer
			if code := run(args, &stderr); code != 2 {
				t.Fatalf("code = %d, want 2", code)
			}
			if !strings.Contains(stderr.String(), "使い方") {
				t.Errorf("使い方が出ていない: %s", &stderr)
			}
			if _, err := os.Stat(out); !os.IsNotExist(err) {
				t.Error("--out を作っている")
			}
		})
	}
}

// CMD-4: 壊れたステージ定義は 1。stderr にファイル名。--out は作られない・あれば変わらない。
func TestRunBrokenStage(t *testing.T) {
	t.Parallel()
	for _, existing := range []bool{false, true} {
		name := "--out がない"
		if existing {
			name = "--out がある"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			data := copyFixture(t)
			writeFile(t, filepath.Join(data, "stages", "broken.yaml"), []byte("format: 1\nid: broken\n"))
			outDir := t.TempDir()
			out := filepath.Join(outDir, "stage-catalog.gen.json")
			const before = "前の中身\n"
			if existing {
				writeFile(t, out, []byte(before))
			}
			var stderr bytes.Buffer
			if code := run([]string{"--data", data, "--out", out}, &stderr); code != 1 {
				t.Fatalf("code = %d, want 1, stderr:\n%s", code, &stderr)
			}
			msg := stderr.String()
			if !strings.HasPrefix(msg, "stagecatalog: ") || !strings.Contains(msg, "broken.yaml") {
				t.Errorf("標準エラーにファイル名がない: %s", msg)
			}
			if existing {
				if got := readFile(t, out); string(got) != before {
					t.Error("--out が変わった")
				}
				if names := entries(t, outDir); len(names) != 1 {
					t.Errorf("ディレクトリに余分なファイルがある: %v", names)
				}
			} else if names := entries(t, outDir); len(names) != 0 {
				t.Errorf("--out のディレクトリにファイルを作った: %v", names)
			}
		})
	}
}

// CMD-5: stages/ が空なら {"format":1,"stages":[]} と改行（字下げは 2 つの空白）。
func TestRunEmpty(t *testing.T) {
	t.Parallel()
	data := t.TempDir()
	writeFile(t, filepath.Join(data, "islands.json"), readFile(t, filepath.Join(fixtureData, "islands.json")))
	if err := os.Mkdir(filepath.Join(data, "stages"), 0o755); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "stage-catalog.gen.json")
	got := generateTo(t, data, out)
	if want := "{\n  \"format\": 1,\n  \"stages\": []\n}\n"; string(got) != want {
		t.Errorf("出力 = %q, want %q", got, want)
	}
}

// CMD-6: 出力を DisallowUnknownFields で読める。< と & がエスケープされない。
func TestRunDecodeStrict(t *testing.T) {
	t.Parallel()
	out := filepath.Join(t.TempDir(), "stage-catalog.gen.json")
	got := generateTo(t, fixtureData, out)
	var c struct {
		Format int
		Stages []catalogStage
	}
	dec := json.NewDecoder(bytes.NewReader(got))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		t.Fatalf("読めない: %v", err)
	}
	if c.Format != 1 || len(c.Stages) != 2 || c.Stages[0].ID != "alpha" || c.Stages[1].ID != "beta" {
		t.Errorf("読んだ値 = %+v", c)
	}
	if c.Stages[0].Demo || !c.Stages[1].Demo {
		t.Errorf("demo = %v・%v, want false・true", c.Stages[0].Demo, c.Stages[1].Demo)
	}
	if !strings.Contains(c.Stages[0].Intro.Text, "<雨> & ") {
		t.Errorf("intro.text = %q", c.Stages[0].Intro.Text)
	}
	esc := []byte{'\\', 'u', '0', '0'} // JSON の \u00XX（HTML のエスケープの形）
	if !bytes.Contains(got, []byte("<雨> & ")) || bytes.Contains(got, esc) {
		t.Error("< と & がエスケープされている")
	}
}

// CMD-7: --out のディレクトリがないと 1。一時ファイルが残らない。
func TestRunNoOutDir(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	out := filepath.Join(parent, "missing", "stage-catalog.gen.json")
	var stderr bytes.Buffer
	if code := run([]string{"--data", fixtureData, "--out", out}, &stderr); code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if names := entries(t, parent); len(names) != 0 {
		t.Errorf("ファイルが残っている: %v", names)
	}
}

// CMD-8: 警告のあるステージは 0 で、stderr に「警告:」の行。
func TestRunWarning(t *testing.T) {
	t.Parallel()
	data := copyFixture(t)
	path := filepath.Join(data, "stages", "alpha.yaml")
	src := string(readFile(t, path))
	if !strings.Contains(src, "electionTick: 40") {
		t.Fatal("alpha.yaml に electionTick: 40 がない")
	}
	writeFile(t, path, []byte(strings.Replace(src, "electionTick: 40", "electionTick: 4", 1)))
	out := filepath.Join(t.TempDir(), "stage-catalog.gen.json")
	var stderr bytes.Buffer
	if code := run([]string{"--data", data, "--out", out}, &stderr); code != 0 {
		t.Fatalf("code = %d, stderr:\n%s", code, &stderr)
	}
	if msg := stderr.String(); !strings.Contains(msg, "stagecatalog: 警告: alpha: raft.electionTick: ") {
		t.Errorf("標準エラーに警告がない: %s", msg)
	}
}
