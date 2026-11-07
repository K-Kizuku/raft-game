package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/coder/websocket"

	"github.com/K-Kizuku/raft-game/server/internal/protocol"
)

// assertJSONHeaders は JSON の応答のヘッダーを確かめる。
func assertJSONHeaders(t *testing.T, res *http.Response) {
	t.Helper()
	if got := res.Header.Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Errorf("content-type = %q", got)
	}
	if got := res.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("cache-control = %q", got)
	}
}

// assertErrorBody は status と本文の code を確かめる。
func assertErrorBody(t *testing.T, res *http.Response, body []byte, status int, code protocol.HTTPErrorCode) {
	t.Helper()
	if res.StatusCode != status {
		t.Fatalf("status = %d %s, want %d", res.StatusCode, body, status)
	}
	assertJSONHeaders(t, res)
	if res.Request.Method == http.MethodHead {
		return
	}
	var e protocol.ErrorResponse
	if err := json.Unmarshal(body, &e); err != nil {
		t.Fatalf("body %s: %v", body, err)
	}
	if e.Code != code || e.Message == "" {
		t.Errorf("body = %s, want code %s", body, code)
	}
}

// H-1: single の HTTP。
func TestHTTPSingle(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t, ModeSingle, nil)

	res, body := ts.do(t, http.MethodGet, "/healthz", nil, "")
	if res.StatusCode != http.StatusOK || string(body) != `{"status":"ok","build":"test-build"}` {
		t.Fatalf("GET /healthz = %d %s", res.StatusCode, body)
	}
	assertJSONHeaders(t, res)

	for _, m := range []string{http.MethodHead, http.MethodPost, http.MethodOptions} {
		res, body := ts.do(t, m, "/healthz", nil, "")
		assertErrorBody(t, res, body, http.StatusMethodNotAllowed, protocol.HTTPErrorCodeMethodNotAllowed)
		if got := res.Header.Get("Allow"); got != "GET" {
			t.Errorf("%s /healthz Allow = %q", m, got)
		}
	}
	for _, p := range []string{"/api/healthz", "/healthz/", "/", "/HEALTHZ", "/%68ealthz", "/ws/", "/api/stages", "/api/sessions"} {
		res, body := ts.do(t, http.MethodGet, p, nil, "")
		assertErrorBody(t, res, body, http.StatusNotFound, protocol.HTTPErrorCodeNotFound)
	}
	res, body = ts.do(t, http.MethodGet, "/ws", nil, "")
	assertErrorBody(t, res, body, http.StatusUpgradeRequired, protocol.HTTPErrorCodeUpgradeRequired)
	res, body = ts.do(t, http.MethodPost, "/ws", nil, "")
	assertErrorBody(t, res, body, http.StatusMethodNotAllowed, protocol.HTTPErrorCodeMethodNotAllowed)
}

// H-2: standalone の HTTP（healthz・stages・404・405）。
func TestHTTPStandalone(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t, ModeStandalone, nil)

	for _, p := range []string{"/healthz", "/api/healthz"} {
		res, body := ts.do(t, http.MethodGet, p, nil, "")
		if res.StatusCode != http.StatusOK || string(body) != `{"status":"ok","build":"test-build"}` {
			t.Errorf("GET %s = %d %s", p, res.StatusCode, body)
		}
		assertJSONHeaders(t, res)
	}

	res, body := ts.do(t, http.MethodGet, "/api/stages", nil, "")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/stages = %d %s", res.StatusCode, body)
	}
	assertJSONHeaders(t, res)
	want := `{"stages":[` +
		`{"id":"alpha","title":"テストの海","region":"波間県（架空）","islands":3,"ferries":1,"durationMs":180000,"unlock":null},` +
		`{"id":"omega","title":"テストの外海","region":"波間県（架空）","islands":3,"ferries":1,"durationMs":180000,"unlock":"alpha"}]}`
	if string(body) != want {
		t.Errorf("GET /api/stages =\n%s\nwant\n%s", body, want)
	}

	methods := []struct {
		method, path, allow string
	}{
		{http.MethodHead, "/api/healthz", "GET"},
		{http.MethodPost, "/healthz", "GET"},
		{http.MethodOptions, "/api/stages", "GET"},
		{http.MethodPut, "/api/sessions", "POST"},
		{http.MethodGet, "/api/sessions", "POST"},
		{http.MethodPost, "/ws/sessions/abc", "GET"},
	}
	for _, m := range methods {
		res, body := ts.do(t, m.method, m.path, nil, "")
		assertErrorBody(t, res, body, http.StatusMethodNotAllowed, protocol.HTTPErrorCodeMethodNotAllowed)
		if got := res.Header.Get("Allow"); got != m.allow {
			t.Errorf("%s %s Allow = %q, want %q", m.method, m.path, got, m.allow)
		}
	}

	notFound := []string{
		"/api/healthz/", "/API/healthz", "/api", "/api/", "/", "/ws", "/ws/sessions/", "/ws/sessions/a/b",
		"/ws/sessions/a%2Fb", "/ws/sessions/%41bc", "/api/%68ealthz", "/ws/sessions/" + strings.Repeat("a", 257),
	}
	for _, p := range notFound {
		for _, m := range []string{http.MethodGet, http.MethodPost} {
			res, body := ts.do(t, m, p, http.Header{"Origin": {"http://evil.example"}}, "")
			assertErrorBody(t, res, body, http.StatusNotFound, protocol.HTTPErrorCodeNotFound)
		}
	}
}

// H-3: Origin の規則。
func TestHTTPOrigin(t *testing.T) {
	t.Parallel()
	self := newTestServer(t, ModeStandalone, nil)
	allowed := newTestServer(t, ModeStandalone, func(c *Config, _ *Server) {
		c.AllowedOrigins = []string{"http://127.0.0.1:15173"}
	})
	upper := strings.Replace(self.origin(), "http://", "HTTP://", 1)

	tests := []struct {
		name   string
		ts     *testServer
		origin []string
		want   int
	}{
		{"自分のオリジン", self, []string{self.origin()}, http.StatusOK},
		{"Origin なし", self, nil, http.StatusOK},
		{"ほかのオリジン", self, []string{"http://127.0.0.1:15173"}, http.StatusForbidden},
		{"大文字の違い", self, []string{upper}, http.StatusForbidden},
		{"null", self, []string{"null"}, http.StatusForbidden},
		{"末尾の /", self, []string{self.origin() + "/"}, http.StatusForbidden},
		{"複数の値", self, []string{self.origin(), self.origin()}, http.StatusForbidden},
		{"許可リスト", allowed, []string{"http://127.0.0.1:15173"}, http.StatusOK},
		{"許可リストのとき自分は不可", allowed, []string{allowed.origin()}, http.StatusForbidden},
		{"許可リストの末尾の /", allowed, []string{"http://127.0.0.1:15173/"}, http.StatusForbidden},
		{"許可リストで Origin なし", allowed, nil, http.StatusOK},
	}
	for _, tt := range tests {
		for _, p := range []string{"/api/stages", "/api/healthz", "/healthz"} {
			res, body := tt.ts.do(t, http.MethodGet, p, http.Header{"Origin": tt.origin}, "")
			if res.StatusCode != tt.want {
				t.Errorf("%s: GET %s = %d %s, want %d", tt.name, p, res.StatusCode, body, tt.want)
			}
			if tt.want == http.StatusForbidden {
				assertErrorBody(t, res, body, http.StatusForbidden, protocol.HTTPErrorCodeForbiddenOrigin)
			}
		}
	}

	// 必須のルートで Origin がない・違う → 403。
	for _, h := range []http.Header{nil, {"Origin": {"http://evil.example"}}} {
		res, body := self.do(t, http.MethodPost, "/api/sessions", withHeader(h, "Content-Type", "application/json"), `{"stageId":"alpha"}`)
		assertErrorBody(t, res, body, http.StatusForbidden, protocol.HTTPErrorCodeForbiddenOrigin)
		res, body = self.do(t, http.MethodGet, "/ws/sessions/s_x", withHeader(h, "Upgrade", "websocket"), "")
		assertErrorBody(t, res, body, http.StatusForbidden, protocol.HTTPErrorCodeForbiddenOrigin)
	}
	res, _ := allowed.do(t, http.MethodPost, "/api/sessions",
		http.Header{"Origin": {"http://127.0.0.1:15173"}, "Content-Type": {"application/json"}}, `{"stageId":"alpha"}`)
	if res.StatusCode != http.StatusCreated {
		t.Errorf("POST /api/sessions with allowed origin = %d", res.StatusCode)
	}
}

func withHeader(h http.Header, k, v string) http.Header {
	out := http.Header{}
	for kk, vv := range h {
		out[kk] = append([]string(nil), vv...)
	}
	out.Set(k, v)
	return out
}

// H-4: POST /api/sessions の本文。
func TestHTTPCreateSession(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t, ModeStandalone, nil)
	post := func(contentType, body string) (*http.Response, []byte) {
		h := http.Header{"Origin": {ts.origin()}}
		if contentType != "" {
			h.Set("Content-Type", contentType)
		}
		return ts.do(t, http.MethodPost, "/api/sessions", h, body)
	}
	const ct = "application/json"
	pad := func(s string, n int) string { return s + strings.Repeat(" ", n-len(s)) }

	bad := []struct{ name, ct, body string }{
		{"4097 バイト", ct, pad(`{"stageId":"alpha"}`, 4097)},
		{"text/plain", "text/plain", `{"stageId":"alpha"}`},
		{"Content-Type なし", "", `{"stageId":"alpha"}`},
		{"知らないキー", ct, `{"stageId":"alpha","x":1}`},
		{"配列", ct, `[]`},
		{"null", ct, `null`},
		{"後ろに余分", ct, `{"stageId":"alpha"} {}`},
		{"壊れた JSON", ct, `{"stageId":"alpha"`},
		{"空", ct, ``},
		{"UTF-8 でない", ct, "{\"stageId\":\"alpha\",\"turnstileToken\":\"\xff\"}"},
		{"stageId がない", ct, `{}`},
		{"stageId が数", ct, `{"stageId":1}`},
		{"stageId が null", ct, `{"stageId":null}`},
		{"知らない stageId", ct, `{"stageId":"nope"}`},
		{"seed が文字列", ct, `{"stageId":"alpha","seed":"1"}`},
		{"seed が負", ct, `{"stageId":"alpha","seed":-1}`},
		{"seed が小数", ct, `{"stageId":"alpha","seed":1.5}`},
		{"seed が 1.0", ct, `{"stageId":"alpha","seed":1.0}`},
		{"seed が 1e3", ct, `{"stageId":"alpha","seed":1e3}`},
		{"seed が 2^53", ct, `{"stageId":"alpha","seed":9007199254740992}`},
		{"seed が null", ct, `{"stageId":"alpha","seed":null}`},
		{"demo が文字列", ct, `{"stageId":"alpha","demo":"true"}`},
		{"デモ操作が無効で demo: true", ct, `{"stageId":"alpha","demo":true}`},
		{"turnstileToken が数", ct, `{"stageId":"alpha","turnstileToken":1}`},
		{"turnstileToken が 2049 文字", ct, `{"stageId":"alpha","turnstileToken":"` + strings.Repeat("a", 2049) + `"}`},
	}
	for _, b := range bad {
		res, body := post(b.ct, b.body)
		if res.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status = %d %s, want 400", b.name, res.StatusCode, body)
			continue
		}
		assertErrorBody(t, res, body, http.StatusBadRequest, protocol.HTTPErrorCodeBadRequest)
	}

	idRE := regexp.MustCompile(`^s_[A-Za-z0-9_-]{22}$`)
	tokenRE := regexp.MustCompile(`^[A-Za-z0-9_-]{22}$`)
	good := []struct{ name, ct, body string }{
		{"seed が 2^53 − 1", ct, `{"stageId":"alpha","seed":9007199254740991}`},
		{"seed が 0", ct, `{"stageId":"alpha","seed":0}`},
		{"seed なし", ct, `{"stageId":"omega"}`},
		{"引数つきの Content-Type", "application/json; charset=utf-8", `{"stageId":"alpha","demo":false,"turnstileToken":"x"}`},
		{"4096 バイト", ct, pad(`{"stageId":"alpha"}`, 4096)},
		{"turnstileToken が 2048 文字", ct, `{"stageId":"alpha","turnstileToken":"` + strings.Repeat("a", 2048) + `"}`},
	}
	seen := map[string]bool{}
	for _, g := range good {
		res, body := post(g.ct, g.body)
		if res.StatusCode != http.StatusCreated {
			t.Errorf("%s: status = %d %s, want 201", g.name, res.StatusCode, body)
			continue
		}
		assertJSONHeaders(t, res)
		var cr protocol.CreateSessionResponse
		if err := json.Unmarshal(body, &cr); err != nil {
			t.Fatal(err)
		}
		if !idRE.MatchString(cr.SessionID) || !tokenRE.MatchString(cr.Token) || cr.WSPath != "/ws/sessions/"+cr.SessionID {
			t.Errorf("%s: body = %s", g.name, body)
		}
		if seen[cr.SessionID] || seen[cr.Token] {
			t.Errorf("%s: ID or token reused: %s", g.name, body)
		}
		seen[cr.SessionID], seen[cr.Token] = true, true
	}
	if s := ts.standaloneSession(t, mustCreate(t, ts, `{"stageId":"alpha","seed":9007199254740991}`).SessionID); s.seed != 9007199254740991 {
		t.Errorf("seed = %d", s.seed)
	}

	demo := newTestServer(t, ModeStandalone, func(c *Config, _ *Server) { c.DemoControls = true })
	if s := demo.standaloneSession(t, mustCreate(t, demo, `{"stageId":"alpha","demo":true}`).SessionID); !s.demo {
		t.Error("demo session is not demo")
	}
}

func mustCreate(t *testing.T, ts *testServer, body string) protocol.CreateSessionResponse {
	t.Helper()
	return ts.createSession(t, body)
}

// H-4: セッションの数の上限（17 個目は 503）。
func TestHTTPCreateSessionLimit(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t, ModeStandalone, nil)
	for i := 0; i < 16; i++ {
		ts.createSession(t, fmt.Sprintf(`{"stageId":"alpha","seed":%d}`, i))
	}
	res, body := ts.do(t, http.MethodPost, "/api/sessions",
		http.Header{"Origin": {ts.origin()}, "Content-Type": {"application/json"}}, `{"stageId":"alpha"}`)
	assertErrorBody(t, res, body, http.StatusServiceUnavailable, protocol.HTTPErrorCodeSessionsDisabled)
	if !strings.Contains(string(body), "too many sessions") {
		t.Errorf("body = %s", body)
	}
	// 本文の誤りは上限より先に判定する。
	res, body = ts.do(t, http.MethodPost, "/api/sessions",
		http.Header{"Origin": {ts.origin()}, "Content-Type": {"application/json"}}, `{"stageId":"nope"}`)
	assertErrorBody(t, res, body, http.StatusBadRequest, protocol.HTTPErrorCodeBadRequest)
}

// H-5: /ws/sessions/{id}。
func TestHTTPWebSocketRoute(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t, ModeStandalone, nil)
	cr := ts.createSession(t, `{"stageId":"alpha"}`)
	origin := http.Header{"Origin": {ts.origin()}}

	c := ts.dialSession(t, cr)
	c.pingPong()

	other := ts.createSession(t, `{"stageId":"alpha"}`)
	wrong := []string{
		cr.WSPath + "?token=" + other.Token,
		cr.WSPath + "?token=",
		cr.WSPath,
		"/ws/sessions/s_unknown?token=" + cr.Token,
		"/ws/sessions/" + strings.Replace(cr.SessionID, "s_", "s%5F", 1) + "?token=" + cr.Token,
		"/ws/sessions/s_%41bc?token=" + cr.Token,
	}
	for _, p := range wrong {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		_, res, err := websocket.Dial(ctx, ts.wsURL(p), &websocket.DialOptions{HTTPHeader: origin})
		cancel()
		if err == nil || res == nil || res.StatusCode != http.StatusNotFound {
			status := 0
			if res != nil {
				status = res.StatusCode
			}
			t.Errorf("dial %s: err %v, status %d, want 404", p, err, status)
		}
	}
	res, body := ts.do(t, http.MethodGet, cr.WSPath+"?token="+cr.Token, origin, "")
	assertErrorBody(t, res, body, http.StatusUpgradeRequired, protocol.HTTPErrorCodeUpgradeRequired)
	// Upgrade の有無より先に、トークンが違っても 426（検査の順序はメソッド → Origin → Upgrade → トークン）。
	res, body = ts.do(t, http.MethodGet, cr.WSPath+"?token=x", origin, "")
	assertErrorBody(t, res, body, http.StatusUpgradeRequired, protocol.HTTPErrorCodeUpgradeRequired)
}
