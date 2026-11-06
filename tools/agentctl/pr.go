package main

// PR へのコメントの投稿と、PR の読み取り（REST だけ。gh pr view などの GraphQL は使わない）。Issue #82。

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/gate"
	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/github"
	"github.com/K-Kizuku/raft-game/tools/agentctl/internal/review"
)

const prUsage = `  agentctl comment --repo OWNER/NAME --pr N (--body TEXT | --body-file FILE|-)
  agentctl pr view         --repo OWNER/NAME --pr N
  agentctl pr codex-record --repo OWNER/NAME --pr N [--sha SHA] [--previous FILE] [--input view.json] [--config PATH]

comment は PR にコメントを作り、{"id": …} を出す。
pr view は PR の状態・コメント・レビュー・行コメントを gate.Input の形の JSON で出す。
pr codex-record は、Codex の GitHub 連携の SHA（省略時は HEAD）のレビューを agent-review/v1 の記録にして出す
（--previous を省略すると、PR のコメントの最新のレビュー記録を前回とする）。
終了コード: 0=成功・2=Codex のレビューがまだ終わっていない（codex-record）・3=内部エラー。
`

// nowFunc は現在の時刻（テストで差し替える）。
var nowFunc = time.Now

func runComment(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("comment", flag.ContinueOnError)
	fs.SetOutput(stderr)
	repo := fs.String("repo", "", "OWNER/NAME")
	pr := fs.Int("pr", 0, "PR の番号")
	body := fs.String("body", "", "コメントの本文")
	bodyFile := fs.String("body-file", "", "コメントの本文のファイル（- は標準入力）")
	if err := fs.Parse(args); err != nil {
		return exitInternal
	}
	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "agentctl comment: "+format+"\n", a...)
		return exitInternal
	}
	if fs.NArg() != 0 {
		return fail("余計な引数 %v", fs.Args())
	}
	if !github.ValidRepo(*repo) {
		return fail("--repo は OWNER/NAME にする（%q）", *repo)
	}
	if *pr <= 0 {
		return fail("--pr を指定する")
	}
	if (*body == "") == (*bodyFile == "") {
		return fail("--body と --body-file のどちらか一方を指定する")
	}
	text := *body
	if *bodyFile != "" {
		data, err := readFileOrStdin(*bodyFile, stdin)
		if err != nil {
			return fail("%v", err)
		}
		text = string(data)
	}
	if strings.TrimSpace(text) == "" {
		return fail("本文が空")
	}
	client, err := newClient()
	if err != nil {
		return fail("%v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	id, err := client.CreateComment(ctx, *repo, *pr, text)
	if err != nil {
		return fail("%v", err)
	}
	fmt.Fprintf(stdout, "{\"id\":%d}\n", id)
	return exitOK
}

func runPR(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, prUsage)
		return exitInternal
	}
	switch args[0] {
	case "view":
		return runPRView(args[1:], stdout, stderr)
	case "codex-record":
		return runCodexRecord(args[1:], stdout, stderr)
	}
	fmt.Fprintf(stderr, "agentctl pr: 未知のサブコマンド %q\n%s", args[0], prUsage)
	return exitInternal
}

type prOpts struct {
	repo, sha, previous, input, cfgPath string
	pr                                  int
}

func (o *prOpts) flags(name string, stderr io.Writer, codex bool) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&o.repo, "repo", "", "OWNER/NAME")
	fs.IntVar(&o.pr, "pr", 0, "PR の番号")
	if codex {
		fs.StringVar(&o.sha, "sha", "", "レビューの対象の SHA（省略時は PR の HEAD）")
		fs.StringVar(&o.previous, "previous", "", "前回のレビュー記録（JSON かコメントの本文）")
		fs.StringVar(&o.input, "input", "", "GitHub に接続せず、この JSON（pr view の出力か gate.Input）を使う")
		fs.StringVar(&o.cfgPath, "config", "", "設定ファイル（省略時は .agents/config.json を親のディレクトリへたどって探す）")
	}
	return fs
}

func (o *prOpts) check(fs *flag.FlagSet) error {
	if fs.NArg() != 0 {
		return fmt.Errorf("余計な引数 %v", fs.Args())
	}
	if !github.ValidRepo(o.repo) {
		return fmt.Errorf("--repo は OWNER/NAME にする（%q）", o.repo)
	}
	if o.pr <= 0 {
		return errors.New("--pr を指定する")
	}
	if o.sha != "" && !shaArgRe.MatchString(o.sha) {
		return fmt.Errorf("--sha は 40 桁の 16 進（小文字）にする（%q）", o.sha)
	}
	return nil
}

func fetchDiscussion(repo string, pr int) (gate.Input, error) {
	client, err := newClient()
	if err != nil {
		return gate.Input{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	return client.FetchDiscussion(ctx, repo, pr)
}

func runPRView(args []string, stdout, stderr io.Writer) int {
	var o prOpts
	fs := o.flags("pr view", stderr, false)
	if err := fs.Parse(args); err != nil {
		return exitInternal
	}
	if err := o.check(fs); err != nil {
		fmt.Fprintf(stderr, "agentctl pr view: %v\n", err)
		return exitInternal
	}
	in, err := fetchDiscussion(o.repo, o.pr)
	if err != nil {
		fmt.Fprintf(stderr, "agentctl pr view: %v\n", err)
		return exitInternal
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(in); err != nil {
		fmt.Fprintf(stderr, "agentctl pr view: %v\n", err)
		return exitInternal
	}
	return exitOK
}

func runCodexRecord(args []string, stdout, stderr io.Writer) int {
	var o prOpts
	fs := o.flags("pr codex-record", stderr, true)
	if err := fs.Parse(args); err != nil {
		return exitInternal
	}
	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "agentctl pr codex-record: "+format+"\n", a...)
		return exitInternal
	}
	if err := o.check(fs); err != nil {
		return fail("%v", err)
	}
	cfg, err := loadConfig(o.cfgPath)
	if err != nil {
		return fail("%v", err)
	}
	var prev *review.Record
	if o.previous != "" {
		data, err := os.ReadFile(o.previous)
		if err != nil {
			return fail("%v", err)
		}
		if prev, err = review.ParseDocument(data); err != nil {
			return fail("--previous: %v", err)
		}
	}

	var in gate.Input
	if o.input != "" {
		if in, err = readInput(o.input); err != nil {
			return fail("%v", err)
		}
		if in.PullRequest.Number != o.pr {
			return fail("--input の PR #%d が --pr と違う", in.PullRequest.Number)
		}
	} else if in, err = fetchDiscussion(o.repo, o.pr); err != nil {
		return fail("%v", err)
	}

	sha := o.sha
	if sha == "" {
		sha = in.PullRequest.HeadSHA
	}
	cr, notes := gate.FindCodexReview(cfg, in)
	for _, n := range notes {
		fmt.Fprintf(stderr, "agentctl pr codex-record: note: %s\n", n)
	}
	switch {
	case cr == nil:
		fmt.Fprintf(stderr, "agentctl pr codex-record: PR #%d に Codex の完了したレビューがまだない\n", o.pr)
		return exitPending
	case cr.SHA != sha:
		fmt.Fprintf(stderr, "agentctl pr codex-record: Codex の最新のレビューは %s のもの（%s を待つ）\n", short(cr.SHA), short(sha))
		return exitPending
	}
	if prev == nil {
		prev = gate.LatestRecord(cfg, in)
	}
	rec := cr.Record(cfg, o.pr, in.PullRequest.BaseSHA, prev, nowFunc())
	if err := rec.Validate(cfg); err != nil {
		return fail("変換した記録が不正: %v", err)
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(rec); err != nil {
		return fail("%v", err)
	}
	return exitOK
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
