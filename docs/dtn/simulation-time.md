# 仮想時間・同時刻の順序・開幕式・正規化トレース

判断は [ADR-D001](./adr/D001-architecture.md)。この文書は、DTN-00・DTN-03・DTN-05・DTN-11 の実装の契約。

## 1. 前提（今の `sim.Engine`）

- イベントの順序は `(At, Class, Seq)`（`server/internal/sim/sched.go`）

  | Class | 値 | 種類 | 分散版での持ち主 |
  |---|---|---|---|
  | `ClassFerryArrive` | 1 | 船の到着 | Coordinator |
  | `ClassFerryReady` | 2 | 何もしない | Coordinator（そのまま残す） |
  | `ClassRaftTick` | 3 | 全ノードの Tick と、待っている依頼の再試行（`retryWaits`） | **島** |
  | `ClassWorldEvent` | 4 | 嵐・デモの嵐（故障・ラッシュ・漂流瓶・欠航は対象外） | Coordinator |
  | `ClassRequestSpawn` | 5 | 依頼の発生 | **島**（発生元の島） |
  | `ClassRequestRetry` | 6 | 依頼の再送 | **島**（発生元の島） |
  | `ClassGameTimer` | 7 | 依頼の期限・沈黙の確認・ラウンドの終了 | Coordinator |

- プレイヤーのコマンドはキューを通らない。`RunUntil(T)` の後、`Now() = T` で即座に適用される。つまり、同じ時刻のどのイベントよりも後
- どのイベントの後にも `afterStep` が全ノードを `Process` する。ただし、ノードに新しい入力（Tick・Step・Propose）がなければ `Process` は何もしない。だから「入力のあった島だけ `Process` する」と同じになる

## 2. 時刻の進め方

### 2.1 上限（horizon）

時刻の印を `(t, c)` の組で表し、辞書式の順で比べる。`c = 8` は「その時刻のすべての種類の後」（コマンドの位置）を表す。

- `advance{horizon: (t, c)}` を受けた島は、自分の待ち行列のイベント `e` のうち `(e.At, e.Class) < (t, c)` のものを、`(At, Class, 島の中の Seq)` の順にすべて処理する
- 島は上限を超えて進まない。上限は単調に増える（減る上限は `invalid` で断る）
- **D-9（区切り方に依存しない）**: 島の状態と出力は、上限の区切り方によらない。`advance(10,8)` → `advance(20,8)` と `advance(20,8)` は同じ結果になる。これは島のイベントが島の中だけで決まることから成り立つ。DTN-03 の試験で確かめる

### 2.2 Coordinator のループ

1. 実時間の 50 ms ごとに、今と同じく `T = Now + 経過 × 速度`（1 回 1 秒まで）を決める
2. Coordinator のイベント（到着・嵐・期限・沈黙・ラウンドの終了）のうち `(At, Class) ≤ (T, 8)` のものを、順に取り出す。取り出したイベント `(t, c)` ごとに:
   1. **すべての島**を `advance{(t, c)}` で進め、出力を受け取る
   2. 受け取った出力を、[§3](#3-島の出力をまとめる順序) の順で CoordinatorCore に渡す
   3. そのイベントを処理する。到着なら [handoff-and-contact.md](./handoff-and-contact.md) の手順で配達し、島に `deliver` を送る
3. 最後に、すべての島を `advance{(T, 8)}` で進め、出力を処理し、`Now = T` にする
4. この間に届いたプレイヤーのコマンドは、`Now = T` の後に 1 つずつ適用する（今と同じ）。コマンドが Handoff を伴うときは、それが確定するまで次のコマンドとループの進行を止める

最初は「Coordinator のイベントごとに全島を進める」単純な規則にする。島の数は最大 6、Coordinator のイベントは疎（到着・期限など）なので、往復の数は「ループ 1 回に 1 回 ＋ イベントごとに 1 回」に収まる。関係する島だけを進める最適化は、測定の後に検討する（[open-questions.md](./open-questions.md) の Q-PERF）。

### 2.3 仮想時間を止める条件

次のどれかのとき、Coordinator は `Now` を進めない。ブラウザには `dtn.status{state: "recovering" | "ok", actor?}` のイベントで知らせる（DTN の環境でだけ出す。形は DTN-16 #364 で [06-protocol.md](../06-protocol.md) の書き方に合わせて決める。今の `notice` は島のキャラクターの通知なので使わない）。

- いずれかの Actor が応答しない・復元中（[persistence-and-recovery.md](./persistence-and-recovery.md)）
- Handoff が確定していない
- Coordinator 自身が復元中

仮想時間を止めている間に届いたプレイヤーのコマンドは、適用せずエラー `dtn_recovering` で断る（入力ログ・journal に記録しない。記録すると参照モデルとの再生が食い違う）。ブラウザは復旧の後に送り直す。

実時間の上限（初期値: 1 回の復旧につき 60 秒、1 ゲームの累計で 180 秒）を超えたら、ゲームを `dtn_actor_lost` で終える。遅延や再接続を「仮想的な手紙の欠落」に置き換えることはしない（D-6）。

### 2.4 一時停止・速度・「次の出来事まで」

意味は今と同じ。どれも Coordinator だけの状態で、Actor には伝えない（Actor は上限の分だけ進む）。「次の出来事まで」（`cmd.game.skip`、デモだけ）の `NextNotableAt` は Coordinator が計算する。

## 3. 島の出力をまとめる順序

島の出力（手紙を作った・捨てた・Term が変わった・役割が変わった・台帳に適用した、など）には、`(t, class, 島の中の Seq)` が付く。CoordinatorCore は、1 回の上限の間に集めた全島の出力を、次の順に並べて処理する。

```
(t, class, islandId, 島の中の Seq)
```

この順は、同じ時刻の `RaftTick`（全ノードを ID 順に処理する）の結果の並びと一致する。依頼の結果（スコアに効く）は、台帳への適用で起き、適用は `RaftTick`（Class 3）か到着（Class 1。島は 1 つだけ）の中でしか起きないので、スコアの途中経過も参照モデルと一致する。

`RequestSpawn`・`RequestRetry` が同じ時刻に複数の島で起きたときの並びは、参照モデル（全体の Seq の順）と一致しないことがある。これらが出すのは島ごとの列のイベントと、累積しない全体のイベント（`request.updated`）だけなので、[§5](#5-正規化トレース同等性の基準) の比べ方では差にならない。

## 4. 開幕式

- 島の Actor は `init` を受けたら、同じ stage・seed で全ノードの `cluster.Node` を手元に作り、`cluster.Opening` を実行して、自島のノードだけを残す。他のノードは捨てる
- 各ノードの乱数（`raft/<id>`、ADR-0005）は、開幕式の中で進んだ状態のまま残る。参照モデルと同じになる
- Coordinator も同じ計算をして、開幕式の役割の変化（`cmd.game.start` で再生するもの）を手元で得る
- 開幕式の電文は Bundle にしない・誰にも送らない。これは「同じ初期条件を各自が計算する」ことで、島の間の通信ではない（D-1 の例外を作らない）

## 5. 正規化トレース（同等性の基準）

### 5.1 手紙の ID（DTN-00）

- 手紙の ID を `islandId × 1,000,000,000 + n` にする。`n` はその島で作った手紙の通し番号（1 から）。依頼の手紙も同じ
- 型は `ferry.LetterID`（uint64）のまま。最大の島の ID は 6 なので、JS の安全な整数（2^53 − 1）に収まる
- 参照モデルもこの規則に変える。これで島ごとの列が、そのまま比べられる

### 5.2 列への振り分け

イベントを、次の持ち主の列に振り分ける。DTN-00 で `server/internal/simtrace` に実装し、参照モデル（`sim.Engine`）と分散版（Coordinator の出力）の両方に使う。

| イベント | 列 |
|---|---|
| `node.state`・`node.leader_elected`・`node.stepped_down` | `island:<id>` |
| `ledger.applied` | `island:<適用した島>` |
| `letter.created` | `island:<送信元>` |
| `letter.dropped`（港の規則 R1〜R6 による破棄） | `island:<その港の島>` |
| `letter.delivered` | `island:<宛先>` |
| `letter.moved`（積む・下ろす・倉庫から積む） | `ferry:<船>` |
| `ferry.departed`・`ferry.arrived`・`ferry.broken`・`ferry.repaired` | `ferry:<船>` |
| `phase.changed`・`forecast`・`route.blocked`・`route.unblocked`・`request.created`・`request.updated`・`letter.stale`・`island.updated` | `global`（累積しない） |
| `score.updated` | `global`（累積する） |
| `round.result` | `final` |

実装（`simtrace.Normalize`）で決めた細部:

- 手紙の ID の採番の規則には依らない。`letter.dropped`（中身に島がない）は、`letter.created` と `letter.moved` から追った手紙の今の位置（港・倉庫）の島、分からなければ送信元の島の列にする。`letter.moved` で下ろす（`loc` が `port`・`relay`）ものは、下ろす前にいた船の列にする
- 持ち主を決められないイベント（`letter.created` のない手紙の `letter.dropped`・`letter.moved` など）は `unknown` の列に入れる（参照モデルの完全な列では空になることを試験で確かめる）。表にない `type` は `global`（累積しない）に入れる
- `round.result` はドメインイベントではない（`sim.Engine.Result()`）。`Trace.WithResult` で別に渡す
- 列の名前は `island:<id>`（ID の昇順）・`ferry:<id>`（ID の昇順）・`unknown` の順に並べる。中身は正準の JSON（キーを辞書順・数は元の表記）で持つ
- `simcli record`・`simcli replay` の `--normalized-trace FILE` で、正規化トレースを JSON（2 字下げ）で書ける（成功したときだけ。`-` は使い方の誤り）

### 5.3 比べ方

1. **島・船の列**: 列ごとに、`(t, type, 中身)` の並びが一致する（`seq` は除く）
2. **`global` の累積しない列**: 同じ `t` ごとに、イベントの集合（中身の正準の JSON で並べたもの）が一致する
3. **`global` の累積する値**: 同じ `t` ごとに、その時刻の最後の `score.updated` が一致する
4. **最後の結果**: `round.result`、各島の台帳の内容、各依頼の最終の状態、各島の最後の `island.updated` が一致する
   - 台帳の内容は、島ごとの `ledger.applied` の中身を適用した順に並べたもの。依頼の最終の状態は、`request.created` の `request` に `request.updated` を順に重ねたもの
   - 各島の最後の `island.updated` は DTN-00 で足した（2. の集合は同じ時刻の中の順を比べないので、同じ時刻に同じ島の `island.updated` が 2 つあると、どちらが最後かを見逃すため。Q-TRACE）

`simtrace.Compare(ref, got)` は、最初に食い違った列・`t`・イベントを返す。1.〜3. の食い違いのうち `t` の最も早いもの（同じ `t` なら列 → `global` → `score`）を返し、1.〜3. が一致したときだけ 4. を比べる。DTN-11 の試験は、失敗したときに seed・入力・食い違いを `server/.dtn-failures/`（`.gitignore` に入れる）に保存する。

### 5.4 比べる組み合わせ

| 組み合わせ | 使う場所 | 速さ |
|---|---|---|
| 参照モデル ⇔ Coordinator ＋ メモリの中の Actor | DTN-05 の単体の試験。多くの seed で回す | 速い（`go test`） |
| 参照モデル ⇔ Coordinator ＋ 別プロセスの Actor（ローカル） | DTN-11 | 数秒〜 |
| 参照モデル ⇔ Cloudflare の DTN の環境 | DTN-12・13 の測定 | 人が実行 |

## 6. 対象外のイベント

- 故障（`breakdown`）・注文ラッシュ（`rush`）・漂流瓶（`drift`）・欠航（`cancellation`）を定義したステージは、分散モードで起動を断る（エラー `dtn_unsupported_stage`）
- 2026-11-08 の `main` では、stage1・stage2・demo-goto・demo-izu は定義していないので遊べる。**stage3（伊豆諸島、#300）は欠航を定義しているので、#365 で欠航に対応するまで断る**
- 漂流瓶は全島の配達の履歴を使うので、対応するには「送信元が自分の送った手紙の写しを持ち、そこから港に置き直す」形に変える必要がある（DTN-17）
- 嵐・デモの嵐は航路の表だけを変える。航路の表は Coordinator が持つので、そのまま対応できる
