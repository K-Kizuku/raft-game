# ADR-0019: AI エージェントの並列開発のため、ローカル環境を作業ディレクトリ単位で分ける

- 状態: Accepted
- 日付: 2026-11-06
- 決めた人: プロダクトオーナー（チームは人間 1 人と AI エージェントの並列開発、全員 Docker Desktop を使う）、Claude（具体化）
- 関連: ADR-0016、[ADR-0021](./0021-hootl-agent-development.md)、[実装計画 §5](../08-implementation-plan.md)、[判断の記録 R-03](../09-assumptions-and-open-questions.md)

この ADR は、ローカル環境（ポート・状態・名前）の分け方だけを決める。エージェントの役割・Issue からマージまでの流れ・Merge Gate などの運用は、[ADR-0021](./0021-hootl-agent-development.md) と [docs/agents/](../agents/README.md) を参照。

## 背景

- 開発は、人間 1 人が AI エージェントを並列に動かして行う
- 1 台の PC の上で、複数のエージェントが同時にサーバーを起動したり、テストを実行したりする
- ローカルでコンテナまで動かすには Docker が必要で、開発者は Docker Desktop を使う（ADR-0016）
- 何もしないと、次のような衝突が起きる
  - ポート番号
  - `cf` のローカルの状態の保存先（既定は全員で共有の `~/.config/cloudflare/state`）
  - Docker のイメージ名・コンテナ名
  - テストが使う一時ファイル

## 検討した選択肢

### 案 A: 作業ディレクトリ（`git worktree`）ごとに、ポート・状態・名前を分ける

- 長所: 1 台の PC の上で、何本でも並列に動かせる。追加のツールが要らない
- 短所: スクリプトで番号を割り振る仕組みが必要

### 案 B: エージェントごとに別の PC やリモートの環境を使う

- 長所: 衝突しない
- 短所: 費用と準備の手間がかかる

### 案 C: 1 本ずつ順番に動かす

- 長所: 単純
- 短所: 並列開発の利点がなくなる

## 決定

案 A。次の約束を、`Makefile`・スクリプト・CLAUDE.md で守らせる。

| 項目 | 約束 |
|---|---|
| 作業場所 | エージェントごとに `git worktree add ../raft-game-<名前> -b <ブランチ>` で作業ディレクトリを分ける |
| 番号 | 作業ディレクトリごとに `DEV_SLOT`（0〜99。先頭が 0 の値は不可）を決める。`scripts/dev-slot.sh` が 1〜`dev_slots`（`.agents/config.json`）の空いている番号を割り当て、0 は人が手で使う。`make dev`・`make dev-env` は `DEV_SLOT` が未設定ならエラーにする |
| ポート | 固定しない。`scripts/dev-env.sh` が `DEV_SLOT` から計算する（正本）。Go サーバー（`standalone`）は `DEV_SERVER_PORT = 18080 + DEV_SLOT×10`、Vite は `DEV_WEB_PORT = 15173 + DEV_SLOT×10`。それぞれ `+1`〜`+9` はデバッグ用に予約する（範囲 18080〜19079 と 15173〜16172 は重ならない）。値は `make dev-env`（`eval "$(scripts/dev-env.sh env)"` で読める）で確かめる |
| `cf` のローカルの状態 | 作業ディレクトリの中に置く（既定の共有の `~/.config/cloudflare/state` は使わない）。`make dev` は `cf dev` を通さずに `vite --strictPort` を直接起動し、状態は `web/vite.config.ts` の `persistState` で `web/.cloudflare/state` に置く（`cf dev` は `--persist-to` を受け付けない。`cf@1.0.0-beta.28` で確認）。`--local` で動く `cf` のコマンドには `--persist-to "$DEV_CF_PERSIST_TO"`（`web/.cloudflare/state`）を付ける |
| Docker | イメージ名は `raft-game-server:<名前>-<ハッシュ>`、コンテナ名は `raft-game-server-<名前>-<ハッシュ>`（`<名前>` は作業ディレクトリの basename を小文字・`[a-z0-9_.-]` に直した先頭 32 文字、空なら `wt`。`<ハッシュ>` は物理パスの `cksum` の 16 進数 8 桁）。ラベル `raft-game.worktree=<物理パス>` を付ける。`make clean-docker` は、このラベルとちょうどの名前のコンテナ・イメージだけを消す（イメージは `-f` なしで名前で消す。prune しない） |
| テスト | Go は `t.TempDir()` を使い、ポートは `:0`（自動で空きを選ぶ）で待つ。固定のファイル名・ポートを使わない。並列に実行して安全なこと（`go test -p` / `t.Parallel()`） |
| 共有の資源 | 本番・デモ用の環境へのデプロイ、ゾーンの設定の変更、Cloudflare のシークレットの更新は、**CI だけ**が行う。エージェントは直接行わない |
| データ | 生成物（`islands.json`、生成された TS の型）は、`make gen` で各自が作り直せる。手で編集しない |

## 結果

- 良いこと: エージェントを何本並列に動かしても、ローカルでは衝突しない。本番に影響する操作は CI に限られる
- 悪いこと: `DEV_SLOT` の管理が必要。Docker のディスクの使用量が増えるので、定期的な掃除が要る

## 差し替えるときの影響範囲

`Makefile`、`scripts/dev-slot.sh`、`scripts/dev-env.sh`、`CLAUDE.md`、`.gitignore`。

## 見直すきっかけ

- 並列で動かすエージェントの数が、PC の資源（CPU・メモリ・Docker）を超えるとき
- リモートの開発環境に移るとき
