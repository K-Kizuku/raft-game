package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/K-Kizuku/raft-game/server/internal/stage"
)

func noEnv(string) string { return "" }

// M-1: 起動の誤りの終了コード。
func TestRunErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
		code int
		want string
	}{
		{"MODE がない", nil, 2, "使い方"},
		{"知らないフラグ", []string{"--mode", "single", "--nope"}, 2, "使い方"},
		{"位置引数", []string{"--mode", "single", "x"}, 2, "使い方"},
		{"--port 70000", []string{"--mode", "single", "--port", "70000"}, 2, "使い方"},
		{"--data がない", []string{"--mode", "standalone", "--data", filepath.Join(t.TempDir(), "nope"), "--port", "0"}, 1, "stage data"},
		{"--data がファイル", []string{"--mode", "standalone", "--data", "testdata/islands.json", "--port", "0"}, 1, "stage data"},
		{"ステージ定義が壊れている", []string{"--mode", "standalone", "--data", "testdata/stages", "--port", "0"}, 1, "islands.json"},
		{"待ち受けの失敗", []string{"--mode", "standalone", "--data", "testdata", "--host", "256.0.0.1", "--port", "0"}, 1, "listen"},
	}
	if _, ok := stage.Embedded(); !ok {
		tests = append(tests, struct {
			name string
			args []string
			code int
			want string
		}{"--data も埋め込みもない", []string{"--mode", "single", "--port", "0"}, 1, "make sync-data"})
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var stderr bytes.Buffer
			if code := run(context.Background(), tt.args, noEnv, &stderr); code != tt.code {
				t.Fatalf("run = %d, want %d\n%s", code, tt.code, stderr.String())
			}
			if !strings.Contains(stderr.String(), tt.want) {
				t.Errorf("stderr does not contain %q:\n%s", tt.want, stderr.String())
			}
		})
	}
}

// M-1: 起動して /healthz が 200 を返し、ctx を止めると 0 で終わる。
func TestRunServesAndStops(t *testing.T) {
	t.Parallel()
	pr, pw := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	codec := make(chan int, 1)
	go func() {
		codec <- run(ctx, []string{"--mode", "standalone", "--data", "testdata", "--port", "0"}, noEnv, pw)
		_ = pw.Close()
	}()

	addrc := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(pr)
		sent := false
		for sc.Scan() { // 終わるまで読み続ける（run が書き込みで止まらないように）
			var line struct {
				Msg  string `json:"msg"`
				Addr string `json:"addr"`
			}
			if json.Unmarshal(sc.Bytes(), &line) == nil && line.Msg == "listening" && !sent {
				addrc <- line.Addr
				sent = true
			}
		}
	}()

	var addr string
	select {
	case addr = <-addrc:
	case code := <-codec:
		t.Fatalf("run returned %d before listening", code)
	case <-time.After(10 * time.Second):
		t.Fatal("no listening log")
	}
	res, err := http.Get("http://" + addr + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(body), `"status":"ok"`) {
		t.Fatalf("GET /healthz = %d %s", res.StatusCode, body)
	}

	cancel()
	select {
	case code := <-codec:
		if code != 0 {
			t.Errorf("run = %d, want 0", code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run did not return")
	}
}
