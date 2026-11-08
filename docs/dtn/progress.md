# DTN の実装の進み具合（再開のための記録）

2026-11-08 にオーナーの指示で一旦止めた時点の状態。`dtn/main` に入っているものと、残りの作業をまとめる。再開するときは、この文書の「残りの作業」と各 Issue の本文を見て、依存の順（[architecture.md §7](./architecture.md#7-実装の順序と-issue)）に進める。

## Issue ごとの状態

| ID | Issue | 状態 | `dtn/main` に入っているもの |
|---|---|---|---|
| DTN-00 | #363 | **完了** | `islandcore`・`coordcore`・`simtrace`、島ごとの手紙 ID（`islandId × 10^9 + n`）、`simcli --normalized-trace`、`traceHash` の固定の試験（`server/cmd/simcli/testdata/tracehash-pin.json`） |
| DTN-01 | #158 | 未着手 | 設計書（この `docs/dtn/`）。ADR-D001・D003 はまだ Proposed。Q-ACCOUNT は未決 |
| DTN-02 | #159 | **完了** | `dtn/protocol`（電文・検証・RFC 8785・RequestDigest・ResponseDigest・LetterCodec）、`dtn/runtime`（Actor・Runtime・Serve）、`cmd/dtnactor` |
| DTN-03 | #160 | **途中** | `dtn/island`（Island Actor・`requests.note`）。単一プロセス版との島ごとの列の一致の試験は通る |
| DTN-04 | #161 | **完了** | `dtn/ferryactor`、`dtnactor` の `ROLE=ferry` |
| DTN-05 | #162 | **途中（最初の部品だけ）** | `sim/remote.go`（`sim.NewRemote`・`sim.Remote`）。`dtn/coordinator` はまだ無い |
| DTN-06 | #163 | **完了** | `dtn/bpv7`（固定の fixture・10 分の fuzz 済み） |
| DTN-07 | #164 | **完了** | `dtn/host`・`host/sqlitestore`・契約の fixture C01〜C15 |
| DTN-08 | #165 | **途中（前半）** | `dtn/handoff`（台帳・CAS・Resume の表）、Host の `transfer.*`、fixture T01〜T05、2 つの Host の障害の試験 |
| DTN-09〜11 | #166〜#168 | 未着手 | — |
| DTN-12 | #169 | **ほぼ完了** | `web/worker/dtn/`（DtnSession・ActorHost・CostGuard の内訳・カタログ・管理者の試運転の口）、DTN 専用の `cloudflare.config.ts`、`server/Dockerfile.dtnactor` |
| DTN-13 | #170 | 未着手（調査だけ） | — |
| DTN-14〜17 | #171・#172・#364・#365 | 未着手 | — |

## 残りの作業

### DTN-03（#160）

- 試験: `TestIslandAdvanceSplitInvariance`（D-9 と `stateDigest`）・`TestIslandDeliverMatchesCore`・`TestIslandUnloadMatchesCore`・`TestIslandRejects*`（重複の配達・digest の不一致・上限の減少・`place` の不一致・他島の計画・stage3）・`TestIslandReplayDeterministic`・`runtime.Runtime` を通した冪等・`TestIslandHoldsOneNode`
- 文書: actor-protocol.md §3.1 に `requests.note` の行、Island Actor の細部（`stateDigest` の中身・`deliver` は上限が `(t,1)`・`unload` は `(t,8)` のときだけ・`request.routed.letterId`・保留の note）。architecture.md §2・simulation-time.md §2.5 の更新

### DTN-05（#162）

- `dtn/coordinator`: Dispatcher（`CallAll`・`Transfer`）・journal・上限のループ（(t, c) の前に全島を進め、出力を `(t, class, islandId, seq)` で並べる）・まとめた advance の重複の出力を島の seq で捨てる・保留（`dtn_recovering`）・watchdog・`Restore`
- 島の出力の受け取り: `dtn/island` が公開した `LetterCreatedData`・`RequestRoutedData` などの型を使う（DTN-05 の当初の案 `{letter: LetterView, …}` ではなく、DTN-03 の実装に合わせる）
- `requests.note` を送る: 依頼の発生（発生元の最初の `request.routed`）で `created`、発生元の重複でない `ledger.applied` で `resolved`。`resolved` は、他の島がその適用の `(t,c)` を越えて進む前に届ける（島のイベントの印ごとに進める）
- 試験用の memfabric（`dtn/island` と `dtn/ferryactor` を `runtime.Runtime` で同じプロセスで動かす）と、`TestCoordinatorMatchesReference`（`simtrace.Compare`）
- `loop`（50 ms・速度・一時停止・skip・watchdog）・`wsfront`（[actor-protocol.md §7.6](./actor-protocol.md) の枠のとおり）・`dtnactor ROLE=coordinator`（`BOT`・`SPEED` の自動の対戦と `stats`）
- 気になる点: 同じ時刻に 2 つの島が同じ依頼の待ちを再試行すると、`request.updated` の順が参照モデルとずれるかもしれない（同等性の試験で確かめる）

### DTN-08（#165）

- Coordinator への組み込み（journal の intent・dispatch・Handoff の間の仮想時間の停止・今の `Transport` と同じ事前の確認）。試験用の `world` を本物の Coordinator に置き換える
- stage1・stage2 × seed 1..50 の参照モデルとの比較、`TestUnloadMixed` と参照の `Unload` の比較
- 電文の変更（DTN-08 で決めた）: `transfer.activate` の `then` の手紙は `{letterId, createdAt, digest}`（`unload` は `place` も）で、`bundle` を入れない（受け取る Host が pending の bytes を付ける）

### DTN-12（#169）の続き

- TS の ActorHost で、DTN-08 の fixture T01〜T05 を通す（今は `web/worker/dtn/host/contract.test.ts` が `T*` を飛ばしている）
- 本物の image のビルド・`cf build --mode dtn`・実環境での測定（Q-COLD・Q-IMAGE・Q-RPC・Q-SQLITE・Q-PERF）。Docker と DTN の環境が要る

### DTN-13（#170）

- 調査の結果（実装はまだ無い）:
  - DTN の Worker には `GAME_SESSION` が無いので、`POST /api/sessions` は今 503。`deps.dtn` があれば `createDtnSession` を通す（Origin → レート制限 → 本文 → Turnstile → 予約）。対象外のステージの判定は本文の読み込みのときに `DTN_STAGE_INFO` で行う
  - `issueSession` の `tokenHash` を DtnSession の `/init` に渡す（試運転のセッションでは省略可）。`DtnPorts` に `connect`・`inputLog` を足す
  - DtnSession のブラウザ側は `web/worker/session/core.ts` の作りを写す（トークンの照合・古い接続の置き換え 4001・`internal.*` を捨てる・`ready` までの最大 32 通の保留・`end` の理由の対応・`idle`）
  - 入力ログ: `ack` が ok（`dtn_recovering` でない）なら `coord_journal` に `{type:"command", cmdId, t, cmd}` を足す
- 提案（DTN-05 と合わせて決める）: ctl の `browser.open`・`browser.close`（ブラウザの再接続で Coordinator が一時停止・`sub.engineer` を戻せるように）。journal の `command` は dispatch の後に足されるので、Coordinator は `t` でコマンドを適用し、dispatch の結果は `id` で引く

### 未着手

- DTN-01（設計の Accepted・Q-ACCOUNT の結論）、DTN-09（Contact の署名と RPC CLA）、DTN-10（復元と fencing。拡張点は `host.Restart`・`Tx.Journals`・`meta.initPayload`・`Hooks.OnActorLost`・`handoff.Resume`）、DTN-11（ローカルの複数プロセス。WebSocket の `ActorConn` が要る）、DTN-14・15（MoQ。Cloudflare の MoQ のリレーにつなげる環境が要る）、DTN-16（画面）、DTN-17（対象外のイベント）

## 検査について

- `golangci-lint`: この環境に入っている v2.5.0 は Go 1.25 で作られていて、Go 1.26 のモジュールを読めない。`go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0 run ./...`（`server/` で）を使う
- `dtn/main` には CI が動かない（`ci.yml` は `main` だけが対象）。push の前に `go -C server test -count=1 ./...`・`pnpm -C web test`・`python3 scripts/check-docs.py` を回す
