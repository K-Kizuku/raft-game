package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/config"
	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/gate"
	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/github"
	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/plan"
	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/snapshot"
	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/state"
)

// plan・state・claim・attempt・recover の終了コード（0・3 は共通）。
const (
	exitRejected     = 1 // state・attempt: 遷移や状態の確認で拒否した
	exitPrecondition = 4 // claim: 前提を満たさない
	exitRetryLimit   = 5 // attempt: 再試行の上限に達した
)

// now は記録の時刻（テストで差し替える）。
var now = func() time.Time { return time.Now().UTC() }

// gitRunner は recover がローカルの Worktree を調べるときの git（テストで差し替える）。
var gitRunner snapshot.Git = snapshot.RunGit

const taskUsage = `使い方:
  agentctl plan    [--repo OWNER/NAME] [--input snapshot.json] [--json | --mermaid]
  agentctl state   <issue> <status> --repo OWNER/NAME [--reason TEXT] [--pr N] [--cause KEY] [--dry-run]
  agentctl claim   <issue> --worker ID --repo OWNER/NAME [--session UUID] [--resume] [--dry-run]
  agentctl attempt <issue> --cause KEY --repo OWNER/NAME [--dry-run]
  agentctl recover [--repo OWNER/NAME] [--input snapshot.json] [--json]

共通: --config（省略時は .agents/config.json を親のディレクトリへたどって探す）、
      --input（GitHub から読まず、スナップショットの JSON を使う）、
      --dry-run（書き込みをせず、行う予定の API の操作を JSON で出す）。
トークンは GITHUB_TOKEN か GH_TOKEN（API の URL は GITHUB_API_URL）。
終了コード: 成功=0、拒否（state・attempt）=1、内部エラー=3、claim の前提を満たさない=4、attempt の上限=5。
`

var subUsage = map[string]string{
	"plan": `agentctl plan: 開いている Issue（status:* のラベルを持つもの）の依存関係の DAG から、着手してよい Issue を選ぶ。
  条件: status:ready、依存先がすべて completed で閉じている、agent/issue-<N> のブランチと開いた PR がない、
        実装中の Issue や同時に選ぶ Issue と「変更予定ファイル」が重ならない（shared_append_files だけの重なりは警告）。
  上限: max_workers −（in-progress と changes-requested の数）。順番: クリティカルパスの長いもの、次に番号の小さいもの。
  上限で選ばなかったものは deferred に入れる。PR は、作者が trusted_authors（か AGENTCTL_BOT_LOGIN）のものだけを数える。
  --json: {ready, blocked_by_deps, conflicts, deferred, active, capacity}、--mermaid: 状態で色分けした DAG。循環があれば終了コード 3。`,
	"state": `agentctl state <issue> <status>: 遷移表で検証し、status:* を付け替えて、状態のコメントを付ける。
  merged へは、Issue に関係する PR が実際にマージされているときだけ。拒否したら終了コード 1。`,
	"claim": `agentctl claim <issue> --worker ID: 着手の宣言（ready → in-progress）。worker・branch・session・attempt を記録する。
  前提（status:ready、依存が完了、ブランチと開いた PR がない）を満たさなければ終了コード 4。
  同じ worker と session での 2 回目は何もせず成功する。
  --resume: in-progress で、ブランチがあり、PR がないときの再開。状態は変えず、attempt を 1 増やした記録だけ付ける
            （同じ session でも冪等の判定はしない）。PR は、作者が信頼する作者のものだけを数える。`,
	"attempt": `agentctl attempt <issue> --cause KEY: 同じ cause の記録の数が retry_limit に達していたら、
  status:blocked と needs-human を付けて終了コード 5。まだなら記録を 1 つ足す。
  blocked から人が戻した記録より後だけを数える。`,
	"recover": `agentctl recover: 作業中の状態の Issue ごとに、GitHub と Git の状態から次の行動を 1 つ出す
  （reset-to-ready・resume-worker・set-state・request-review・merge・escalate・fix-labels）。
  ローカルの Worktree（git worktree list の .claude/worktrees/issue-<N>、--input なら worktrees 欄）も見て、
  ブランチも PR もなくても、未コミットの変更か push していないコミットがあれば resume-worker にする。
  resume-worker には、状態の記録にある最新の session を含める。
  in-review で HEAD のレビュー記録がなくても、前の SHA のレビューを Merge Gate の C6 と同じ条件で引き継げる
  （main の取り込みだけ。review.carry_over_on_base_merge）なら request-review を出さない（--input なら pulls[].gate 欄を使う）。`,
}

type taskOpts struct {
	repo, cfgPath, input string
	dryRun               bool
}

func newTaskFlags(name string, o *taskOpts, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&o.repo, "repo", "", "OWNER/NAME")
	fs.StringVar(&o.cfgPath, "config", "", "設定ファイル（省略時は .agents/config.json を親のディレクトリへたどって探す）")
	fs.StringVar(&o.input, "input", "", "GitHub から読まず、このスナップショットの JSON を使う")
	fs.BoolVar(&o.dryRun, "dry-run", false, "書き込みをせず、行う予定の API の操作を JSON で出す")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), subUsage[name])
		fs.PrintDefaults()
	}
	return fs
}

// parseInterleaved は、位置引数とフラグが混ざった引数を読む（`state 12 ready --reason x`）。
func parseInterleaved(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			return pos, nil
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
}

// taskEnv は、設定・スナップショット・クライアントをまとめたもの。
type taskEnv struct {
	cfg    *config.Config
	snap   *snapshot.Snapshot
	client *github.Client
	login  string
}

func (e *taskEnv) trusted(login string) bool {
	return e.cfg.IsTrustedAuthor(login) || strings.EqualFold(login, e.login)
}

func newClient() (*github.Client, error) {
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		token = os.Getenv("GH_TOKEN")
	}
	return github.New(os.Getenv("GITHUB_API_URL"), token)
}

// prepare は設定とスナップショットを読む。write なら、--dry-run でないときに書き込み用のクライアントも作る。
func prepare(ctx context.Context, o *taskOpts, write bool) (*taskEnv, error) {
	if o.repo != "" && !github.ValidRepo(o.repo) {
		return nil, fmt.Errorf("--repo は OWNER/NAME にする（%q）", o.repo)
	}
	if o.repo == "" && (write || o.input == "") {
		return nil, errors.New("--repo を指定する")
	}
	cfg, err := loadConfig(o.cfgPath)
	if err != nil {
		return nil, err
	}
	e := &taskEnv{cfg: cfg, login: os.Getenv("AGENTCTL_BOT_LOGIN")}
	if e.login == "" {
		e.login = defaultBotLogin
	}
	if o.input == "" || (write && !o.dryRun) {
		if e.client, err = newClient(); err != nil {
			return nil, err
		}
	}
	if o.input != "" {
		e.snap, err = snapshot.Load(o.input)
	} else {
		e.snap, err = snapshot.Fetch(ctx, e.client, o.repo, cfg.BranchPrefix)
	}
	if err != nil {
		return nil, err
	}
	return e, nil
}

func warn(w io.Writer, cmd string, msgs []string) {
	for _, m := range msgs {
		fmt.Fprintf(w, "agentctl %s: 警告: %s\n", cmd, m)
	}
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func runPlan(args []string, stdout, stderr io.Writer) int {
	var o taskOpts
	fs := newTaskFlags("plan", &o, stderr)
	asJSON := fs.Bool("json", false, "JSON で出す")
	asMermaid := fs.Bool("mermaid", false, "Mermaid の DAG を出す")
	if err := fs.Parse(args); err != nil {
		return helpCode(err)
	}
	fail := failer(stderr, "plan")
	if fs.NArg() != 0 {
		return fail("余計な引数 %v", fs.Args())
	}
	if *asJSON && *asMermaid {
		return fail("--json と --mermaid は同時に指定できない")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	e, err := prepare(ctx, &o, false)
	if err != nil {
		return fail("%v", err)
	}
	if *asMermaid {
		out, err := plan.Mermaid(e.snap)
		if err != nil {
			return fail("%v", err)
		}
		fmt.Fprint(stdout, out)
		return exitOK
	}
	res, err := plan.Plan(e.snap, e.cfg, e.trusted)
	if err != nil {
		return fail("%v", err)
	}
	warn(stderr, "plan", res.Warnings)
	if *asJSON {
		if err := writeJSON(stdout, res); err != nil {
			return fail("%v", err)
		}
		return exitOK
	}
	fmt.Fprintf(stdout, "capacity: %d\n", res.Capacity)
	section := func(title string, items []plan.Item) {
		fmt.Fprintf(stdout, "%s:\n", title)
		if len(items) == 0 {
			fmt.Fprintln(stdout, "  （なし）")
		}
		for _, it := range items {
			fmt.Fprintf(stdout, "  #%d [%s] cp=%d %s\n", it.Number, it.Status, it.CriticalPath, it.Title)
			for _, r := range it.Reasons {
				fmt.Fprintf(stdout, "    - %s\n", r)
			}
			for _, w := range it.Warnings {
				fmt.Fprintf(stdout, "    ! %s\n", w)
			}
		}
	}
	section("ready", res.Ready)
	section("blocked_by_deps", res.BlockedByDeps)
	section("conflicts", res.Conflicts)
	section("deferred", res.Deferred)
	section("active", res.Active)
	return exitOK
}

func runRecover(args []string, stdout, stderr io.Writer) int {
	var o taskOpts
	fs := newTaskFlags("recover", &o, stderr)
	asJSON := fs.Bool("json", false, "JSON で出す")
	if err := fs.Parse(args); err != nil {
		return helpCode(err)
	}
	fail := failer(stderr, "recover")
	if fs.NArg() != 0 {
		return fail("余計な引数 %v", fs.Args())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	e, err := prepare(ctx, &o, false)
	if err != nil {
		return fail("%v", err)
	}
	if o.input == "" {
		// --input のときはスナップショットの worktrees 欄を使う。そうでなければローカルの git から集める。
		wd, err := os.Getwd()
		if err != nil {
			return fail("%v", err)
		}
		if e.snap.Worktrees, err = snapshot.LocalWorktrees(ctx, gitRunner, wd, e.cfg.BranchPrefix); err != nil {
			return fail("ローカルの Worktree を調べられない: %v", err)
		}
	}
	res := plan.Recover(e.snap, e.cfg, e.trusted)
	if o.input == "" && e.cfg.Review.CarryOverOnBaseMerge && fetchCarryOver(ctx, e, o.repo, res, stderr) {
		// 前の SHA のレビューを引き継げる PR には request-review を出さない（Merge Gate の C6 と同じ条件）。
		res = plan.Recover(e.snap, e.cfg, e.trusted)
	}
	warn(stderr, "recover", res.Warnings)
	if *asJSON {
		if err := writeJSON(stdout, res); err != nil {
			return fail("%v", err)
		}
		return exitOK
	}
	if len(res.Actions) == 0 {
		fmt.Fprintln(stdout, "行動なし")
	}
	for _, a := range res.Actions {
		extra := ""
		if a.To != "" {
			extra += " " + a.To
		}
		if a.PR > 0 {
			extra += fmt.Sprintf(" (PR #%d)", a.PR)
		}
		fmt.Fprintf(stdout, "#%d [%s] %s%s — %s\n", a.Issue, a.Status, a.Action, extra, a.Reason)
	}
	return exitOK
}

// fetchCarryOver は、request-review の PR ごとに Merge Gate の判定の材料（レビュー記録の引き継ぎの材料を含む）を集め、
// スナップショットの PR の gate 欄に入れる。1 つでも入れたら true。集められなかった PR は警告を出し、request-review のままにする。
func fetchCarryOver(ctx context.Context, e *taskEnv, repo string, res *plan.RecoverResult, stderr io.Writer) bool {
	changed := false
	for _, a := range res.Actions {
		if a.Action != plan.ActionRequestReview || a.PR <= 0 {
			continue
		}
		p := e.snap.Pull(a.PR)
		if p == nil {
			continue
		}
		in, err := e.client.FetchInput(ctx, repo, a.PR)
		if err != nil {
			warn(stderr, "recover", []string{fmt.Sprintf("PR #%d のレビューの引き継ぎを確かめられない: %v", a.PR, err)})
			continue
		}
		sha := gate.CarryOverCandidate(e.cfg, in)
		if sha == "" {
			continue
		}
		e.client.FetchCarryOver(ctx, repo, &in, sha)
		p.Gate = &in
		changed = true
	}
	return changed
}

// runWrite は state・claim・attempt の共通部分。build が操作を組み立て、--dry-run なら JSON で出し、そうでなければ送る。
func runWrite(name string, args []string, stdout, stderr io.Writer, npos int,
	define func(fs *flag.FlagSet), build func(e *taskEnv, env state.Env, pos []string) (*state.Result, error)) int {
	var o taskOpts
	fs := newTaskFlags(name, &o, stderr)
	define(fs)
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return helpCode(err)
	}
	fail := failer(stderr, name)
	if len(pos) != npos {
		return fail("引数の数が違う（%v）\n%s", pos, taskUsage)
	}
	if _, err := strconv.Atoi(pos[0]); err != nil {
		return fail("Issue の番号が不正（%q）", pos[0])
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	e, err := prepare(ctx, &o, true)
	if err != nil {
		return fail("%v", err)
	}
	env := state.Env{
		Repo: o.repo, BranchPrefix: e.cfg.BranchPrefix, RetryLimit: e.cfg.RetryLimit,
		Now: now(), Actor: state.DefaultActor, Trusted: e.trusted,
	}
	res, err := build(e, env, pos)
	if err != nil {
		if r, ok := state.IsReject(err); ok {
			fmt.Fprintf(stderr, "agentctl %s: %v\n", name, r)
			if r.Precondition {
				return exitPrecondition
			}
			return exitRejected
		}
		return fail("%v", err)
	}
	warn(stderr, name, res.Warnings)
	code := exitOK
	if res.LimitReached {
		code = exitRetryLimit
	}
	if o.dryRun {
		ops := res.Ops
		if ops == nil {
			ops = []github.Op{}
		}
		if err := writeJSON(stdout, map[string]any{"operations": ops}); err != nil {
			return fail("%v", err)
		}
		return code
	}
	if done, err := e.client.Apply(ctx, res.Ops); err != nil {
		return fail("%v（%d/%d 件を送った。agentctl recover で状態を確かめる）", err, done, len(res.Ops))
	}
	fmt.Fprintln(stdout, res.Message)
	return code
}

func issueArg(s string) int { n, _ := strconv.Atoi(s); return n }

func runState(args []string, stdout, stderr io.Writer) int {
	var reason, cause string
	var pr int
	return runWrite("state", args, stdout, stderr, 2, func(fs *flag.FlagSet) {
		fs.StringVar(&reason, "reason", "", "理由")
		fs.IntVar(&pr, "pr", 0, "関係する PR の番号")
		fs.StringVar(&cause, "cause", "", "原因のキー（例: ci:ci-ok、review:F3、conflict）")
	}, func(e *taskEnv, env state.Env, pos []string) (*state.Result, error) {
		to, err := state.Parse(pos[1])
		if err != nil {
			return nil, err
		}
		if pr < 0 {
			return nil, errors.New("--pr は 0 以上にする")
		}
		return state.Transition(e.snap, env, issueArg(pos[0]), to, state.TransitionOpts{Reason: reason, Cause: cause, PR: pr})
	})
}

func runClaim(args []string, stdout, stderr io.Writer) int {
	var worker, session string
	var resume bool
	return runWrite("claim", args, stdout, stderr, 1, func(fs *flag.FlagSet) {
		fs.StringVar(&worker, "worker", "", "Worker の ID（例: issue-12）")
		fs.StringVar(&session, "session", "", "セッションの UUID")
		fs.BoolVar(&resume, "resume", false, "in-progress の作業を再開する")
	}, func(e *taskEnv, env state.Env, pos []string) (*state.Result, error) {
		if worker == "" {
			return nil, errors.New("--worker を指定する")
		}
		return state.Claim(e.snap, env, issueArg(pos[0]), worker, session, resume)
	})
}

func runAttempt(args []string, stdout, stderr io.Writer) int {
	var cause string
	return runWrite("attempt", args, stdout, stderr, 1, func(fs *flag.FlagSet) {
		fs.StringVar(&cause, "cause", "", "原因のキー（例: ci:ci-ok、review:F3、conflict）")
	}, func(e *taskEnv, env state.Env, pos []string) (*state.Result, error) {
		if cause == "" {
			return nil, errors.New("--cause を指定する")
		}
		return state.Attempt(e.snap, env, issueArg(pos[0]), cause)
	})
}

func failer(stderr io.Writer, name string) func(format string, a ...any) int {
	return func(format string, a ...any) int {
		fmt.Fprintf(stderr, "agentctl "+name+": "+format+"\n", a...)
		return exitInternal
	}
}

// helpCode は、-h なら 0、それ以外のフラグの誤りなら 3 を返す。
func helpCode(err error) int {
	if errors.Is(err, flag.ErrHelp) {
		return exitOK
	}
	return exitInternal
}
