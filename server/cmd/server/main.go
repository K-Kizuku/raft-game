// Command server は Go のゲームサーバー（DesignDoc §12.1、T-302）。
//
//	MODE=single PORT=8080 server                         本番（コンテナの中。Durable Object からだけつながる）
//	MODE=standalone PORT=18080 DATA_DIR=data server      ローカル開発（make dev）とデモの保険
//
// 設定はフラグと環境変数（フラグが優先）。一覧は server --help。
// ビルドの識別子は go build -ldflags "-X main.build=git:3f2c1ab" で入れる（なければ VCS の情報か "dev"）。
// ステージ定義は --data（DATA_DIR）のディレクトリか、make sync-data の後に -tags embeddata で埋め込んだもの。
//
// 終了コード: 0 = シグナルで止めた、1 = 起動の失敗（ステージ定義・待ち受け）、2 = 使い方の誤り。
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/K-Kizuku/raft-game/server/internal/server"
)

// build はビルドの識別子（-ldflags "-X main.build=…"）。
var build string

// defaultMaxSessions は server.ParseConfig の MaxSessions の既定値（single で warn を出す判定に使う）。
const defaultMaxSessions = 16

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Getenv, os.Stderr))
}

// run は終了コードを返す。0: ctx で止めた、1: 起動の失敗（ステージ定義・待ち受け）、2: 使い方の誤り。
func run(ctx context.Context, args []string, getenv func(string) string, stderr io.Writer) int {
	cfg, err := server.ParseConfig(args, getenv)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		if errors.Is(err, server.ErrUsage) {
			return 2
		}
		return 1
	}
	log := slog.New(slog.NewJSONHandler(stderr, &slog.HandlerOptions{Level: cfg.LogLevel}))
	cfg.Build = server.BuildID(build)
	log = log.With("build", cfg.Build)

	if cfg.Mode == server.ModeSingle &&
		(len(cfg.AllowedOrigins) > 0 || cfg.DemoControls || cfg.MaxSessions != defaultMaxSessions) {
		log.Warn("standalone-only settings are ignored in single mode",
			"allowedOrigins", len(cfg.AllowedOrigins), "demoControls", cfg.DemoControls, "maxSessions", cfg.MaxSessions)
	}

	fsys, desc, err := server.OpenData(cfg.DataDir)
	if err != nil {
		log.Error("stage data", "err", err.Error())
		return 1
	}
	cat, err := server.LoadCatalog(fsys)
	if err != nil {
		log.Error("stage data", "data", desc, "err", err.Error())
		return 1
	}
	for _, w := range cat.Warnings() {
		log.Warn("stage warning", "warning", w)
	}
	stages := len(cat.Stages())
	if stages == 0 {
		log.Warn("no stages", "data", desc)
	}

	ln, err := net.Listen("tcp", net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)))
	if err != nil {
		log.Error("listen", "err", err.Error())
		return 1
	}
	log.Info("listening", "mode", string(cfg.Mode), "addr", ln.Addr().String(), "data", desc, "stages", stages)

	if err := server.New(cfg, cat, log).Serve(ctx, ln); err != nil {
		log.Error("serve", "err", err.Error())
		return 1
	}
	log.Info("stopped")
	return 0
}
