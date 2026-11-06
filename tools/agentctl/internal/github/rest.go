package github

import (
	"context"
	"fmt"
	"net/http"
)

// Op は書き込みの API の操作。`--dry-run` ではこれを JSON で出し、実行するときは順に送る。
type Op struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	Body   any    `json:"body,omitempty"`
}

// RepoPath は `/repos/OWNER/NAME`。
func RepoPath(repo string) string { return repoPath(repo) }

// GetJSON は path を GET し、out に JSON を読む。
func (c *Client) GetJSON(ctx context.Context, path string, out any) error {
	_, err := c.do(ctx, http.MethodGet, path, nil, out)
	return err
}

// GetAll は、JSON の配列を返す API のすべてのページを読む。
func GetAll[T any](ctx context.Context, c *Client, path string) ([]T, error) {
	return getPages(ctx, c, path, listOf[T])
}

// GetPages は、extract で各ページから要素を取り出しながら、すべてのページを読む。
func GetPages[T any](ctx context.Context, c *Client, path string, extract func([]byte) ([]T, error)) ([]T, error) {
	return getPages(ctx, c, path, extract)
}

// Apply は ops を順に送る。失敗したら、そこで止めて、送り終えた件数とエラーを返す。
func (c *Client) Apply(ctx context.Context, ops []Op) (int, error) {
	for i, op := range ops {
		if _, err := c.do(ctx, op.Method, op.Path, op.Body, nil); err != nil {
			return i, fmt.Errorf("%d 件目の操作（%s %s）で失敗: %w", i+1, op.Method, op.Path, err)
		}
	}
	return len(ops), nil
}
