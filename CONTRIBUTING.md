# CONTRIBUTING.md — 開発に参加する人へ

## はじめに

このリポジトリは、AI エージェントと人が並行して開発している。この文書は、人（オーナー・ほかの開発者）向けの入口。規約そのものはここで繰り返さず、次の文書を正とする。

- 規約（守ること・コマンド）: [CLAUDE.md](CLAUDE.md)
- レビューの観点: [REVIEW.md](REVIEW.md)
- エージェントの運用（役割・Issue からマージまでの流れ）: [docs/agents/](docs/agents/README.md)
- 要件・設計・ADR: [docs/](docs/README.md)

## 前提のツールと版

版は、右の列に書いた場所を正とする（ここに書いた版は、書いた時点の値）。

| ツール | 版 | 正とする場所・補足 |
|---|---|---|
| Go | 1.26 | `server/go.mod` の `go` の行。手元の Go が古いときは `GOTOOLCHAIN=auto`（既定）で自動で取得される。golangci-lint は入れなくてよい（`make lint` が `go run` で Makefile の `GOLANGCI_LINT_VERSION` の版を使う） |
| Node.js | `>=22.23.0` | `web/package.json` の `engines.node`。CI は 22 |
| pnpm | `pnpm@9.15.0` | ルートの `package.json` の `packageManager`。`corepack enable` で使う |
| Python | 3.9 以上 | `scripts/` は標準ライブラリだけを使う |
| GNU make・bash・git | — | make は macOS の標準の 3.81、bash は macOS の 3.2 で動く |
| shellcheck | — | `make lint` が使う。なければ飛ばす |
| Docker | — | `cf build`・`cf dev` のコンテナ、`spike/`、`make clean-docker` で使う |

## 最初の準備

```bash
git clone https://github.com/K-Kizuku/raft-game.git
cd raft-game
corepack enable && pnpm install   # ワークスペース: web・spike
make test                         # テスト
make lint                         # 静的検査
make dev                          # 開発用のサーバーを起動する
```

- 複数の作業ツリーで並列に動かすときは、先に `scripts/dev-slot.sh acquire` で `DEV_SLOT` を取る。ポートの決まりは [ADR-0019](docs/adr/0019-parallel-local-development.md)、この作業ツリーの値は `make dev-env` で確かめる
- コマンドの一覧は `make help`。ほかのコマンドは [CLAUDE.md](CLAUDE.md) の「コマンド」を見る

## 作業の流れ

- タスクは GitHub の Issue で管理する。作業の前に、対応する Issue を確かめる
- `main` に直接 push しない。ブランチを切って PR を出す（`agent/issue-*` はエージェント用なので、人は使わない）
- PR は `.github/pull_request_template.md` に従い、`Closes #<Issue>` と、関係する要件 ID（`FR-…`）やタスク ID（`T-…`）を書く。マージの条件（`ci-ok`・レビュー記録・Merge Gate）は [docs/agents/workflow.md](docs/agents/workflow.md) を見る
- コミットのメッセージは `<種類>: <日本語の要約> (#<Issue>)`。種類は `feat`・`fix`・`docs`・`ci`・`chore`・`data`・`test` など（`git log` の例に合わせる）
- 仕様を変えるときは、先に `docs/` を更新し、同じ PR でコードを変える。新しい技術判断をしたら `docs/adr/` に ADR を足す（`docs/adr/template.md`）
- ワークフローは `.github/workflow-sources/` に書き、`.github/workflows/` には書かない。取り込みは `scripts/github/install-workflows.sh` で行う（`workflow` の権限を持つ人だけ）

## シークレット

- 公開リポジトリなので、シークレットをコミットしない。`.env`・`.env.*`・`*.secrets*` は `.gitignore` に入っている
- Cloudflare のシークレットは `cf deploy --secrets-file` で渡す。本番・デモ用の環境へのデプロイは CI だけが行う

## ライセンス

- [Apache License 2.0](LICENSE)。貢献したものは、同じライセンスで提供される（Apache-2.0 の第 5 条）
- 同梱する OSS・提供データ・生成した素材を足したり変えたりしたら、[NOTICE](NOTICE) を更新する（[ADR-0020](docs/adr/0020-public-repository-apache-license.md)）
