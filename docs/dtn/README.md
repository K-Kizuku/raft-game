# DTN（Ferry Container × DTN）の設計

親 Issue: #157。子 Issue: #158〜#172 と、2026-11-08 に足した DTN-00・DTN-16・DTN-17。

1 ゲームを 1 つの `sim.Engine` で動かす今の構成（MVP）を、**1 島 = 1 Container・1 隻 = 1 Container** に分ける。島は本物の Raft ノード、船は Bundle（BPv7）を永続的に預かって運ぶ DTN の中継ノードになる。Raft のメッセージは、実際に別々のプロセスと永続ストレージの間を、寄港したときだけ移動する（Store-Carry-Forward）。

## 位置付け

- **MVP の後の技術的な拡張**。MVP の必須要件・リリース条件には含めない
- **オーナーが実装する**。AI エージェント（Orchestrator・Worker）はこの Epic の Issue に着手しない。Issue はすべて `status:backlog` のまま
- **MVP と完全に分ける**（[D002](./adr/D002-environment-and-branch.md)）
  - ブランチ: `dtn/main`。`main`（MVP）は発表用に凍結し、DTN の変更を入れない。**`dtn/main` に直接コミットする**（PR・Codex のレビュー・Merge Gate・Ruleset は使わない）
  - デプロイ: prod・demo と別の新しい環境 `raft-dtn.kizuku-hackathon.work`。Worker・Durable Object・CostGuard・シークレットも別
- この `docs/dtn/` は `dtn/main` にだけ置く。既存の docs（`02`・`03`・`08` など）は書き換えない。DTN が既存の仕様を上書きするところは、このディレクトリに差分として書く

## DTN の不変条件

| ID | 不変条件 |
|---|---|
| D-1 | 島から島への Bundle の直接配送をしない（開幕式も通信しない） |
| D-2 | 有効な寄港 Contact の外で、島と船の配送を確定しない |
| D-3 | 各 Bundle の論理的な持ち主は常に 1 つ。移行中の物理的な写しは複数でよい |
| D-4 | 同じ Bundle の再送・ACK の消失で、宛先への適用を二重にしない |
| D-5 | 永続保存を確かめるまで、送り元の最後の有効な写しを消さない |
| D-6 | seed・入力・仮想イベントの列が同じなら、実ネットワークの遅延によらず同じ結果になる |
| D-7 | 復元・CLA の切断・古い epoch の応答が Raft の Safety を破らない |
| D-8 | 終了・中断の後に動いている Actor を残さず、停止の処理は再実行できる |
| D-9 | 島の状態と出力は、上限（horizon）の区切り方に依存しない |

## 読む順

| 文書 | 内容 |
|---|---|
| [adr/D001-architecture.md](./adr/D001-architecture.md) | 構成・時刻・同等性・開幕式などの大きな判断 |
| [adr/D002-environment-and-branch.md](./adr/D002-environment-and-branch.md) | ブランチ・デプロイ環境・費用の分離 |
| [adr/D003-bpv7-profile.md](./adr/D003-bpv7-profile.md) | BPv7 の対応範囲と CBOR のライブラリ |
| [architecture.md](./architecture.md) | 構成要素と責務、今の `sim.Engine` の責務の振り分け、制御とデータの経路 |
| [simulation-time.md](./simulation-time.md) | 仮想時間の進め方、同時刻の順序、開幕式、正規化トレース |
| [actor-protocol.md](./actor-protocol.md) | Coordinator・Actor Host・Actor の間の電文 |
| [handoff-and-contact.md](./handoff-and-contact.md) | 寄港（Contact）と、Bundle の所有権の引き渡し（Handoff） |
| [persistence-and-recovery.md](./persistence-and-recovery.md) | Actor Host の保存の形、確定の順序、復元、fencing |
| [bpv7-profile.md](./bpv7-profile.md) | Bundle の wire format |
| [environment.md](./environment.md) | DTN の環境の構成・費用・人がする作業 |
| [open-questions.md](./open-questions.md) | 未確定のこと・測定や PoC が要ること |
| [progress.md](./progress.md) | 実装の進み具合と残りの作業（再開のための記録） |

ADR は MVP の `docs/adr/` と番号が衝突しないよう、`docs/dtn/adr/D001` のように `D` を付けた別の番号にする。

## 決まったこと（2026-11-08、オーナーとの話し合い）

| # | 項目 | 決定 | 詳しく |
|---|---|---|---|
| 1 | ゴール | DTN-01〜15（MoQ まで）を、すぐ実装できる粒度で書く | この文書群と各 Issue |
| 2 | 同等性の基準 | **正規化トレース**。手紙の ID を「送信元の島・その島での通し番号」に付け替え、島・船ごとのイベント列と、最後の台帳・依頼の結果・スコアを比べる | [simulation-time.md §5](./simulation-time.md#5-正規化トレース同等性の基準) |
| 3 | 時刻の同期 | **島が自律**して自分の Tick・依頼の窓口・再送を回す。Coordinator は「ここまで進めてよい」という上限と、寄港での配達だけを指示する | [simulation-time.md §2](./simulation-time.md#2-時刻の進め方) |
| 4 | Coordinator の実行場所 | **Go の制御用 Container**（今の計画・スコア・航路のコードを使う） | [architecture.md](./architecture.md) |
| 5 | 開幕式 | 各島が同じ seed で全ノードの開幕式を**手元で再計算**し、自島のノードだけ残す。通信しない | [simulation-time.md §4](./simulation-time.md#4-開幕式) |
| 6 | 対象外のイベント | 故障・注文ラッシュ・漂流瓶・欠航は対象外。定義したステージは分散モードで起動を断る。嵐とデモの嵐は対応する | [simulation-time.md §6](./simulation-time.md#6-対象外のイベント) |
| 7 | 起動できる人とステージ | DTN の環境で**誰でも**（Turnstile・レート制限あり）。**全ステージ**（demo-izu の 6 島 6 隻も含む）。ただし欠航を定義した stage3 は、#365 で対応するまで断る | [environment.md](./environment.md) |
| 8 | ローカルの Actor Host | **Go の SQLite adapter**。DO 版と同じ契約テスト（JSON の fixture）を両方に通す | [persistence-and-recovery.md](./persistence-and-recovery.md) |
| 9 | 復元の単位 | **止まった Actor だけ**を復元する。その間は仮想時間を止める | [persistence-and-recovery.md §4](./persistence-and-recovery.md#4-復元) |
| 10 | 所有権の確定 | **Coordinator の台帳**で CAS する | [handoff-and-contact.md](./handoff-and-contact.md) |
| 11 | 画面 | エンジニアモードに分散の表示を足す（DTN-16） | [architecture.md §6](./architecture.md#6-画面dtn-16) |
| 12 | BPv7 | CBOR は `fxamacker/cbor`、BPv7 の構造は自前。EID は `ipn`。作成時刻は 0、仮想時間は Bundle Age ブロックで運ぶ | [D003](./adr/D003-bpv7-profile.md) |
| 13 | 分離 | ブランチ `dtn/main`、新しいデプロイ環境、別の CostGuard（1 日 1 USD・1 か月 10 USD） | [D002](./adr/D002-environment-and-branch.md) |
| 14 | 進め方 | `dtn/main` に直接コミットする。PR・Codex のレビュー・Merge Gate・Ruleset は使わない | [D002](./adr/D002-environment-and-branch.md) |

## Issue との対応

依存は [architecture.md §7](./architecture.md#7-実装の順序と-issue) を参照。

| ID | Issue | 作業 | 主な文書 |
|---|---|---|---|
| DTN-00 | （新規） | 下ごしらえ：島ごとの依頼の窓口・島ごとの手紙 ID・正規化トレース（参照モデル側） | simulation-time.md |
| DTN-01 | #158 | 設計の確定（この文書群のレビューと、PoC が要る項目の結論） | すべて |
| DTN-02 | #159 | Actor の電文と共通の Runtime | actor-protocol.md |
| DTN-03 | #160 | Island Runtime | architecture.md・simulation-time.md |
| DTN-04 | #161 | Ferry Runtime | architecture.md |
| DTN-05 | #162 | Coordinator（仮想時計と決定的な実行） | simulation-time.md |
| DTN-06 | #163 | BPv7 の Codec | bpv7-profile.md |
| DTN-07 | #164 | Actor Host の保存（Go の SQLite adapter・契約テスト） | persistence-and-recovery.md |
| DTN-08 | #165 | 永続 ACK と所有権の Handoff | handoff-and-contact.md |
| DTN-09 | #166 | Contact と RPC CLA | handoff-and-contact.md |
| DTN-10 | #167 | 復元と fencing | persistence-and-recovery.md |
| DTN-11 | #168 | ローカルでの分散実行と障害試験 | simulation-time.md §5・persistence-and-recovery.md |
| DTN-12 | #169 | Cloudflare の Actor Host・費用の予約・全停止・セッションを作る共通の処理と管理者用の試運転の口 | environment.md |
| DTN-13 | #170 | 分散モードを DTN の環境の一般の入り口（`POST /api/sessions`）・WS につなぐ・運用の手順 | environment.md |
| DTN-14 | #171 | MoQ の PoC | open-questions.md |
| DTN-15 | #172 | MoQ の CLA | handoff-and-contact.md §5 |
| DTN-16 | （新規） | エンジニアモードの分散の表示 | architecture.md §6 |
| DTN-17 | （新規） | 対象外にしたイベントへの対応（後続） | simulation-time.md §6 |
