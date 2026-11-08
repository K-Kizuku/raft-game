package runtime

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// WSPath は Runtime を開く WebSocket の経路（actor-protocol.md §1）。
const WSPath = "/ws"

const (
	// serveWriteTimeout は応答 1 つの書き込みのタイムアウト。
	serveWriteTimeout = 10 * time.Second
	// readHeaderTimeout は HTTP の要求の頭を読むタイムアウト。
	readHeaderTimeout = 10 * time.Second
)

// Serve は ln で WebSocket の /ws だけを開き、JSON のテキストフレーム 1 つを要求 1 つとして rt に渡し、
// 応答を 1 つのテキストフレームで返す。1 つの接続の中では届いた順に 1 つずつ処理し、接続をまたぐ要求は
// Runtime の待ち行列が届いた順に直列にする。
//
// ctx が終わるか、rt が shutdown を処理したら（その応答を書き終えてから）すべての接続を閉じて nil を返す。
// ln は Serve が閉じる。rt は閉じない（呼び出し側が Close する）。
func Serve(ctx context.Context, ln net.Listener, rt *Runtime) error {
	connCtx, cancelConns := context.WithCancel(context.Background())
	defer cancelConns()
	// inflight は、処理と書き込みの途中の接続が持つ読み取りの鍵。止めるときは書き込みの鍵で待つ
	var inflight sync.RWMutex
	// handlers は動いている接続の数。closed の後は新しい接続を受けない
	var (
		mu       sync.Mutex
		closed   bool
		handlers sync.WaitGroup
	)

	mux := http.NewServeMux()
	mux.HandleFunc(WSPath, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		if closed {
			mu.Unlock()
			http.Error(w, "shutting down", http.StatusServiceUnavailable)
			return
		}
		handlers.Add(1)
		mu.Unlock()
		defer handlers.Done()
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return // Accept が HTTP の応答を書いた
		}
		defer func() { _ = c.CloseNow() }()
		// 上限をわずかに超える電文には too_large の応答を返せるよう、読み取りの上限は 2 倍にする
		c.SetReadLimit(int64(rt.Limits().MaxMessageBytes) * 2)
		for {
			typ, data, err := c.Read(connCtx)
			if err != nil {
				return
			}
			if typ != websocket.MessageText {
				_ = c.Close(websocket.StatusUnsupportedData, "text frames only")
				return
			}
			inflight.RLock()
			resp := rt.HandleRaw(data)
			wctx, cancel := context.WithTimeout(connCtx, serveWriteTimeout)
			err = c.Write(wctx, websocket.MessageText, resp)
			cancel()
			inflight.RUnlock()
			if err != nil {
				return
			}
			select {
			case <-rt.Stopped():
				// 閉じるときの握手（相手の close フレームを待つ）はしない。プロセスはこの後に終わる
				return
			default:
			}
		}
	})
	mux.HandleFunc("/", http.NotFound)

	srv := &http.Server{Handler: mux, ReadHeaderTimeout: readHeaderTimeout}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()

	select {
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	case <-rt.Stopped():
	}
	// 書き込みの途中の応答（shutdown の応答など）を書き終えるのを待ってから、接続を閉じる
	inflight.Lock()
	cancelConns()
	inflight.Unlock()
	mu.Lock()
	closed = true
	mu.Unlock()
	_ = srv.Close()
	// srv.Close の後の Serve は http.ErrServerClosed を返す（止めた理由は ctx か shutdown なので、それ以外だけを返す）
	if err := <-errc; err != nil && !errors.Is(err, http.ErrServerClosed) {
		handlers.Wait()
		return err
	}
	handlers.Wait()
	return nil
}
