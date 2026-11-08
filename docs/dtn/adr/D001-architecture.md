# ADR-D001: 1 島・1 隻を独立の Actor にし、島が自律して進み、Coordinator が時刻の上限と寄港だけを指示する

- 状態: Proposed（DTN-01 #158 でオーナーが確かめて Accepted にする）
- 日付: 2026-11-08
- 決めた人: オーナー（話し合いの記録: [README の「決まったこと」](../README.md#決まったこと2026-11-08オーナーとの話し合い)）
- 関連: #157、[ADR-0004](../../adr/0004-virtual-time-discrete-event-simulation.md)（仮想時計）、[ADR-0005](../../adr/0005-deterministic-raft-rng-patch.md)（乱数のパッチ）、[ADR-0017](../../adr/0017-persist-input-log-in-durable-object.md)（入力ログ）、[ADR-0018](../../adr/0018-cost-guard.md)（費用）

## 背景

今の `sim.Engine` は、1 つのプロセスの中で全島の `RawNode`・港・船・仮想時計を持ち、全島を同時に見ながら進む（main `ffd8720` で確認）。

- 500 ms ごとの 1 つの `RaftTick` イベントが、全ノードを ID 順に `Tick` する
- どのイベントの後にも、`afterStep` が全ノードを `Process` する
- 依頼の窓口 `route()` は、島の Raft の状態をその場で読んで Propose する（判断に使うのはその島の状態だけ）
- 開幕式 `cluster.Opening` は、フェリーなしで全ノード間を即座に配達する
- 手紙の ID（`Transport.lastID`）とイベントの連番（`Engine.seq`）は全体で 1 本。`traceHash` はこれと、島をまたぐイベントの並びに依存する

これを 1 島 = 1 Container・1 隻 = 1 Container に分けるには、時刻の進め方・同等性の基準・開幕式・全体を見渡す処理の置き場所を決める必要がある。

## 検討した選択肢

### 時刻の進め方

- **案 T1: 全イベントの厳密なロックステップ**。Tick も含めて Coordinator が 1 つずつ配り、永続化の確定を待って次へ進む
  - 長所: 単純。今の順序と自明に一致する
  - 短所: 500 ms（仮想時間）ごとに全島と往復する。島が自分では何も決めず、「DTN の独立したノード」らしさが薄い
- **案 T2: 島が自律し、Coordinator は上限（horizon）と寄港だけを指示する**（保守的な並列離散イベントシミュレーション）
  - 長所: 島どうしはフェリーを介してしか影響し合わないので、寄港と寄港の間なら島は独立に進められる。往復が少ない。島が自分の Tick・依頼の窓口・再送を持つ
  - 短所: 同時刻の順序の規則（どの種類のイベントより前で止めるか）を正しく決める必要がある

### 同等性の基準

- **案 E1: `traceHash` の完全一致**。Coordinator が全出力を受け取り、今と同じ順序で ID と連番を振り直す
  - 短所: Coordinator が全部の手紙を仲介し、島の自律性が下がる
- **案 E2: 正規化トレース**。手紙の ID を島ごとの番号にし、島・船ごとのイベント列と最後の結果を比べる
- **案 E3: 結果だけ**（台帳・依頼の結果・スコア）
  - 短所: 途中の経過のずれを見逃す

### 開幕式

- **案 O1: 各島が手元で再計算**する。同じ seed で全ノードの開幕式を計算し、自島のノードだけ残す
- **案 O2: Coordinator が開幕式の電文だけを中継**する（不変条件 D-1 の例外になる）
- **案 O3: 開幕式もフェリーで運ぶ**（ゲームの序盤が変わり、同等性が成り立たない）

### Coordinator の実行場所

- **案 C1: Go の制御用 Container**（今の計画・スコア・航路のコードを使う。Container が 1 つ増える）
- **案 C2: DO の中で Go を WASM にして動かす**（ADR-0028 のビルドを使う。DO の CPU とメモリの上限が未検証）
- **案 C3: TypeScript で書き直す**（ゲームのロジックが二重になる）

## 決定

**T2・E2・O1・C1** を選ぶ。加えて、次の構造にする。

### 1. `sim.Engine` を「島の核」と「Coordinator の核」に分け、同じ部品を単一プロセスでも分散でも使う

- **島の核（IslandCore）**: 1 島の `cluster.Node`・台帳・港（合流と破棄の規則 R1〜R6）・積み替えの倉庫・依頼の窓口と再送・島で起きるイベント（`RaftTick`・`RequestSpawn`・`RequestRetry`）の待ち行列
- **Coordinator の核（CoordinatorCore）**: 仮想時計・全体のイベント（到着・嵐・依頼の期限・沈黙の確認・ラウンドの終了）・航路の表・船の時刻表・依頼の帳簿・スコア・手紙のメタデータの写し（所有権の台帳を兼ねる）
- **参照モデル**: 今の `sim.Engine` を、N 個の IslandCore と 1 つの CoordinatorCore を**今と同じ順序で**呼ぶ形に組み替える（DTN-00）。`traceHash` が組み替えの前後で一致することで、組み替えが振る舞いを変えていないと確かめる
- **分散版**: 同じ CoordinatorCore が、Actor の電文（[actor-protocol.md](../actor-protocol.md)）を通して、別プロセスの IslandCore を動かす。島と船の Actor は、それぞれ 1 つの IslandCore・1 隻の船だけを持つ

同じコードを両方で使うので、同等性の試験が確かめるのは「分散の仕組み（時刻の上限・Handoff・永続化・復元）が結果を変えないこと」になる。ゲームのロジックの二重実装はしない。

### 2. 時刻の進め方（T2）

- 島は自分のイベントを `(時刻, 種類の優先度, 連番)` の順に処理する。Coordinator は「`(t, c)` より前まで進めてよい」という上限だけを渡す
- Coordinator は、島に影響する出来事（到着・プレイヤーのコマンド）を起こす前に、関係する島をその手前まで進める
- 島の進み方は、上限の区切り方に依存しない（`AdvanceTo(10)` → `AdvanceTo(20)` と `AdvanceTo(20)` は同じ結果）。これを不変条件 D-9 として試験する
- 詳しくは [simulation-time.md](../simulation-time.md)

### 3. 同等性（E2）

- DTN-00 で、手紙の ID を `島の ID × 1,000,000,000 + その島での通し番号` に変える（参照モデルも同じ規則にする）。型は今の `LetterID`（uint64）のままで、JS の安全な整数の範囲に収まる
- 比べるのは、島ごと・船ごとのイベント列（順序と時刻と中身）、全体のイベントの同じ時刻ごとの集合、最後の台帳・依頼の結果・スコア。詳しくは [simulation-time.md §5](../simulation-time.md#5-正規化トレース同等性の基準)

### 4. 開幕式（O1）

- 各島の Actor は、起動時に同じ stage・seed で、全ノードを手元に作って `cluster.Opening` を実行し、自島のノードだけを残して他を捨てる
- これは「同じ初期条件を各自が計算する」ことで、島の間の通信ではない。D-1 の例外を作らない。Coordinator も、開幕式の結果（Leader と役割の変化）を同じ方法で手元で計算する

### 5. 全体を見渡す処理は Coordinator がメタデータで行う

- 古い手紙の検出（`emitStale`）・依頼の状態の更新（`refreshRequests`）・スコア・自動の積み込み（`autoload`）の選択は、Coordinator が手紙のメタデータ（ID・種類・送信元・宛先・Term・メッセージの種類・所在）で行う。Raft のメッセージの中身（Bundle のペイロード）は Coordinator に集めない
- 島は、手紙を作った・捨てた・Term が変わった・台帳に適用した、をメタデータとして Coordinator に報告する

### 6. 対象外

- 故障・注文ラッシュ・漂流瓶・欠航は、最初の対象から外す。これらを定義したステージは、分散モードで起動を断る（DTN-17 で後から対応する）
- Time Warp（楽観的な並列化）・ライブマイグレーション・管理用の別の Raft・島の間の直接配送・船の間の直接の積み替え・BPv7 の全機能・Contact Graph Routing・Fragmentation・BPSec

## 結果（この判断によって起きること）

- 良いこと:
  - 島の Actor は、自分の Tick・選挙・依頼の窓口を自分で回す。Coordinator が決めるのは時刻の上限と寄港だけ
  - ゲームのロジックは 1 つの実装のまま。参照モデルとの同等性の試験が意味を持つ
  - 開幕式に D-1 の例外がない
- 悪いこと・受け入れるリスク:
  - DTN-00 で `sim`・`ferry` を大きく組み替える。MVP の凍結の後に `dtn/main` でだけ行う（[D002](./D002-environment-and-branch.md)）
  - 手紙の ID の規則が変わるので、`dtn/main` の `traceHash` は MVP と一致しなくなる。組み替え（ID は今のまま）と ID の変更を別の PR に分け、前者は `traceHash` の一致で確かめる
  - Coordinator の Container が 1 つ増える（費用の予約に含める）

## 差し替えるときの影響範囲

- T1 に変える: [simulation-time.md](../simulation-time.md) の上限の規則と、[actor-protocol.md](../actor-protocol.md) の `advance` を、イベントごとの `tick`・`spawn`・`retry` に分ける
- C2 に変える: [environment.md](../environment.md) の Container の数と費用、DTN-12 の構成

## 見直すきっかけ

- DTN-12 の測定で、1 回の上限の往復が遅すぎて等倍で遊べない（[open-questions.md](../open-questions.md) の Q-PERF）
- ステージに、島どうしが船を介さずに影響し合う仕組み（漂流瓶など）を入れたくなったとき
