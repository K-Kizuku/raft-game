# ADR-0011: インフラは AWS（CloudFront + S3 + ALB + ECS Fargate）、IaC は Terraform

- 状態: Proposed（仮決め）
- 日付: 2026-11-01
- 決めた人: Claude（オーナーの指定「AWS もしくは Cloudflare」から選択）
- 関連: C-03、NFR-06・15、[アーキテクチャ §5](../02-architecture.md)、ADR-0009、ADR-0012

## 背景

ADR-0009 により、Go のサーバーが WebSocket を長時間（1 ラウンド数分＋α）保持し、セッションの状態をメモリに持つ。フロントエンドは静的ファイル（SPA）。オーナーの指定は AWS か Cloudflare。ハッカソンの短い期間で、デモの日に確実に動くことが最優先。

## 検討した選択肢

### 案 A: AWS — CloudFront（単一ドメイン）＋ S3（SPA）＋ ALB ＋ ECS Fargate（Go）

- 長所: 常駐するコンテナで WebSocket とメモリ上の状態を素直に扱える。構成が枯れていて情報が多い。CloudFront で同一オリジンにでき、CORS が要らない
- 短所: ALB の固定費（月 20 USD 前後）がかかる。構成要素が多い（Terraform で吸収する）

### 案 B: Cloudflare — Workers（Static Assets で SPA を配信）＋ Durable Objects ＋ Containers（Go）

- 長所: 1 セッション = 1 Durable Object = 1 コンテナという対応が、このゲームの「セッションごとに独立した世界」と相性が良い。静的配信は無料で速い。スケールアウトが自然
- 短所: Containers は比較的新しく、情報・事例が少ない。コンテナの起動時間（コールドスタート）がデモで目立つ可能性がある。Durable Object から Containers への WebSocket の中継の作り込みが必要

### 案 C: AWS — App Runner / Lambda

- 長所: 運用がより簡単
- 短所: App Runner は WebSocket に対応していない。Lambda は長時間・状態を持つ接続に向かない（API Gateway の WebSocket はセッションの状態を別に持つ必要がある）

### 案 D: AWS — EC2 1 台

- 長所: 最も安く単純
- 短所: OS の管理・デプロイの仕組みを自分で作る必要がある

## 決定

案 A。

| 要素 | 設定 |
|---|---|
| リージョン | ap-northeast-1 |
| CloudFront | デフォルト → S3（OAC）。`/api/*`・`/ws/*` → ALB（キャッシュなし・ヘッダ・クエリを転送） |
| ALB | idle timeout 3600 秒、ヘルスチェック `/api/healthz` |
| ECS | Fargate、ARM64、0.5 vCPU / 1 GB、desired=1、public subnet（NAT Gateway なし） |
| イメージ | ECR、distroless |
| IaC | Terraform（`infra/terraform`）。state は S3 ＋ DynamoDB のロック |
| CI/CD | GitHub Actions（OIDC で IAM ロールを引き受ける） |
| 監視 | CloudWatch Logs、メトリクスフィルタ、アラーム |

IaC は Terraform にする（CDK も候補だが、Go のチームで TypeScript の CDK を使うより、宣言的な HCL のほうが学習の負担が小さいと判断した）。

### 概算費用（月額・東京リージョン・常時 1 タスク）

| 項目 | 目安 |
|---|---|
| ALB | 約 20〜25 USD |
| Fargate（ARM64・0.5 vCPU / 1 GB） | 約 15〜20 USD |
| CloudFront・S3・ECR・CloudWatch・パブリック IPv4 | 数 USD |
| 合計 | **約 40〜50 USD**（ハッカソン後はサービスを止めれば ALB 以外はほぼ 0） |

※ 料金は変わるので、構築時に AWS の料金ページで確認すること。

## 結果

- 良いこと: 実績のある構成で、デモの日の確実性が高い
- 悪いこと: 固定費がかかる。タスクを再起動すると遊んでいるセッションが切れる（デモの前はデプロイを凍結）

## 差し替えるときの影響範囲

`infra/`、`.github/workflows/deploy.yml`、アーキテクチャ §5。**`server/` と `web/` のコードは変わらない**（サーバーは環境変数 `PORT`・`ALLOWED_ORIGINS`・`DEMO_CONTROLS` だけに依存させる）。

Cloudflare（案 B）に差し替える場合の作業:

1. `infra/cloudflare/`（`wrangler.toml`）を作る。Worker は SPA の配信と、`/ws/sessions/{id}` を Durable Object に振り分ける
2. Durable Object が、セッションごとに Go のコンテナを起動して WebSocket を中継する（あるいは、1 つのコンテナを全セッションで共有する）
3. CI のデプロイを `wrangler deploy` に変える

## 見直すきっかけ

- オーナーが Cloudflare を希望したとき、または AWS のアカウントが使えないとき（[Q-03](../09-assumptions-and-open-questions.md#q-03)）
- 同時接続が増え、タスク 1 つで足りなくなったとき（ALB のスティッキーセッション、または案 B）
