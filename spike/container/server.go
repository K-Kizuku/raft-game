package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/K-Kizuku/raft-game/spike/container/loadmodel"
)

// DefaultPort は PORT が未設定のときの待ち受けのポート（コンテナの中だけで使う）。
const DefaultPort = 8080

// options はサーバーの差し替え口（テストで偽物を渡す）。
type options struct {
	cgroupRoot string // cgroup の読む元（既定 /sys/fs/cgroup）
	getenv     func(string) string
	hostname   func() (string, error)
	startedAt  time.Time
	egress     egressDeps
	exit       func(code int) // /crash で呼ぶ（既定 os.Exit）
	crashDelay time.Duration
	logger     *log.Logger
}

func defaultOptions() options {
	return options{
		cgroupRoot: "/sys/fs/cgroup",
		getenv:     os.Getenv,
		hostname:   os.Hostname,
		startedAt:  time.Now(),
		egress:     defaultEgressDeps(),
		exit:       os.Exit,
		crashDelay: 100 * time.Millisecond,
		logger:     log.New(os.Stderr, "", log.LstdFlags|log.LUTC),
	}
}

// server はコンテナの HTTP の処理。
type server struct {
	opts  options
	busy  atomic.Bool // bench は同時に 1 つ
	mux   *http.ServeMux
	shut  context.Context
	close context.CancelFunc
}

func newServer(opts options) *server {
	s := &server{opts: opts, mux: http.NewServeMux()}
	s.shut, s.close = context.WithCancel(context.Background())
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)
	s.mux.HandleFunc("GET /info", s.handleInfo)
	s.mux.HandleFunc("GET /ws", s.handleWS)
	s.mux.HandleFunc("POST /bench/raft", s.handleBenchRaft)
	s.mux.HandleFunc("POST /bench/loop", s.handleBenchLoop)
	s.mux.HandleFunc("POST /egress", s.handleEgress)
	s.mux.HandleFunc("POST /crash", s.handleCrash)
	return s
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

func (s *server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("content-type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok"))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("content-type", "application/json")
	w.Header().Set("cache-control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]string{"error": code, "message": msg})
}

// query は引数の検査の補助。知らない名前・重複・整数でない値をエラーにする。
type query struct {
	v       url.Values
	allowed map[string]bool
	err     error
}

func newQuery(v url.Values, names ...string) *query {
	q := &query{v: v, allowed: map[string]bool{}}
	for _, n := range names {
		q.allowed[n] = true
	}
	for k, vals := range v {
		if !q.allowed[k] {
			q.fail(fmt.Errorf("unknown parameter %q", k))
		} else if len(vals) != 1 {
			q.fail(fmt.Errorf("parameter %q must be given once", k))
		}
	}
	return q
}

func (q *query) fail(err error) {
	if q.err == nil {
		q.err = err
	}
}

func (q *query) intOr(name string, def int) int {
	raw, ok := q.v[name]
	if !ok || len(raw) == 0 {
		return def
	}
	n, err := parseStrictInt(raw[0])
	if err != nil {
		q.fail(fmt.Errorf("parameter %q: %w", name, err))
		return def
	}
	return n
}

func (q *query) stringOr(name, def string) string {
	raw, ok := q.v[name]
	if !ok || len(raw) == 0 {
		return def
	}
	return raw[0]
}

// parseStrictInt は 10 進の整数だけを受け付ける（先頭の 0・符号・空白を許さない。"0" は可）。
func parseStrictInt(s string) (int, error) {
	if s == "" || len(s) > 10 {
		return 0, fmt.Errorf("not an integer: %q", s)
	}
	for i, ch := range s {
		if ch < '0' || ch > '9' || (i == 0 && ch == '0' && len(s) > 1) {
			return 0, fmt.Errorf("not an integer: %q", s)
		}
	}
	return strconv.Atoi(s)
}

// parsePort は PORT の値を解釈する。未設定なら DefaultPort。
func parsePort(raw string) (int, error) {
	if raw == "" {
		return DefaultPort, nil
	}
	n, err := parseStrictInt(raw)
	if err != nil || n < 1 || n > 65535 {
		return 0, fmt.Errorf("PORT must be an integer in 1..65535, got %q", raw)
	}
	return n, nil
}

func (s *server) tryBusy(w http.ResponseWriter) bool {
	if !s.busy.CompareAndSwap(false, true) {
		writeError(w, http.StatusConflict, "bench_running", "another bench is running")
		return false
	}
	return true
}

func (s *server) handleBenchRaft(w http.ResponseWriter, r *http.Request) {
	q := newQuery(r.URL.Query(), "nodes", "minutes", "proposalsPerSec")
	cfg := loadmodel.RaftConfig{
		Nodes:           q.intOr("nodes", loadmodel.DefaultNodes),
		Minutes:         q.intOr("minutes", loadmodel.DefaultMinutes),
		ProposalsPerSec: q.intOr("proposalsPerSec", loadmodel.DefaultProposalsPerSec),
	}
	if q.err == nil {
		q.fail(cfg.Validate())
	}
	if q.err != nil {
		writeError(w, http.StatusBadRequest, "bad_param", q.err.Error())
		return
	}
	if !s.tryBusy(w) {
		return
	}
	defer s.busy.Store(false)
	res, err := loadmodel.RunRaft(cfg)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "bench_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *server) handleBenchLoop(w http.ResponseWriter, r *http.Request) {
	q := newQuery(r.URL.Query(), "nodes", "speed", "seconds", "proposalsPerSec", "eventsPerFlush")
	cfg := loadmodel.LoopConfig{
		Nodes:           q.intOr("nodes", loadmodel.DefaultNodes),
		Speed:           q.stringOr("speed", loadmodel.DefaultSpeed),
		Seconds:         q.intOr("seconds", loadmodel.DefaultSeconds),
		ProposalsPerSec: q.intOr("proposalsPerSec", loadmodel.DefaultProposalsPerSec),
		EventsPerFlush:  q.intOr("eventsPerFlush", loadmodel.DefaultEventsPerFlush),
	}
	if q.err == nil {
		q.fail(cfg.Validate())
	}
	if q.err != nil {
		writeError(w, http.StatusBadRequest, "bad_param", q.err.Error())
		return
	}
	if !s.tryBusy(w) {
		return
	}
	defer s.busy.Store(false)
	res, err := loadmodel.RunLoop(r.Context(), cfg)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "bench_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *server) handleCrash(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusAccepted, map[string]any{"crashing": true, "exitCode": 3})
	s.opts.logger.Printf(`{"event":"crash","exitCode":3}`)
	time.AfterFunc(s.opts.crashDelay, func() { s.opts.exit(3) })
}
