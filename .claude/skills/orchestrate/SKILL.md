---
name: orchestrate
description: Orchestrator として、Issue の計画・Worker の起動・レビュー・Merge Gate を通したマージ・Worktree の片付けを 1 周行う。/orchestrate で呼ぶ。
---

# Orchestrator の 1 周の手順

あなたは K-Kizuku/raft-game の **Orchestrator** です。メインの作業ツリー（リンクした Worktree の外）で、`AGENT_ROLE=orchestrator` として動きます。

- コード・テスト・ドキュメントは**直接変更しない**（hooks で `.agents/`・`.claude/worktrees/` 以外への書き込みは拒否される）。変更は Worker に任せる
- マージは `scripts/agents/merge.sh` だけで行う（`gh pr merge` は拒否される）
- `agentctl` は `scripts/agents/agentctl.sh` で呼ぶ

## 手順

1. **復旧**: `scripts/agents/agentctl.sh recover --repo K-Kizuku/raft-game --json`。出た行動を先に片付ける
   - `reset-to-ready` → `agentctl.sh state <n> ready --reason …`
   - `resume-worker` → `scripts/agents/spawn-worker.sh <n> --resume`（changes-requested なら `--mode fix`）
   - `set-state …` → `agentctl.sh state <n> <状態> [--pr N]`
   - `request-review` → 手順 5
   - `merge` → 手順 7
   - `escalate`・`fix-labels` → 人に判断を求める（下の「止めて報告する条件」）
2. **計画**: `scripts/agents/agentctl.sh plan --repo K-Kizuku/raft-game --json`。`ready` に出た Issue を、出た順に選ぶ（`capacity` を超えない）
3. **Issue を補う**: 選ぶ Issue の本文に、スコープ・実装仕様・変更予定ファイル・変更禁止範囲・依存 Issue・テストケース・Acceptance Criteria が足りなければ、Issue のコメントに補足を書いてから `status:ready` にする（`agentctl.sh state <n> ready`）
4. **Worker の起動**: `scripts/agents/spawn-worker.sh <n> --background`。`max_workers` と DEV_SLOT はスクリプトが守る。`agentctl claim` が拒否したら（終了コード 4）その Issue は飛ばす
5. **PR を待ってレビュー**: Worker が PR を作ったら `agentctl.sh state <n> in-review --pr <PR>`、`scripts/agents/review.sh <PR>`
   - Codex が使えないときは、スクリプトが Claude Code（別のセッション）に切り替える。`fallback_reason` が記録に残る
6. **指摘への対応**: `request_changes` なら `agentctl.sh state <n> changes-requested`、`agentctl.sh attempt <n> --cause review:<id>`（終了コード 5 なら止めて報告）、`scripts/agents/spawn-worker.sh <n> --mode fix [--note FILE]`。直ったら手順 5 に戻る
   - CI の失敗も同じ（`--cause ci:<チェック名>`）
   - Conflict は、解消の方針を決めてファイルに書き、`--cause conflict` で数えてから `spawn-worker.sh <n> --mode conflict --note <方針のファイル>`
7. **マージ**: `ci-ok` と `merge-gate` を確かめ（`gh pr checks <PR>`）、`agentctl.sh state <n> ready-to-merge`、`scripts/agents/merge.sh <PR>`。Merge Gate が pass のときだけ、判定した SHA を指定して squash merge され、`status:merged` と Worktree の片付けまで行われる
8. **片付け**: `scripts/agents/wt.sh cleanup`。残ったものは理由を確かめる（`scripts/agents/status.sh`）

## 止めて報告する条件（人に判断を求める）

次に当たったら、その Issue の作業を止め、Issue に `needs-human` を付けて、状況と選択肢を報告する:

- 同じ原因の再試行が `retry_limit` に達した（`agentctl attempt` が終了コード 5）
- `agentctl recover` が `escalate`（1 つの Issue に開いた PR が 2 つ以上）か、自動で直せない `fix-labels` を出した
- Worker や Reviewer が `## Orchestrator への報告` で仕様の変更を求めた
- レビューで `protected_paths_ack.weakens_guards` が true、または保護されたパスの変更の扱いに迷う
- 依存関係の循環・存在しない依存先など、計画が立てられない
- Conflict の解消の方針を Issue の仕様から決められない
- シークレット・課金・本番環境・Rulesets・`.github/workflows/` に関わる変更が必要になった（人だけが行う）
- Codex も Claude Code もレビューできない（`review.sh` が終了コード 5）
