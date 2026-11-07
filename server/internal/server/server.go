package server

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/K-Kizuku/raft-game/server/internal/protocol"
)

// 時間の内部の値（テストで差し替える）。
const (
	defaultInitTimeout   = 10 * time.Second // single: 接続から最初の internal.init・internal.restore までの上限
	defaultIdleTTL       = 5 * time.Minute  // standalone: 接続のないセッションを保持する時間（DesignDoc §12.5）
	defaultSweepInterval = 30 * time.Second // standalone: 片付けを調べる間隔
	shutdownTimeout      = 5 * time.Second
)

// Server は HTTP の振り分けと、セッションの持ち主（single の枠か standalone の一覧）。
type Server struct {
	cfg Config
	cat *Catalog
	log *slog.Logger

	// テストで差し替える内部の値。
	now           func() time.Time
	initTimeout   time.Duration
	idleTTL       time.Duration
	sweepInterval time.Duration
	handleHook    func(protocol.Envelope) // セッションの handle の最初に呼ぶ（panic の試験用）

	baseCtx    context.Context // セッションの親。Serve の終わりに止める
	baseCancel context.CancelFunc

	single     *singleHost
	standalone *standaloneHost

	mu       sync.Mutex
	conns    map[*wsConn]chan struct{} // つながっている接続（停止で shutting_down を送る）→ ハンドラーが終わったら閉じる
	stopping bool
}

// New はサーバーを作る。cfg.Mode で振り分けが決まる。
func New(cfg Config, cat *Catalog, log *slog.Logger) *Server {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{
		cfg:           cfg,
		cat:           cat,
		log:           log,
		now:           time.Now,
		initTimeout:   defaultInitTimeout,
		idleTTL:       defaultIdleTTL,
		sweepInterval: defaultSweepInterval,
		baseCtx:       ctx,
		baseCancel:    cancel,
		conns:         map[*wsConn]chan struct{}{},
	}
	switch cfg.Mode {
	case ModeStandalone:
		s.standalone = &standaloneHost{srv: s, sessions: map[string]*standaloneEntry{}}
	default:
		s.single = &singleHost{srv: s}
	}
	return s
}

// Handler は HTTP のハンドラーを返す。
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.Mode == ModeStandalone {
			s.serveStandalone(w, r)
			return
		}
		s.serveSingle(w, r)
	})
}

// Serve は ln で待ち受け、ctx が終わったら止める（すべての接続に shutting_down を送り、
// http.Server.Shutdown を 5 秒で呼び、セッションの goroutine を止める）。止めたら nil。
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	hs := &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		MaxHeaderBytes:    16 << 10,
		IdleTimeout:       60 * time.Second,
		ErrorLog:          slog.NewLogLogger(s.log.Handler(), slog.LevelWarn),
	}
	errc := make(chan error, 1)
	go func() { errc <- hs.Serve(ln) }()

	sweepDone := make(chan struct{})
	sweepCtx, stopSweep := context.WithCancel(context.Background())
	if s.standalone != nil {
		go func() {
			defer close(sweepDone)
			s.standalone.sweepLoop(sweepCtx)
		}()
	} else {
		close(sweepDone)
	}

	var serveErr error
	select {
	case <-ctx.Done():
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			serveErr = err
		}
	}
	stopSweep()
	<-sweepDone

	deadline, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	s.shutdownConns()
	if err := hs.Shutdown(deadline); err != nil {
		s.log.Warn("http shutdown", "err", err.Error())
	}
	s.waitHandlers(deadline)
	s.closeSessions()
	if serveErr != nil {
		return serveErr
	}
	return nil
}

// closeSessions はセッションの goroutine をすべて止める（Serve が最後に呼ぶ。Handler だけを使うテストの後始末にも使う）。
func (s *Server) closeSessions() {
	s.baseCancel()
	if s.single != nil {
		s.single.stop()
	}
	if s.standalone != nil {
		s.standalone.stopAll()
	}
}

// shutdownConns は、つながっている接続すべてに shutting_down（fatal、1001）を送る。以後の接続も同じ。
func (s *Server) shutdownConns() {
	s.mu.Lock()
	s.stopping = true
	conns := make([]*wsConn, 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c)
	}
	// 待ち受けを止める前に入った接続は track で受ける（stopping なら shutting_down を送る）。
	s.mu.Unlock()
	for _, c := range conns {
		c.fatal(shutdownError())
	}
}

func shutdownError() *fatalError {
	return &fatalError{code: codeShuttingDown, message: "server is shutting down", close: closeGoingAway}
}

// waitHandlers は WebSocket のハンドラーが終わるのを ctx まで待つ。
func (s *Server) waitHandlers(ctx context.Context) {
	for {
		var released chan struct{}
		s.mu.Lock()
		for _, ch := range s.conns {
			released = ch
			break
		}
		s.mu.Unlock()
		if released == nil {
			return
		}
		select {
		case <-released:
		case <-ctx.Done():
			s.log.Warn("websocket handlers did not finish in time")
			return
		}
	}
}

// track は接続を登録する。止めている途中なら shutting_down を送る。untrack を defer で呼ぶ。
func (s *Server) track(c *wsConn) {
	s.mu.Lock()
	s.conns[c] = make(chan struct{})
	stopping := s.stopping
	s.mu.Unlock()
	if stopping {
		c.fatal(shutdownError())
	}
}

func (s *Server) untrack(c *wsConn) {
	s.mu.Lock()
	if ch, ok := s.conns[c]; ok {
		close(ch)
		delete(s.conns, c)
	}
	s.mu.Unlock()
}

// sessionParams は、このサーバーの設定を入れたセッションの値を返す。
func (s *Server) sessionParams() sessionParams {
	return sessionParams{
		mode:  s.cfg.Mode,
		build: s.cfg.Build,
		log:   s.log,
		now:   s.now,
		hook:  s.handleHook,
	}
}
