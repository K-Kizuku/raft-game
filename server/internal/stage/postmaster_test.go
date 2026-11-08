package stage

import (
	"reflect"
	"strings"
	"testing"
)

// 郵便局長（islands[].postmaster。データ仕様 §5.4、T-406c）の読み込みと検査。

// alpha.yaml の島の行（流れ形式）。
const (
	alphaIsland0 = `  - { ref: hama-hoshizuna, layout: { x: 600, y: 200 }, color: "#E07A5F" }`
	alphaIsland1 = `  - { ref: hama-tsukikage, layout: { x: 400, y: 300 }, color: "#3D85C6" }`
	alphaIsland2 = `  - { ref: hama-todai, layout: { x: 200, y: 300 }, color: "#81B29A" }`
)

var alphaIslandLines = []string{alphaIsland0, alphaIsland1, alphaIsland2}

// withPostmaster は alpha.yaml の島の行 line（流れ形式）の末尾に、postmaster: pm を足す置き換え。
func withPostmaster(line, pm string) edit {
	return edit{line, strings.TrimSuffix(line, " }") + ", postmaster: " + pm + " }"}
}

// alphaWithPostmaster は alpha.yaml の島 i に postmaster: pm を足したもの。
func alphaWithPostmaster(t *testing.T, i int, pm string, more ...edit) []byte {
	t.Helper()
	edits := append([]edit{withPostmaster(alphaIslandLines[i], pm)}, more...)
	return []byte(applyEdits(t, readTestdata(t, "stages/alpha.yaml"), edits...))
}

func issuePaths(se *Error) []string {
	var paths []string
	for _, iss := range se.Issues {
		paths = append(paths, iss.Path)
	}
	return paths
}

// PM-1 3 島すべてに正しい postmaster。
func TestPostmasterValid(t *testing.T) {
	t.Parallel()
	data := applyEdits(t, readTestdata(t, "stages/alpha.yaml"),
		withPostmaster(alphaIsland0, `{ name: ほしずな局長, tone: casual, provenance: creative }`),
		withPostmaster(alphaIsland1, `{ name: つきかげ局長, tone: polite, provenance: derived, `+
			`source: { dataset: fixture-2026, region: 星砂地域, field: MARITIME_TRANSPORT, quote: "月影~星砂(3km・10分" } }`),
		withPostmaster(alphaIsland2, `{ name: "  とうだい局長 ", tone: elder, portrait: todai-1.png, provenance: creative }`),
	)
	st, err := Parse("a.yaml", []byte(data), fixtureIslands(t))
	if err != nil {
		t.Fatal(err)
	}
	want := []*Postmaster{
		{Name: "ほしずな局長", Tone: "casual", Provenance: Creative},
		{Name: "つきかげ局長", Tone: "polite", Provenance: Derived,
			Source: &Source{Dataset: "fixture-2026", Region: "星砂地域", Field: "MARITIME_TRANSPORT", Quote: "月影~星砂(3km・10分"}},
		{Name: "  とうだい局長 ", Tone: "elder", Portrait: "todai-1.png", Provenance: Creative},
	}
	for i, isl := range st.Islands {
		if !reflect.DeepEqual(isl.Postmaster, want[i]) {
			t.Errorf("Islands[%d].Postmaster = %+v, want %+v", i, isl.Postmaster, want[i])
		}
	}
}

// PM-2 書かない・null なら nil。
func TestPostmasterAbsent(t *testing.T) {
	t.Parallel()
	is := fixtureIslands(t)
	cases := map[string][]byte{
		"書かない": []byte(readTestdata(t, "stages/alpha.yaml")),
		"null": alphaWithPostmaster(t, 0, "null"),
	}
	for name, data := range cases {
		st, err := Parse("a.yaml", data, is)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for i, isl := range st.Islands {
			if isl.Postmaster != nil {
				t.Errorf("%s: Islands[%d].Postmaster = %+v, want nil", name, i, isl.Postmaster)
			}
		}
	}
}

// PM-3 空のマッピングは name・tone・provenance の必須のエラー（この順）。
func TestPostmasterEmptyMapping(t *testing.T) {
	t.Parallel()
	_, err := Parse("a.yaml", alphaWithPostmaster(t, 0, "{}"), fixtureIslands(t))
	se := asError(t, err)
	want := []string{"islands[0].postmaster.name", "islands[0].postmaster.tone", "islands[0].postmaster.provenance"}
	if got := issuePaths(se); !reflect.DeepEqual(got, want) {
		t.Errorf("Paths = %v, want %v", got, want)
	}
}

// postmasterCase は 1 つの欄の値ごとの検査。
type postmasterCase struct {
	pm      string
	badPath string // "" = エラーにならない
}

func runPostmasterCases(t *testing.T, island int, cases []postmasterCase) {
	t.Helper()
	is := fixtureIslands(t)
	for _, tc := range cases {
		_, err := Parse("a.yaml", alphaWithPostmaster(t, island, tc.pm), is)
		if tc.badPath == "" {
			if err != nil {
				t.Errorf("%s: %v", tc.pm, err)
			}
			continue
		}
		se := asError(t, err)
		if got := issuePaths(se); !reflect.DeepEqual(got, []string{tc.badPath}) {
			t.Errorf("%s: Paths = %v, want [%s]", tc.pm, got, tc.badPath)
		}
		requireIssue(t, err, "a.yaml", tc.badPath)
	}
}

// PM-4 名前の空白だけ・長さ。
func TestPostmasterName(t *testing.T) {
	t.Parallel()
	runPostmasterCases(t, 1, []postmasterCase{
		{`{ name: "  ", tone: casual, provenance: creative }`, "islands[1].postmaster.name"},
		{`{ name: あいうえおかきくけこさしすせそた, tone: casual, provenance: creative }`, ""},                            // 16 文字
		{`{ name: あいうえおかきくけこさしすせそたち, tone: casual, provenance: creative }`, "islands[1].postmaster.name"}, // 17 文字
	})
}

// PM-5 口調。
func TestPostmasterTone(t *testing.T) {
	t.Parallel()
	runPostmasterCases(t, 2, []postmasterCase{
		{`{ name: テスト局長, tone: Casual, provenance: creative }`, "islands[2].postmaster.tone"},
		{`{ name: テスト局長, tone: dialect-sanuki, provenance: creative }`, "islands[2].postmaster.tone"},
		{`{ name: テスト局長, tone: casual, provenance: creative }`, ""},
		{`{ name: テスト局長, tone: polite, provenance: creative }`, ""},
		{`{ name: テスト局長, tone: elder, provenance: creative }`, ""},
	})
}

// PM-6 絵のファイル名。
func TestPostmasterPortrait(t *testing.T) {
	t.Parallel()
	runPostmasterCases(t, 0, []postmasterCase{
		{`{ name: テスト局長, tone: casual, portrait: Olive.png, provenance: creative }`, "islands[0].postmaster.portrait"},
		{`{ name: テスト局長, tone: casual, portrait: olive.gif, provenance: creative }`, "islands[0].postmaster.portrait"},
		{`{ name: テスト局長, tone: casual, portrait: ../x.png, provenance: creative }`, "islands[0].postmaster.portrait"},
		{`{ name: テスト局長, tone: casual, portrait: olive-1.webp, provenance: creative }`, ""},
	})
}

// PM-7 出典区分は derived か creative。
func TestPostmasterProvenance(t *testing.T) {
	t.Parallel()
	runPostmasterCases(t, 0, []postmasterCase{
		{`{ name: テスト局長, tone: casual, provenance: fact }`, "islands[0].postmaster.provenance"},
		{`{ name: テスト局長, tone: casual, provenance: other }`, "islands[0].postmaster.provenance"},
	})
}

// PM-8 derived の出典。
func TestPostmasterSource(t *testing.T) {
	t.Parallel()
	runPostmasterCases(t, 0, []postmasterCase{
		{`{ name: テスト局長, tone: casual, provenance: derived }`, "islands[0].postmaster.source"},
		{`{ name: テスト局長, tone: casual, provenance: derived, ` +
			`source: { dataset: fixture-2026, region: 星砂地域, field: MARITIME_TRANSPORT, quote: "架空の名産" } }`,
			"islands[0].postmaster.source.quote"},
	})
}

// PM-9 未知のキーは yaml の読み込みのエラー。
func TestPostmasterUnknownKey(t *testing.T) {
	t.Parallel()
	_, err := Parse("a.yaml", alphaWithPostmaster(t, 0, `{ nmae: テスト局長, tone: casual, provenance: creative }`), fixtureIslands(t))
	requireIssue(t, err, "a.yaml", "")
}

// PM-10 postmaster の誤りで航路の検査が止まらない。順序は島の検査 → postmaster → 航路。
func TestPostmasterDoesNotStopRoutes(t *testing.T) {
	t.Parallel()
	data := alphaWithPostmaster(t, 0, `{ name: テスト局長, tone: dialect-x, provenance: creative }`,
		edit{`color: "#3D85C6"`, `color: "blue"`},
		edit{"travelMs: 1500", "travelMs: 0"},
	)
	_, err := Parse("a.yaml", data, fixtureIslands(t))
	se := asError(t, err)
	want := []string{"islands[1].color", "islands[0].postmaster.tone", "routes[0].travelMs"}
	if got := issuePaths(se); !reflect.DeepEqual(got, want) {
		t.Errorf("Paths = %v, want %v", got, want)
	}
}

// postmaster の誤りだけなら航路の連結の検査も行う（islandsOK を変えない）。
func TestPostmasterKeepsIslandsOK(t *testing.T) {
	t.Parallel()
	data := alphaWithPostmaster(t, 0, `{ name: テスト局長, tone: dialect-x, provenance: creative }`,
		edit{"  - { a: hama-tsukikage, b: hama-todai, travelMs: 4000, provenance: creative }\n", ""},
	)
	_, err := Parse("a.yaml", data, fixtureIslands(t))
	se := asError(t, err)
	want := []string{"islands[0].postmaster.tone", "routes"}
	if got := issuePaths(se); !reflect.DeepEqual(got, want) {
		t.Errorf("Paths = %v, want %v", got, want)
	}
}
