# 合意、乗船中。— 離島Raft共和国

> その手紙が届かないと、島のお祭りが始まらない。

日本の離島を舞台にした、フェリー配送パズルゲームです。プレイヤーは郵便船の船長として島々の手紙を運びます。その手紙の正体は、本物の分散合意アルゴリズム Raft（[`etcd-io/raft`](https://github.com/etcd-io/raft)）が送受信するメッセージです。**船を止めると、分散合意も止まります。**

- ハッカソンのテーマ: オープンデータ × 推し（推し: 分散システム・分散合意アルゴリズム）
- オープンデータ: 国土交通省の離島関連データ（離島振興対策実施地域一覧・離島振興計画書 ほか）

## 状態

**設計段階**（コードはまだありません）。ドキュメントは [`docs/`](./docs/README.md) にあります。

| ドキュメント | |
|---|---|
| [企画書](./docs/00-proposal.md) | 原文 |
| [要件定義書](./docs/01-requirements.md) | 何を作るか |
| [アーキテクチャ](./docs/02-architecture.md) | 全体の構成図 |
| [DesignDoc](./docs/03-design-doc.md) | どう作るか |
| [ゲームの進行表](./docs/05-game-progression.md) | プレイヤーが何をどの順で体験するか |
| [ADR](./docs/adr/README.md) | 技術判断の記録 |
| [仮決め一覧](./docs/09-assumptions-and-open-questions.md) | オーナーのレビュー待ちの判断 |
| [実装計画](./docs/08-implementation-plan.md) | 次にやること |

## 技術構成（仮決め）

| 領域 | 技術 |
|---|---|
| フロントエンド | React 19 + TypeScript + PixiJS v8 + Vite |
| バックエンド | Go 1.26、`go.etcd.io/raft/v3` v3.7.0（乱数源を注入するパッチ付き） |
| 通信 | WebSocket（JSON） |
| インフラ | AWS（CloudFront + S3 + ALB + ECS Fargate）、Terraform |

## リポジトリ構成（予定）

```text
server/   Go のゲームサーバーとシミュレーションのコア
web/      ブラウザのクライアント
data/     オープンデータの原本・島データ・ステージ定義
infra/    Terraform
docs/     ドキュメント
```

## ライセンス

未定（[Q-12](./docs/09-assumptions-and-open-questions.md#q-12)）。同梱予定の `etcd-io/raft` は Apache License 2.0 です。
