# アーキテクチャ

| 項目 | 内容 |
|---|---|
| 版 | v0.1（仮決め版） |
| 状態 | Draft |
| 関連 ADR | [0002](./adr/0002-use-etcd-raft.md) / [0003](./adr/0003-drive-rawnode-single-goroutine.md) / [0004](./adr/0004-virtual-time-discrete-event-simulation.md) / [0006](./adr/0006-ferry-transport-outside-raft.md) / [0009](./adr/0009-server-authoritative-simulation-over-websocket.md) / [0010](./adr/0010-frontend-react-pixijs.md) / [0011](./adr/0011-infrastructure-aws-ecs-fargate.md) / [0014](./adr/0014-monorepo-and-shared-types.md) |

図はすべて Mermaid で書いているので、GitHub 上でそのまま表示されます。

---

## 1. システムコンテキスト

```mermaid
flowchart LR
    player([プレイヤー<br/>ブラウザ])
    eng([エンジニア / 学習者<br/>ブラウザ])
    ops([開発者・デモ担当])
    mlit[(国土交通省<br/>離島オープンデータ)]

    subgraph system[合意、乗船中。]
        web[Web クライアント<br/>React + PixiJS]
        srv[ゲームサーバー<br/>Go + etcd-io/raft]
    end

    player -- 遊ぶ --> web
    eng -- エンジニアモードで観察 --> web
    web <-- WebSocket / HTTPS --> srv
    ops -- ETL を実行し、生成 JSON をコミット --> repo[(GitHub リポジトリ)]
    mlit -- CSV/Excel/PDF を手で取得 --> ops
    repo -- CI/CD --> system
```

ポイント:

- オープンデータは**ビルド時**に取り込みます（ETL を実行して JSON を生成し、コミットする）。実行中に外部 API は呼びません（[ADR-0013](./adr/0013-open-data-build-time-etl.md)）。
- プレイヤーは匿名です。ログインはありません。

---

## 2. 論理アーキテクチャ（コンテナ図）

```mermaid
flowchart TB
    subgraph browser[ブラウザ]
        ui[React UI<br/>HUD・フェリー操作・島の詳細・<br/>エンジニアパネル・リザルト]
        pixi[PixiJS マップ描画<br/>島・航路・船・手紙]
        store[(クライアント状態<br/>zustand<br/>サーバーイベントを反映)]
        net[WS クライアント<br/>再接続・コマンド送信]
        ui <--> store
        pixi --> store
        ui --> net
        net --> store
    end

    subgraph server[Go サーバー：1 プロセス]
        http[HTTP / WS ハンドラ<br/>net/http + coder/websocket]
        sm[セッションマネージャ<br/>セッションの作成・破棄・上限]
        subgraph session[セッション：1 ゲームにつき 1 goroutine]
            loop[セッションループ<br/>実時間 → ゲーム内時間]
            sim[シミュレーションエンジン<br/>決定的・I/O なし]
        end
        stages[(ステージ定義・島データ<br/>go:embed)]
        http --> sm --> session
        sim --> stages
    end

    net <-- "WebSocket(JSON)<br/>コマンド ↑ / イベント ↓" --> http
```

- **サーバー権威型**: ゲームの状態とルールはすべてサーバーが持ちます。クライアントは描画と入力だけを担当します（[ADR-0009](./adr/0009-server-authoritative-simulation-over-websocket.md)）。
- **1 セッション = 1 goroutine**: セッション内部にはロックがありません。外部とのやり取りはチャネルだけで行います。
- **シミュレーションエンジンは I/O を持たない純粋なライブラリ**です。これにより、テスト・CLI での再生・将来の WASM 化（ブラウザ内実行）を同じコードで行えます。

---

## 3. シミュレーションエンジンのコンポーネント

```mermaid
flowchart TB
    subgraph engine["sim.Engine（1 セッション分の世界）"]
        clock[仮想時計<br/>SimTime ms]
        sched[イベントスケジューラ<br/>優先度キュー：時刻, 連番]
        rng[乱数<br/>シードから派生した PCG]

        subgraph cluster["cluster：Raft クラスタ"]
            n1[島ノード 1<br/>RawNode + MemoryStorage<br/>+ 台帳]
            n2[島ノード 2]
            n3[島ノード N]
        end

        subgraph ferry["ferry：Ferry Transport"]
            ports[港キュー<br/>島ごと・上限と合流の規則]
            ships[フェリー<br/>位置・積み荷・状態]
            routes[航路グラフ<br/>航行時間・遮断状態]
        end

        subgraph game["game：ゲームルール"]
            req[依頼の生成と追跡]
            score[スコア・満足度]
            evts[イベント台本<br/>嵐・故障・ラッシュ]
        end

        trace[トレース記録<br/>ドメインイベント列]
    end

    cmd([プレイヤーのコマンド]) --> sched
    sched -->|RaftTick| cluster
    sched -->|FerryArrive| ferry
    sched -->|RequestSpawn / Storm ...| game
    cluster -- "Ready.Messages → 手紙" --> ports
    ships -- "到着時に配達 → Step()" --> cluster
    cluster -- "適用結果（台帳）" --> req
    req -- "依頼書（アプリの手紙）" --> ports
    evts --> routes
    evts --> ships
    cluster & ferry & game --> trace
    rng --> game
    rng -. "選挙タイムアウトの乱数" .-> cluster
```

| パッケージ | 責務 | 依存してよい先 |
|---|---|---|
| `internal/sim` | 仮想時計・スケジューラ・`Engine`（全体の組み立て）・不変条件の検査 | 下記すべて |
| `internal/cluster` | `RawNode` の生成・Ready ループ・台帳への適用 | `go.etcd.io/raft/v3`, `internal/ledger` |
| `internal/ferry` | 手紙・港キュー・フェリー・航路 | `raftpb`（メッセージを保持するため） |
| `internal/ledger` | 共有台帳の状態機械（花火・屋台・予算） | なし（純粋なロジック） |
| `internal/game` | 依頼・スコア・満足度・イベント台本 | `internal/ledger` |
| `internal/stage` | ステージ定義（YAML）と島データ（JSON）の読み込み・検証 | なし |
| `internal/protocol` | WebSocket メッセージの型（TS の型を生成する元） | なし |
| `internal/server` | HTTP / WS・セッション管理・レート制限 | `internal/sim`, `internal/protocol` |

依存の向きは「`server` → `sim` → (`cluster`, `ferry`, `game`) → (`ledger`, `stage`)」の一方向です。`cluster` と `ferry` はお互いを知りません（`sim` がつなぎます）。

---

## 4. 主要なシーケンス

### 4.1 依頼が確定するまで（3 島・Leader が島 A、依頼元が島 C）

```mermaid
sequenceDiagram
    autonumber
    participant C as 島C（依頼元・Follower）
    participant PC as 港C
    participant F as フェリー
    participant PA as 港A
    participant A as 島A（Leader）
    participant B as 島B（Follower）

    Note over C: 住民の依頼が発生（reqId=r1）
    C->>PC: 依頼書（宛先＝C が知っている Leader：A）
    Note over F: プレイヤーが依頼書を積んで A へ出航
    F->>A: 到着・配達
    A->>A: RawNode.Propose(r1)
    A->>PA: MsgApp(r1) → B 宛て、C 宛て
    Note over F: プレイヤーが B 宛ての MsgApp を積んで B へ
    F->>B: MsgApp を配達 → Step()
    B->>B: ログに追記（Ready → Storage）
    B-->>F: MsgAppResp（港B に置かれ、次の便で A へ）
    F->>A: MsgAppResp を配達
    A->>A: 過半数（A, B）が記録 → Commit
    Note over A: 「共和国で確定」（A の台帳に適用）
    A->>PA: 次の MsgApp / Heartbeat に Commit Index が載る
    Note over F: プレイヤーが C 宛ての手紙を C へ
    F->>C: 配達 → C がエントリを受け取り、Commit を知る
    C->>C: 台帳に適用 → 依頼元に通知
    Note over C: 「確定」— ここでスコアが入る（ADR-0015）
```

このシーケンスが、企画書の「手紙の配送に成功したからといって、その場ですべての処理が完了するわけではない」をそのまま表しています。

### 4.2 嵐による分断と Leader の交代（デモの山場）

```mermaid
sequenceDiagram
    autonumber
    participant A as 島A（旧 Leader）
    participant B as 島B
    participant C as 島C
    Note over A,C: 嵐：A に接する航路がすべて遮断（A は孤立）
    A->>A: CheckQuorum：選挙タイムアウトの間に過半数から返事がない → Follower に戻る
    B->>B: A から連絡がないまま選挙タイムアウト → PreCandidate
    B-->>C: MsgPreVote（船で運ぶ）
    C-->>B: MsgPreVoteResp（許可）
    B->>B: Candidate（Term+1）
    B-->>C: MsgVote
    C-->>B: MsgVoteResp（投票）
    B->>B: Leader（新 Term）— 溜まっていた依頼の処理が再開
    Note over A,C: 嵐が去る
    B-->>A: MsgHeartbeat / MsgApp（新しい Term）
    A->>A: 新しい Term を知って Follower に。A のログで Commit されていなかった部分は上書きされる
    Note over A: A の港に残っていた古い Term の手紙が届いても、受け取った側で無視される（V-4）
```

### 4.3 プレイヤーのコマンドの処理

```mermaid
sequenceDiagram
    participant UI as ブラウザ
    participant WS as WS ハンドラ
    participant S as セッションループ
    participant E as sim.Engine
    UI->>WS: {"type":"cmd.ferry.depart","payload":{...},"cmdId":"c-12"}
    WS->>S: cmdCh <- cmd（goroutine の境界）
    S->>E: 次のステップの最初に Apply(cmd, now)
    E->>E: 入力ログに追記（SimTime と cmd）
    E-->>S: 検証の結果と、発生したドメインイベント
    S-->>WS: ack(c-12) + events（100ms ごとにまとめて送る）
    WS-->>UI: JSON
```

---

## 5. デプロイ構成（AWS：仮決め）

```mermaid
flowchart LR
    user([ブラウザ])
    subgraph aws[AWS ap-northeast-1]
        cf[CloudFront<br/>単一ドメイン]
        s3[(S3<br/>SPA の静的ファイル<br/>OAC で非公開)]
        alb[ALB<br/>idle timeout 3600s]
        subgraph vpc[VPC]
            subgraph pub[Public Subnet × 2 AZ]
                ecs[ECS Fargate Service<br/>Go サーバー<br/>ARM64 0.5vCPU / 1GB<br/>desired=1]
            end
        end
        ecr[(ECR)]
        cw[CloudWatch Logs<br/>/ Metrics / Alarm]
    end
    gh[GitHub Actions<br/>OIDC]

    user -- HTTPS --> cf
    cf -- "/*（デフォルト）" --> s3
    cf -- "/api/* , /ws/*" --> alb
    alb --> ecs
    ecs --> cw
    gh -- "docker push" --> ecr
    gh -- "s3 sync + invalidation" --> s3
    gh -- "ecs update-service" --> ecs
    ecr --> ecs
```

| 要素 | 設定 | 理由 |
|---|---|---|
| CloudFront | `/ws/*` と `/api/*` を ALB へ（キャッシュなし・全ヘッダ転送）、それ以外は S3 へ | 同じオリジンにすることで CORS を不要にし、WebSocket も CloudFront を経由させる |
| ALB | idle timeout 3600 秒。ヘルスチェックは `/api/healthz` | WebSocket を長く保持するため。アプリ側でも 15 秒ごとに ping を送る |
| ECS Fargate | desired=1、最小限のタスクサイズ。public subnet + public IP（NAT Gateway なし） | ハッカソン規模ならタスク 1 つで足りる。NAT Gateway の費用を避ける |
| セッションの状態 | タスクのメモリ上にだけ持つ（DB なし） | [ADR-0012](./adr/0012-no-persistence-in-mvp.md)。再デプロイ時には遊んでいるセッションが切れる（デモの前はデプロイを止める） |
| IaC | Terraform（`infra/terraform`） | [ADR-0011](./adr/0011-infrastructure-aws-ecs-fargate.md) |

スケールアウトするとき（P2）: セッションはタスクに固定されるため、ALB のスティッキーセッション（Cookie）を使うか、決定性を活かして「入力ログを持って別のタスクで再構築する」方式にします。MVP では扱いません。

### 5.1 Cloudflare に差し替える場合

[ADR-0011](./adr/0011-infrastructure-aws-ecs-fargate.md) の「代替案 B」にあたります。

```mermaid
flowchart LR
    user([ブラウザ]) --> w[Cloudflare Workers<br/>Static Assets で SPA を配信<br/>+ /ws をルーティング]
    w --> dobj[Durable Object<br/>1 セッション = 1 インスタンス]
    dobj --> ctr[Cloudflare Containers<br/>Go サーバーのイメージ]
```

アプリケーションのコード（`server/`・`web/`）は変更不要です。変わるのは `infra/` と CI のデプロイ手順だけです。

---

## 6. ディレクトリ構成（モノレポ）

```text
raft-game/
├── README.md
├── CLAUDE.md                    # 実装エージェント・開発者向けの規約
├── docs/                        # 本ドキュメント群
├── server/                      # Go module: github.com/K-Kizuku/raft-game/server
│   ├── go.mod                   # go 1.26 / go.etcd.io/raft/v3 v3.7.0（replace でパッチ版を使う）
│   ├── cmd/
│   │   ├── server/              # 本番サーバー（HTTP + WS）
│   │   ├── simcli/              # ヘッドレス実行・入力ログの再生・ボット対戦
│   │   └── islandsdata/         # オープンデータの ETL
│   ├── internal/
│   │   ├── sim/                 # Engine・時計・スケジューラ・不変条件
│   │   ├── cluster/             # RawNode のラッパーと Ready ループ
│   │   ├── ferry/               # 手紙・港・フェリー・航路
│   │   ├── ledger/              # 台帳の状態機械
│   │   ├── game/                # 依頼・スコア・イベント
│   │   ├── stage/               # ステージと島データの読み込み
│   │   ├── protocol/            # WS メッセージの型（tygo で TS を生成）
│   │   └── server/              # HTTP / WS / セッション管理
│   └── third_party/
│       └── etcd-raft/           # v3.7.0 + 乱数を注入するパッチ（ADR-0005）
├── web/                         # Vite + React + TypeScript
│   ├── src/
│   │   ├── app/                 # ルーティング・画面
│   │   ├── game/                # PixiJS の描画（マップ・船・手紙）
│   │   ├── ui/                  # HUD・パネル類
│   │   ├── state/               # zustand ストアとイベントの反映（reducer）
│   │   ├── net/                 # WS クライアント
│   │   └── protocol/            # 生成された型（直接編集しない）
│   └── public/
├── data/
│   ├── raw/                     # 取得したオープンデータの原本と README（出典・取得日）
│   ├── curated/                 # 人手で抜き出した計画書の記述（出典ページ付き YAML）
│   ├── islands.json             # ETL が生成（コミットする）
│   └── stages/                  # ステージ定義 YAML（stage1.yaml ...）
├── infra/
│   └── terraform/
└── .github/workflows/           # ci.yml / deploy.yml
```

`data/` は `server/` から `go:embed` で取り込みます。Go の embed は親ディレクトリを参照できないため、ビルド時に `data/` を `server/internal/stage/embedded/` にコピーします（`make sync-data`）。コピー先は `.gitignore` に入れます。

---

## 7. 品質特性と、それを実現する仕組み

| 品質特性 | 仕組み |
|---|---|
| 正しさ（Raft の安全性） | Raft のアルゴリズムには手を入れない。輸送層だけを差し替える。不変条件を常に検査するシミュレーションテスト |
| 決定性 | 単一 goroutine・仮想時間・シードから派生する乱数・Raft の乱数源の注入・map の走査順に依存しない実装 |
| 応答性 | サーバーは 50ms ごとにステップを進め、100ms ごとにイベントをまとめて送る。船の位置はクライアントが補間する |
| 差し替えやすさ | シミュレーションエンジンは I/O なし。インフラは `infra/` に閉じる。プロトコルの型は 1 か所から生成する |
| 観測性 | トレース（ドメインイベント）を全て記録し、リザルトとエンジニアモードもここから作る |
