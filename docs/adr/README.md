# ADR（Architecture Decision Records）

このプロジェクトの技術的な判断を記録します。**ADR の目的は、あとから「なぜこうしたのか」を確認でき、必要なら判断を差し替えられるようにすること**です。

## 状態の意味

| 状態 | 意味 |
|---|---|
| `Proposed` | 提案中。プロダクトオーナーのレビュー待ち。実装はこの内容で進めてよい |
| `Accepted` | レビュー済みで確定 |
| `Superseded by ADR-XXXX` | 別の ADR に置き換えられた（本文は消さずに残す） |
| `Deprecated` | もう当てはまらないが、置き換える ADR はない |

## 一覧

2026-11-06 に、プロダクトオーナーのレビュー（[判断の記録](../09-assumptions-and-open-questions.md)。仮決めの 24 項目と、その後の未確認事項・未決事項）を反映しました。

| # | タイトル | 状態 | 差し替えやすさ |
|---|---|---|---|
| [0001](./0001-record-architecture-decisions.md) | ADR で判断を記録する | Accepted | — |
| [0002](./0002-use-etcd-raft.md) | Raft の実装に etcd-io/raft を使う | Accepted | 難しい |
| [0003](./0003-drive-rawnode-single-goroutine.md) | `RawNode` を単一 goroutine で駆動する | Accepted | 難しい |
| [0004](./0004-virtual-time-discrete-event-simulation.md) | 仮想時間による離散イベントシミュレーション | Accepted | 難しい |
| [0005](./0005-deterministic-raft-rng-patch.md) | 決定性のため etcd-io/raft に乱数源を注入する最小パッチを当てる | Accepted | 中 |
| [0006](./0006-ferry-transport-outside-raft.md) | Ferry Transport を Raft の外側に置き、港キューに上限と合流の規則を設ける | Accepted | 中（規則の値は容易） |
| [0007](./0007-enable-prevote-and-checkquorum.md) | PreVote と CheckQuorum を有効にする | Accepted | 容易（設定値） |
| [0008](./0008-application-level-request-forwarding.md) | 提案の転送はアプリ層の「依頼書」で行う | Accepted | 中 |
| [0009](./0009-server-authoritative-simulation-over-websocket.md) | シミュレーションはサーバーで実行し、WebSocket で配信する | Accepted | 中 |
| [0010](./0010-frontend-react-pixijs.md) | フロントエンドは React + PixiJS v8 | Accepted | 中 |
| [0011](./0011-infrastructure-aws-ecs-fargate.md) | インフラは AWS（CloudFront + S3 + ALB + ECS Fargate）、IaC は Terraform | **Superseded by 0016** | — |
| [0012](./0012-no-persistence-in-mvp.md) | MVP では永続化しない | **Superseded by 0017** | — |
| [0013](./0013-open-data-build-time-etl.md) | オープンデータはビルド時の ETL で取り込む | Accepted | 容易 |
| [0014](./0014-monorepo-and-shared-types.md) | モノレポにし、WS の型は Go から生成する | Accepted | 中 |
| [0015](./0015-request-success-judged-at-origin-apply.md) | 依頼の成功は依頼元の島で適用された時点で判定する | Accepted | 容易 |
| [0016](./0016-infrastructure-cloudflare-with-cf-cli.md) | インフラは Cloudflare（Workers ＋ Durable Objects ＋ Containers）、構成は Cloudflare CLI（`cf`）だけで管理する | Accepted | 中（アプリのコアは影響しない） |
| [0017](./0017-persist-input-log-in-durable-object.md) | 操作の記録を Durable Object に保存して復元・リプレイに使い、リプレイの共有に R2 を使う | Accepted | 中 |
| [0018](./0018-cost-guard.md) | コストガード — 意図しない課金の増加を、アプリ側の上限で防ぐ | Accepted | 中（個別の仕組みは管理用 API で無効にできる） |
| [0019](./0019-parallel-local-development.md) | AI エージェントの並列開発のため、ローカル環境を作業ディレクトリ単位で分ける | Accepted | 容易 |
| [0020](./0020-public-repository-apache-license.md) | リポジトリを公開し、ライセンスは Apache-2.0 にする | Accepted | 中 |

## 判断を差し替える手順

1. 新しい ADR を [template.md](./template.md) から作る（番号は連番）
2. 「背景」に、元の ADR の何がなぜ合わなくなったかを書く
3. 元の ADR の状態を `Superseded by ADR-XXXX` に変える（本文は残す）
4. 元の ADR の「差し替えるときの影響範囲」に挙げたドキュメント・コードを更新する
5. [判断の記録](../09-assumptions-and-open-questions.md) の該当する項目の「決定」と「変更箇所」を更新する
6. この README の一覧を更新する

## 書き方の約束

- 1 つの ADR には 1 つの判断だけを書く
- 必ず「検討した選択肢」を 2 つ以上書き、それぞれの長所と短所を残す（あとで差し替えるときの出発点になる）
- 「差し替えるときの影響範囲」と「見直すきっかけ」を必ず書く
