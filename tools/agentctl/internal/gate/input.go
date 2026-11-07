package gate

// Input は判定に使う GitHub の状態のスナップショット。
// `agentctl gate --input` は、これを JSON にしたファイルを読む。例は testdata/pass.json。
type Input struct {
	Repo        string      `json:"repo"`
	PullRequest PullRequest `json:"pull_request"`
	// Files は PR の変更ファイル（GET /pulls/{n}/files の全ページ）。
	Files []File `json:"files"`
	// Issue は PR の本文の `Closes #N` でリンクした Issue。見つからなければ null。
	Issue *Issue `json:"issue"`
	// CheckRuns は HEAD の SHA の check run（GET /commits/{sha}/check-runs の全ページ）。
	CheckRuns []CheckRun `json:"check_runs"`
	// Statuses は HEAD の SHA のコミットステータス（コンテキストごとの最新。GET /commits/{sha}/status）。
	Statuses []Status `json:"statuses"`
	// Comments は PR のコメント（GET /issues/{n}/comments の全ページ）。
	Comments []Comment `json:"comments"`
	// Reviews は PR のレビュー（GET /pulls/{n}/reviews の全ページ）。
	Reviews []PullReview `json:"reviews"`
	// ReviewComments は PR の行コメント（GET /pulls/{n}/comments の全ページ）。
	ReviewComments []ReviewComment `json:"review_comments"`
	// Compare は GET /compare/{base}...{head} の結果。
	Compare *Compare `json:"compare"`
	// CarryOver は、HEAD より前の SHA のレビュー記録を引き継げるか（C6。Issue #120）の判定の材料。
	// 最新のレビューが HEAD より前の SHA のもので、設定 review.carry_over_on_base_merge が true のときだけ集める。
	// なければ引き継がない。
	CarryOver *CarryOverInput `json:"carry_over,omitempty"`
}

// CarryOverInput は、レビュー記録の引き継ぎの判定の材料。
type CarryOverInput struct {
	// SHA は、引き継ぎの候補（最新のレビューの SHA。以下 S）。
	SHA string `json:"sha"`
	// Commits は、HEAD から最初の親をたどって S に着くまでのコミット（HEAD から順に。S は含まない）。
	// マージではないコミットに当たったら、そこで止める（そのコミットまでを含む）。
	Commits []Commit `json:"commits"`
	// InBase は、マージコミットの 2 番目以降の親の SHA ごとに、それがベースのブランチの履歴に含まれるか
	// （GET /compare/{base}...{親} の ahead_by が 0）。
	InBase map[string]bool `json:"in_base"`
	// Files は、ベースとのマージベースから S までの変更ファイル（GET /compare/{base}...{S} の files。patch を含む）。
	Files []File `json:"files"`
	// Error は、材料を集められなかった理由（API の誤りなど）。空でなければ引き継がない。
	Error string `json:"error,omitempty"`
}

// Commit はコミットとその親。
type Commit struct {
	SHA     string   `json:"sha"`
	Parents []string `json:"parents"`
}

// PullRequest は PR の状態。
type PullRequest struct {
	Number         int      `json:"number"`
	State          string   `json:"state"` // open | closed
	Merged         bool     `json:"merged"`
	Draft          bool     `json:"draft"`
	Title          string   `json:"title"`
	Body           string   `json:"body"`
	Author         string   `json:"author"`
	Labels         []string `json:"labels"`
	BaseRef        string   `json:"base_ref"`
	BaseSHA        string   `json:"base_sha"`
	HeadRef        string   `json:"head_ref"`
	HeadSHA        string   `json:"head_sha"`
	Mergeable      *bool    `json:"mergeable"` // null は未計算
	MergeableState string   `json:"mergeable_state"`
	ChangedFiles   int      `json:"changed_files"`
}

// File は PR の変更ファイル。
type File struct {
	Filename         string `json:"filename"`
	PreviousFilename string `json:"previous_filename,omitempty"`
	Status           string `json:"status"`
	// Patch は差分（GitHub の files[].patch）。大きすぎる・バイナリのファイルでは空。
	// レビュー記録の引き継ぎ（C6）の patch-id の比較に使う。
	Patch string `json:"patch,omitempty"`
}

// Issue はリンクした Issue。
type Issue struct {
	Number        int      `json:"number"`
	State         string   `json:"state"`
	Title         string   `json:"title"`
	Body          string   `json:"body"`
	Labels        []string `json:"labels"`
	IsPullRequest bool     `json:"is_pull_request"`
}

// CheckRun は check run。
type CheckRun struct {
	Name       string `json:"name"`
	HeadSHA    string `json:"head_sha"`
	Status     string `json:"status"`     // queued | in_progress | completed | waiting | requested | pending
	Conclusion string `json:"conclusion"` // completed のとき: success | failure | neutral | cancelled | skipped | timed_out | action_required | stale
	App        string `json:"app,omitempty"`
}

// Status はコミットステータス。
type Status struct {
	Context string `json:"context"`
	State   string `json:"state"` // error | failure | pending | success
}

// Comment は PR のコメント。
type Comment struct {
	ID        int64  `json:"id"`
	Author    string `json:"author"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// PullReview は PR のレビュー。
type PullReview struct {
	ID          int64  `json:"id"`
	Author      string `json:"author"`
	CommitID    string `json:"commit_id"`
	Body        string `json:"body"`
	SubmittedAt string `json:"submitted_at"`
}

// ReviewComment は PR の行コメント。
type ReviewComment struct {
	ID       int64  `json:"id"`
	Author   string `json:"author"`
	CommitID string `json:"commit_id"`
	// OriginalCommitID は、コメントを書いたときの SHA（GitHub は commit_id を新しいコミットへ付け替えることがある）。
	OriginalCommitID string `json:"original_commit_id,omitempty"`
	Path             string `json:"path"`
	Line             int    `json:"line"`
	Body             string `json:"body"`
	CreatedAt        string `json:"created_at"`
}

// ReviewedSHA は、行コメントが対象にした SHA（original_commit_id があればそれ、なければ commit_id）。
func (c ReviewComment) ReviewedSHA() string {
	if c.OriginalCommitID != "" {
		return c.OriginalCommitID
	}
	return c.CommitID
}

// Compare はベースとの比較。
type Compare struct {
	Status   string `json:"status"`
	AheadBy  int    `json:"ahead_by"`
	BehindBy int    `json:"behind_by"`
}
