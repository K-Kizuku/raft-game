# ADR-D002: DTN は別のブランチ `dtn/main` と、prod・demo と別の新しいデプロイ環境で進める

- 状態: Accepted
- 日付: 2026-11-08
- 決めた人: オーナー
- 関連: #157、[ADR-0016](../../adr/0016-infrastructure-cloudflare-with-cf-cli.md)（cf）、[ADR-0018](../../adr/0018-cost-guard.md)（費用）、[environment.md](../environment.md)

## 背景

DTN は `sim`・`ferry`・Worker・Durable Object を大きく組み替える。うまくいかなかった場合でも、発表で見せるもの（MVP）が確実に動くように、MVP を凍結しておきたい（オーナーの指示）。

## 検討した選択肢

### 案 A: `main` に機能フラグ（既定は無効）で入れる

- 短所: `main` の `sim`・Worker を書き換えるので、凍結した MVP を壊す危険がある。prod・demo の予算とデプロイを共有する

### 案 B: ブランチと環境を分ける

- 長所: `main`・prod・demo に一切手を入れない。DTN の失敗が MVP に波及しない
- 短所: `main` を取り込む手間・CI とデプロイの設定・環境の用意（人の作業）が要る

## 決定

**案 B**。

### ブランチ

- `dtn/main` を、2026-11-08 の `main`（`55b942a`）から作った
- DTN の設計書・実装はすべて `dtn/main` に入れる。作業のブランチは `dtn/main` から切り、PR の行き先も `dtn/main`
- MVP を凍結するとき、`main` にタグ（例: `mvp-final`）を付ける。そのあと `main` を `dtn/main` に 1 回取り込んでから、DTN-00 を始める
- `dtn/main` から `main` へはマージしない

### デプロイの環境

- prod（`raft.kizuku-hackathon.work`）・demo（`raft-demo.kizuku-hackathon.work`）と別の、新しい環境を作る。ホスト名は `raft-dtn.kizuku-hackathon.work`
- Worker・Durable Object の名前空間・Container の image・CostGuard・シークレットを、prod・demo と共有しない
- CostGuard は DTN の環境の専用。上限は **1 日 1 USD・1 か月 10 USD**。prod・demo の予算を減らさない
- 誰でも起動できる（Turnstile・レート制限は今と同じく有効）。全ステージを遊べる
- デプロイは CI だけが行う（`dtn/main` の push → DTN の環境）。prod・demo のデプロイのワークフローは `main` だけを対象にしたままにする

## 結果（この判断によって起きること）

- 良いこと: MVP（`main`・prod・demo）は DTN の作業の影響を受けない
- 悪いこと・受け入れるリスク:
  - 人の作業が要る（[environment.md §5](../environment.md#5-人がする作業)）。今の CI（`ci.yml`）は `main` への PR だけを対象にしているので、`dtn/main` への PR では CI が動かない
  - 同じ Cloudflare のアカウントを使う場合、アカウント全体の上限（同時に動かせる Container の数など）を prod・demo と共有する（[open-questions.md](../open-questions.md) の Q-ACCOUNT）

## 差し替えるときの影響範囲

[environment.md](../environment.md)・`web/cloudflare.config.ts`（`dtn/main` の）・`.github/workflow-sources/`（`dtn/main` の）。

## 見直すきっかけ

- 発表が終わり、MVP を凍結しておく必要がなくなったとき（`dtn/main` を `main` に戻すかを決める）
