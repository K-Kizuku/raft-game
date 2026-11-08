package gate

import (
	"fmt"
	"strings"

	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/config"
	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/issuebody"
)

// SmallPR は、PR が「小さな PR」（レビューを省き、CI だけでマージする。Issue #243）かを判定する。
// 小さな PR なら ok が true で、reason にその理由を返す。そうでなければ ok が false で、reason に満たさない条件を返す。
//
// 条件（すべて満たすとき）:
//  1. 設定 review.skip_small_prs が true
//  2. 変更ファイルの一覧が完全（changed_files と一覧の件数が同じで、1 件以上）
//  3. 変更ファイルがすべて review.skip_paths の glob のどれかに一致し、config.NeverSkipPaths（docs/adr/**）に一致しない
//     （名前を変えたファイルは、前と後の両方の名前で確かめる）
//  4. 保護されたパス（protected_paths）を 1 つも変えていない
//  5. 追加と削除の行数の合計が分かり、review.skip_max_lines 以下
//
// 判定の材料が不完全なときは、小さな PR にしない（安全側）。
func SmallPR(cfg *config.Config, in Input) (ok bool, reason string) {
	if cfg == nil {
		return false, "設定がない"
	}
	r := cfg.Review
	if !r.SkipSmallPRs {
		return false, "review.skip_small_prs が false"
	}
	pr := in.PullRequest
	if len(in.Files) == 0 || pr.ChangedFiles != len(in.Files) {
		return false, fmt.Sprintf("変更ファイルの一覧が不完全（%d 件中 %d 件）", pr.ChangedFiles, len(in.Files))
	}
	paths := filePaths(in.Files)
	skip := r.SmallPRSkipPaths()
	var outside, never []string
	for _, p := range paths {
		if strings.TrimSpace(p) == "" {
			return false, "変更ファイルに名前のないものがある"
		}
		if hit, err := issuebody.MatchAny(config.NeverSkipPaths, p); err != nil || hit {
			never = append(never, p)
			continue
		}
		if hit, err := issuebody.MatchAny(skip, p); err != nil || !hit {
			outside = append(outside, p)
		}
	}
	if len(never) > 0 {
		return false, "レビューを省かないパスの変更がある: " + strings.Join(never, ", ")
	}
	if len(outside) > 0 {
		return false, "review.skip_paths に一致しない変更ファイルがある: " + strings.Join(outside, ", ")
	}
	if hit := ProtectedFiles(cfg, in.Files); len(hit) > 0 {
		return false, "保護されたパスの変更がある: " + strings.Join(hit, ", ")
	}
	if pr.Additions == nil || pr.Deletions == nil {
		return false, "追加と削除の行数が分からない"
	}
	if *pr.Additions < 0 || *pr.Deletions < 0 {
		return false, fmt.Sprintf("追加と削除の行数が不正（+%d −%d）", *pr.Additions, *pr.Deletions)
	}
	lines, limit := *pr.Additions+*pr.Deletions, r.SmallPRMaxLines()
	if lines > limit {
		return false, fmt.Sprintf("追加と削除が %d 行（review.skip_max_lines の %d 行を超える）", lines, limit)
	}
	return true, fmt.Sprintf("%d ファイルがすべて review.skip_paths に一致・保護されたパスの変更なし・追加と削除が %d 行（上限 %d 行）",
		len(in.Files), lines, limit)
}
