package stage

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readTestdata は testdata/ のファイルを読む（testdata/ は読むだけ）。
func readTestdata(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// fixtureIslands は testdata/islands.json を読む。
func fixtureIslands(t *testing.T) *Islands {
	t.Helper()
	is, err := ParseIslands("islands.json", []byte(readTestdata(t, "islands.json")))
	if err != nil {
		t.Fatalf("ParseIslands: %v", err)
	}
	return is
}

// edit は old → new の置き換え 1 つ。
type edit struct{ old, new string }

// applyEdits は s に edits を順に当てる。old がちょうど 1 回だけ現れることを確かめる
// （fixture を変えたときに、テストが黙って空振りしないように）。
func applyEdits(t *testing.T, s string, edits ...edit) string {
	t.Helper()
	for _, e := range edits {
		if n := strings.Count(s, e.old); n != 1 {
			t.Fatalf("置き換え元 %q が %d 回現れる（1 回のはず）", e.old, n)
		}
		s = strings.Replace(s, e.old, e.new, 1)
	}
	return s
}

// asError は err から *Error を取り出す。
func asError(t *testing.T, err error) *Error {
	t.Helper()
	if err == nil {
		t.Fatal("エラーにならなかった")
	}
	var se *Error
	if !errors.As(err, &se) {
		t.Fatalf("*Error ではない: %T %v", err, err)
	}
	return se
}

// requireIssue は、err が file の *Error で、path の Issue を含み、
// Error() にファイル名と path が含まれることを確かめる。
func requireIssue(t *testing.T, err error, file, path string) {
	t.Helper()
	se := asError(t, err)
	if se.File != file {
		t.Fatalf("File = %q, want %q", se.File, file)
	}
	found := false
	for _, is := range se.Issues {
		if is.Message == "" {
			t.Errorf("Message が空の Issue: %+v", is)
		}
		if is.Path == path {
			found = true
		}
	}
	if !found {
		t.Fatalf("Path %q の Issue がない: %v", path, err)
	}
	msg := err.Error()
	if !strings.Contains(msg, file) {
		t.Errorf("Error() にファイル名 %q がない: %s", file, msg)
	}
	if path != "" && !strings.Contains(msg, "stage: "+file+": "+path+": ") {
		t.Errorf("Error() に Path %q がない: %s", path, msg)
	}
}

// fileErrors は errors.Join でまとめたエラーから、ファイルごとの *Error を取り出す。
func fileErrors(t *testing.T, err error) map[string]*Error {
	t.Helper()
	out := map[string]*Error{}
	var list []error
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		list = j.Unwrap()
	} else {
		list = []error{err}
	}
	for _, e := range list {
		se := asError(t, e)
		out[se.File] = se
	}
	return out
}

func hasPath(se *Error, path string) bool {
	for _, is := range se.Issues {
		if is.Path == path {
			return true
		}
	}
	return false
}
