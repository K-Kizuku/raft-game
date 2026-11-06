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
	// Compare は GET /compare/{base}...{head} の結果。
	Compare *Compare `json:"compare"`
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

// Compare はベースとの比較。
type Compare struct {
	Status   string `json:"status"`
	AheadBy  int    `json:"ahead_by"`
	BehindBy int    `json:"behind_by"`
}
