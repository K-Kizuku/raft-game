// Command agentctl は、AI エージェントによる並列開発を GitHub の状態から管理する。
//
//	agentctl gate --repo OWNER/NAME (--pr N | --sha SHA) [--config PATH] [--input snapshot.json]
//	              [--json] [--set-status] [--comment] [--target-url URL]
//	agentctl review validate [--config PATH] <file|->
//	agentctl review render   [--config PATH] <file|->
//	agentctl plan    [--input snapshot.json] [--json | --mermaid]
//	agentctl state   <issue> <status> [--reason TEXT] [--pr N] [--cause KEY]
//	agentctl claim   <issue> --worker ID [--session UUID] [--resume]
//	agentctl attempt <issue> --cause KEY
//	agentctl recover [--input snapshot.json] [--json]
//
// gate の終了コード: pass=0・fail=1・pending=2・内部エラー=3。
// plan〜recover の終了コードは tasks.go の taskUsage を参照。
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/config"
	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/gate"
	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/github"
	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/review"
)

// 終了コード。
const (
	exitOK       = 0
	exitFail     = 1
	exitPending  = 2
	exitInternal = 3
)

// defaultBotLogin は、Merge Gate のコメントを書くアカウント（GITHUB_TOKEN の場合）。
const defaultBotLogin = "github-actions[bot]"

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

const usage = `使い方:
  agentctl gate --repo OWNER/NAME (--pr N | --sha SHA) [--config PATH] [--input snapshot.json]
                [--json] [--set-status] [--comment] [--target-url URL]
  agentctl review validate [--config PATH] <file|->
  agentctl review render   [--config PATH] <file|->
`

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return exitInternal
	}
	switch args[0] {
	case "gate":
		return runGate(args[1:], stdout, stderr)
	case "plan":
		return runPlan(args[1:], stdout, stderr)
	case "state":
		return runState(args[1:], stdout, stderr)
	case "claim":
		return runClaim(args[1:], stdout, stderr)
	case "attempt":
		return runAttempt(args[1:], stdout, stderr)
	case "recover":
		return runRecover(args[1:], stdout, stderr)
	case "review":
		if len(args) < 2 {
			fmt.Fprint(stderr, usage)
			return exitInternal
		}
		switch args[1] {
		case "validate":
			return runReview(args[2:], stdin, stdout, stderr, false)
		case "render":
			return runReview(args[2:], stdin, stdout, stderr, true)
		}
	case "-h", "--help", "help":
		fmt.Fprint(stdout, usage+taskUsage)
		return exitOK
	}
	fmt.Fprintf(stderr, "agentctl: 未知のコマンド %q\n%s%s", strings.Join(args, " "), usage, taskUsage)
	return exitInternal
}

// loadConfig は設定を読む。path が空なら、カレントディレクトリから親へたどって探す。
func loadConfig(path string) (*config.Config, error) {
	if path == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		if path, err = config.Find(wd); err != nil {
			return nil, err
		}
	}
	return config.Load(path)
}

func readFileOrStdin(name string, stdin io.Reader) ([]byte, error) {
	if name == "-" {
		return io.ReadAll(stdin)
	}
	return os.ReadFile(name)
}

func runReview(args []string, stdin io.Reader, stdout, stderr io.Writer, render bool) int {
	fs := flag.NewFlagSet("review", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "", "設定ファイル（省略時は .agents/config.json を親のディレクトリへたどって探す）")
	if err := fs.Parse(args); err != nil {
		return exitInternal
	}
	if fs.NArg() != 1 {
		fmt.Fprint(stderr, usage)
		return exitInternal
	}
	cfg, err := loadConfig(*cfgPath)
	if err != nil {
		fmt.Fprintf(stderr, "agentctl: %v\n", err)
		return exitInternal
	}
	data, err := readFileOrStdin(fs.Arg(0), stdin)
	if err != nil {
		fmt.Fprintf(stderr, "agentctl: %v\n", err)
		return exitInternal
	}
	rec, err := review.ParseDocument(data)
	if err == nil {
		err = rec.Validate(cfg)
	}
	if err != nil {
		fmt.Fprintf(stderr, "agentctl: %v\n", err)
		return exitFail
	}
	if !render {
		fmt.Fprintf(stdout, "OK: PR #%d round %d %s（%s）\n", rec.PR, rec.Round, rec.Verdict, rec.Reviewer)
		return exitOK
	}
	out, err := review.Render(rec)
	if err != nil {
		fmt.Fprintf(stderr, "agentctl: %v\n", err)
		return exitInternal
	}
	fmt.Fprint(stdout, out)
	return exitOK
}

var shaArgRe = regexp.MustCompile(`^[0-9a-f]{40}$`)

type gateOpts struct {
	repo, sha, cfgPath, input, targetURL string
	pr                                   int
	asJSON, setStatus, comment           bool
}

func runGate(args []string, stdout, stderr io.Writer) int {
	var o gateOpts
	fs := flag.NewFlagSet("gate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&o.repo, "repo", "", "OWNER/NAME")
	fs.IntVar(&o.pr, "pr", 0, "PR の番号")
	fs.StringVar(&o.sha, "sha", "", "commit の SHA（その SHA が HEAD の開いている PR を判定する）")
	fs.StringVar(&o.cfgPath, "config", "", "設定ファイル（省略時は .agents/config.json を親のディレクトリへたどって探す）")
	fs.StringVar(&o.input, "input", "", "GitHub に接続せず、このスナップショット（gate.Input の JSON）で判定する")
	fs.BoolVar(&o.asJSON, "json", false, "条件ごとの結果を JSON で出す")
	fs.BoolVar(&o.setStatus, "set-status", false, "HEAD の SHA にコミットステータス merge-gate を付ける")
	fs.BoolVar(&o.comment, "comment", false, "PR に Merge Gate のコメントを作る・更新する")
	fs.StringVar(&o.targetURL, "target-url", "", "ステータスとコメントに載せる URL")
	if err := fs.Parse(args); err != nil {
		return exitInternal
	}
	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "agentctl gate: "+format+"\n", a...)
		return exitInternal
	}
	if fs.NArg() != 0 {
		return fail("余計な引数 %v", fs.Args())
	}
	if !github.ValidRepo(o.repo) {
		return fail("--repo は OWNER/NAME にする（%q）", o.repo)
	}
	if (o.pr > 0) == (o.sha != "") || o.pr < 0 {
		return fail("--pr と --sha のどちらか一方を指定する")
	}
	if o.sha != "" && !shaArgRe.MatchString(o.sha) {
		return fail("--sha は 40 桁の 16 進（小文字）にする（%q）", o.sha)
	}

	// 設定が読めなくても、判定は G0 の fail として結果を出す（fail closed）。
	cfg, cfgErr := loadConfig(o.cfgPath)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	var client *github.Client
	needClient := o.input == "" || o.setStatus || o.comment
	if needClient {
		token := os.Getenv("GITHUB_TOKEN")
		if token == "" {
			token = os.Getenv("GH_TOKEN")
		}
		var err error
		if client, err = github.New(os.Getenv("GITHUB_API_URL"), token); err != nil {
			return fail("%v", err)
		}
	}

	var inputs []gate.Input
	if o.input != "" {
		in, err := readInput(o.input)
		if err != nil {
			return fail("%v", err)
		}
		if in.Repo == "" {
			in.Repo = o.repo
		}
		if in.Repo != o.repo {
			return fail("--input の repo %q が --repo と違う", in.Repo)
		}
		if o.pr > 0 && in.PullRequest.Number != o.pr {
			return fail("--input の PR #%d が --pr と違う", in.PullRequest.Number)
		}
		if o.sha != "" && in.PullRequest.HeadSHA != o.sha {
			return fail("--input の HEAD %s が --sha と違う", in.PullRequest.HeadSHA)
		}
		inputs = append(inputs, in)
	} else {
		prs := []int{o.pr}
		if o.sha != "" {
			var err error
			if prs, err = client.OpenPullsForSHA(ctx, o.repo, o.sha); err != nil {
				return fail("%v", err)
			}
			if len(prs) == 0 {
				fmt.Fprintf(stderr, "agentctl gate: HEAD が %s の開いている PR がない（判定しない）\n", o.sha)
				return exitOK
			}
		}
		for _, n := range prs {
			in, err := client.FetchInput(ctx, o.repo, n)
			if err != nil {
				return fail("%v", err)
			}
			inputs = append(inputs, in)
		}
	}

	botLogin := os.Getenv("AGENTCTL_BOT_LOGIN")
	if botLogin == "" {
		botLogin = defaultBotLogin
	}

	code := exitOK
	for _, in := range inputs {
		var rep gate.Report
		if cfgErr != nil {
			rep = gate.EvaluateConfigError(cfgErr, in)
		} else {
			rep = gate.Evaluate(cfg, in)
		}
		if err := writeReport(stdout, &rep, o.asJSON); err != nil {
			return fail("%v", err)
		}
		if rep.Closed {
			fmt.Fprintf(stderr, "agentctl gate: PR #%d は closed・merged なので、ステータスとコメントは付けない\n", rep.PR)
		} else {
			if o.setStatus {
				if err := client.SetStatus(ctx, o.repo, rep.HeadSHA, rep.StatusState(), config.SelfContext, rep.Description(), o.targetURL); err != nil {
					return fail("%v", err)
				}
			}
			if o.comment {
				if err := client.UpsertComment(ctx, o.repo, rep.PR, in.Comments, gate.CommentMarker, botLogin, rep.RenderComment(o.targetURL)); err != nil {
					return fail("%v", err)
				}
			}
		}
		code = worse(code, rep.Verdict.ExitCode())
	}
	return code
}

// worse は、2 つの終了コードのうち悪いほう（fail > pending > pass）を返す。
func worse(a, b int) int {
	rank := map[int]int{exitOK: 0, exitPending: 1, exitFail: 2}
	if rank[b] > rank[a] {
		return b
	}
	return a
}

func readInput(path string) (gate.Input, error) {
	var in gate.Input
	data, err := os.ReadFile(path)
	if err != nil {
		return in, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return in, fmt.Errorf("--input %s を読めない: %w", path, err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return in, fmt.Errorf("--input %s: JSON の後ろに余計なデータがある", path)
	}
	return in, nil
}

func writeReport(w io.Writer, rep *gate.Report, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(rep)
	}
	fmt.Fprintf(w, "merge-gate: %s（PR #%d、HEAD %s）\n", rep.Verdict, rep.PR, rep.HeadSHA)
	for _, r := range rep.Results {
		fmt.Fprintf(w, "  %-4s %-7s %s — %s\n", r.ID, r.Status, r.Title, r.Detail)
	}
	for _, n := range rep.Notes {
		fmt.Fprintf(w, "  note: %s\n", n)
	}
	return nil
}
