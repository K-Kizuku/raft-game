# DesignDoc — 合意、乗船中。

| 項目 | 内容 |
|---|---|
| 版 | v0.4 |
| 更新日 | 2026-11-07 |
| 状態 | レビュー済み（[判断の記録](./09-assumptions-and-open-questions.md)を反映） |
| 対象読者 | 実装者（サーバー・フロントエンド・インフラ）、レビュアー |
| 前提ドキュメント | [要件定義書](./01-requirements.md)、[アーキテクチャ](./02-architecture.md)、[ゲームデザイン仕様](./04-game-design.md) |

この文書は「どう作るか」を、実装に着手できる粒度まで具体化したものです。ゲームのルールや数値（何点入るか等）は [04-game-design.md](./04-game-design.md)、WebSocket の電文は [06-protocol.md](./06-protocol.md)、データ形式は [07-data-spec.md](./07-data-spec.md) に分けています。

---

## 1. 概要

### 1.1 一言で

`etcd-io/raft` の `RawNode` を島の数だけ 1 つの Go プロセス（Cloudflare Containers 上の、1 ゲーム専用のコンテナ）内に立て、それらの間のネットワークを「プレイヤーが操作するフェリー」に差し替えた、**決定的な離散イベントシミュレーション**を作る。ブラウザはその世界を WebSocket（Cloudflare の Worker と Durable Object が中継する）で受け取って描画し、プレイヤーの操作をコマンドとして送り返す。

### 1.2 ゴール

1. Raft のアルゴリズムには手を入れず、輸送（Transport）だけをゲームにする
2. 同じ入力からは必ず同じ結果になる（決定性）。これにより、テスト・リプレイ・デモでの再現ができる
3. ゲームとして遊べる時間感覚（1 ラウンド 3 分前後）に、Raft の時間（tick）を対応させる
4. 実装を「シミュレーションのコア」「サーバー」「クライアント」に分け、並行して開発できるようにする

### 1.3 非ゴール

- 実ネットワーク上での Raft（ノードを別プロセスやコンテナに分ける構成）。将来の拡張として、境界だけ用意しておく
- ログのコンパクション・スナップショット（ラウンドが短く、ログは数百エントリにしかならない）
- メンバー変更（ConfChange）
- 線形化可能な読み取り（ReadIndex）

---

## 2. 設計の全体像

```mermaid
flowchart LR
    subgraph engine[sim.Engine]
        S[スケジューラ] --> C[cluster<br/>RawNode × N]
        S --> F[ferry<br/>港・船・航路]
        S --> G[game<br/>依頼・スコア・イベント]
        C -- 手紙 --> F
        F -- 配達 --> C
        C -- 適用結果 --> G
        G -- 依頼書 --> F
    end
    cmd[コマンド] --> S
    engine --> ev[ドメインイベント] --> trace[トレース] & client[クライアント]
```

- **世界はすべて `sim.Engine` の中にある**。`Engine` は I/O・goroutine・実時間を一切使わない
- 外から `Engine` を動かす方法は 2 つだけ: `Apply(cmd)`（操作）と `RunUntil(t)`（時間を進める）
- `Engine` の出力はドメインイベントの列だけ。クライアントへの送信・トレース・リザルト・エンジニアモードは、すべてこの列から作る

---

## 3. 時間モデル

### 3.1 2 種類の時間

| 名前 | 単位 | 意味 |
|---|---|---|
| 実時間 | — | 現実の時計。サーバーのセッションループだけが参照する |
| ゲーム内時間 `SimTime` | int64 ミリ秒 | シミュレーションの時計。0 = 航行開始。すべてのイベントはこの時刻で並ぶ |

`SimTime` の進み方は、セッションループが決めます（§12.2）。

- 通常: 実時間 1 秒 = ゲーム内 1 秒（`speed = 1.0`）
- 速度変更: 0.5x / 1x / 2x / 4x（P0）。デモ操作では 8x と「次の到着・出来事まで進める」も使える（A-17）
- 一時停止: 進まない

速度を変えても、`Engine` から見れば「`RunUntil` に渡される時刻の刻み方」が変わるだけです。イベントの時刻は実時間に依存しないので、**結果は変わりません**（コマンドが届いたゲーム内時刻だけは実時間に依存しますが、これは入力ログに記録されるので再現できます）。

### 3.2 Raft の tick との対応

`etcd-io/raft` は時間を知らず、`Tick()` が呼ばれた回数だけを数えます。そこで、ゲーム内時間の一定間隔ごとに全ノードの `Tick()` を呼びます。

| パラメータ | 既定値 | 意味 |
|---|---|---|
| `raft.tickMs` | 500 | ゲーム内 500ms ごとに 1 tick |
| `raft.heartbeatTick` | 2 | Leader は 1 秒ごとに定期連絡（Heartbeat）を出す |
| `raft.electionTick` | Stage 1: 120 / Stage 2 以降: 40 / デモ: 16 | 選挙タイムアウト。実際の値は `electionTick` 以上 `2×electionTick` 未満の乱数（Stage 1 なら 60〜120 秒、Stage 2 なら 20〜40 秒、デモなら 8〜16 秒） |

**調整の目安**: 選挙タイムアウト（`electionTick × tickMs`）は「Leader と、最も近い Follower の間をフェリーが往復する時間」の 2 倍以上にします。これより短いと、プレイヤーがどう頑張っても選挙が頻発します。逆に長すぎると、Stage 2 の学習目標（Heartbeat が途切れると選挙が始まる）が体験できません。Stage 1 は Log Replication に集中させつつ、終盤に「放っておかれた島が立候補の相談を始める」様子を軽く見せるため、長め（60〜120 秒）にしています。最終的な値はプレイテストで決めます（[A-07](./09-assumptions-and-open-questions.md#a-07)）。

### 3.3 Election Timeout の残り時間について

`etcd-io/raft` は各ノードの `electionElapsed` や乱数で決まったタイムアウト値を外部に公開していません。エンジニアモードで「選挙まであと何秒」を表示したい場合（FR-ENG-07、P2）は、ゲーム側で「最後に現在の Leader から手紙を受け取ってからの tick 数」を数えて、**推定値**として表示します。表示には必ず「推定」と書きます。

---

## 4. イベントスケジューラ

### 4.1 データ構造

```go
type SimTime int64 // ミリ秒

type Event struct {
    At    SimTime
    Class EventClass // 同じ時刻のイベントの処理順
    Seq   uint64     // 登録順。完全に同じ順序にするための最後の比較キー
    Kind  EventKind
    Data  any
}

// 優先度キュー（container/heap）の比較順: (At, Class, Seq)
```

### 4.2 イベントの種類と、同時刻のときの順番

同じ `SimTime` に複数のイベントがあるときは、`Class` の小さいものから処理します。

| Class | イベント | 内容 |
|---|---|---|
| 0 | `CommandApplied` | プレイヤーのコマンド（実際には `Apply` で即時に処理し、記録用にこのクラスを使う） |
| 1 | `FerryArrive` | 船が島に着く → その島宛ての手紙を配達 |
| 2 | `FerryReady` | 停泊時間が終わり、出航できるようになる |
| 3 | `RaftTick` | 全ノードの `Tick()`（ノード ID の昇順） |
| 4 | `WorldEvent` | 嵐の開始・終了、故障、漂流瓶など台本にあるイベント |
| 5 | `RequestSpawn` | 住民の依頼が発生 |
| 6 | `RequestRetry` | 依頼書を再送するかどうかの判定 |
| 7 | `GameTimer` | 満足度の減少判定・期限切れの判定・ラウンド終了 |

各イベントの処理のあとで、**全ノードの Ready を ID の昇順で処理しきる**（§5.4）。Raft の出力（手紙）は港に置かれるだけで、配達は船の到着を待つので、この処理は必ず止まります。

### 4.3 `Engine` の公開 API

```go
package sim

type Engine struct { /* 非公開 */ }

// 設定（Config）・シードから Engine を作り、開会式（§5.3）まで計算する。Phase() は Intro、Now() は 0。
func New(cfg Config, seed uint64, opt Options) (*Engine, error)

func (e *Engine) Now() SimTime
func (e *Engine) Phase() Phase // Intro / Opening / Sailing / Ended

// コマンドを Now() の時刻で適用する。成功したコマンドだけが入力ログに記録される。
// 失敗したときは、状態・イベント・入力ログ・時刻のどれも変えない。
func (e *Engine) Apply(cmd Command) Ack

// t 以下の時刻のイベントをすべて処理し、Now() を t にする。航行中（Sailing）でなければ何もしない。
// ラウンド終了時刻を超える t を渡しても、終了時刻で止まる。
func (e *Engine) RunUntil(t SimTime)

// 前回呼んでから発生したドメインイベントを取り出す（呼ばなければ溜まり続ける）。
func (e *Engine) DrainEvents() []DomainEvent

func (e *Engine) InputLog() []InputEntry // 成功したコマンドと適用した時刻（§10.1）
func (e *Engine) TraceHash() [32]byte    // §10.2

// 今の状態（再接続時などに送る全体の状態のうち Engine が持つ分）。状態を変えない。T-301 が protocol.Snapshot に変換する。
func (e *Engine) Snapshot() Snapshot
// 次に「プレイヤーに見せたい出来事」が起きる時刻。今は船の到着だけ（台本の出来事は T-204、依頼の発生は T-202 が足す）。
func (e *Engine) NextNotableAt() (SimTime, bool)
```

- `Phase` の `Intro` は `cmd.game.start` を待つ間（ステージ紹介）。この間の `RunUntil` は時刻を進めない（開会式はゲーム内時間を使わないため）
- 開会式は `New` の中で計算し（決定的な計算なので結果は同じ）、そのイベント（`phase.changed`・`node.*`）は `cmd.game.start` を適用したときに出す。Leader が決まらないような設定の誤りは、`New` のエラーになる
- `stage`（T-201）と `protocol`（T-301）ができるまでは、`sim` の中で定義した型（`Config`・`Command`・`Ack`・`DomainEvent`・`InputEntry`）で受け、返す。`stage.Stage` → `Config` の変換は T-201 で作る。`sim` は自分の型のまま。`protocol` は同じ JSON の形の型を別に持ち、`protocol/simconv` が変換する。両者の JSON が一致することはテストで確かめる
- `Snapshot` の `letters` は港と船の上の手紙だけ（ID の昇順）。`nodes` は最後に出した `node.state` の値。`lastSeq` 以下のイベントを当てはめた状態と一致する
- `letter.stale` は、ノードの Term が上がったとき、そのノードが送信元で港と船にある Raft の手紙のうち、新しく古くなったものを ID の昇順に 1 回だけ出す。各ノードの出力の処理の中で、役割の変化の後・手紙の前に出す
- `cmd.ferry.autoload` は `Apply` のコマンドで、ゲームデザイン §3.4 の順に空きを埋める。入力ログには `{ferryId, to}` だけが入る（リプレイでも同じ手紙が選ばれる）
- `NextNotableAt` は `FerryReady` を含めない。30 秒の上限と一時停止はセッションループ（§12.4）が持つ
- 後のタスクで次を足す: `NodeDetail(id)`（エンジニアモード用）は T-407、`Result()`（終了後のみ）は T-203、`Replay`（入力ログを最初から適用し直して同じ状態のエンジンを作る）は T-304
- Engine の誤り（`Process`・`PostRaft`・`Arrive`・`AdvanceTo`・`Push` のエラー、コードに対応しない `ferry` のエラー、台帳の結果の型の違い、テストモードの不変条件の違反）は panic する。正しく使えば起きない。セッションループ（T-303）が panic を回復し、そのセッションを `error`（`fatal`）で閉じる

---

## 5. Raft との統合（`internal/cluster`）

### 5.1 使うライブラリと版

- `go.etcd.io/raft/v3` **v3.7.0**（Go 1.26 が必要）
- v3.7 では `raftpb` が protobuf-go 製になり、メッセージは `*pb.Message`、フィールドはポインタ型（`m.GetTerm()` などのゲッターで読む）になっています。Web 上のサンプル（v3.5 以前、gogo-protobuf 製）とは書き方が違うので注意してください
- 乱数源を注入するための最小パッチを当てたものを `server/third_party/etcd-raft` に置き、`go.mod` の `replace` で使います（[ADR-0005](./adr/0005-deterministic-raft-rng-patch.md)）

### 5.2 `raft.Config`（既定値。ステージ定義で上書きできる）

| フィールド | 値 | 理由 |
|---|---|---|
| `ID` | 1..N（島の順） | — |
| `ElectionTick` / `HeartbeatTick` | §3.2 | — |
| `Storage` | `raft.NewMemoryStorage()` | 永続化しない（[ADR-0012](./adr/0012-no-persistence-in-mvp.md)） |
| `MaxSizePerMsg` | 4096 | 1 通の「記録して！」に数件のエントリをまとめる |
| `MaxInflightMsgs` | 4 | 1 つの Follower に対して、返事を待たずに送る「記録して！」は 4 通まで |
| `CheckQuorum` | true | 孤立した Leader が自分から退く（デモの見せ場）。[ADR-0007](./adr/0007-enable-prevote-and-checkquorum.md) |
| `PreVote` | true | 孤立していた島が復帰したときに、Term を無駄に上げて Leader を引きずり下ろすのを防ぐ。同 ADR |
| `DisableProposalForwarding` | true | 提案の転送は「依頼書」としてゲーム側で行う。[ADR-0008](./adr/0008-application-level-request-forwarding.md) |
| `AsyncStorageWrites` | false | 同期的な Ready ループにする |
| `RandIntn`（パッチで追加） | ノードごとの乱数源 | 決定性のため |
| `Logger` | トレースに流す独自の Logger | Raft の内部ログもエンジニアモードで見られるようにする（P1） |

### 5.3 起動と「開会式」

ゲーム開始直後は Leader がいません。ここで船を使った選挙から始めると、プレイヤーは何をすればよいか分からないまま時間が過ぎるので、**開会式フェーズ**を設けます。

1. 全ノードを `RawNode.Bootstrap(peers)` で同じメンバー構成で初期化する
2. **即時配達モード**（出てきたメッセージを船を使わず、FIFO で直接宛先に `Step()` する）で、Bootstrap の構成変更のエントリを全ノードで適用する。構成変更を適用する前の `Campaign()` は raft が無視するので、立候補より先に行う
3. ステージ定義の `initialLeader` の島で `Campaign()` を呼ぶ
4. 手紙がなくなるまで、FIFO で即時配達する（上限は開会式全体で 1,000 通。超えたらエラー）。条件を満たした時点で止めず、手紙がなくなるまで配達する。途中で止めると Follower の `MsgAppResp` が Leader に届かず、Leader の進捗の管理が古いまま航行が始まるため。Tick を呼ばないので、配達は必ず止まる
5. Leader がちょうど 1 人に決まり、全ノードがその Leader を知っていて、全ノードが Leader の最初の（空の）エントリまで適用したことを確かめる（満たさなければエラー）
6. 港は空のまま（開会式の手紙は港を通らない）、`SimTime = 0` で航行フェーズを始める

この手順は `cluster.Opening(nodes, initialLeader)` が行います（`sim.New` の中で呼ぶ）。開会式の間もドメインイベントは記録します。`Opening` が返す `RoleChange` の列を、`sim.Engine` が `cmd.game.start` の適用のときに `node.leader_elected`・`node.stepped_down`・`node.state` のイベントにするので、最初の選挙の結果も見られます。即時配達した手紙は港を通らず、手紙の ID もないので、手紙のイベントにはしません。画面上は「開会式：代表に〇〇島が選ばれました」という演出にします。

> **Leader を指定するのは「ゲーム側が Leader を決めない」（C-05）に反しないか？** `Campaign()` は「選挙タイムアウトが来た」のと同じ扱いで、立候補させるだけです。当選するかどうかは Raft の投票で決まります。実際に、開会式で他のノードが先に立候補していれば別の島が当選し得ます（決定的な乱数のもとでは毎回同じ結果になります）。

### 5.4 Ready ループ（V-5 の実装）

1 ノード分の処理です。この関数の外で Ready を扱うことは禁止します。

```go
// processReady は HasReady が false になるまで Ready を処理する。
// 戻り値の Messages は送信元の港に置かれる。エラーのときは途中までの結果を捨てて Output{}, err を返す。
func (n *Node) processReady() (Output, error) {
    var out Output
    for n.rn.HasReady() {
        rd := n.rn.Ready()

        // (1) 永続化：スナップショット → エントリ → HardState の順
        if rd.Snapshot != nil && !raft.IsEmptySnap(rd.Snapshot) {
            return Output{}, errUnexpectedSnapshot // コンパクションしないので来ない
        }
        if err := n.storage.Append(rd.Entries); err != nil { return Output{}, ... }
        if rd.HardState != nil && !raft.IsEmptyHardState(rd.HardState) {
            if err := n.storage.SetHardState(rd.HardState); err != nil { return Output{}, ... }
            n.term = rd.HardState.GetTerm()
        }

        // (2) 送信：永続化の後。Raft のバッファを共有しないよう複製する
        for _, m := range rd.Messages {
            out.Messages = append(out.Messages, proto.Clone(m).(*pb.Message))
        }

        // (3) 適用：Commit 済みのエントリを状態機械（台帳の複製）に順に適用する
        for _, ent := range rd.CommittedEntries {
            switch ent.GetType() {
            case pb.EntryNormal:
                if len(ent.GetData()) == 0 { break } // Leader 就任時の空エントリ。状態機械を呼ばない
                d := bytes.Clone(ent.GetData())
                res := n.sm.Apply(ent.GetIndex(), d)
                out.Applied = append(out.Applied, Applied{Index: ent.GetIndex(), Term: ent.GetTerm(), Data: d, Result: res})
            case pb.EntryConfChange, pb.EntryConfChangeV2:
                // Bootstrap 時のメンバー追加のみ。ゲーム中には発生しない。状態機械を呼ばない
                var cc pb.ConfChangeV2 /* デコード */
                n.rn.ApplyConfChange(cc)
            }
            n.applied = ent.GetIndex()
        }

        // (4) 次の Ready へ
        n.rn.Advance(rd)

        // ソフトステート（役割・Leader）が実際に変わっていればドメインイベントにする
        if ss := rd.SoftState; ss != nil && (ss.RaftState != n.role || ss.Lead != n.lead) {
            out.Events = append(out.Events, RoleChange{Node: n.id, Term: n.term,
                PrevRole: n.role, Role: ss.RaftState, PrevLead: n.lead, Lead: ss.Lead})
            n.role, n.lead = ss.RaftState, ss.Lead
        }
    }
    return out, nil
}
```

- 順序は「追記 → HardState 保存 → 送信 → 適用 → Advance」で固定（`etcd-io/raft` の README が求める順序を満たす）
- テストでは、`Storage` をラップして呼び出し順を記録し、この順序になっていることを確認します
- 公開の入口は `Process`（`processReady` を呼ぶだけ）。`sim.Engine` は各イベントの処理のあと、全ノードの `Process` を ID の昇順に呼ぶ。`RaftTick` では `Tick` を呼んでから `Process` を呼ぶ
- `cluster` は `sim` を import しない（docs/02-architecture.md §3 の依存の向き）。ドメインイベントは時刻を持たず、`sim.Engine` が `Process` を呼んだ時点の `Now()` を付ける
- 1 つの Ready の間に役割が 2 回変わったときは、Ready に入る最後の状態だけが見える（raft の仕様）
- 実際のコードでは API の細部（ゲッター名など）を v3.7.0 のソースに合わせてください

### 5.5 ノードがメッセージを受け取るとき

```go
func (n *Node) Deliver(m *pb.Message) error {
    err := n.rn.Step(m)
    // ErrStepLocalMsg / ErrStepPeerNotFound はゲームのバグなので、トレースに警告を出して続行
    return err
}
```

`Step()` が受け取らなかったメッセージ（古い Term の手紙など）も、エラーにはならず Raft の内部で無視されます。これが V-4 の安全性の根拠です。

### 5.6 1 ノードが持つもの

```go
// StateMachine は、このノードの状態機械（台帳の複製）。Commit 済みの通常のエントリのうち
// Data が空でないものを、index の昇順に 1 回ずつ受け取る。決定的でなければならない。
type StateMachine interface {
    Apply(index uint64, data []byte) any
}

type Node struct {
    id      uint64
    rn      *raft.RawNode
    storage nodeStorage    // raft.Storage＋Append・SetHardState（テストで記録用の包みに差し替える）
    sm      StateMachine   // このノード専用の台帳の複製（sim が *ledger.Ledger を包んで渡す）
    applied uint64         // 最後に適用したエントリの index（構成変更と空のエントリを含む）
    term    uint64         // 最後に保存した HardState の Term
    role    raft.StateType // 最後に観察した SoftState の役割（初期値 StateFollower）
    lead    uint64         // 最後に観察した SoftState の Leader（0 = 知らない）
}
```

`cluster` は `ledger` を import せず、`StateMachine` 越しに台帳を使います。`sim` が `*ledger.Ledger` を `StateMachine` に合わせる薄い包みを作って `NodeConfig.StateMachine` に渡します。台帳はノードごとに 1 つずつ持ちます（Raft の「状態機械の複製」そのもの）。V-2・V-3 の検査は、全ノードの台帳を比べて行います。

読み取り用の口として、`Status()` のほかに `Log() ([]LogEntry, error)` を持ちます。ストレージにあるエントリ（index 1 から LastIndex まで）の複製を返し、Raft の状態とストレージを変えません。不変条件の検査（§11）と、エンジニアモード（T-407）が使います。

---

## 6. Ferry Transport（`internal/ferry`）

### 6.1 手紙

```go
type LetterID uint64

type LetterKind uint8
const (
    LetterRaft    LetterKind = iota + 1 // Raft のメッセージ（0 は使わない）
    LetterRequest                       // 依頼書（アプリケーションの手紙）
)

type LocationKind uint8
const (
    LocPort      LocationKind = iota + 1 // 港（Island = その港の島 = 送信元）
    LocFerry                             // 船（Ferry）
    LocDelivered                         // 配達済み（Island = 宛先）
    LocDropped                           // 港の規則で破棄した
)

type Location struct {
    Kind   LocationKind
    Island uint64
    Ferry  FerryID
}

type Letter struct {
    ID        LetterID
    Kind      LetterKind
    From, To  uint64       // 島（= ノード）ID
    CreatedAt int64        // ミリ秒（sim.SimTime と同じ値。ferry は sim を import しない）
    Raft      *pb.Message  // Kind == LetterRaft のとき。生成後は変更しない
    Req       *RequestNote // Kind == LetterRequest のとき
    Location  Location
}
```

- 時刻は `int64`（ミリ秒、`sim.SimTime` と同じ値）。変換は `sim.Engine` が行う
- 中継倉庫（P1）の場所の種類は、まだ定義しない
- 手紙の ID はエンジン内の連番（1 から、決定的）
- `Raft` は Ready から取り出した時点で複製済み。以降は読み取り専用
- 手紙の内容（Raft メッセージ）は**一切書き換えない**（企画書 §5.3）

### 6.2 港キューと、その上限・合流の規則（V-7）

各島の港は、その島が**送信元**の手紙を、生成順に並べたリストとして持ちます。手紙を港に置くとき、次の規則を順に適用します。

| # | 対象 | 規則 | 破棄理由 |
|---|---|---|---|
| R1 | `MsgHeartbeat` / `MsgHeartbeatResp` | 同じ (種類, 送信元, 宛先) の手紙が港にあれば、古い方を破棄する | `superseded` |
| R2 | `MsgPreVote` / `MsgVote` / `MsgPreVoteResp` / `MsgVoteResp` | 同じ (種類, 送信元, 宛先) で、Term が新しい手紙以下のものを破棄する | `superseded` |
| R3 | `MsgAppResp`（`Reject=false`） | 同じ (送信元, 宛先) の拒否でない `MsgAppResp` を破棄する（新しい方が大きい index を伝えるため） | `superseded` |
| R4 | `MsgApp` | 同じ (送信元, 宛先) の `MsgApp` が `perLinkAppCap`（4）通を超えたら、最も古いものを破棄する | `overflow` |
| R5 | Raft の手紙全体 | 港にある Raft の手紙が `portCap`（24）通を超えたら、優先度が最も低い種類の、最も古いものを破棄する（優先度は低い順に Heartbeat 系 → AppResp → App → 選挙系） | `overflow` |
| R6 | 依頼書 | 同じ依頼 ID の依頼書が港にあれば、古い方を破棄する（再送で置き換わる） | `superseded` |

**なぜ破棄してよいのか**: Raft は「メッセージは失われ得る」前提で設計されています。破棄はネットワークでの欠落と同じで、Raft は後続の Heartbeat への応答などを契機に再送します（`etcd-io/raft` では、Heartbeat の応答を受けた Leader は、Follower のログが遅れていれば `MsgApp` を送り直します）。合流（R1〜R3）は「古い手紙を、同じ意味を持つ新しい手紙で置き換える」だけで、安全性には影響しません。

**上限の保証**: 港の Raft の手紙は `portCap` 以下、依頼書は「その島が依頼元で、まだ終わっていない依頼の数」以下、船の積み荷は積載量以下、中継倉庫（P1）は `relayCap`（12）以下。したがって手紙の総数は `島の数 × (portCap + 依頼の上限) + Σ積載量 + 島の数 × relayCap` で抑えられます。

**終わった手紙の片付け**: 配達済み・破棄した手紙は、`Transport.Reclaim` を呼ぶまで残り、`Letter` で引けます（ドメインイベントを作るため）。`sim.Engine` は、コマンド 1 つ・スケジューラのイベント 1 つの処理を終えてドメインイベントを作ったあとに `Reclaim` を呼びます。片付けの時点は操作の結果（手紙の ID・破棄・配達）に影響せず、手紙の ID は再利用しません。上の上限は、`Reclaim` の直後に `Transport` が持つ手紙の数にも当てはまります。

**古い手紙**: 送信元の今の Term より Term が古い手紙も、規則に当てはまらない限り港に残します（実際のネットワークでも古いパケットは残るため）。エンジニアモードでは「古い手紙」のタグを付けて表示します。送信元の Term が上がって古くなった手紙は、`letter.stale` で 1 回だけ知らせる（§4.3）。

### 6.3 フェリー

```mermaid
stateDiagram-v2
    [*] --> Docked
    Docked --> Docked: 積む / 降ろす（いつでも）
    Docked --> Sailing: 出航（readyAt 以降・航路が通れる・故障していない）
    Sailing --> Docked: FerryArrive（宛先の島宛ての手紙を配達、readyAt = 到着時刻 + dockMs）
    Docked --> Broken: 故障イベント（P1）
    Broken --> Docked: 修理完了
```

```go
type Ferry struct {
    ID            FerryID
    Name          string
    Capacity      int
    SpeedPermille int64      // 航行時間の倍率の千分率（1000 = 航路の標準時間、1200 = 1.2 倍の時間がかかる）
    State         FerryState // Docked / Sailing（Broken は P1 で足す）
    At            uint64     // Docked のときの島。Sailing のときは 0
    From, To      uint64     // Sailing のとき。Docked のときは 0
    DepartAt      int64      // Sailing のとき（ミリ秒、sim.SimTime と同じ値）
    ArriveAt      int64      // Sailing のとき
    ReadyAt       int64      // Docked のとき、この時刻から出航できる
    Cargo         []LetterID // 積んだ順
}
```

- 航行時間 = `(route.travelMs × ferry.SpeedPermille + 500) / 1000`（整数で四捨五入。1 未満なら 1）
- 出航の条件: `Docked` であること、`now ≥ ReadyAt`、行き先へ直接の航路があり、その航路が遮断されていないこと
- 故障（`Broken`）と中継倉庫は P1 で、まだ定義しない
- **到着時の処理**: 積み荷のうち、宛先がその島の手紙を、積んだ順に配達します。Raft の手紙は `Node.Deliver`（`proto.Clone` した複製を渡す）、依頼書はアプリケーション層（§8、T-202）へ渡します。配達のたびに、そのノードの Ready を処理します（返事がすぐにその島の港に並び、同じ船で持ち帰れる）
- 宛先が別の島の手紙は、そのまま積んでおきます（経由輸送、FR-FERRY-05）
- 航行中に嵐が来ても、船はそのまま進みます（[04-game-design.md §6](./04-game-design.md)）

### 6.4 航路

- 無向グラフ。辺は `travelMs` と `blockedUntil` を持つ
- 嵐は辺の集合を指定した時間だけ遮断する
- ネットワーク分断の状態（FR-ENG-08）は「遮断されていない辺だけでの連結成分」として計算する。ただし、これは「船が行き来できるか」を表すだけで、実際に通信できるかは船の運航次第です

### 6.5 中継倉庫（P1）

船から降ろした、その島宛てではない手紙を預かる場所です。預かった手紙は Raft では処理されません（企画書 §10.2）。別の船に積み直せます。

---

## 7. 共有台帳（`internal/ledger`）

### 7.1 コマンドの形式（Raft のエントリの `Data`）

```json
{
  "v": 1,
  "reqId": "s2-0007",
  "origin": 3,
  "kind": "firework.reserve",
  "resource": "firework:grand",
  "amount": 0
}
```

| `kind` | 内容 | 成功条件 | 優先 |
|---|---|---|---|
| `firework.reserve` | 花火の予約 | `resource` の花火がまだ誰にも予約されていない | P0 |
| `stall.reserve` | 屋台の出店枠 | 予約数が定員未満 | P0 |
| `budget.spend` | 共有予算からの支払い | 残高 ≥ `amount` | P1 |

エンコードは JSON（キーの順序は固定）。エンジニアモードでそのまま読めることを優先し、容量は気にしません。

### 7.2 適用の規則

```go
type Result struct {
    ReqID  string // Decode できないときと reqId が空のときは ""
    Status string // "ok" | "rejected" | "duplicate"
    Reason string // rejected のときだけ（下の一覧）。ほかは ""
}

func (l *Ledger) Apply(index uint64, data []byte) Result
```

- `Reason` の一覧: `already_reserved`（花火がすでに予約されている）・`full`（屋台の定員に達している）・`unknown_resource`・`resource_kind_mismatch`（`kind` と資源の種類が合わない）・`unknown_kind`・`unsupported_version`・`invalid_command`（Decode できない・`reqId` が空・`origin` が 0）
- `budget.spend` は P1 まで `unknown_kind` で `rejected` にする
- `index` が前回の `index` 以下（0 を含む）なら、プログラムの誤りとして panic する

1. `reqId` がすでに適用済みなら、何もせず `duplicate` を返す（再送による二重消費を防ぐ、FR-REQ-06）
2. `kind` ごとの成功条件を判定し、成功なら資源を消費して `ok`、失敗なら `rejected`
3. 結果を `reqId → Result` として記録する（`duplicate` の判定と、依頼元への通知に使う）

台帳は**決定的な純粋関数**です。同じ順序でエントリを適用すれば、すべてのノードで同じ状態になります（これが状態機械の複製の前提）。

### 7.3 不変条件（V-3）

- どの花火も、予約者は 0 件か 1 件
- どの屋台も、予約数は定員以下
- 予算の残高は 0 以上
- 同じ index まで適用した 2 つのノードの台帳は完全に一致する

---

## 8. 依頼の処理（アプリケーション層、`internal/game`）

### 8.1 島ごとの「役場」

各島には、住民の依頼を代表の島へ届ける「役場」があります（ゲーム側のロジックで、Raft の外にあります）。

```text
route(req, island):
    node = island のノード
    if node.role == Leader:
        err = node.rn.Propose(command(req))
        if err == nil:            req.status = 審議中（初回のみ）
        if err == ErrProposalDropped: 次の RaftTick で route をやり直す
    else if node.lead != 0 (Leader を知っている):
        依頼書を作る（From = island, To = node.lead）→ island の港へ（規則 R6）
        req.status = 送付待ち
    else:
        req.status = 送付待ち（代表が分からない）→ 次の RaftTick で route をやり直す
```

- **依頼の発生時**: 依頼元の島で `route(req, origin)`
- **依頼書が島 X に届いたとき**: `route(req, X)`。X が Leader でなければ、X が知っている Leader へさらに転送する（X の港に新しい依頼書を作る）
- **再送**: 依頼元の島で、最後に送ってから `retryMs`（25 秒）経っても依頼元の台帳に適用されていなければ、`route(req, origin)` をやり直す。同じ `reqId` なので、二重に適用されても台帳が `duplicate` として無視する

> Leader が提案したあと Commit される前に交代すると、そのエントリは新しい Leader によって上書きされ、消えることがあります。この場合、依頼は再送によって救われます。これも Raft の実際の振る舞いとして、エンジニアモードで観察できます。

### 8.2 依頼の状態の判定

依頼の状態は、エンジン全体を見渡して判定します（プレイヤーへの表示用）。

| 状態 | 判定 |
|---|---|
| 受付 | 発生した直後 |
| 送付待ち | 依頼書が港にある、または代表が分からず待っている |
| 輸送中 | 依頼書が船に積まれている |
| 審議中 | いずれかの Leader が `Propose` した |
| 共和国で確定 | いずれかのノードの台帳に、その `reqId` の最初のエントリが適用された（＝ Commit された） |
| 確定 / 予約失敗 | 依頼元のノードの台帳に適用され、結果が `ok` / `rejected` |
| 期限切れ | 期限を過ぎても「確定 / 予約失敗」になっていない（フラグとして重ねて表示。あとから確定もできる） |

スコアは「確定」になった時点で入ります（[ADR-0015](./adr/0015-request-success-judged-at-origin-apply.md)）。

---

## 9. 乱数とシード

### 9.1 派生

ラウンドのシード（uint64）から、用途ごとに独立した乱数列を作ります。用途を足しても、他の用途の乱数列は変わりません。

```go
func Sub(seed uint64, name string) *rand.Rand {
    h := fnv64a(name)
    return rand.New(rand.NewPCG(seed, h)) // math/rand/v2
}
```

| 名前 | 用途 |
|---|---|
| `requests` | 依頼の発生時刻・種類（台本以外の部分） |
| `events` | 台本で「この時間帯のどこか」と指定されたイベントの時刻 |
| `raft/<nodeID>` | ノードごとの選挙タイムアウトの乱数（パッチで注入） |
| `drift` | 漂流瓶で、どの過去の手紙を複製するか |

### 9.2 決定性を壊すもの（実装上の禁止事項）

`internal/sim` とその下のパッケージでは、次を禁止します。

- `time.Now` / `time.Since` などの実時間
- goroutine・チャネル・`select`
- `map` の走査順に依存する処理（走査する場合はキーをソートしてから）
- グローバルな `math/rand`（上記の派生乱数以外）
- 浮動小数点の計算をスコアや判定に使うこと（スコアは整数で計算する）

ここでの「その下」は、`sim.Engine` が組み立てて動かす世界（`internal/sim`・`internal/cluster`・`internal/ferry`・`internal/ledger`・`internal/game`）を指します。`stage`（YAML の小数を読む）・`protocol`・`server`（セッションループは実時間を使う）・`cmd` は対象外です。

CI と `make lint` では、golangci-lint の `forbidigo`（設定は `server/.golangci.yml`。テストのファイルも対象）で、上の 5 つのパッケージについて次を検出します。

- 実時間の関数: `time.Now`・`Since`・`Until`・`Sleep`・`After`・`AfterFunc`・`Tick`・`NewTimer`・`NewTicker`（`time.Second` などの定数・型は使ってよい）
- `math/rand`（v1）のすべて（別名で import しても検出する）
- `math/rand/v2` のパッケージ全体で共有される乱数（`rand.IntN`・`rand.Shuffle` など）。`rand.New(rand.NewPCG(…))` と、その `*rand.Rand` のメソッドは使ってよい
- `float32`・`float64` を型として書くこと（フィールド・変数・変換）

`forbidigo` は識別子しか見ないので、goroutine・チャネル・`select`・`map` の走査順への依存・型を書かない浮動小数点（`x := 1.5`）は検出できません。これらはレビューと各タスクの AC で確かめます。

`etcd-io/raft` の内部は、進捗の管理（`tracker`）で ID 順に走査するので決定的です。乱数だけがパッチの対象です。

---

## 10. 記録・リプレイ

### 10.1 入力ログ

```json
{
  "format": 1,
  "build": "git:3f2c1ab",
  "stageId": "stage2",
  "stageHash": "sha256:…",
  "seed": 20261101,
  "commands": [
    {"t": 0,    "cmd": {"type": "cmd.ferry.load",   "payload": {"ferryId": "f1", "letterIds": [12, 13]}}},
    {"t": 1200, "cmd": {"type": "cmd.ferry.depart", "payload": {"ferryId": "f1", "to": 2}}}
  ]
}
```

- `t` はコマンドを適用したゲーム内時刻
- 一時停止・速度変更のコマンドも記録しますが、エンジンの結果には影響しません（記録はするがリプレイ時には無視してよい）
- **保存先**: 入力ログは Durable Object の SQLite に保存します。コンテナはコマンドを適用したら `ack` に適用時刻 `t` を入れて返し、Durable Object が `(t, cmd)` を追記します（[ADR-0017](./adr/0017-persist-input-log-in-durable-object.md)）
- **復元**: コンテナが止まったら、Durable Object が新しいコンテナに入力ログを渡し、`Replay` で同じ状態に戻します
- **リプレイ** = 同じステージ・シードで `Engine` を作り、各コマンドの `t` まで `RunUntil` してから `Apply` することの繰り返し

### 10.2 トレース

ドメインイベント（[06-protocol.md §5](./06-protocol.md) のイベントと同じ型）を、発生順に連番を付けて JSON Lines で並べたものです。

- **トレースのハッシュ** = 各イベントの JSON（`seq`・`t`・`type`、そのあと中身のフィールドの順に並べた平らなオブジェクト。`encoding/json` の出力で、余分な空白なし）の後ろに改行（`\n`）を 1 つ付けて、出した順に連結したものの SHA-256。最後のイベントの後ろにも改行を付ける。イベントが 0 件なら空のバイト列の SHA-256。`DrainEvents` で取り出したかどうかに関係なく、`New` からの全イベントが対象。決定性のテスト（V-6）に使います
- リザルトの統計（Leader の交代回数、平均 Commit Latency など）はトレースから計算します。トレースを持っていれば、あとから別の統計を足すこともできます

### 10.3 統計の定義

| 指標 | 定義 |
|---|---|
| Leader の交代回数 | 開会式のあとに、新しい Term で Leader が誕生した回数 |
| Commit Latency | 依頼が Leader に `Propose` されてから「共和国で確定」になるまでのゲーム内時間 |
| 平均 Commit Latency | 確定した依頼の Commit Latency の平均 |
| 最長の音信不通 | 各島について「手紙が 1 通も配達されなかった最長の時間」の最大値 |
| 配った手紙の数 | 配達した手紙の数（種類別にも集計） |

---

## 11. 不変条件の検査（`internal/sim/invariants`）

テストモード（`Options.CheckInvariants = true`）では、イベントを 1 つ処理するたびに次を検査し、違反したら即座に panic します（本番では無効）。

| # | 不変条件 | 対応 |
|---|---|---|
| I-1 | 1 つの Term に Leader は最大 1 人（観測した Leader の記録を Term ごとに持つ） | Election Safety |
| I-2 | 2 つのノードで同じ (index, term) のエントリがあれば、そこまでの全エントリが一致する | Log Matching |
| I-3 | 全ノードで、適用済みのエントリは index ごとに一致する | V-2 |
| I-4 | エントリが Commit された時点で、そのエントリは過半数のノードのストレージにある | V-1 |
| I-5 | 各ノードの Term・Commit・Applied は減らない | 単調性 |
| I-6 | 台帳の不変条件（§7.3） | V-3 |
| I-7 | 港・船・中継倉庫の手紙の数が上限以下 | V-7 |

検査のしかた（T-107）:

- **呼ぶ時点**: 1 ステップ（成功した `Apply`・スケジューラのイベント 1 つ）の後処理の最後、`Reclaim` の後。全ノードの `Process` が済み、未処理の Ready がない
- **読むもの**: 各ノードの `Status`・`Log`（§5.6）、各台帳の `AppliedIndex`・`Snapshot`・`CheckInvariants`、`Transport.CheckInvariants`、まだ取り出していないドメインイベント。どれも読むだけで、状態を変える呼び出しをしない。前回の検査で観測した値（Term ごとの Leader・各ノードの Term・Commit・Applied・見たイベントの Seq）だけを覚えておく
- **I-1**: 前回の検査より後に出た `node.leader_elected` と、今の `Status` で Leader のノードの両方で観測する（1 ステップの中で Leader になってすぐ退いたノードも見逃さない）
- **I-2**: 2 つのログが初めて違った index より後に、Term の等しい index があれば違反。Index・Term・Type・Data を比べ、空のエントリと構成変更のエントリも対象にする
- **I-4**: 「Commit された時点で」より強い条件として、毎回 Commit 済みのすべての index を検査する（Commit 済みのエントリはどのログからも消えないので、正しい Raft では常に成り立つ）
- **I-5**: 補助の条件として「Applied ≤ Commit」も検査する
- **I-6**: 各台帳の `CheckInvariants` と「台帳の `AppliedIndex` ≤ ノードの Applied」を検査し、`AppliedIndex` が等しい台帳どうしの `Snapshot` を比べる（`AppliedIndex` はデータのあるエントリだけで進むので、ノードの Applied より小さいことがある）
- **I-7**: `Transport.CheckInvariants`（港の上限・同じ宛先の `MsgApp` の上限・合流・積載量）。依頼書の数の上限は T-202、中継倉庫の上限は P1 で足す
- **違反したとき**: 最初の 1 つで `panic("sim: invariant violated at t=<Now>: I-<n>: <説明>")` を出す（島の ID は 1 から）

---

## 12. サーバー（Go のゲームサーバーと、Cloudflare の Worker・Durable Object）

### 12.1 役割分担

| 層 | 実装 | 責務 |
|---|---|---|
| Worker | TypeScript（`web/worker/index.ts`） | 静的ファイルの配信（SPA）、`/api/*` と `/ws/*` の受付、署名の確認・Turnstile・レート制限・`Origin` の検査、セッションの作成、Durable Object への振り分け、管理用 API |
| Durable Object `CostGuard` | TypeScript（`web/worker/cost-guard.ts`） | 全体で 1 つ。費用の予約・精算・上限の判定、ガードの設定、切り替えの記録（[ADR-0018](./adr/0018-cost-guard.md)） |
| Durable Object `GameSession` | TypeScript（`web/worker/game-session.ts`） | 1 ゲームに 1 つ。コンテナの起動・停止、トークンの検証、ブラウザとコンテナの間の WebSocket の中継、入力ログの保存（SQLite）、コンテナが止まったときの復元、寿命の管理（アラーム） |
| Go サーバー `single` モード | Go（`server/cmd/server --mode single`）。コンテナの中で動く | 1 プロセス＝1 ゲーム。Durable Object からの WebSocket だけを受け、セッションループでシミュレーションを動かす |
| Go サーバー `standalone` モード | 同じバイナリ（`--mode standalone`） | ローカル開発とデモの保険（`make demo-local`）。1 プロセスで複数のゲームを持ち、§12.2 の公開 API を自分で提供する |

インフラの構成は [ADR-0016](./adr/0016-infrastructure-cloudflare-with-cf-cli.md) を参照してください。

### 12.2 公開 API（Worker が受ける）

| メソッド | パス | 内容 |
|---|---|---|
| GET | `/api/healthz` | ヘルスチェック（Worker が直接返す） |
| GET | `/api/stages` | ステージ一覧（ビルド時に `data/stages/` から作った JSON を Worker に同梱） |
| POST | `/api/sessions` | セッションを作る（`{stageId, seed?, turnstileToken}` → `{sessionId, token}`）。Turnstile とレート制限を確認し、`CostGuard` で費用を予約してから、Durable Object を作り、コンテナの起動を始める |
| GET | `/ws/sessions/{id}?token=…` | WebSocket に切り替え。**sessionId の署名を確認してから** Durable Object に転送する |
| GET / PATCH | `/api/admin/guards` | コストガードの設定の確認と切り替え（`ADMIN_TOKEN` が必要） |

詳細は [06-protocol.md](./06-protocol.md)。Go 側の WebSocket ライブラリは `github.com/coder/websocket` を使います（`context` 対応で、依存が少ないため）。

### 12.3 Durable Object `GameSession`

コンテナは、推奨の `durable_object` 方式で、Durable Object のコードから起動します（[ADR-0016](./adr/0016-infrastructure-cloudflare-with-cf-cli.md)）。

```ts
// 概略。API は Cloudflare の Durable Object Container API（ctx.container）に従う。
export class GameSession extends DurableObject {
  async init(req: { stageId: string; seed: number; demo: boolean; tokenHash: string }) {
    // SQLite: session(stage_id, seed, demo, token_hash, state, created_at, alarm_count)
    this.ctx.container.start({
      image: this.ctx.container.images.server,
      instance: this.env.CONTAINER_INSTANCE,   // 一般公開 "lite" / デモ "standard-1"
      enableInternet: false,                   // 外部への通信は禁止
      env: { MODE: "single", PORT: "8080" },
    });
    // start() は準備完了の前に戻る。monitor() で起動の失敗を検知する
    // アラーム: 起動から 30 分で強制終了（コストガード G2）
  }
  async fetch(request: Request) {
    // 1. token を検証（ハッシュで比較）
    // 2. ブラウザとの WebSocket を受け付ける
    // 3. getTcpPort(8080).fetch("http://container/healthz") が成功するまで待つ
    // 4. コンテナの /ws に WebSocket を開き、最初に internal.init（または internal.restore）を送る
    // 5. 双方向に中継する。ブラウザ → コンテナの cmd.* は cmdId ごとに覚えておき、
    //    コンテナ → ブラウザの ack(ok=true, t) を見たら (t, cmd) を SQLite の input_log に追記する
    // 6. ブラウザ由来の internal.* は破棄する（内部の電文はこの Durable Object だけが送る）
  }
  async onContainerLost() {
    // 新しいコンテナを起動 → internal.restore(stageId, seed, input_log) → コンテナが snapshot を返す
  }
  async onRoundEnded() {
    // ctx.container.destroy() ですぐに止め、実際に動いた時間で CostGuard に精算する
  }
  async alarm() {
    // 切断から 5 分、起動から 30 分のいずれかでコンテナを止める
    // アラームの再設定は 1 ゲームで 20 回まで（繰り返しが止まらなくなるのを防ぐ。コストガード G2）
  }
}
```

### 12.3.1 Durable Object `CostGuard`

全体で 1 つの Durable Object です（`idFromName("global")`。ほかの名前では作らない）。一般公開とデモ用の Worker が共有します（デモ用の Worker は、一般公開の Worker の `CostGuard` を参照する）。費用の予約・精算・上限の判定（G1）、受付の一括停止（G6）、ガードの設定の上書きと切り替えの記録を持ちます（[ADR-0018](./adr/0018-cost-guard.md)）。

`DurableObject` を継承せず、`fetch` のハンドラーで内部の API を受けます（RPC と `cloudflare:workers` を使わない。[ADR-0023](./adr/0023-durable-object-fetch-handler.md)）。中身は `web/worker/admin/ledger.ts` の `CostGuardCore`（SQLite の上の同期の処理だけ）で、Worker・`GameSession` からは `web/worker/admin/client.ts` の `CostGuardClient`（`getCostGuardClient(env)`）で呼びます。

| メソッド | パス | 本文 | 応答 |
|---|---|---|---|
| `POST` | `/reserve` | `{ sessionId, instance: "lite" \| "standard-1" }` | `200 { result: "ok" \| "budget_exhausted" \| "sessions_disabled" }` |
| `POST` | `/cancel` | `{ sessionId }` | `200 {}`。未精算の予約だけを取り消す（額 0） |
| `POST` | `/settle` | `{ sessionId, runSeconds }`（0〜86400 の整数） | `200 {}`。未精算の予約だけを、実際に動いた秒数で精算する |
| `GET` | `/view` | — | `200 GuardsView`（[プロトコル §2](./06-protocol.md) の `GET` の応答）・`503 guards_invalid` |
| `POST` | `/patch` | `{ change: 平らな形, reason, by }` | `200 GuardsView`・`400 bad_request`・`400 locked`・`503 guards_invalid` |

- URL のホストは何でもよい（クライアントは `https://cost-guard.internal/<op>` を使う）。知らないパスは 404、メソッドが違えば 405、本文が壊れている・型が違えば `400 bad_request`。`sessionId` は `^[A-Za-z0-9._-]{1,256}$`、`by` は 1〜64 文字
- `/patch` は、Worker を通らない呼び出しに備えて、`change` を管理用 API と同じ規則でもう一度確かめる
- アラームを使わない（`alarm()` を持たず、アラームを設定しない）。ログを出さない

SQLite の表（`constructor` で `CREATE TABLE IF NOT EXISTS`。`sql.exec` には 1 回に 1 つの文だけを渡す）:

```sql
CREATE TABLE IF NOT EXISTS ledger (
  session_id     TEXT PRIMARY KEY,
  instance       TEXT NOT NULL,          -- "lite" | "standard-1"
  rate_micro     INTEGER NOT NULL,       -- 予約したときの 30 分あたりの単価（µUSD）
  reserved_micro INTEGER NOT NULL,
  settled_micro  INTEGER,                -- NULL = 未精算
  status         TEXT NOT NULL,          -- "reserved" | "settled" | "cancelled"
  started_at     INTEGER NOT NULL,       -- 予約した時刻（UNIX ミリ秒）
  ended_at       INTEGER                 -- 精算・取り消しの時刻
);
CREATE INDEX IF NOT EXISTS ledger_started_at ON ledger (started_at);
CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);  -- 上書き（値は JSON）
CREATE TABLE IF NOT EXISTS audit (
  id INTEGER PRIMARY KEY AUTOINCREMENT, at INTEGER NOT NULL, by TEXT NOT NULL,
  change TEXT NOT NULL, reason TEXT NOT NULL                                       -- change は JSON
);
```

費用の計算:

- 金額は内部で**マイクロ USD の整数**（1 USD = 1,000,000 µUSD）で持ち、SQLite にも整数で入れる（浮動小数点の足し算の誤差を貯めない）。USD → µUSD は `Math.round(usd × 1,000,000)`
- 単価 `unitCostUsd[instance]` は 30 分あたりの最悪の費用（`lite` 0.0065・`standard-1` 0.040 USD）
- 予約額 = `ceil(単価µ × maxMinutes / 30)`（今の設定の `gameTime.maxMinutes`）
- 精算額 = `ceil(単価µ × runSeconds / 1800)`。単価は予約したときの値（`rate_micro`）を使う。予約額を超えても上限で切らない
- 行ごとの額は、`cancelled` は 0、`settled` は精算額、`reserved` は予約額。1 日・1 か月は **UTC** の暦で区切り、今日（今月）の 0 時以降に**予約した**行の合計を使う

`reserve` の判定（確認と書き込みの間に `await` を挟まない。Durable Object の中で直列になる）:

1. 設定 `GUARDS` が不正 → `sessions_disabled`
2. 同じ `sessionId` の行がすでにある → 何も変えずに `ok`（冪等）
3. `acceptGames` が `false` → `sessions_disabled`（予算より先に判定。G6）
4. `budget.enabled` が `true` で、今日の合計 + 予約額 > `dailyUsd` か、今月の合計 + 予約額 > `monthlyUsd` → `budget_exhausted`（行を足さない。ちょうど上限は通す。G1）
5. 行を足す（`budget.enabled` が `false` でも足す）。`ok`

設定 `GUARDS`（一般公開の Worker の JSON のバインディング。`CostGuard` だけが読む。デモ用の Worker の `GUARDS` は `CostGuard` に届かない）:

```json
{
  "budget":      { "enabled": true, "monthlyUsd": 30, "dailyUsd": 1 },
  "gameTime":    { "maxMinutes": 30, "maxAlarms": 20 },
  "turnstile":   { "enabled": true },
  "rateLimit":   { "enabled": true, "perIpPerMinute": 10 },
  "cpuMs":       50,
  "acceptGames": true,
  "unitCostUsd": { "lite": 0.0065, "standard-1": 0.04 }
}
```

- すべての項目が必須で、未知のキーや `signature` のキーがあれば不正（G3 を設定でも外せない）。値の範囲: 金額の上限は 0〜1000、`maxMinutes` は 1〜60 の整数、`maxAlarms` は 1〜100 の整数、`perIpPerMinute` は 1〜1000 の整数、`cpuMs` は 1〜300000 の整数（表示だけ）、単価は 0 より大きく 10 以下。文字列で渡されたら `JSON.parse` してから確かめる
- **不正・未設定なら拒否する側に倒す**: `reserve` は `sessions_disabled`、`view`・`patch` は `guards_invalid`（管理用 API は `503 guards_invalid`）。`cancel`・`settle` は帳簿の行の値だけで動くので続けて動く
- 管理用 API の上書き（`settings`。金額は µUSD の整数）を重ねたものが今の設定。保存された上書きが今の規則で不正なら、その上書きだけを無視して既定値を使う。変更は次の要求からすぐに効く（キャッシュしない）
- `signature`・`cpuMs`・`gameTime.enabled`・`gameTime.maxAlarms` は上書きできない（`locked`）

片付け: `reserve` で行を足したとき、先月の 1 日（UTC）より前に予約した `ledger` の行を消す。`audit` は足したとき、新しい 1000 件だけを残す。

- 見込みの費用の単価・上限は `GUARDS`（`cloudflare.config.ts`）に既定値を持ち、管理用 API で上書きできる
- 呼ぶのはゲームの開始と終了のときだけなので、全体で 1 つでも負荷は小さい

### 12.4 セッションループ（Go）

```go
func (s *Session) run(ctx context.Context) {
    step := time.NewTicker(50 * time.Millisecond)  // シミュレーションを進める間隔
    flush := time.NewTicker(100 * time.Millisecond) // イベントをまとめて送る間隔
    last := time.Now()
    for {
        select {
        case <-ctx.Done(): return
        case c := <-s.cmdCh:          // WS の読み取り goroutine から
            switch c.Type {
            case "cmd.game.speed":    // 0.5 / 1 / 2 / 4（デモ操作では 8 も）
                s.speed = c.Speed
            case "cmd.game.skip":     // デモ操作のみ：次の出来事まで進めて一時停止
                if t, ok := s.engine.NextNotableAt(); ok && t-s.engine.Now() <= 30_000 {
                    s.engine.RunUntil(t)
                }
                s.paused = true
            }
            ack := s.engine.Apply(c)   // ack には適用時刻 t が入る（ADR-0017）
            s.out.enqueueAck(ack)
        case now := <-step.C:
            if !s.paused && s.engine.Phase() != sim.Ended {
                dt := now.Sub(last)
                adv := min(sim.SimTime(float64(dt.Milliseconds())*s.speed), 1000) // 1 回に進めるのは 1 秒まで
                s.engine.RunUntil(s.engine.Now() + adv)
            }
            last = now
        case <-flush.C:
            s.out.send(s.engine.DrainEvents(), s.clock())
        }
    }
}
```

- `Engine` に触れるのはこの goroutine だけ（ロック不要）
- 速度はセッションループの中だけの値で、エンジンの結果には影響しません。8 倍速でも、1 ステップで進めるのは 400ms（＜上限の 1 秒）です
- 送信キューが詰まった場合（クライアントが遅い）は、イベントを捨てずに次回まとめて送ります。一定量（1,000 件）を超えたらイベント列を捨て、代わりにスナップショットを送ります
- `internal.restore` を受けたら、`sim.Replay` で入力ログの最後の時刻まで一気に進め、一時停止の状態でスナップショットを返します

### 12.5 寿命と上限

| 項目 | 値 | どこで守るか |
|---|---|---|
| 費用の上限 | 1 か月 30 USD・1 日 1 USD（見込みの費用の合計） | `CostGuard`（コストガード G1） |
| 同時に遊べるゲームの数 | 上限なし（R-01） | — |
| 1 ゲームの最長時間 | 30 分 | Durable Object のアラーム（G2） |
| アラームの再設定の回数 | 1 ゲームで 20 回まで | Durable Object（G2） |
| 切断されてからの保持時間 | 5 分（その間は自動で一時停止） | Durable Object のアラーム |
| ラウンド終了後 | すぐにコンテナを止める。入力ログと結果は Durable Object に残る | Durable Object |
| セッション作成のレート制限 | 1 IP あたり 10 回/分 ＋ Turnstile | Worker（G4） |
| コマンドのレート制限 | 1 セッションあたり 30 回/秒 | Durable Object |
| WS の受信メッセージの最大サイズ | 16 KiB | Durable Object と Go の両方 |
| Worker 1 回あたりの CPU 時間 | `limits.cpuMs`（値は T-007 で測って決める） | Cloudflare（G5） |

---

## 13. フロントエンド（`web/`）

### 13.1 構成

| 層 | 技術 | 責務 |
|---|---|---|
| 画面・パネル | React 19 + TypeScript | HUD、フェリー操作、島の詳細、通知、エンジニアパネル、リザルト |
| マップの描画 | PixiJS v8（React の外で命令的に使う） | 海・島・航路・船・手紙の山・嵐のエフェクト |
| 状態 | zustand | サーバーのスナップショットとイベントを反映した世界の状態 |
| ドラッグ＆ドロップ | dnd-kit | 港 → 船への積み込み |
| ビルド | Vite | — |
| テスト | Vitest / Playwright | — |

[ADR-0010](./adr/0010-frontend-react-pixijs.md) を参照。

### 13.2 状態の反映

```ts
type World = {
  stage: StageMeta;
  clock: { simTime: number; speed: number; paused: boolean; receivedAt: number };
  islands: Record<IslandId, IslandView>;
  routes: Record<RouteId, RouteView>;
  ferries: Record<FerryId, FerryView>;
  letters: Record<LetterId, LetterView>;
  requests: Record<RequestId, RequestView>;
  nodes: Record<IslandId, NodeView>;
  score: ScoreView;
};

function applyEvent(w: World, ev: ServerEvent): World // 純粋関数。Vitest でテストする
```

- `snapshot` を受け取ったら丸ごと置き換え、`events` を受け取ったら順に `applyEvent` する
- イベントの `seq` に抜けがあれば、サーバーにスナップショットを要求する（`cmd.sync`）

### 13.3 描画とアニメーション

- 船の位置はサーバーから送られてこない。`departAt` / `arriveAt` と、クライアントで推定したゲーム内時刻から補間する
  - 推定ゲーム内時刻 = `clock.simTime + (performance.now() − clock.receivedAt) × speed`（一時停止中は進めない）
- PixiJS のティッカーは、毎フレーム zustand のストアを直接読む（`useStore.getState()`）。React の再描画は経由しない
- 手紙は島ごとに「宛先別の山」として描き、数をバッジで表示する。1 通ずつのアイコンは島のパネル（DOM）に出す
- 手紙の種類は**色と形の両方**で区別する（[04-game-design.md §2](./04-game-design.md)）

### 13.4 エンジニアパネル

- E キーで表示を切り替える。表示中は `sub.engineer` で購読し、選んだノードの詳細（ログの末尾 50 件、Progress）を 2 Hz で受け取る
- ノードの役割・Term・Commit などの軽い情報は、エンジニアモードかどうかに関係なく常に `node.state` イベントで届く（簡易表示に使う）

### 13.5 サーバーなしでの開発

`simcli` でボットに 1 ラウンド遊ばせ、送られるはずのイベント列をファイルに書き出せます（`simcli record --stage stage1 --seed 1 --bot greedy > web/src/fixtures/stage1.json`）。フロントエンドはこれを再生するモックの WS クライアントで、サーバーなしで開発できます。

### 13.6 画面の遷移と URL

T-401 で決めた画面の骨組みです。画面の流れは [04-game-design.md §7.1](./04-game-design.md) に従います。ルートは `web/src/app/router/routes.ts`（純粋な関数 `parsePath`・`formatRoute`）、出し分けと転送は `web/src/app/App.tsx`、選んだステージと遊んでいるラウンドは `web/src/app/flow.ts`（純粋な reducer）が持ちます。

| パス | 画面 | 条件と転送 |
|---|---|---|
| `/` | タイトル（`TitleScreen`） | — |
| `/howto` | 遊び方（`HowToScreen`） | — |
| `/credits` | クレジット（`CreditsScreen`） | — |
| `/settings` | 設定（`SettingsScreen`。中身は T-412） | — |
| `/stages` | ステージ選択（`StageSelectScreen`） | — |
| `/stages/<stageId>` | ステージ紹介（`StageIntroScreen`） | 一覧の読み込み中・失敗はステージ選択と同じ表示。`stageId` が一覧に無ければ「ステージが見つかりません。」 |
| `/play` | ゲーム（`GameScreen`） | ラウンドが無ければ `/stages`、終わっていれば `/result` へ `replace` |
| `/result` | リザルト（`ResultScreen`。中身は T-408） | ラウンドが無ければ `/stages`、終わっていなければ `/play` へ `replace` |
| それ以外 | — | `/` へ `replace` |

- `<stageId>` は `^[a-z0-9][a-z0-9-]{0,31}$` に合うものだけ。末尾の `/` は 1 つだけ許し、大文字・小文字は区別し、パーセントエンコードは解かない。セグメントが余分なものは「それ以外」
- 転送の先はどれも転送しない画面なので、転送は繰り返されない。転送は描画の中ではなく effect の中で行う
- ステージの一覧（`GET /api/stages`）は、ステージ選択か紹介を最初に表示したときに 1 回だけ読み込み、以降はキャッシュする。失敗したときは「もう一度読み込む」だけが読み込み直す。形が 1 件でも違えば全体を失敗にする
- ルートが変わったら、新しい画面の `<h1>` にフォーカスを移す（最初の表示では移さない）

**`push`／`replace` の規則（終わったゲームに戻らない）**

| 操作 | 履歴 |
|---|---|
| タイトルからステージ選択・遊び方・クレジット・設定へ | `push` |
| ステージ選択で選ぶ → ステージ紹介 | `push` |
| ステージ紹介の「出航する」 → `/play` | `replace` |
| ゲームの終わり → `/result` | `replace` |
| リザルトの「同じ条件でもう一度」 → `/play` | `replace` |
| リザルトの「次のステージ」 → 次のステージの紹介・「ステージ選択」 → `/stages` | `push` |

タイトル → ステージ選択 → 紹介 → ゲーム → リザルトと進んだ後の履歴は `[/, /stages, /result]` になり、リザルトで戻ると `/stages` に戻ります。

**ゲーム画面のスロット**

`GameScreen` が各領域の要素（[04-game-design.md §7.2](./04-game-design.md) のレイアウト）を持ち、中に次のコンポーネントを置きます。スロットは props を取らず（`TutorialOverlay` を除く）、データはストア（T-403）から読みます（§13.3・[ADR-0010](./adr/0010-frontend-react-pixijs.md)）。後続のタスクは、ファイルのパスと export の名前を変えずに中身を書き換えます。

| 領域 | コンポーネント（ファイル） | 中身を書くタスク |
|---|---|---|
| 状況 | `HudBar`（`web/src/ui/hud/HudBar.tsx`） | T-406 |
| マップ | `MapView`（`web/src/game/MapView.tsx`） | T-404 |
| 島の詳細 | `IslandPanel`（`web/src/ui/island/IslandPanel.tsx`） | T-406 |
| フェリー操作 | `FerryPanel`（`web/src/ui/ferry/FerryPanel.tsx`） | T-405 |
| お知らせ（マップの隅に重ねる） | `Notifications`（`web/src/ui/hud/Notifications.tsx`） | T-406 |
| （重ねて表示） | `EngineerPanel`（`web/src/ui/engineer/EngineerPanel.tsx`） | T-407 |
| （重ねて表示） | `DemoKeys`（`web/src/ui/demo/DemoKeys.tsx`） | T-411 |
| （重ねて表示） | `TutorialOverlay`（`web/src/app/tutorial/TutorialOverlay.tsx`、`stageId` を受ける） | T-409 |

WS の接続と、ラウンドの終わりで `onRoundEnd` を呼ぶのは T-402・T-403 が `GameScreen` に足します。セッションの作成（`StageSelection.session` を入れること）は T-413 です。

**ルーターのライブラリを使わない理由**

ルートは 8 つで、ネストもデータの読み込みの仕組みも要らないので、History API と `useSyncExternalStore` の小さなルーター（`web/src/app/router/`）で足ります。依存を増やさずに済みます。ルーターのライブラリが必要になったら、ADR を書いて別の PR で入れます。

**セッションを URL に持たない**

`/play` と `/result` は URL にセッション（ID・トークン）を持たず、App の状態だけで持ちます。トークンが履歴や共有した URL に残らないようにするためです。再読み込みするとラウンドの状態は消え、`/stages` に転送されます（仕様）。

**島の紹介文**

ステージ紹介の島の紹介文と「学べること」のデータの出どころ（`/api/stages` に足すか、別の API か）は未定です（T-401 では「準備中」の表示）。

### 13.7 Raft の用語の併記

T-412 で決めた、設定「Raft の用語を併記する」（[FR-UI-10](./01-requirements.md)・[04-game-design.md §8](./04-game-design.md)・[Q-10](./09-assumptions-and-open-questions.md)）と、その表示の部品です。ファイルは `web/src/ui/terms/`（`preference.ts`・`glossary.ts`・`RaftTerm.tsx`）にあります。

**設定の持ち方**

設定は画面をまたいで使い、React の外（PixiJS のマップ）からも読むので、App の状態ではなく、ルーター（§13.6）と同じ「モジュールに 1 つの外部ストア＋`useSyncExternalStore`」で持ちます（`termsPreference`）。Provider が要らないので `App.tsx`・`main.tsx` を変えずに済み、ラウンドが消えても設定は残ります。ゲームの世界の状態ではないので、T-403 のストアには入れません。

- localStorage のキーは `raft-game.settings`、値は JSON の文字列 `{"v":1,"showRaftTerms":<boolean>}`（キーの順序はこのとおり）。既定はオフ（種明かしの驚きを守る）
- 読み込みは遅延し、最初の `getSnapshot()` で 1 回だけ読む。以降は値が変わるまで同じオブジェクトを返す
- 保存された値が壊れている・形が違う・`v` が 1 でないときはオフとして扱い、読んだだけでは書き戻さない（利用者が切り替えたときに上書きする。知らないキーは保たない）
- localStorage が使えない（`window` が無い・アクセスで `SecurityError`・`getItem` が投げる）ときや、`setItem` が投げる（容量の上限など）ときも、切り替えは画面にすぐ効く。そのときはスナップショットの `persistent` が `false` になり、設定画面に「このブラウザには設定を保存できませんでした。ページを閉じると元に戻ります。」を出す。例外は外に投げない
- 別のタブとの同期はしない（各タブは開いたときの値を読む）。`reload()` は読み込みをやり直すので、後で `storage` イベントにつなげられる
- [NFR-08](./01-requirements.md) との関係: 保存する値は真偽値 1 つだけで、サーバーには送らない。Cookie・sessionStorage は使わない。localStorage に触れるのは `preference.ts` の `openBrowserStorage` だけ

**部品の API**

| export | 使い方 |
|---|---|
| `RaftTerm`（`{ id: RaftTermId; children?: ReactNode }`） | 用語集の言葉。`children` を省くと `RAFT_TERMS[id].game` を出す。活用形などは `children` で渡す（例: `<RaftTerm id="commit">確定</RaftTerm>`） |
| `WithRaftTerm`（`{ raft: string \| null; children: ReactNode }`） | 任意の言葉（手紙の名前など）。`raft` が `null`・`""` なら何も添えない |
| `withRaftTerm(game, raft, show)` | 文字列が要るところ（PixiJS の文字・`aria-label`・`title`）。`show` で `raft` があれば「`game`（`raft`）」、なければ `game` |
| `letterRaftTerm(label)` | 手紙の `label`（[06-protocol.md §5.2](./06-protocol.md)）から添える用語。知らない `label` は `null`（§8） |
| `RAFT_TERMS`・`LETTER_RAFT_TERMS` | 下の表 |
| `useShowRaftTerms()`・`useTermsPreference()`・`setShowRaftTerms(value)` | React から読む・切り替える |
| `termsPreference` | 外部ストア（`getSnapshot`・`subscribe`・`setShowRaftTerms`・`reload`）。React の外から読む |

オフのとき（または `raft` が `null`・`""`）は `children` だけを返し、要素を足しません（レイアウトを変えない）。オンのときは後ろに `<small class="raft-term-note" data-raft-term="…">（<span lang="en">…</span>）</small>` を添えます（例:「代表（Leader）」）。括弧は部品と `withRaftTerm` が付けるので、表の `raft` には全角の括弧を含めません。

**`RAFT_TERMS`（ゲームの言葉 ⇔ Raft の用語。画面に出す文字列の正本）**

| `id` | `game` | `raft` |
|---|---|---|
| `leader` | 代表 | Leader |
| `follower` | 一般の島 | Follower |
| `preCandidate` | 立候補の相談中 | PreCandidate |
| `candidate` | 立候補中 | Candidate |
| `term` | 代表の任期 | Term |
| `ledger` | 共和国の台帳 | Log |
| `entry` | 記録 | Log Entry |
| `commit` | 共和国で確定 | Commit |
| `apply` | 確定 | Apply |
| `quorum` | 過半数 | Quorum |
| `node` | 島 | Node |
| `letter` | 手紙 | Message |
| `heartbeat` | 定期便 | Heartbeat |
| `vote` | 投票 | Vote |
| `electionTimeout` | 定期便が途切れる | Election Timeout |
| `checkQuorum` | 孤立した代表が退く | CheckQuorum |
| `partition` | 嵐 | Network Partition |
| `staleLetter` | 古い手紙 | 古い Term |

**`LETTER_RAFT_TERMS`（手紙の `label` ⇔ `Msg*`。[04-game-design.md §2.1](./04-game-design.md)）**

| `label` | 添える用語 |
|---|---|
| `heartbeat` | MsgHeartbeat |
| `heartbeat_resp` | MsgHeartbeatResp |
| `append`・`append_probe` | MsgApp |
| `append_resp` | MsgAppResp |
| `append_reject` | MsgAppResp・Reject |
| `prevote` | MsgPreVote |
| `prevote_resp` | MsgPreVoteResp |
| `vote` | MsgVote |
| `vote_resp` | MsgVoteResp |
| `request`・`other` | なし（住民の依頼書は Raft の手紙ではない） |

手紙のゲーム上の名前（「お元気ですか？」など）はこの表に持たず、表示する側（T-404・T-405・T-406）が §2.1 の名前を使い、`letterRaftTerm(label)` で添える用語を得ます。`LetterLabel` はローカルの型で、T-301 の生成された型ができたら置き換えます。

**使い分け**

- 通常画面と通知で、ゲームの言葉を出すところに使う。1 つの画面・パネルの中では、同じ言葉の**最初の 1 か所**にだけ付ける（毎回付けると読みにくい）
- エンジニアモード（T-407）とリザルトの解説（T-408）には使わない（設定に関係なく、常に Raft の用語で書く）
- React の外（PixiJS）では、`termsPreference.getSnapshot().showRaftTerms` を読み、`termsPreference.subscribe()` で変更を知って、`withRaftTerm` で文字列を作る
- 各画面に併記を付けるのは、それぞれのタスク（マップ T-404・フェリー操作 T-405・HUD／島の詳細／通知 T-406・チュートリアル T-409）。今は遊び方の画面の「手紙」「確定」に付けている
- export の名前・意味を変えるとき、`RAFT_TERMS` に項目を足すときは、この節も直す

---

## 14. テスト戦略

| 種類 | 対象 | 方法 | CI |
|---|---|---|---|
| 単体テスト | スケジューラ、港の規則、台帳、スコア計算 | 表駆動テスト | 毎回 |
| Ready の順序 | `cluster` | Storage をラップして呼び出し順を検査（V-5） | 毎回 |
| シナリオテスト | 分断・復帰・古い手紙・再送 | 台本どおりに船を動かし、期待する状態を検査（V-1, V-2, V-4） | 毎回 |
| シミュレーションテスト | エンジン全体 | ランダムに動くボット船長 × ランダムな嵐・漂流瓶で多数のシードを回し、§11 の不変条件を常に検査 | PR は 200 シード、main は 1,000 シード |
| 決定性テスト | エンジン全体 | 同じ入力ログを 2 回実行してトレースのハッシュを比べる。`RunUntil` の刻み方を変えた実行（速度の違い・早送り・次の出来事まで進める）とも比べる（V-6） | 毎回 |
| 復元テスト | エンジン全体 | 途中まで遊んだエンジンと、その入力ログから `Replay` したエンジンの `Snapshot` が一致すること（FR-GAME-09） | 毎回 |
| Worker / Durable Object | `web/worker/` | Vitest。中継・入力ログの追記・`internal.*` の破棄・トークンの検証。コンテナはモックの WS で置き換える | 毎回 |
| ファジング | コマンド列 | `go test -fuzz`。不正なコマンドで panic しないこと、不変条件が保たれること | 夜間（P1） |
| パッチの検証 | `third_party/etcd-raft` | 上流との差分が乱数の部分だけであることをスクリプトで確認。上流のテストがすべて通ること | 毎回 |
| フロントの単体 | `applyEvent`、補間 | Vitest + 記録したイベント列 | 毎回 |
| E2E | 主要な操作の流れ | Playwright（ローカルでサーバーを起動し、Stage 1 で積む → 出航 → 配達まで） | 毎回（P1） |
| 負荷テスト | サーバー | Go 製のボットクライアントで 50 セッションを同時に動かし、tick の遅れを計測 | 手動 |

**ボット船長**: テスト用の自動プレイヤーです。`random`（ランダムに積んで出航）と `greedy`（優先度の高い手紙が多い島へ向かう）の 2 種類を用意します。`greedy` はステージの難易度調整（クリアできるか）にも使います。

---

## 15. 観測性

- ログ: Go は `log/slog` の JSON 出力、Worker と Durable Object は `console.log` の JSON。セッション ID を必ず付ける
- 保存と閲覧: `cloudflare.config.ts` の `observability.logs` とコンテナの `observability` を有効にし、Cloudflare のダッシュボードで見る（`cf` はベータ版で、リアルタイムのログ表示に未対応）
- 記録する指標（ログの項目として出す）: セッションの作成数、コンテナの起動にかかった時間、1 ステップの処理時間の p99、送信キューの長さ、復元の回数、エラー数
- デモの前: 起動時間と復元の回数を確認する

---

## 16. セキュリティ

| 脅威 | 対策 |
|---|---|
| セッションの乗っ取り | セッション作成時に推測できないトークン（128 bit）を発行し、WS の接続時に検証する |
| 大量のセッション作成による資源の枯渇・課金の増加 | コストガード（[ADR-0018](./adr/0018-cost-guard.md)）：Turnstile、IP ごとのレート制限、ゾーンの WAF と AI クローラーの遮断、費用の上限 |
| でたらめな ID による Durable Object の大量作成 | sessionId に HMAC の署名を付け、Worker で検証してから Durable Object を呼ぶ（G3。**無効にしない**） |
| 管理用 API の悪用 | `ADMIN_TOKEN`（`--secrets-file` で渡す）を必須にし、切り替えの記録を残す。合言葉は定期的に変える |
| シークレットの漏えい（公開リポジトリ） | `.env` などは `.gitignore` に入れ、CI でシークレットの検査（secret scanning）を行う |
| 巨大・大量のメッセージ | WS の受信サイズの上限、コマンドのレート制限、JSON のデコードは型を決めて行う |
| 他のサイトからの WS 接続 | Worker で `Origin` ヘッダーを許可リスト（`ALLOWED_ORIGINS`）と照合する |
| コンテナへの直接の接続 | コンテナは外部に公開しない。Durable Object からだけ接続する。ブラウザ由来の `internal.*` の電文は Durable Object が破棄する |
| デモ用の機能の悪用 | 嵐を起こす・8 倍速・次の出来事まで進める機能は、`demo` モードでデプロイした別の Worker（`DEMO_CONTROLS=1`）でだけ有効にする |
| 依存ライブラリの脆弱性 | Dependabot、`govulncheck`、`npm audit` を CI で実行する |

---

## 17. 検討した代替案（要約）

| 論点 | 採用 | 主な代替案 | ADR |
|---|---|---|---|
| Raft の実装 | etcd-io/raft | hashicorp/raft、自作 | [0002](./adr/0002-use-etcd-raft.md) |
| Raft の駆動方法 | `RawNode` を単一 goroutine で | `Node`（goroutine + チャネル） | [0003](./adr/0003-drive-rawnode-single-goroutine.md) |
| 時間 | 仮想時間の離散イベントシミュレーション | 実時間で動かす | [0004](./adr/0004-virtual-time-discrete-event-simulation.md) |
| 決定性 | Raft に乱数を注入する最小パッチ | 記録の再生のみ、時刻を外から制御 | [0005](./adr/0005-deterministic-raft-rng-patch.md) |
| 輸送 | Raft の外側に配送層を置く | Raft を改造する | [0006](./adr/0006-ferry-transport-outside-raft.md) |
| Raft の設定 | PreVote と CheckQuorum を有効 | 無効 | [0007](./adr/0007-enable-prevote-and-checkquorum.md) |
| 提案の転送 | アプリ層の依頼書 | Raft 標準の転送（`MsgProp`） | [0008](./adr/0008-application-level-request-forwarding.md) |
| 実行場所 | サーバーで実行し WS で配信 | Go を WASM にしてブラウザで実行 | [0009](./adr/0009-server-authoritative-simulation-over-websocket.md) |
| フロントエンド | React + PixiJS | Phaser、Canvas 直書き | [0010](./adr/0010-frontend-react-pixijs.md) |
| インフラ | Cloudflare（Workers ＋ Durable Objects ＋ Containers）、`cf` で管理 | AWS（ECS Fargate）、Wrangler、Terraform | [0016](./adr/0016-infrastructure-cloudflare-with-cf-cli.md)（[0011](./adr/0011-infrastructure-aws-ecs-fargate.md) を置き換え） |
| 永続化 | 入力ログを Durable Object に保存、リプレイの共有は R2 | 保存しない | [0017](./adr/0017-persist-input-log-in-durable-object.md)（[0012](./adr/0012-no-persistence-in-mvp.md) を置き換え） |
| オープンデータ | ビルド時の ETL | 実行時に取得 | [0013](./adr/0013-open-data-build-time-etl.md) |
| リポジトリ | モノレポ + 型の生成 | リポジトリを分ける、protobuf | [0014](./adr/0014-monorepo-and-shared-types.md) |
| 依頼の成功判定 | 依頼元の島で適用された時点 | Commit された時点 | [0015](./adr/0015-request-success-judged-at-origin-apply.md) |
| コストの抑制 | アプリ側のコストガード（費用の上限など 6 つ） | 予算アラートだけ、同時に動く数の上限 | [0018](./adr/0018-cost-guard.md) |
| ローカルの開発環境 | 作業ディレクトリごとに分けて並列に動かす | 別の PC、1 本ずつ順番に | [0019](./adr/0019-parallel-local-development.md) |
| 公開とライセンス | 公開・Apache-2.0 | MIT、ライセンスなし | [0020](./adr/0020-public-repository-apache-license.md) |

---

## 18. リスクと対策

| リスク | 影響 | 対策 |
|---|---|---|
| ゲームとして面白くならない（待ち時間が長い・選挙が頻発する） | 大 | 最初に `simcli` とボットでパラメータを探索し、早い段階で紙芝居レベルの画面でプレイテストする。パラメータはすべてステージ定義で変えられるようにする |
| 手紙が多すぎて画面が煩雑になる | 中 | 港の合流規則で数を抑える。宛先別の山とバッジで表示する。「おまかせ積み込み」 |
| etcd-io/raft v3.7 の API がサンプルと違い、実装に時間がかかる | 中 | M1（コアの実装）の最初に、3 ノードを即時配達で動かす最小の例を作る |
| 乱数のパッチが上流の更新で当たらなくなる | 小 | 版を v3.7.0 に固定する。パッチは 1 か所だけ。上流への提案も検討する |
| 本番でのデモ中に再デプロイやコンテナの再起動でゲームが切れる | 大 | 入力ログから復元する（FR-GAME-09）。デモの前はデプロイを凍結する。ローカルで起動する手順（`make demo-local`）を用意しておく |
| `cf` と `durable_object` 方式のコンテナがベータ版で、コマンドや設定の形式が変わる | 中 | `cf` の版を固定する。上げるときは専用の PR で `cf build` と `--dry-run` を確認する |
| bot のアクセスなどで課金が意図せず増える | 大 | コストガード（[ADR-0018](./adr/0018-cost-guard.md)）。予算アラートを 5 USD に設定する |
| コストガードがデモの最中に受付を止めてしまう | 中 | 管理用 API でその場で外せる。デモの前に 1 日の上限の残りを確かめる |
| `lite` では 8 倍速や復元が遅い | 中 | T-007 と負荷テストで測り、足りなければ `standard-1` に上げる（R-02） |
| コンテナの起動待ちがデモで目立つ | 中 | ステージ選択の時点で起動を始め、ステージ紹介と開会式の間に隠す。リハーサルで起動時間を計る |
| Durable Object とコンテナの API の細部が想定と違う | 中 | 実装計画 T-007（M0 のスパイク）で、最小の構成を実際にデプロイして確かめる |
| 計画書の記述が地域によって薄い（長崎県など） | 小 | 記述の薄い地域の航路は `creative` として扱い、画面で区別する。生成した JSON をコミットするので、ETL が間に合わなくても進められる |

---

## 19. 未解決の論点

[09-assumptions-and-open-questions.md](./09-assumptions-and-open-questions.md) の「未決事項」を参照してください。
