package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/K-Kizuku/raft-game/server/internal/protocol"
)

const (
	testBuild   = "test-build"
	testTimeout = 10 * time.Second
)

// testCatalog は testdata/ のステージ定義（alpha・omega）を読む。
func testCatalog(t *testing.T) *Catalog {
	t.Helper()
	cat, err := LoadCatalog(os.DirFS("testdata"))
	if err != nil {
		t.Fatalf("LoadCatalog(testdata): %v", err)
	}
	return cat
}

// logBuffer は並行に書いてよいログの書き込み先。
type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *logBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// fakeClock は差し替えられる時刻。
type fakeClock struct{ ns atomic.Int64 }

func newFakeClock() *fakeClock {
	c := &fakeClock{}
	c.ns.Store(time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC).UnixNano())
	return c
}

func (c *fakeClock) Now() time.Time          { return time.Unix(0, c.ns.Load()) }
func (c *fakeClock) Advance(d time.Duration) { c.ns.Add(int64(d)) }

// testServer はテスト用のサーバー。
type testServer struct {
	srv  *Server
	http *httptest.Server
	logs *logBuffer
}

// newTestServer は mode のサーバーを httptest で動かす。edit で Config と内部の値を変えられる（Handler の前）。
func newTestServer(t *testing.T, mode Mode, edit func(*Config, *Server)) *testServer {
	t.Helper()
	cfg := Config{Mode: mode, MaxSessions: defaultMaxSessions, Build: testBuild, Invariants: true}
	logs := &logBuffer{}
	log := slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	srv := New(cfg, testCatalog(t), log)
	if edit != nil {
		edit(&srv.cfg, srv)
	}
	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		srv.shutdownConns()
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()
		srv.waitHandlers(ctx)
		hs.Close()
		srv.closeSessions()
	})
	return &testServer{srv: srv, http: hs, logs: logs}
}

// origin は、このサーバー自身のオリジン（http:// ＋ Host）。
func (ts *testServer) origin() string { return ts.http.URL }

func (ts *testServer) wsURL(path string) string {
	return "ws" + strings.TrimPrefix(ts.http.URL, "http") + path
}

// do は HTTP の要求を送り、状態と本文を返す。
func (ts *testServer) do(t *testing.T, method, path string, header http.Header, body string) (*http.Response, []byte) {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, ts.http.URL+path, r)
	if err != nil {
		t.Fatal(err)
	}
	for k, vs := range header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	res, err := ts.http.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res, b
}

// createSession は standalone で POST /api/sessions を送り、201 の本文を返す。
func (ts *testServer) createSession(t *testing.T, body string) protocol.CreateSessionResponse {
	t.Helper()
	res, b := ts.do(t, http.MethodPost, "/api/sessions",
		http.Header{"Origin": {ts.origin()}, "Content-Type": {"application/json"}}, body)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("POST /api/sessions %s = %d %s", body, res.StatusCode, b)
	}
	var out protocol.CreateSessionResponse
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// client は WebSocket のクライアント。
type client struct {
	t *testing.T
	c *websocket.Conn
}

// dial は url につなぐ。
func dial(t *testing.T, url string, header http.Header) *client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	c, res, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		status := 0
		if res != nil {
			status = res.StatusCode
		}
		t.Fatalf("dial %s: %v (status %d)", url, err, status)
	}
	c.SetReadLimit(8 << 20)
	t.Cleanup(func() { _ = c.CloseNow() })
	return &client{t: t, c: c}
}

// dialSingle は single の /ws につなぐ。
func (ts *testServer) dialSingle(t *testing.T) *client {
	return dial(t, ts.wsURL("/ws"), nil)
}

// dialSession は standalone の /ws/sessions/{id} につなぐ。
func (ts *testServer) dialSession(t *testing.T, s protocol.CreateSessionResponse) *client {
	return dial(t, ts.wsURL(s.WSPath+"?token="+s.Token), http.Header{"Origin": {ts.origin()}})
}

// sendRaw は電文をそのまま送る。
func (c *client) sendRaw(typ websocket.MessageType, b []byte) {
	c.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	if err := c.c.Write(ctx, typ, b); err != nil {
		c.t.Fatalf("write: %v", err)
	}
}

// send は電文 1 つを送る。payload が nil なら {}。
func (c *client) send(typ protocol.MessageType, cmdID string, payload any) {
	c.t.Helper()
	b, err := protocol.EncodeEnvelope(typ, 0, cmdID, payload)
	if err != nil {
		c.t.Fatal(err)
	}
	c.sendRaw(websocket.MessageText, b)
}

// recvRaw は次の電文を読む。
func (c *client) recvRaw() ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	typ, b, err := c.c.Read(ctx)
	if err != nil {
		return nil, err
	}
	if typ != websocket.MessageText {
		c.t.Fatalf("binary frame from server")
	}
	return b, nil
}

// recv は次の電文を読んでエンベロープにする。
func (c *client) recv() protocol.Envelope {
	c.t.Helper()
	b, err := c.recvRaw()
	if err != nil {
		c.t.Fatalf("read: %v", err)
	}
	env, err := protocol.DecodeEnvelope(b)
	if err != nil {
		c.t.Fatalf("decode %s: %v", b, err)
	}
	return env
}

// expect は次の電文が typ であることを確かめ、payload を v に読む（v が nil なら読まない）。
func (c *client) expect(typ protocol.MessageType, v any) protocol.Envelope {
	c.t.Helper()
	env := c.recv()
	if env.Type != typ {
		c.t.Fatalf("got %s %s, want %s", env.Type, env.Payload, typ)
	}
	if v != nil {
		if err := json.Unmarshal(env.Payload, v); err != nil {
			c.t.Fatalf("decode %s payload %s: %v", typ, env.Payload, err)
		}
	}
	return env
}

// expectError は次の電文が error で、code・fatal が一致することを確かめる。
func (c *client) expectError(code string, fatal bool) protocol.ErrorPayload {
	c.t.Helper()
	var p protocol.ErrorPayload
	c.expect(protocol.MessageTypeError, &p)
	if p.Code != code || p.Fatal != fatal {
		c.t.Fatalf("error = %+v, want code %s fatal %v", p, code, fatal)
	}
	if p.Message == "" {
		c.t.Errorf("error %s has no message", code)
	}
	return p
}

// expectClose は、接続が code で閉じられることを確かめる（その前の電文は読み捨てない。次の読み取りで閉じること）。
func (c *client) expectClose(code websocket.StatusCode) {
	c.t.Helper()
	b, err := c.recvRaw()
	if err == nil {
		c.t.Fatalf("got %s, want close %d", b, code)
	}
	if got := websocket.CloseStatus(err); got != code {
		c.t.Fatalf("close status = %d (%v), want %d", got, err, code)
	}
}

// expectFatal は fatal の error の後に code で閉じられることを確かめる。
func (c *client) expectFatal(errCode string, closeCode websocket.StatusCode) {
	c.t.Helper()
	c.expectError(errCode, true)
	c.expectClose(closeCode)
}

// pingPong は ping を送り、次の電文が pong であることを確かめる（その間に何も届かないことの確認）。
func (c *client) pingPong() {
	c.t.Helper()
	c.send(protocol.MessageTypePing, "", protocol.PingPayload{ClientTime: 1})
	c.expect(protocol.MessageTypePong, nil)
}

// initPayload は internal.init の payload。
func initPayload(id, stageID string, seed uint64) protocol.InternalInitPayload {
	return protocol.InternalInitPayload{SessionID: id, StageID: stageID, Seed: seed}
}

// initSingle は single の接続で internal.init を送り、snapshot を受ける。
func (c *client) initSingle(id string, seed uint64) protocol.Snapshot {
	c.t.Helper()
	c.send(protocol.MessageTypeInternalInit, "", initPayload(id, "alpha", seed))
	var snap protocol.Snapshot
	c.expect(protocol.MessageTypeSnapshot, &snap)
	return snap
}

// singleSession は single の枠のセッションを返す。
func (ts *testServer) singleSession(t *testing.T) *session {
	t.Helper()
	ts.srv.single.mu.Lock()
	defer ts.srv.single.mu.Unlock()
	if ts.srv.single.sess == nil {
		t.Fatal("no session")
	}
	return ts.srv.single.sess
}

// standaloneSession は standalone の一覧のセッションを返す。
func (ts *testServer) standaloneSession(t *testing.T, id string) *session {
	t.Helper()
	h := ts.srv.standalone
	h.mu.Lock()
	defer h.mu.Unlock()
	e, ok := h.sessions[id]
	if !ok {
		t.Fatalf("no session %s", id)
	}
	return e.sess
}

// assertNoNullArrays は、JSON の中に null がないことを確かめる（null を許すキーを除く）。
func assertNoNullArrays(t *testing.T, b []byte) {
	t.Helper()
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{"blockedUntil": true, "unlock": true, "lastDeliveredAt": true, "result": true}
	var walk func(path string, v any)
	walk = func(path string, v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, e := range x {
				if e == nil && !allowed[k] {
					t.Errorf("%s.%s is null", path, k)
				}
				walk(path+"."+k, e)
			}
		case []any:
			for _, e := range x {
				if e == nil {
					t.Errorf("%s has null element", path)
				}
				walk(path+"[]", e)
			}
		}
	}
	walk("$", v)
}

// mustJSON は v の JSON。
func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// contextWithTimeout は testTimeout の context。
func contextWithTimeout() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), testTimeout)
}

// discardLogger は何も出さないロガー。
func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
