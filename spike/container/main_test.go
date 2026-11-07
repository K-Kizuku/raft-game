package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// testOptions は本物のネットワーク・終了・環境変数を使わない options を返す。
func testOptions(t *testing.T) options {
	t.Helper()
	env := map[string]string{"MODE": "single", "PORT": "8080"}
	return options{
		cgroupRoot: t.TempDir(),
		getenv:     func(k string) string { return env[k] },
		hostname:   func() (string, error) { return "spike-test", nil },
		startedAt:  time.Now(),
		egress: egressDeps{
			lookup: func(context.Context, string) ([]string, error) { return nil, errors.New("lookup disabled in test") },
			dial: func(context.Context, string, string) (net.Conn, error) {
				return nil, errors.New("dial disabled in test")
			},
			get: func(context.Context, string) (int, error) { return 0, errors.New("get disabled in test") },
		},
		exit:       func(int) { t.Error("exit called unexpectedly") },
		crashDelay: time.Millisecond,
		logger:     log.New(io.Discard, "", 0),
	}
}

func startServer(t *testing.T, opts options) (*server, *httptest.Server) {
	t.Helper()
	s := newServer(opts)
	ts := httptest.NewServer(s)
	t.Cleanup(func() {
		s.close()
		ts.Close()
	})
	return s, ts
}

func do(t *testing.T, method, url string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, b
}

func decodeMap(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("decode %q: %v", b, err)
	}
	return m
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// G-1: /healthz と /info。
func TestHealthzAndInfo(t *testing.T) {
	t.Parallel()
	_, ts := startServer(t, testOptions(t))
	code, body := do(t, http.MethodGet, ts.URL+"/healthz")
	if code != http.StatusOK || string(body) != "ok" {
		t.Fatalf("healthz: %d %q", code, body)
	}
	code, body = do(t, http.MethodGet, ts.URL+"/info")
	if code != http.StatusOK {
		t.Fatalf("info: %d", code)
	}
	m := decodeMap(t, body)
	want := []string{"cgroup", "env", "goVersion", "goarch", "gomaxprocs", "goos", "hostname", "numCPU", "startedAt", "uptimeMs"}
	if got := sortedKeys(m); !reflect.DeepEqual(got, want) {
		t.Fatalf("info keys = %v, want %v", got, want)
	}
	env := m["env"].(map[string]any)
	if env["MODE"] != "single" || env["PORT"] != "8080" {
		t.Fatalf("env = %v", env)
	}
	cg := m["cgroup"].(map[string]any)
	if cg["cpuMax"] != nil || cg["memoryMax"] != nil {
		t.Fatalf("cgroup with empty root = %v, want nulls", cg)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func strp(s string) *string { return &s }

// G-1: cgroup v2・v1・どちらもない。
func TestReadCgroup(t *testing.T) {
	t.Parallel()
	v2 := t.TempDir()
	writeFile(t, filepath.Join(v2, "cpu.max"), "6250 100000\n")
	writeFile(t, filepath.Join(v2, "memory.max"), "268435456\n")
	v1 := t.TempDir()
	writeFile(t, filepath.Join(v1, "cpu", "cpu.cfs_quota_us"), "-1\n")
	writeFile(t, filepath.Join(v1, "cpu", "cpu.cfs_period_us"), "100000\n")
	writeFile(t, filepath.Join(v1, "memory", "memory.limit_in_bytes"), "4294967296\n")
	for _, tc := range []struct {
		name string
		root string
		want cgroupInfo
	}{
		{"v2", v2, cgroupInfo{CPUMax: strp("6250 100000"), MemoryMax: strp("268435456")}},
		{"v1", v1, cgroupInfo{CPUMax: strp("max 100000"), MemoryMax: strp("4294967296")}},
		{"none", t.TempDir(), cgroupInfo{}},
	} {
		if got := readCgroup(tc.root); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

// G-2: /ws のエコー。
func TestWSEcho(t *testing.T) {
	t.Parallel()
	_, ts := startServer(t, testOptions(t))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	c.SetReadLimit(1 << 20)

	if err := c.Write(ctx, websocket.MessageText, []byte(`{"seq":1,"pad":"x"}`)); err != nil {
		t.Fatal(err)
	}
	_, b, err := c.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	m := decodeMap(t, b)
	if m["seq"] != float64(1) || m["pad"] != "x" {
		t.Fatalf("echo = %s", b)
	}
	if at, ok := m["serverAt"].(float64); !ok || at <= 0 {
		t.Fatalf("serverAt missing: %s", b)
	}

	if err := c.Write(ctx, websocket.MessageText, []byte("not json")); err != nil {
		t.Fatal(err)
	}
	if _, b, err = c.Read(ctx); err != nil || string(b) != "not json" {
		t.Fatalf("plain echo = %q, %v", b, err)
	}

	exact := bytes.Repeat([]byte("a"), MaxWSMessageBytes)
	if err := c.Write(ctx, websocket.MessageText, exact); err != nil {
		t.Fatal(err)
	}
	if _, b, err = c.Read(ctx); err != nil || len(b) != MaxWSMessageBytes {
		t.Fatalf("16 KiB echo: len=%d err=%v", len(b), err)
	}

	if err := c.Write(ctx, websocket.MessageText, append(exact, 'a')); err != nil {
		t.Fatal(err)
	}
	_, _, err = c.Read(ctx)
	if got := websocket.CloseStatus(err); got != websocket.StatusMessageTooBig {
		t.Fatalf("16 KiB + 1: close status %v (err %v), want 1009", got, err)
	}
}

// G-3: /bench/raft。
func TestBenchRaftHTTP(t *testing.T) {
	t.Parallel()
	_, ts := startServer(t, testOptions(t))
	code, body := do(t, http.MethodPost, ts.URL+"/bench/raft?nodes=3&minutes=1")
	if code != http.StatusOK {
		t.Fatalf("bench/raft: %d %s", code, body)
	}
	m := decodeMap(t, body)
	if m["leaderChanges"].(float64) < 1 || m["committed"].(float64) < 1 {
		t.Fatalf("bench/raft = %s", body)
	}
	if m["wallMs"].(float64) <= 0 || m["virtualMinutesPerSecond"].(float64) <= 0 {
		t.Fatalf("bench/raft = %s", body)
	}
	for _, q := range []string{
		"nodes=0", "nodes=10", "minutes=0", "minutes=61", "proposalsPerSec=21",
		"nodes=x", "nodes=03", "nodes=3&nodes=3", "unknown=1",
	} {
		if code, body := do(t, http.MethodPost, ts.URL+"/bench/raft?"+q); code != http.StatusBadRequest {
			t.Errorf("%s: %d %s, want 400", q, code, body)
		}
	}
}

// G-4: /bench/loop と、実行中の 2 つ目の bench・/healthz。
func TestBenchLoopHTTP(t *testing.T) {
	t.Parallel()
	s, ts := startServer(t, testOptions(t))
	if code, body := do(t, http.MethodPost, ts.URL+"/bench/loop?nodes=3&speed=3&seconds=1"); code != http.StatusBadRequest {
		t.Fatalf("speed=3: %d %s", code, body)
	}

	type result struct {
		code int
		body []byte
	}
	done := make(chan result, 1)
	go func() {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/bench/loop?nodes=3&speed=8&seconds=1", nil)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			done <- result{0, []byte(err.Error())}
			return
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		done <- result{res.StatusCode, b}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for !s.busy.Load() {
		if time.Now().After(deadline) {
			t.Fatal("bench/loop did not start")
		}
		time.Sleep(time.Millisecond)
	}
	if code, body := do(t, http.MethodPost, ts.URL+"/bench/raft?nodes=3&minutes=1"); code != http.StatusConflict {
		t.Fatalf("second bench: %d %s, want 409", code, body)
	}
	if code, _ := do(t, http.MethodGet, ts.URL+"/healthz"); code != http.StatusOK {
		t.Fatalf("healthz during bench: %d", code)
	}
	r := <-done
	if r.code != http.StatusOK {
		t.Fatalf("bench/loop: %d %s", r.code, r.body)
	}
	m := decodeMap(t, r.body)
	if m["steps"].(float64) < 15 {
		t.Fatalf("steps = %v, want >= 15", m["steps"])
	}
	step := m["stepMs"].(map[string]any)
	if step["p99"].(float64) > step["max"].(float64) {
		t.Fatalf("stepMs = %v", step)
	}
	want := []string{"cpuUtil", "gc", "lateMs", "seconds", "speed", "stepMs", "steps"}
	if got := sortedKeys(m); !reflect.DeepEqual(got, want) {
		t.Fatalf("keys = %v, want %v", got, want)
	}
}

type fakeConn struct{ net.Conn }

func (fakeConn) Close() error { return nil }

// G-5: /egress は固定の宛先だけを試す。
func TestEgress(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var seen []string
	record := func(s string) {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, s)
	}
	okDeps := egressDeps{
		lookup: func(_ context.Context, host string) ([]string, error) {
			record(host)
			return []string{"192.0.2.1"}, nil
		},
		dial: func(_ context.Context, _, addr string) (net.Conn, error) { record(addr); return fakeConn{}, nil },
		get:  func(_ context.Context, url string) (int, error) { record(url); return 200, nil },
	}
	opts := testOptions(t)
	opts.egress = okDeps
	_, ts := startServer(t, opts)
	code, body := do(t, http.MethodPost, ts.URL+"/egress?host=evil.example&url=https://evil.example/")
	if code != http.StatusOK {
		t.Fatalf("egress: %d", code)
	}
	var got egressResponse
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got != (egressResponse{DNS: "ok", TCP: "ok", HTTPS: "ok"}) {
		t.Fatalf("all ok: %+v", got)
	}
	if want := []string{egressDNSHost, egressTCPAddr, egressHTTPSURL}; !reflect.DeepEqual(seen, want) {
		t.Fatalf("targets = %v, want %v", seen, want)
	}

	failed := runEgress(context.Background(), testOptions(t).egress)
	if failed.DNS == "ok" || failed.TCP == "ok" || failed.HTTPS == "ok" || failed.DNS == "" {
		t.Fatalf("all failed: %+v", failed)
	}
	if !strings.Contains(failed.HTTPS, "disabled") {
		t.Fatalf("reason missing: %+v", failed)
	}
}

// G-6: /crash は 202 の後に終了の関数をコード 3 で 1 回呼ぶ。
func TestCrash(t *testing.T) {
	t.Parallel()
	codes := make(chan int, 2)
	opts := testOptions(t)
	opts.exit = func(code int) { codes <- code }
	_, ts := startServer(t, opts)
	code, _ := do(t, http.MethodPost, ts.URL+"/crash")
	if code != http.StatusAccepted {
		t.Fatalf("crash: %d", code)
	}
	select {
	case c := <-codes:
		if c != 3 {
			t.Fatalf("exit code %d, want 3", c)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("exit not called")
	}
	select {
	case c := <-codes:
		t.Fatalf("exit called twice (%d)", c)
	case <-time.After(50 * time.Millisecond):
	}
}

// G-7: PORT の解釈。
func TestParsePort(t *testing.T) {
	t.Parallel()
	if p, err := parsePort(""); err != nil || p != 8080 {
		t.Fatalf("empty: %d %v", p, err)
	}
	if p, err := parsePort("18080"); err != nil || p != 18080 {
		t.Fatalf("18080: %d %v", p, err)
	}
	for _, bad := range []string{"0", "65536", "x", "-1", "080", " 80"} {
		if _, err := parsePort(bad); err == nil {
			t.Errorf("%q: want error", bad)
		}
	}
}

// G-7: サーバーの組み立てを 127.0.0.1:0 で確かめる。
func TestServerOnEphemeralPort(t *testing.T) {
	t.Parallel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := newServer(testOptions(t))
	srv := &http.Server{Handler: s, ReadHeaderTimeout: time.Second}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()
	code, _ := do(t, http.MethodGet, "http://"+ln.Addr().String()+"/healthz")
	if code != http.StatusOK {
		t.Fatalf("healthz: %d", code)
	}
	if code, _ := do(t, http.MethodGet, "http://"+ln.Addr().String()+"/bench/raft"); code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /bench/raft: %d, want 405", code)
	}
}

// G-8: bench raft のサブコマンドが /bench/raft と同じキーの JSON を出す。
func TestBenchSubcommand(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	if code := run([]string{"bench", "raft", "--nodes", "3", "--minutes", "1"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	cli := decodeMap(t, stdout.Bytes())
	_, ts := startServer(t, testOptions(t))
	_, body := do(t, http.MethodPost, ts.URL+"/bench/raft?nodes=3&minutes=1")
	httpRes := decodeMap(t, body)
	if a, b := sortedKeys(cli), sortedKeys(httpRes); !reflect.DeepEqual(a, b) {
		t.Fatalf("cli keys %v != http keys %v", a, b)
	}
	if cli["nodes"] != float64(3) || cli["minutes"] != float64(1) {
		t.Fatalf("cli = %v", cli)
	}

	for _, args := range [][]string{
		{"bench"}, {"bench", "nope"}, {"bench", "raft", "--nodes", "0"}, {"bench", "loop", "--speed", "3"},
		{"bench", "raft", "extra"}, {"serve"},
	} {
		stdout.Reset()
		if code := run(args, &stdout, io.Discard); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
}

func TestAddServerAt(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ in, want string }{
		{`{"a":1}`, `{"a":1,"serverAt":42}`},
		{`[1,2]`, `[1,2]`},
		{`null`, `null`},
		{`"s"`, `"s"`},
		{`{bad`, `{bad`},
	} {
		if got := string(addServerAt([]byte(tc.in), 42)); got != tc.want {
			t.Errorf("%s: got %s, want %s", tc.in, got, tc.want)
		}
	}
}
