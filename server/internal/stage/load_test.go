package stage

import (
	"os"
	"strings"
	"testing"
	"testing/fstest"
)

// L-1 testdata を読む。
func TestLoadDirTestdata(t *testing.T) {
	t.Parallel()
	stages, err := LoadDir(os.DirFS("testdata"), "islands.json", "stages")
	if err != nil {
		t.Fatal(err)
	}
	if len(stages) != 2 || stages[0].ID != "alpha" || stages[1].ID != "beta" {
		t.Fatalf("stages = %v", stages)
	}
}

// L-2 fstest.MapFS で読む。
func TestLoadDirMapFS(t *testing.T) {
	t.Parallel()
	islandsJSON := readTestdata(t, "islands.json")
	alpha := readTestdata(t, "stages/alpha.yaml")
	withID := func(t *testing.T, id, unlock string) string {
		t.Helper()
		return applyEdits(t, alpha, edit{"id: alpha", "id: " + id}, edit{"unlock: null", "unlock: " + unlock})
	}
	file := func(s string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(s)} }

	type want struct {
		ids   []string            // エラーがないときのステージの id
		files map[string][]string // エラーのときの、ファイル → 含むはずの Path
	}
	cases := []struct {
		name  string
		fsys  func(t *testing.T) fstest.MapFS
		want  want
		other bool // islands.json の誤りなど、ステージのファイル以外のエラー
	}{
		{
			name: "無視するファイル",
			fsys: func(*testing.T) fstest.MapFS {
				return fstest.MapFS{
					"islands.json":         file(islandsJSON),
					"stages/alpha.yaml":    file(alpha),
					"stages/.gitkeep":      file(""),
					"stages/README.md":     file("# x"),
					"stages/sub/bad.yaml":  file("not: [valid"),
					"stages/sub/bad2.yml":  file(""),
					"stages/dir.yaml/a.md": file(""),
				}
			},
			want: want{ids: []string{"alpha"}},
		},
		{
			name: "yamlがない",
			fsys: func(*testing.T) fstest.MapFS {
				return fstest.MapFS{"islands.json": file(islandsJSON), "stages/.gitkeep": file("")}
			},
			want: want{ids: []string{}},
		},
		{
			name: "ymlのファイル",
			fsys: func(*testing.T) fstest.MapFS {
				return fstest.MapFS{"islands.json": file(islandsJSON), "stages/alpha.yaml": file(alpha), "stages/x.yml": file(alpha)}
			},
			want: want{files: map[string][]string{"stages/x.yml": {""}}},
		},
		{
			name: "islands.jsonがない",
			fsys: func(*testing.T) fstest.MapFS {
				return fstest.MapFS{"stages/alpha.yaml": file(alpha)}
			},
			other: true,
		},
		{
			name: "islands.jsonが壊れている",
			fsys: func(*testing.T) fstest.MapFS {
				return fstest.MapFS{"islands.json": file("{"), "stages/alpha.yaml": file(alpha)}
			},
			other: true,
		},
		{
			name: "idとファイル名",
			fsys: func(*testing.T) fstest.MapFS {
				return fstest.MapFS{"islands.json": file(islandsJSON), "stages/other.yaml": file(alpha)}
			},
			want: want{files: map[string][]string{"stages/other.yaml": {"id"}}},
		},
		{
			name: "unlockがない",
			fsys: func(t *testing.T) fstest.MapFS {
				return fstest.MapFS{"islands.json": file(islandsJSON), "stages/alpha.yaml": file(withID(t, "alpha", "nothing"))}
			},
			want: want{files: map[string][]string{"stages/alpha.yaml": {"unlock"}}},
		},
		{
			name: "unlockの循環",
			fsys: func(t *testing.T) fstest.MapFS {
				return fstest.MapFS{
					"islands.json":      file(islandsJSON),
					"stages/a.yaml":     file(withID(t, "a", "b")),
					"stages/b.yaml":     file(withID(t, "b", "a")),
					"stages/alpha.yaml": file(alpha),
					"stages/c.yaml":     file(withID(t, "c", "a")), // 循環を指すが、循環には含まれない
				}
			},
			want: want{files: map[string][]string{"stages/a.yaml": {"unlock"}, "stages/b.yaml": {"unlock"}}},
		},
		{
			name: "2つのファイルが壊れている",
			fsys: func(t *testing.T) fstest.MapFS {
				return fstest.MapFS{
					"islands.json":      file(islandsJSON),
					"stages/alpha.yaml": file(alpha),
					"stages/b1.yaml":    file(applyEdits(t, withID(t, "b1", "alpha"), edit{"tickMs: 500", "tickMs: 0"})),
					"stages/b2.yaml":    file("format: [\n"),
				}
			},
			want: want{files: map[string][]string{"stages/b1.yaml": {"raft.tickMs"}, "stages/b2.yaml": {""}}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			stages, err := LoadDir(tc.fsys(t), "islands.json", "stages")
			switch {
			case tc.other:
				if stages != nil {
					t.Error("エラーなのにステージを返した")
				}
				requireIssue(t, err, "islands.json", "")
			case tc.want.files != nil:
				if stages != nil {
					t.Error("エラーなのにステージを返した")
				}
				got := fileErrors(t, err)
				if len(got) != len(tc.want.files) {
					t.Errorf("エラーのファイル = %v", err)
				}
				for name, paths := range tc.want.files {
					se, ok := got[name]
					if !ok {
						t.Errorf("%s のエラーがない: %v", name, err)
						continue
					}
					for _, p := range paths {
						if !hasPath(se, p) {
							t.Errorf("%s に Path %q の Issue がない: %v", name, p, se)
						}
					}
					if !strings.Contains(err.Error(), name) {
						t.Errorf("Error() に %s がない", name)
					}
				}
			default:
				if err != nil {
					t.Fatal(err)
				}
				if stages == nil {
					t.Fatal("nil のスライスを返した")
				}
				var ids []string
				for _, st := range stages {
					ids = append(ids, st.ID)
				}
				if len(ids) != len(tc.want.ids) || strings.Join(ids, ",") != strings.Join(tc.want.ids, ",") {
					t.Errorf("ids = %v, want %v", ids, tc.want.ids)
				}
			}
		})
	}
}

// ファイルの名前の順に返し、エラーの順も同じ。
func TestLoadDirOrder(t *testing.T) {
	t.Parallel()
	islandsJSON := readTestdata(t, "islands.json")
	alpha := readTestdata(t, "stages/alpha.yaml")
	fsys := fstest.MapFS{
		"islands.json":      {Data: []byte(islandsJSON)},
		"stages/zz.yml":     {Data: nil},
		"stages/b.yml":      {Data: nil},
		"stages/alpha.yaml": {Data: []byte(alpha)},
	}
	_, err := LoadDir(fsys, "islands.json", "stages")
	msg := err.Error()
	if i, j := strings.Index(msg, "stages/b.yml"), strings.Index(msg, "stages/zz.yml"); i < 0 || j < 0 || i > j {
		t.Errorf("エラーがファイルの名前の順でない: %s", msg)
	}
	// ディレクトリがない → *Error。
	_, err = LoadDir(fsys, "islands.json", "nodir")
	requireIssue(t, err, "nodir", "")
}
