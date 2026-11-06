// Package github は、Merge Gate が使う GitHub の REST API の最小限のクライアント。
//
// どの API でも、失敗（レート制限・5xx・通信の失敗・ページ送りの異常）はエラーとして返す。
// 呼び出し側は、部分的な情報で判定しない（内部エラーとして終了コード 3 にする）。
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// DefaultBaseURL は GitHub の REST API の URL。
const DefaultBaseURL = "https://api.github.com"

// maxPages はページ送りの上限（100 件 × 100 ページ）。超えたらエラーにする。
const maxPages = 100

// ErrNotFound は 404。
var ErrNotFound = errors.New("github: 見つからない（404）")

// APIError は 2xx 以外の応答。
type APIError struct {
	Method, URL string
	StatusCode  int
	Message     string
	RateLimited bool
}

func (e *APIError) Error() string {
	s := fmt.Sprintf("github: %s %s: %d %s", e.Method, e.URL, e.StatusCode, e.Message)
	if e.RateLimited {
		s += "（レート制限）"
	}
	return s
}

// Client は REST API のクライアント。
type Client struct {
	base  *url.URL
	token string
	HTTP  *http.Client
	// Sleep は待ち（テストで差し替える）。
	Sleep func(time.Duration)
}

// New はクライアントを作る。baseURL が空なら DefaultBaseURL。
func New(baseURL, token string) (*Client, error) {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	u, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, fmt.Errorf("github: 不正な API の URL %q", baseURL)
	}
	if token == "" {
		return nil, errors.New("github: トークンがない（GITHUB_TOKEN か GH_TOKEN を設定する）")
	}
	return &Client{
		base:  u,
		token: token,
		HTTP:  &http.Client{Timeout: 30 * time.Second},
		Sleep: time.Sleep,
	}, nil
}

var repoRe = regexp.MustCompile(`^[A-Za-z0-9-]+/([A-Za-z0-9_.-]+)$`)

// ValidRepo は OWNER/NAME の形かを返す。
func ValidRepo(repo string) bool {
	m := repoRe.FindStringSubmatch(repo)
	return m != nil && m[1] != "." && m[1] != ".."
}

func (c *Client) resolve(pathOrURL string) (*url.URL, error) {
	if strings.HasPrefix(pathOrURL, "http://") || strings.HasPrefix(pathOrURL, "https://") {
		u, err := url.Parse(pathOrURL)
		if err != nil {
			return nil, err
		}
		// トークンをほかのホストへ送らない。
		if u.Scheme != c.base.Scheme || u.Host != c.base.Host {
			return nil, fmt.Errorf("github: API と別のホストの URL %q", pathOrURL)
		}
		return u, nil
	}
	ref, err := url.Parse(pathOrURL)
	if err != nil {
		return nil, err
	}
	u := *c.base
	u.Path = c.base.Path + ref.Path
	u.RawPath = ""
	if ref.RawPath != "" {
		u.RawPath = c.base.EscapedPath() + ref.RawPath
	}
	u.RawQuery = ref.RawQuery
	return &u, nil
}

// do はリクエストを送り、out に JSON を読む。応答のヘッダーを返す。
func (c *Client) do(ctx context.Context, method, pathOrURL string, body, out any) (http.Header, error) {
	u, err := c.resolve(pathOrURL)
	if err != nil {
		return nil, err
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("User-Agent", "raft-game-agentctl")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github: %s %s: %w", method, u.Path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, fmt.Errorf("github: %s %s: 応答を読めない: %w", method, u.Path, err)
	}
	if resp.StatusCode == http.StatusNotFound {
		return resp.Header, ErrNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var m struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(data, &m)
		return resp.Header, &APIError{
			Method: method, URL: u.Path, StatusCode: resp.StatusCode, Message: m.Message,
			RateLimited: resp.StatusCode == http.StatusTooManyRequests ||
				(resp.StatusCode == http.StatusForbidden && (resp.Header.Get("X-RateLimit-Remaining") == "0" || strings.Contains(strings.ToLower(m.Message), "rate limit"))),
		}
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return resp.Header, fmt.Errorf("github: %s %s: JSON を読めない: %w", method, u.Path, err)
		}
	}
	return resp.Header, nil
}

var linkNextRe = regexp.MustCompile(`<([^>]+)>\s*;\s*rel="next"`)

func nextLink(h http.Header) string {
	for _, v := range h.Values("Link") {
		if m := linkNextRe.FindStringSubmatch(v); m != nil {
			return m[1]
		}
	}
	return ""
}

// getPages は Link ヘッダーの rel="next" をたどり、すべてのページを読む。
func getPages[T any](ctx context.Context, c *Client, path string, extract func([]byte) ([]T, error)) ([]T, error) {
	var out []T
	next := path
	for page := 0; next != ""; page++ {
		if page >= maxPages {
			return nil, fmt.Errorf("github: %s のページが %d を超えた", path, maxPages)
		}
		var raw json.RawMessage
		h, err := c.do(ctx, http.MethodGet, next, nil, &raw)
		if err != nil {
			return nil, err
		}
		items, err := extract(raw)
		if err != nil {
			return nil, fmt.Errorf("github: %s: %w", path, err)
		}
		out = append(out, items...)
		next = nextLink(h)
	}
	return out, nil
}

func listOf[T any](raw []byte) ([]T, error) {
	var v []T
	err := json.Unmarshal(raw, &v)
	return v, err
}

func esc(s string) string { return url.PathEscape(s) }
