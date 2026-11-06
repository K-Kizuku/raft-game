package snapshot

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Git は git を dir で実行し、標準出力を返す（テストで差し替えられる）。
type Git func(ctx context.Context, dir string, args ...string) (string, error)

// RunGit は本物の git を実行する。
func RunGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("snapshot: git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errOut.String()))
	}
	return out.String(), nil
}

// worktreeDirRe は、Worker の Worktree のパス（`.claude/worktrees/issue-<N>`）。
var worktreeDirRe = regexp.MustCompile(`(?:^|/)\.claude/worktrees/issue-(\d+)$`)

// LocalWorktrees は、`git worktree list --porcelain` から、Issue に対応する Worktree を集める。
// パスが `.claude/worktrees/issue-<N>` か、ブランチが branchPrefix+N のものを Issue N の Worktree とみなす。
// 未コミットの変更（`git status --porcelain`）と、どのリモートにもないコミットの数
// （`git rev-list --count HEAD --not --remotes`）も調べる。git が失敗したらエラー（部分的な情報で判断しない）。
func LocalWorktrees(ctx context.Context, git Git, dir, branchPrefix string) ([]Worktree, error) {
	out, err := git(ctx, dir, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	type entry struct {
		path, branch string
		prunable     bool
	}
	var entries []entry
	var cur *entry
	for _, line := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		key, val, _ := strings.Cut(line, " ")
		switch key {
		case "worktree":
			entries = append(entries, entry{path: val})
			cur = &entries[len(entries)-1]
		case "branch":
			if cur != nil {
				cur.branch = strings.TrimPrefix(val, "refs/heads/")
			}
		case "prunable":
			if cur != nil {
				cur.prunable = true
			}
		}
	}

	ws := []Worktree{}
	for _, e := range entries {
		n := 0
		if m := worktreeDirRe.FindStringSubmatch(filepath.ToSlash(e.path)); m != nil {
			n, _ = strconv.Atoi(m[1])
		} else if rest, ok := strings.CutPrefix(e.branch, branchPrefix); ok && e.branch != "" {
			if v, err := strconv.Atoi(rest); err == nil {
				n = v
			}
		}
		if n <= 0 || e.prunable {
			continue
		}
		st, err := git(ctx, e.path, "status", "--porcelain")
		if err != nil {
			return nil, err
		}
		cnt, err := git(ctx, e.path, "rev-list", "--count", "HEAD", "--not", "--remotes")
		if err != nil {
			return nil, err
		}
		ahead, err := strconv.Atoi(strings.TrimSpace(cnt))
		if err != nil {
			return nil, fmt.Errorf("snapshot: %s の push していないコミットの数を読めない（%q）", e.path, cnt)
		}
		ws = append(ws, Worktree{Issue: n, Path: e.path, Branch: e.branch, Dirty: strings.TrimSpace(st) != "", Ahead: ahead})
	}
	return ws, nil
}
