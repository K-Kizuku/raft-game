package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	dtnproto "github.com/K-Kizuku/raft-game/server/internal/dtn/protocol"
)

// startServe は rt を :0 で開き、URL と、Serve の終わりを待つ関数（5 秒で諦める）を返す。
func startServe(t *testing.T, rt *Runtime) (string, func() (bool, error)) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, ln, rt) }()
	wait := sync.OnceValues(func() (bool, error) {
		select {
		case err := <-done:
			return true, err
		case <-time.After(5 * time.Second):
			return false, nil
		}
	})
	t.Cleanup(func() {
		cancel()
		if ok, _ := wait(); !ok {
			t.Error("Serve did not return")
		}
	})
	return "ws://" + ln.Addr().String() + WSPath, wait
}

func dial(t *testing.T, url string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.CloseNow() })
	return c
}

func roundTrip(t *testing.T, c *websocket.Conn, raw []byte) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Write(ctx, websocket.MessageText, raw); err != nil {
		t.Fatal(err)
	}
	typ, b, err := c.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if typ != websocket.MessageText {
		t.Fatalf("frame type %v", typ)
	}
	return b
}

func status(t *testing.T, b []byte) dtnproto.Response {
	t.Helper()
	var r dtnproto.Response
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestServeWS(t *testing.T) {
	a := &counterActor{}
	rt := newRT(t, a)
	url, done := startServe(t, rt)
	c := dial(t, url)

	if b := roundTrip(t, c, initReq(1).raw()); status(t, b).Status != dtnproto.StatusOK || len(status(t, b).Outputs) != 1 {
		t.Fatalf("init: %s", b)
	}
	first := roundTrip(t, c, advanceReq(2, 100).raw())
	if r := status(t, first); r.Status != dtnproto.StatusOK {
		t.Fatalf("advance: %s", first)
	}
	// 応答が失われたとして、別の接続から同じ eventSeq で送り直す
	c2 := dial(t, url)
	if again := roundTrip(t, c2, advanceReq(2, 100).raw()); !bytes.Equal(first, again) {
		t.Fatalf("resend differs:\n%s\n%s", first, again)
	}
	_ = rt.Handle(req{kind: dtnproto.KindInspect, payload: `{}`}.raw()) // Runtime の goroutine と同期してから a を読む
	if a.handles != 1 {
		t.Fatalf("Handle called %d times", a.handles)
	}
	// 1 MiB を超える電文には too_large を返す
	big := make([]byte, dtnproto.DefaultLimits.MaxMessageBytes+1)
	for i := range big {
		big[i] = ' '
	}
	if r := status(t, roundTrip(t, c2, big)); r.Error == nil || r.Error.Code != dtnproto.CodeTooLarge {
		t.Fatalf("big: %+v", r)
	}
	// shutdown の応答を返してから、Serve が終わる
	if r := status(t, roundTrip(t, c, req{seq: 3, kind: dtnproto.KindShutdown, payload: `{}`}.raw())); r.Status != dtnproto.StatusOK {
		t.Fatalf("shutdown: %+v", r)
	}
	if ok, err := done(); !ok || err != nil {
		t.Fatalf("Serve did not return after shutdown: %v", err)
	}
}

func TestServeRejectsBinaryAndOtherPaths(t *testing.T) {
	rt := newRT(t, &counterActor{})
	url, _ := startServe(t, rt)
	c := dial(t, url)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Write(ctx, websocket.MessageBinary, []byte("{}")); err != nil {
		t.Fatal(err)
	}
	_, _, err := c.Read(ctx)
	if websocket.CloseStatus(err) != websocket.StatusUnsupportedData {
		t.Fatalf("got %v", err)
	}
	if _, _, err := websocket.Dial(ctx, url[:len(url)-len(WSPath)]+"/other", nil); err == nil {
		t.Fatal("dial /other succeeded")
	}
}
