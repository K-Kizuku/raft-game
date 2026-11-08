// Command dtnactor は DTN 版の Actor の Container のエントリ（DTN-02 #159、architecture.md §1・§3）。
// image は 1 つで、環境変数 ROLE で役割を変える。
//
//	ROLE=island dtnactor        島の Actor（DTN-03。server/internal/dtn/island）
//	ROLE=ferry dtnactor         船の Actor（DTN-04。server/internal/dtn/ferryactor）
//	ROLE=coordinator dtnactor   Coordinator（DTN-05 で登録する）
//
// PORT があればそのポート（全てのアドレス。Cloudflare の Container）、なければ 127.0.0.1:0 で待ち受け、
// 標準出力の 1 行目に {"listen":"<host:port>"} を出す（actor-protocol.md §1。dtnfabric が読む）。
// ログは標準エラーに JSON で出す。WebSocket の /ws で要求を受ける（runtime.Serve）。
//
// ステージ定義は、今の server と同じく make sync-data の後に -tags embeddata で埋め込んだもの
// （DATA_DIR があればそのディレクトリ）。
//
// 終了コード: 0 = シグナルか shutdown で止めた、1 = 起動の失敗（ステージ定義・待ち受け・Actor の作成）、
// 2 = 未知の ROLE・まだ実装のない ROLE・PORT の誤り。
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/K-Kizuku/raft-game/server/internal/dtn/runtime"
	"github.com/K-Kizuku/raft-game/server/internal/stage"
)

// Env は役割の Actor を作るときの材料。
type Env struct {
	Role     string
	Data     fs.FS  // ステージ定義（islands.json と stages/ を根に持つ）。無ければ nil
	DataDesc string // ログ用の説明（"embedded" か "dir:<絶対パス>"）
	Log      *slog.Logger
}

// roleFactory は役割の Actor を作る。
type roleFactory func(env Env) (runtime.Actor, error)

// knownRoles は ROLE に書ける値。
var knownRoles = []string{"coordinator", "island", "ferry"}

// roles は実装のある役割。coordinator は DTN-05 で登録する。
var roles = map[string]roleFactory{
	"island": newIslandActor, // DTN-03（island.go）
	"ferry":  newFerryActor,  // DTN-04（ferry.go）
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Getenv, os.Stdout, os.Stderr, roles))
}

// run は終了コードを返す。
func run(ctx context.Context, getenv func(string) string, stdout, stderr io.Writer, roles map[string]roleFactory) int {
	log := slog.New(slog.NewJSONHandler(stderr, nil))
	role := getenv("ROLE")
	known := false
	for _, r := range knownRoles {
		known = known || r == role
	}
	if !known {
		log.Error("unknown ROLE", "role", role, "want", knownRoles)
		return 2
	}
	log = log.With("role", role)
	factory, ok := roles[role]
	if !ok {
		log.Error("ROLE is not implemented yet", "role", role)
		return 2
	}
	addr := "127.0.0.1:0"
	if p := getenv("PORT"); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			log.Error("invalid PORT", "port", p)
			return 2
		}
		addr = ":" + p
	}

	data, desc, err := openData(getenv("DATA_DIR"))
	if err != nil {
		log.Error("stage data", "err", err.Error())
		return 1
	}
	actor, err := factory(Env{Role: role, Data: data, DataDesc: desc, Log: log})
	if err != nil {
		log.Error("create actor", "err", err.Error())
		return 1
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Error("listen", "err", err.Error())
		return 1
	}
	line, _ := json.Marshal(struct {
		Listen string `json:"listen"`
	}{ln.Addr().String()})
	if _, err := fmt.Fprintf(stdout, "%s\n", line); err != nil {
		_ = ln.Close()
		log.Error("write listen line", "err", err.Error())
		return 1
	}
	log.Info("listening", "addr", ln.Addr().String(), "data", desc)

	rt := runtime.New(actor)
	defer rt.Close()
	if err := runtime.Serve(ctx, ln, rt); err != nil {
		log.Error("serve", "err", err.Error())
		return 1
	}
	log.Info("stopped")
	return 0
}

// openData は、dataDir が空でなければそのディレクトリを、空なら埋め込んだステージ定義を返す。
// どちらも無ければ (nil, "", nil)（ステージ定義を使わない役割もあるので、要るかは役割が決める）。
func openData(dataDir string) (fs.FS, string, error) {
	if dataDir == "" {
		if sub, ok := stage.Embedded(); ok {
			return sub, "embedded", nil
		}
		return nil, "", nil
	}
	abs, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, "", fmt.Errorf("DATA_DIR %q: %w", dataDir, err)
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return nil, "", fmt.Errorf("DATA_DIR %q: %w", dataDir, err)
	}
	if !fi.IsDir() {
		return nil, "", fmt.Errorf("DATA_DIR %q: not a directory", dataDir)
	}
	return os.DirFS(abs), "dir:" + abs, nil
}
