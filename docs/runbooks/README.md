# 本番の運用の手順（runbooks）

このディレクトリは、本番（`raft.kizuku-hackathon.work`・Worker `raft-game`）とデモ用（`raft-demo.kizuku-hackathon.work`・Worker `raft-game-demo`）の運用の手順を置く。

- **実行するのは人（オーナー）。** AI エージェントは手順を読んで提案するだけで、本番を変えるコマンド（`cf deploy`（`--dry-run` を除く）・`cf workers deployments create` など）を実行しない（[CLAUDE.md](../../CLAUDE.md)）
- 構成は `web/cloudflare.config.ts` と `cf` だけで管理する。Wrangler やダッシュボードでの手作業の変更は使わない（[ADR-0016](../adr/0016-infrastructure-cloudflare-with-cf-cli.md)）
- 手順の中の `<…>` は置き換える値。本物の値（アカウント ID・トークン・版の ID）をリポジトリに書かない（公開リポジトリ）

## 共通の準備

- `web/` で `pnpm exec cf …` を使う（`web/package.json` に固定した版の `cf`）
- テレメトリを送らない: `export CF_SEND_TELEMETRY=false`
- 認証: 手元では `pnpm exec cf auth login`（OAuth）か、環境変数 `CLOUDFLARE_API_TOKEN`。アカウントは `export CLOUDFLARE_ACCOUNT_ID=<アカウント ID>`。`pnpm exec cf auth whoami` で確かめる
- 書き込むコマンドは、まず `--dry-run` を付けて送る内容（URL と本文）を見てから、`--dry-run` を外して実行する
- `--quiet`（`-q`）は結果の出力も消すので付けない

```bash
cd web
export CF_SEND_TELEMETRY=false
export CLOUDFLARE_ACCOUNT_ID=<アカウント ID>
pnpm exec cf auth login
pnpm exec cf auth whoami
```

## 一覧

| ファイル | いつ使うか |
|---|---|
| [deploy-rollback.md](./deploy-rollback.md) | デプロイの後に不具合が出た（前の版に戻す） |
| [cf-upgrade.md](./cf-upgrade.md) | `cf` の版を上げる |
| [cost-emergency.md](./cost-emergency.md) | 費用が想定より増えた（受付の停止・入り口の遮断・予算アラート。T-607） |

## 手順を足すとき

- この表に 1 行足す
- 手順の中の `cf` のコマンドは、今の版で `--dry-run` か `--help` で確かめたものだけを書く。`scripts/tests/test_runbooks.py` が、このディレクトリのすべての `*.md` のコードブロックの中の `cf` のコマンドとフラグが、入っている `cf` にあるかを確かめる（RB-1・RB-2。`web/node_modules` が要る）

```bash
python3 -m unittest discover -s scripts/tests -p 'test_runbooks.py'
```
