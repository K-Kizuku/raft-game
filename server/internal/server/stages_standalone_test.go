package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/K-Kizuku/raft-game/server/internal/protocol"
	"github.com/K-Kizuku/raft-game/server/internal/protocol/stageconv"
	"github.com/K-Kizuku/raft-game/server/internal/stage"
)

// replaceOnce は s の中に 1 か所だけある old を new に置き換える。1 か所でなければ t.Fatal。
func replaceOnce(t *testing.T, s, old, new string) string {
	t.Helper()
	if n := strings.Count(s, old); n != 1 {
		t.Fatalf("%q が %d か所ある（1 か所のはず）", old, n)
	}
	return strings.Replace(s, old, new, 1)
}

// demoStageYAML は testdata の alpha.yaml から、デモ用のステージ delta の定義を作る。
func demoStageYAML(t *testing.T) []byte {
	t.Helper()
	s := string(readTestdata(t, "stages/alpha.yaml"))
	s = replaceOnce(t, s, "id: alpha", "id: delta")
	s = replaceOnce(t, s, "title: テストの海", "title: テストのデモ")
	if !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	s += "demo:\n  endOnAllCriticalSecured: true\n"
	return []byte(s)
}

// demoStageCatalog は testdata の alpha・omega に、デモ用のステージ delta を足したカタログを読む。
// withNormal が偽なら delta だけ。
func demoStageCatalog(t *testing.T, withNormal bool) *Catalog {
	t.Helper()
	fsys := fstest.MapFS{
		"islands.json":      {Data: readTestdata(t, "islands.json")},
		"stages/delta.yaml": {Data: demoStageYAML(t)},
	}
	if withNormal {
		fsys["stages/alpha.yaml"] = &fstest.MapFile{Data: readTestdata(t, "stages/alpha.yaml")}
		fsys["stages/omega.yaml"] = &fstest.MapFile{Data: readTestdata(t, "stages/omega.yaml")}
	}
	cat, err := LoadCatalog(fsys)
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	return cat
}

// newDemoCatalogServer は cat を使う standalone のサーバーを作る。
func newDemoCatalogServer(t *testing.T, cat *Catalog, demoControls bool) *testServer {
	t.Helper()
	return newTestServer(t, ModeStandalone, func(c *Config, s *Server) {
		s.cat = cat
		c.DemoControls = demoControls
	})
}

// getStages は GET /api/stages を送り、200 の本文と、読んだ値を返す。
func getStages(t *testing.T, ts *testServer) ([]byte, protocol.StagesResponse) {
	t.Helper()
	res, body := ts.do(t, http.MethodGet, "/api/stages", nil, "")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/stages = %d %s", res.StatusCode, body)
	}
	assertJSONHeaders(t, res)
	var out protocol.StagesResponse
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	return body, out
}

// stageIDs は一覧の id を順に返す。
func stageIDs(r protocol.StagesResponse) []string {
	ids := make([]string, len(r.Stages))
	for i, s := range r.Stages {
		ids[i] = s.ID
	}
	return ids
}

// lookupStages は cat から ids のステージを順に取り出す。
func lookupStages(t *testing.T, cat *Catalog, ids ...string) []*stage.Stage {
	t.Helper()
	out := make([]*stage.Stage, len(ids))
	for i, id := range ids {
		st, ok := cat.Lookup(id)
		if !ok {
			t.Fatalf("ステージ %s がない", id)
		}
		out[i] = st
	}
	return out
}

// marshalStages は stages から作った /api/stages の本文（writeJSON と同じ json.Marshal）を返す。
func marshalStages(t *testing.T, stages []*stage.Stage) string {
	t.Helper()
	b, err := json.Marshal(protocol.StagesResponse{Stages: stageconv.Summaries(stages)})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// SS-1: デモ操作が無効なら、デモ用のステージを一覧に出さない。
func TestStandaloneStagesHideDemo(t *testing.T) {
	t.Parallel()
	cat := demoStageCatalog(t, true)
	ts := newDemoCatalogServer(t, cat, false)
	body, got := getStages(t, ts)
	if ids := strings.Join(stageIDs(got), ","); ids != "alpha,omega" {
		t.Errorf("id = %s, want alpha,omega", ids)
	}
	if want := marshalStages(t, lookupStages(t, cat, "alpha", "omega")); string(body) != want {
		t.Errorf("GET /api/stages =\n%s\nwant\n%s", body, want)
	}
}

// SS-2: デモ操作が有効なら、すべてのステージをファイル名の順に出す。
func TestStandaloneStagesShowDemo(t *testing.T) {
	t.Parallel()
	cat := demoStageCatalog(t, true)
	ts := newDemoCatalogServer(t, cat, true)
	body, got := getStages(t, ts)
	if ids := strings.Join(stageIDs(got), ","); ids != "alpha,delta,omega" {
		t.Errorf("id = %s, want alpha,delta,omega", ids)
	}
	for _, s := range got.Stages {
		if want := s.ID == "delta"; s.Demo != want {
			t.Errorf("%s の demo = %v, want %v", s.ID, s.Demo, want)
		}
	}
	if want := marshalStages(t, cat.Stages()); string(body) != want {
		t.Errorf("GET /api/stages =\n%s\nwant\n%s", body, want)
	}
}

// postSession は standalone で POST /api/sessions を送る。
func postSession(t *testing.T, ts *testServer, body string) (*http.Response, []byte) {
	t.Helper()
	return ts.do(t, http.MethodPost, "/api/sessions",
		http.Header{"Origin": {ts.origin()}, "Content-Type": {"application/json"}}, body)
}

// SS-3: デモ操作が無効なら、デモ用のステージのセッションを作らない。
func TestStandaloneCreateDemoStageDisabled(t *testing.T) {
	t.Parallel()
	ts := newDemoCatalogServer(t, demoStageCatalog(t, true), false)
	for _, body := range []string{`{"stageId":"delta"}`, `{"stageId":"delta","demo":false}`} {
		res, b := postSession(t, ts, body)
		assertErrorBody(t, res, b, http.StatusBadRequest, protocol.HTTPErrorCodeBadRequest)
	}
	ts.srv.standalone.mu.Lock()
	n := len(ts.srv.standalone.sessions)
	ts.srv.standalone.mu.Unlock()
	if n != 0 {
		t.Errorf("セッションが %d 個できた, want 0", n)
	}
	ts.createSession(t, `{"stageId":"alpha"}`)
}

// SS-3（上限）: デモ用のステージの 400 は、上限の 503 より先に返し、上限の数にも数えない。
func TestStandaloneCreateDemoStageBeforeLimit(t *testing.T) {
	t.Parallel()
	cat := demoStageCatalog(t, true)
	ts := newTestServer(t, ModeStandalone, func(c *Config, s *Server) {
		s.cat = cat
		c.MaxSessions = 1
	})
	res, b := postSession(t, ts, `{"stageId":"delta"}`)
	assertErrorBody(t, res, b, http.StatusBadRequest, protocol.HTTPErrorCodeBadRequest)
	ts.createSession(t, `{"stageId":"alpha"}`)
	res, b = postSession(t, ts, `{"stageId":"delta"}`)
	assertErrorBody(t, res, b, http.StatusBadRequest, protocol.HTTPErrorCodeBadRequest)
}

// SS-4: デモ操作が有効なら、デモ用のステージのセッションを作れる。
func TestStandaloneCreateDemoStageEnabled(t *testing.T) {
	t.Parallel()
	ts := newDemoCatalogServer(t, demoStageCatalog(t, true), true)
	cases := []struct {
		body string
		demo bool
	}{
		{`{"stageId":"delta","demo":true}`, true},
		{`{"stageId":"delta"}`, false},
		{`{"stageId":"alpha","demo":true}`, true},
	}
	for _, tc := range cases {
		created := ts.createSession(t, tc.body)
		if got := ts.standaloneSession(t, created.SessionID).demo; got != tc.demo {
			t.Errorf("%s: demo = %v, want %v", tc.body, got, tc.demo)
		}
	}
}

// SS-5: デモ用のステージだけのカタログで、デモ操作が無効なら空の一覧。
func TestStandaloneStagesOnlyDemo(t *testing.T) {
	t.Parallel()
	ts := newDemoCatalogServer(t, demoStageCatalog(t, false), false)
	body, _ := getStages(t, ts)
	if want := `{"stages":[],"features":{"autoFerry":false}}`; string(body) != want {
		t.Errorf("GET /api/stages = %s, want %s", body, want)
	}
}
