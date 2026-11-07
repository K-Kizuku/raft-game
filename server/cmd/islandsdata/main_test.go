package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixtureArgs(out string, extra ...string) []string {
	return append([]string{"--raw", fixtureRaw, "--curated", fixtureCurated, "--select", fixtureSelect, "--out", out}, extra...)
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

// E-11: 成功すると 0 で、--out が golden と一致する。一時ファイルが残らない。
func TestRunWrites(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	out := filepath.Join(dir, "islands.json")
	var stderr bytes.Buffer
	if code := run(fixtureArgs(out), &stderr); code != 0 {
		t.Fatalf("code = %d, stderr:\n%s", code, &stderr)
	}
	if got, want := readFile(t, out), readFile(t, fixtureGolden); !bytes.Equal(got, want) {
		t.Fatal("--out が golden と違う")
	}
	if msg := stderr.String(); strings.Count(msg, "\n") != 1 || !strings.Contains(msg, "島 4") || !strings.Contains(msg, "抜き出し 3") {
		t.Errorf("標準エラー = %q", msg)
	}
	if names := entries(t, dir); len(names) != 1 {
		t.Errorf("ディレクトリに余分なファイルがある: %v", names)
	}
}

// E-11: フラグの誤りは 2。
func TestRunUsage(t *testing.T) {
	t.Parallel()
	out := filepath.Join(t.TempDir(), "islands.json")
	cases := map[string][]string{
		"フラグがない":    {},
		"--out がない": {"--raw", fixtureRaw, "--curated", fixtureCurated, "--select", fixtureSelect},
		"未知のフラグ":    fixtureArgs(out, "--verbose"),
		"余分な引数":     fixtureArgs(out, "extra"),
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

// E-11: 入力のエラーは 1。前からあった --out は変わらず、一時ファイルも残らない。
func TestRunInputError(t *testing.T) {
	t.Parallel()
	in := baseInputs()
	in.sel = replaceOnce(t, in.sel, "format: 1", "format: 2")
	raw, cur, sel := in.write(t)
	dir := t.TempDir()
	out := filepath.Join(dir, "islands.json")
	writeFile(t, out, "前の中身\n")
	var stderr bytes.Buffer
	if code := run([]string{"--raw", raw, "--curated", cur, "--select", sel, "--out", out}, &stderr); code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if !strings.HasPrefix(stderr.String(), "islandsdata: ") || !strings.Contains(stderr.String(), sel) {
		t.Errorf("標準エラー = %q", &stderr)
	}
	if got := string(readFile(t, out)); got != "前の中身\n" {
		t.Errorf("--out が変わった: %q", got)
	}
	if names := entries(t, dir); len(names) != 1 {
		t.Errorf("一時ファイルが残っている: %v", names)
	}
}

// E-11: --out のディレクトリがない・--curated のディレクトリがないと 1。どちらも作らない。
func TestRunMissingDirs(t *testing.T) {
	t.Parallel()
	t.Run("--out のディレクトリがない", func(t *testing.T) {
		t.Parallel()
		outDir := filepath.Join(t.TempDir(), "nope")
		var stderr bytes.Buffer
		if code := run(fixtureArgs(filepath.Join(outDir, "islands.json")), &stderr); code != 1 {
			t.Fatalf("code = %d, want 1", code)
		}
		if _, err := os.Stat(outDir); !os.IsNotExist(err) {
			t.Error("--out のディレクトリを作っている")
		}
	})
	t.Run("--curated のディレクトリがない", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		cur := filepath.Join(dir, "nope")
		out := filepath.Join(dir, "islands.json")
		var stderr bytes.Buffer
		args := []string{"--raw", fixtureRaw, "--curated", cur, "--select", fixtureSelect, "--out", out}
		if code := run(args, &stderr); code != 1 {
			t.Fatalf("code = %d, want 1", code)
		}
		if _, err := os.Stat(cur); !os.IsNotExist(err) {
			t.Error("--curated のディレクトリを作っている")
		}
		if _, err := os.Stat(out); !os.IsNotExist(err) {
			t.Error("--out を作っている")
		}
	})
}

// E-11: --check は書かない。同じなら 0、違う・ないなら 1。
func TestRunCheck(t *testing.T) {
	t.Parallel()
	golden := readFile(t, fixtureGolden)
	cases := []struct {
		name    string
		content []byte // nil なら --out を作らない
		want    int
	}{
		{"同じ", golden, 0},
		{"違う", append(bytes.Clone(golden), '\n'), 1},
		{"ない", nil, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			out := filepath.Join(dir, "islands.json")
			if c.content != nil {
				if err := os.WriteFile(out, c.content, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			var stderr bytes.Buffer
			if code := run(fixtureArgs(out, "--check"), &stderr); code != c.want {
				t.Fatalf("code = %d, want %d, stderr:\n%s", code, c.want, &stderr)
			}
			if c.want == 1 && !strings.Contains(stderr.String(), "再生成") {
				t.Errorf("再生成の案内がない: %s", &stderr)
			}
			if c.content == nil {
				if _, err := os.Stat(out); !os.IsNotExist(err) {
					t.Error("--check で --out を作っている")
				}
			} else if got := readFile(t, out); !bytes.Equal(got, c.content) {
				t.Error("--check で --out を変えている")
			}
			if names := entries(t, dir); len(names) > 1 {
				t.Errorf("余分なファイルがある: %v", names)
			}
		})
	}
}
