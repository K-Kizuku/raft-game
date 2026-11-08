package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	dtnproto "github.com/K-Kizuku/raft-game/server/internal/dtn/protocol"
	"github.com/K-Kizuku/raft-game/server/internal/dtn/runtime"
)

// TestMain は、DTNACTOR_TEST_MAIN=1 のとき試験のバイナリを dtnactor の main として動かす（別のプロセスで
// 終了コードを確かめるため）。
func TestMain(m *testing.M) {
	if os.Getenv("DTNACTOR_TEST_MAIN") == "1" {
		main()
		return
	}
	os.Exit(m.Run())
}

// stubActor は試験用の Actor（何もしない）。
type stubActor struct{}

func (stubActor) Init(dtnproto.InitPayload) ([]dtnproto.Output, error) { return nil, nil }
func (stubActor) Handle(dtnproto.Kind, json.RawMessage) ([]dtnproto.Output, error) {
	return nil, nil
}
func (stubActor) Inspect() (json.RawMessage, error) { return json.RawMessage(`{}`), nil }
func (stubActor) Cursor() dtnproto.Cursor           { return dtnproto.Cursor{StateDigest: dtnproto.Digest(nil)} }

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestDtnactorListenLine(t *testing.T) {
	testRoles := map[string]roleFactory{"island": func(Env) (runtime.Actor, error) { return stubActor{}, nil }}
	pr, pw := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var stderr bytes.Buffer
	code := make(chan int, 1)
	go func() {
		code <- run(ctx, env(map[string]string{"ROLE": "island"}), pw, &stderr, testRoles)
		_ = pw.Close()
	}()

	line, err := bufio.NewReader(pr).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]string
	if err := json.Unmarshal([]byte(line), &got); err != nil || len(got) != 1 {
		t.Fatalf("line 1 = %q: %v", line, err)
	}
	host, port, err := net.SplitHostPort(got["listen"])
	if err != nil || host != "127.0.0.1" || port == "0" {
		t.Fatalf("listen = %q", got["listen"])
	}
	go func() { _, _ = io.Copy(io.Discard, pr) }()

	// /ws で往復する
	dctx, dcancel := context.WithTimeout(ctx, 5*time.Second)
	defer dcancel()
	c, _, err := websocket.Dial(dctx, "ws://"+got["listen"]+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.CloseNow() }()
	initMsg := `{"v":1,"sessionId":"s_1","actor":"island:1","epoch":1,"coordEpoch":1,"eventSeq":1,"kind":"init",` +
		`"payload":{"stage":"stage1","stageHash":"sha256:` + strings.Repeat("0", 64) + `","seed":1,"actor":"island:1"}}`
	if err := c.Write(dctx, websocket.MessageText, []byte(initMsg)); err != nil {
		t.Fatal(err)
	}
	_, b, err := c.Read(dctx)
	if err != nil {
		t.Fatal(err)
	}
	var resp dtnproto.Response
	if err := json.Unmarshal(b, &resp); err != nil || resp.Status != dtnproto.StatusOK {
		t.Fatalf("init: %s", b)
	}

	cancel()
	select {
	case c := <-code:
		if c != 0 {
			t.Fatalf("exit code %d; stderr: %s", c, stderr.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not return")
	}
}

func TestDtnactorUnknownRole(t *testing.T) {
	for _, role := range []string{"", "boat", "ISLAND", "island", "ferry", "coordinator"} {
		var stdout, stderr bytes.Buffer
		if c := run(context.Background(), env(map[string]string{"ROLE": role}), &stdout, &stderr, roles); c != 2 {
			t.Errorf("ROLE=%q: exit code %d, want 2", role, c)
		}
		if stdout.Len() != 0 {
			t.Errorf("ROLE=%q: wrote to stdout: %q", role, stdout.String())
		}
	}
	testRoles := map[string]roleFactory{"ferry": func(Env) (runtime.Actor, error) { return stubActor{}, nil }}
	for _, port := range []string{"x", "0", "70000"} {
		var stdout, stderr bytes.Buffer
		if c := run(context.Background(), env(map[string]string{"ROLE": "ferry", "PORT": port}), &stdout, &stderr, testRoles); c != 2 {
			t.Errorf("PORT=%q: exit code %d, want 2", port, c)
		}
	}
	// 別のプロセスとして動かしても、終了コード 2 で終わる
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), "DTNACTOR_TEST_MAIN=1", "ROLE=unknown")
	err := cmd.Run()
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 2 {
		t.Fatalf("subprocess: %v, want exit code 2", err)
	}
}
