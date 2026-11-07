package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/K-Kizuku/raft-game/server/internal/protocol"
)

// SV-1: Serve を止めると、接続に shutting_down（1001）が届き、5 秒のうちに nil を返す。
func TestServeShutdown(t *testing.T) {
	t.Parallel()
	for _, mode := range []Mode{ModeSingle, ModeStandalone} {
		t.Run(string(mode), func(t *testing.T) {
			t.Parallel()
			cfg := Config{Mode: mode, MaxSessions: defaultMaxSessions, Build: testBuild}
			srv := New(cfg, testCatalog(t), slog.New(slog.NewJSONHandler(&logBuffer{}, nil)))
			srv.sweepInterval = time.Millisecond // 片付けの goroutine も動かす
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			errc := make(chan error, 1)
			go func() { errc <- srv.Serve(ctx, ln) }()

			addr := ln.Addr().String()
			var c *client
			if mode == ModeSingle {
				c = dial(t, "ws://"+addr+"/ws", nil)
				c.initSingle("s_sv.1", 1)
			} else {
				cr := createOn(t, "http://"+addr)
				c = dial(t, "ws://"+addr+cr.WSPath+"?token="+cr.Token, http.Header{"Origin": {"http://" + addr}})
				c.pingPong()
			}

			start := time.Now()
			cancel()
			c.expectFatal(codeShuttingDown, websocket.StatusGoingAway)
			select {
			case err := <-errc:
				if err != nil {
					t.Errorf("Serve = %v", err)
				}
			case <-time.After(shutdownTimeout + time.Second):
				t.Fatal("Serve did not return")
			}
			if d := time.Since(start); d > shutdownTimeout {
				t.Errorf("shutdown took %v", d)
			}
		})
	}
}

// createOn は base の standalone のサーバーでセッションを作る。
func createOn(t *testing.T, base string) protocol.CreateSessionResponse {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, base+"/api/sessions", strings.NewReader(`{"stageId":"alpha"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", base)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("POST /api/sessions = %d", res.StatusCode)
	}
	var cr protocol.CreateSessionResponse
	if err := json.NewDecoder(res.Body).Decode(&cr); err != nil {
		t.Fatal(err)
	}
	return cr
}
