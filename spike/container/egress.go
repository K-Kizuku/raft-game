package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

// 外部への通信の確認（M-9）の宛先。引数で受け取らず、ここに固定する。
const (
	egressDNSHost  = "example.com"
	egressTCPAddr  = "1.1.1.1:443"
	egressHTTPSURL = "https://example.com/"
	egressTimeout  = 3 * time.Second
)

// egressDeps は名前解決・接続・HTTP の差し替え口（テストで本物のネットワークを使わない）。
type egressDeps struct {
	lookup func(ctx context.Context, host string) ([]string, error)
	dial   func(ctx context.Context, network, addr string) (net.Conn, error)
	get    func(ctx context.Context, url string) (int, error)
}

func defaultEgressDeps() egressDeps {
	var resolver net.Resolver
	var dialer net.Dialer
	client := &http.Client{Timeout: egressTimeout}
	return egressDeps{
		lookup: resolver.LookupHost,
		dial:   dialer.DialContext,
		get: func(ctx context.Context, url string) (int, error) {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			if err != nil {
				return 0, err
			}
			res, err := client.Do(req)
			if err != nil {
				return 0, err
			}
			defer res.Body.Close()
			_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 1<<16))
			return res.StatusCode, nil
		},
	}
}

type egressResponse struct {
	DNS   string `json:"dns"`
	TCP   string `json:"tcp"`
	HTTPS string `json:"https"`
}

// handleEgress は固定の 3 つの宛先への通信を試し、ok か失敗の理由を返す。引数は見ない。
func (s *server) handleEgress(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, runEgress(r.Context(), s.opts.egress))
}

func runEgress(parent context.Context, d egressDeps) egressResponse {
	try := func(f func(ctx context.Context) error) string {
		ctx, cancel := context.WithTimeout(parent, egressTimeout)
		defer cancel()
		if err := f(ctx); err != nil {
			return err.Error()
		}
		return "ok"
	}
	return egressResponse{
		DNS: try(func(ctx context.Context) error {
			addrs, err := d.lookup(ctx, egressDNSHost)
			if err != nil {
				return err
			}
			if len(addrs) == 0 {
				return fmt.Errorf("no addresses for %s", egressDNSHost)
			}
			return nil
		}),
		TCP: try(func(ctx context.Context) error {
			c, err := d.dial(ctx, "tcp", egressTCPAddr)
			if err != nil {
				return err
			}
			return c.Close()
		}),
		HTTPS: try(func(ctx context.Context) error {
			code, err := d.get(ctx, egressHTTPSURL)
			if err != nil {
				return err
			}
			if code < 200 || code > 399 {
				return fmt.Errorf("unexpected status %d", code)
			}
			return nil
		}),
	}
}
