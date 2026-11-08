// Command container は、スパイク（T-007a / #134）のコンテナ。
//
//	container                                   PORT（既定 8080）で HTTP を待ち受ける
//	container bench raft [--nodes 7] [--minutes 30] [--proposals-per-sec 2]
//	container bench loop [--nodes 7] [--speed 8] [--seconds 20] [--proposals-per-sec 2] [--events-per-flush 20]
//
// bench は /bench/raft・/bench/loop と同じ処理を手元で実行し、同じ形の JSON を標準出力に出す
// （M-2・M-3 の手元の基準値）。測定用で、server/internal/sim の決定性の規則の対象ではない。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/K-Kizuku/raft-game/spike/container/loadmodel"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		if args[0] != "bench" {
			fmt.Fprintf(stderr, "unknown command %q (use no arguments or \"bench raft|loop\")\n", args[0])
			return 2
		}
		return runBench(args[1:], stdout, stderr)
	}
	port, err := parsePort(os.Getenv("PORT"))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if err := serve(port); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func serve(port int) error {
	opts := defaultOptions()
	s := newServer(opts)
	ln, err := net.Listen("tcp", net.JoinHostPort("", strconv.Itoa(port)))
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: s, ReadHeaderTimeout: 10 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	opts.logger.Printf(`{"event":"listening","port":%d}`, port)
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	opts.logger.Printf(`{"event":"shutdown"}`)
	s.close()
	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		return err
	}
	if err := <-errc; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func runBench(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: bench raft|loop [flags]")
		return 2
	}
	fs := flag.NewFlagSet("bench "+args[0], flag.ContinueOnError)
	fs.SetOutput(stderr)
	nodes := fs.Int("nodes", loadmodel.DefaultNodes, "number of nodes (1..9)")
	pps := fs.Int("proposals-per-sec", loadmodel.DefaultProposalsPerSec, "proposals per game second (0..20)")
	parse := func() bool {
		if err := fs.Parse(args[1:]); err != nil {
			return false
		}
		if fs.NArg() > 0 {
			fmt.Fprintf(stderr, "unexpected arguments: %v\n", fs.Args())
			return false
		}
		return true
	}
	var result any
	var err error
	switch args[0] {
	case "raft":
		minutes := fs.Int("minutes", loadmodel.DefaultMinutes, "game minutes to replay (1..60)")
		if !parse() {
			return 2
		}
		result, err = loadmodel.RunRaft(loadmodel.RaftConfig{Nodes: *nodes, Minutes: *minutes, ProposalsPerSec: *pps})
	case "loop":
		speed := fs.String("speed", loadmodel.DefaultSpeed, "speed (0.5, 1, 2, 4, 8)")
		seconds := fs.Int("seconds", loadmodel.DefaultSeconds, "wall seconds (1..60)")
		events := fs.Int("events-per-flush", loadmodel.DefaultEventsPerFlush, "events per 100ms flush (0..1000)")
		if !parse() {
			return 2
		}
		result, err = loadmodel.RunLoop(context.Background(), loadmodel.LoopConfig{
			Nodes: *nodes, Speed: *speed, Seconds: *seconds, ProposalsPerSec: *pps, EventsPerFlush: *events,
		})
	default:
		fmt.Fprintf(stderr, "unknown bench %q (use raft or loop)\n", args[0])
		return 2
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(result); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}
