# ADR-0028: ブラウザでのリプレイは、同じ Go のコード（`sim`）を WebAssembly にしてブラウザで再シミュレーションし、出てきた記録を web の記録の再生で流す

- 状態: Accepted（2026-11-08 にプロダクトオーナーが案 A を承認。#312）
- 日付: 2026-11-08
- 決めた人: プロダクトオーナー（2026-11-08 に案 A を承認）、Claude（案。T-720・#301）
- 関連: FR-LOG-03・FR-LOG-04・FR-LOG-05・FR-LOG-06、V-6、T-714（#271）・T-719（#299）、[ADR-0009](./0009-server-authoritative-simulation-over-websocket.md)・[ADR-0016](./0016-infrastructure-cloudflare-with-cf-cli.md)・[ADR-0017](./0017-persist-input-log-in-durable-object.md)・[ADR-0018](./0018-cost-guard.md)・[ADR-0025](./0025-stage-data-source.md)、[DesignDoc §10・§13.5](../03-design-doc.md#10-記録リプレイ)

## 背景

要件 FR-LOG-04（P1）は「入力ログを読み込むとリプレイ（再シミュレーション）できる」。材料と前提は次のとおり。

- **入力ログ**（[DesignDoc §10.1](../03-design-doc.md#101-入力ログ)）: Durable Object の SQLite に保存し（FR-LOG-01・ADR-0017）、ブラウザからダウンロードできる（FR-LOG-03・T-714 #271）。1 ラウンドで 6〜11 KB（下の計測）
- **トレース**（§10.2）: 保存しない。ゲームは決定的（ADR-0005・V-6）なので、同じステージ定義・シード・入力ログから同じイベント列を作り直す（#271 の方針）。作り直す CLI は `simcli replay --trace`（T-719 #299 で足す。出力は `simcli record` と同じ記録の形式。§13.5）
- **共有**（FR-LOG-06）: ADR-0017 は「リプレイの共有（入力ログの JSON）を R2 に置く」と決めている
- **ライブのゲーム**はサーバーで実行する（ADR-0009）。ADR-0009 は WASM でのブラウザ実行を「ゲームの実行場所」としては採らなかったが、`internal/sim` を I/O を持たない純粋なライブラリにして WASM 化の道を残した。リプレイは**読むだけ**なので、権威（不正・改ざん）の問題は起きない
- **コストガード**（ADR-0018）: コンテナと Durable Object を作る処理は `CostGuard` の予約を通し、1 日 1 USD・1 か月 30 USD の上限の中に収める。署名（G3）は外さない

作り直す手段は 3 つ考えられ、費用・配信の大きさ・作る量が大きく違う。この ADR は、3 案を同じ物差しで比べ、計測した数字を添えて 1 つを選ぶ。

## 検討した選択肢

### 案 A: Go の `sim` を WebAssembly にして、ブラウザで再シミュレーションする

`GOOS=js GOARCH=wasm` でビルドした WASM（`sim`・`stage` と、記録の組み立て）をブラウザで動かし、利用者が読み込んだ入力ログを `sim.Replay` で再生する。出てきたイベント列を記録の形式（§13.5）にして、web の記録の再生（`web/src/net/mock/record.ts`・`server.ts`）でゲーム画面に流す。

- 長所: サーバーの費用がゼロ（静的ファイルの配信だけ。コストガードを通さなくてよい）。手元の入力ログをそのまま読める（サーバーに送らない）。サーバーと同じ Go のコードなので、同じトレースのハッシュになる（計測で 3 ステージとも一致）。オフラインでも動く
- 短所: WASM が大きい（brotli で約 2.0 MB、gzip -9 で約 2.8 MB）。新しいビルドの道（`GOOS=js GOARCH=wasm`・`wasm_exec.js`・CI とデプロイの手順）が増える。エンジニアモードの `node.detail` は、そのままでは出せない

### 案 B: サーバーで再生する

Durable Object がコンテナを起動し、`internal.restore` と同じ道で入力ログを適用して、イベントを WS で流す。

- 長所: ライブのゲームと同じ道（`internal.restore`・WS・ゲーム画面）をほぼそのまま使える。一時停止・速度・`node.detail` がそのまま使える。ブラウザに大きなファイルを配らない
- 短所: 1 回のリプレイごとにコンテナと Durable Object を作るので、コストガード（予約・上限・アラーム・`destroy()`）を通す必要があり、1 日の上限（1 USD）をゲームと取り合う。利用者の任意の入力ログをコンテナに入れる（アップロードの口・大きさの上限・検査が要る）。オフラインでは動かない

### 案 C: あらかじめ作った記録を、web の記録の再生で流す

記録（`simcli record`・`simcli replay --trace` の形式。§13.5）を先に作っておき、ブラウザはそれを取得して記録の再生で流す。記録を作る場所で 3 つに分かれる。

- **C1（CI で作る）**: CI がボットに遊ばせて記録を作り、静的ファイルとして配る
- **C2（ラウンドの終わりのコンテナで作る）**: ラウンドの終わりに、`destroy()` の前にコンテナが記録を作って R2 に置く
- **C3（手元の `simcli` で作る）**: 利用者が `simcli replay --trace` で記録を作り、ブラウザで読み込む
- 長所: ブラウザに配るのは記録だけで小さい（gzip で約 14 KB）。取得してすぐ再生できる。web の作る量が最も少ない
- 短所: 「入力ログを読み込むと再シミュレーションできる」（FR-LOG-04）を、利用者の手元の入力ログについては満たさない（C1 はボットの回だけ、C2 はサーバーで遊んだ回だけ、C3 は開発者だけ）。記録の形式（§13.5）は Engine の分だけで、画面に要る `stage`（StageView）と `round.result` を持たない。C2 はラウンドの終わりのコンテナの時間と R2 の書き込みが増える

### 物差しの表

| 物差し | A（WASM） | B（サーバーで再生） | C（記録を流す） |
|---|---|---|---|
| 1 回のリプレイの費用 | サーバーの費用はほぼゼロ。WASM・JS は静的ファイル（リクエストは無料・無制限。ADR-0018）。コンテナ・DO・R2 を使わない（R2 の共有を使うときだけ、入力ログの読み出し 1 回）。**コストガードを通す必要がない** | ゲーム 1 回とほぼ同じ。1 倍速で 210 秒流すとコンテナと DO が 210 秒動く（lite で約 0.0008 USD。30 分の予約は 0.0065 USD）。全部を一気に作って送り、すぐ `destroy()` しても、起動と DO の呼び出しは毎回かかる。**予約・上限・アラームの回数・`destroy()` を通す必要がある**。1 日の上限をゲームと取り合う | C1: ゼロ（静的ファイル）。C2: ラウンドの終わりのコンテナの時間（記録を作る数 ms〜数十 ms と R2 への書き込みの待ち）＋R2 の書き込み 1 回（Class A）＋保存（約 14 KB/回。gzip）。予約は今のゲームの予約に含まれる（`destroy()` が少し遅れる）。C3: ゼロ |
| 配信の大きさ | WASM 約 12.4 MB（gzip -9 で約 2.8 MB、brotli で約 2.0 MB）＋`wasm_exec.js` 約 17 KB。Workers の静的ファイルの 1 ファイルの上限（25 MiB）に収まる。リプレイの画面を開いたときだけ読む（ゲームの画面の読み込みは増やさない） | 増えない（今のゲーム画面と WS だけ） | 記録 1 本 約 180〜200 KB（gzip -9 で約 14 KB、brotli で約 9 KB） |
| 再生までの時間 | WASM の取得（2.0 MB。50 Mbps で約 0.3 秒、10 Mbps で約 1.6 秒。2 回目からはキャッシュ）＋起動とステージ定義の読み込み（Node で約 80 ms）＋再シミュレーション（Node で約 60 ms。下の計測）。M2 の Node でプロセス全体が約 250 ms。遅い端末でも数倍と見込む（見積もり） | コンテナの起動（T-007 のスパイクの lite で p50 247 ms・p95 1112 ms・同時起動の最大 1706 ms。ADR-0016 の目安は 1〜3 秒）＋Worker・DO・WS の往復（p50 140 ms）＋再計算（数十 ms〜数百 ms）。合わせて約 1〜3 秒（見積もり） | 記録の取得だけ（14 KB。数十 ms）。C3 は利用者が `simcli` を動かす手間が先にある |
| 任意の入力ログを読めるか | 読める。ファイルはブラウザの外に出ない。`stageHash` が WASM に埋め込んだステージ定義と違えば、`sim.Replay` が `ErrReplayStage` で断る（下の「影響」） | 読めるが、ファイルをサーバーにアップロードする口が要る（大きさの上限・厳密な検査・レート制限）。`stageHash` が違えばコンテナが断る（`restore_failed` と同じ） | 読めない（C1・C2）。C3 だけが読めるが、ブラウザが読むのは入力ログではなく記録 |
| 決定性・正しさ | サーバーと同じ Go のコード。計測で、3 ステージとも WASM の `traceHash` がネイティブの `simcli replay` と一致した。WASM の版が入力ログの `build` と違えば食い違うことがある（`ErrReplayDiverged` で気づける） | サーバーと同じコード・同じ道（復元と同じ）。最も確か | 記録をそのまま流すので、記録を作ったときの結果そのもの。記録の `traceHash` で中身を確かめられる |
| 安全性 | 入力はブラウザの中だけで処理するので、サーバーの攻撃面が増えない。コストガードを迂回されるおそれがない。G3 は関係しない（R2 の共有の取得は FR-LOG-06 の設計で扱う） | 任意の入力をコンテナに入れる（厳密な読み込みで断るが、検査の誤りがそのまま攻撃面になる）。リプレイの口がコンテナを作るので、Turnstile・レート制限・`CostGuard` の予約・G3（署名した sessionId）を全部通す必要がある。抜けると費用が急増する典型（ADR-0018 の背景） | C1・C3 は静的ファイルとブラウザだけ。C2 は R2 への書き込みが増え、R2 の読み出しは G1 の外（レート制限で抑える） |
| 作る量 | 新しいビルドの道（`GOOS=js GOARCH=wasm`・`wasm_exec.js`・`-tags embeddata`。Makefile・CI・デプロイ）、JS との薄い橋（`syscall/js`）、リプレイの画面。使える既存の部品: `sim.Replay`・`simcli` の入力ログの読み込みと記録の組み立て（切り出して共通にする）・`protocol/simconv`・web の記録の再生・ゲーム画面のストア | DO の「リプレイ」の種類のセッション、アップロードの口、コストガードの予約の種類、コンテナの「リプレイ」のモード（イベントを流して終わったらすぐ止める）、web の画面。`internal.restore` とゲーム画面は使える | 最小。web の記録の再生を本番のビルドに入れ（今は `net/index.ts` から再輸出していない）、リプレイの画面を作るだけ。C2 は DO・コンテナ・R2 の書き込みが増える。どれも `stage`・`round.result` を別に用意する必要がある |
| 画面との組み合わせ | 一時停止・速度・「次の出来事まで」は記録の再生がすでに持つ。シーク（任意の時刻へ飛ぶ）は、イベント列を手元に全部持つので、初めのスナップショットからストアに流し直せばできる（後の子 Issue）。`node.detail` は初版では使えない（WASM の Engine を時刻 t まで進めれば作れるので、のちに足せる） | 一時停止・速度・「次の出来事まで」・`node.detail` はそのまま。シークは `internal.restore` の `until` でやり直せるが、1 回ごとに再計算とスナップショットの送り直し | 一時停止・速度・「次の出来事まで」は記録の再生が持つ。シークは A と同じく流し直し。`node.detail` は記録に無いので使えない |
| オフライン・デモ | 一度読み込めば回線が無くても動く。`make demo-local` の Vite からも同じ静的ファイルを配れる | 回線とコンテナが要る。`make demo-local` では standalone のサーバーに同じ道を作らないと使えない | C1・C3 は回線が無くても動く。C2 は R2 が要る |

### 計測

2026-11-08、Apple M2・macOS（Darwin 24.6）・Go 1.26.0（`server/go.mod` の `go 1.26`）・Node v24.22.0、main の `e545cce` で測った。ボットの入力ログは `simcli record --inputs-dir` で作った（シードはすべて 1）。時間は 3〜5 回の値の範囲。

#### WASM の大きさ

| ビルド | 生 | gzip -9 | brotli -q 11 |
|---|---|---|---|
| 計測用の `main`（`sim`・`stage` と、`simcli` の入力ログの読み込みを写したもの）、`GOOS=js GOARCH=wasm go build` | 12,361,011 B（約 12.4 MB） | 2,841,863 B（約 2.8 MB） | 2,016,435 B（約 2.0 MB） |
| 同じもの、`-trimpath -ldflags="-s -w"` | 11,967,000 B（約 12.0 MB） | 2,776,261 B（約 2.8 MB） | 1,974,335 B（約 2.0 MB） |
| `simcli` 全体（`bot` を含む）、`GOOS=js GOARCH=wasm go build ./cmd/simcli` | 12,879,335 B（約 12.9 MB） | 2,962,146 B（約 3.0 MB） | 2,094,541 B（約 2.1 MB） |
| （参考）同じ計測用の `main` のネイティブ（darwin/arm64） | 9,030,130 B | — | — |
| （参考）`wasm_exec.js` | 16,992 B | — | — |

- Orchestrator の参考値（`sim`・`stage` を import するだけで約 10.2 MB・gzip -9 で約 2.4 MB）より約 2 MB 大きいのは、入力ログの JSON の読み込み（`encoding/json`・`regexp`）と `sim.Replay` を実際に呼ぶため
- `bot` を足しても +0.5 MB（gzip で +0.1 MB）。リプレイには `bot` は要らない
- ステージ定義の埋め込み（`-tags embeddata`。ADR-0025）は、`islands.json`（83,556 B）と `stages/*.yaml`（計 19,200 B）で、生で約 +0.1 MB と見積もる（計測用の `main` は `os.DirFS` で読んだので含まない。**見積もり**）
- **TinyGo は試せなかった**。手元の TinyGo 0.33.0 が対応する Go は 1.19〜1.23 で、`server/go.mod` の `go 1.26` のモジュールをビルドできない（`requires go version 1.19 through 1.23, got go1.26`）。新しい TinyGo でも、protobuf-go（`raftpb`）の反射で通らないおそれがある。今回の判断は標準の Go の大きさで行う

#### 再シミュレーションの時間

| 入力ログ | ラウンドの長さ | コマンド | イベント | ネイティブの `simcli replay`（プロセス全体。`--invariants=false` / `true`） | ネイティブの計測用 `main`（プロセス内。読み込み / 再生） | WASM の計測用 `main`（Node。プロセス全体） | WASM（プロセス内。読み込み / 1 回目の再生 / 2 回目以降） | `traceHash` |
|---|---|---|---|---|---|---|---|---|
| `stage2-steady-1` | 210 秒 | 118 | 1,166 | 28〜30 ms / 32〜33 ms | 4 ms / 3 ms | 249〜257 ms | 77〜81 ms / 60〜64 ms / 18〜28 ms | ネイティブと一致（`sha256:606f9c21…`） |
| `stage1-greedy-1` | 180 秒 | 67 | 1,167 | 28 ms / 30〜31 ms | 4 ms / 3〜4 ms | 244〜259 ms | 71〜87 ms / 58〜64 ms / 18〜23 ms | 一致（`sha256:474f693f…`） |
| `demo-izu-greedy-1` | 120 秒 | 124 | 1,178 | 28〜29 ms / 40〜41 ms | 5 ms / 3〜5 ms | 241〜266 ms | 76〜83 ms / 62〜68 ms / 25〜29 ms | 一致（`sha256:fbfb70fc…`） |

- Issue の想定は「Stage 2 の 240 秒のラウンド」だが、今の `data/stages/stage2.yaml` の `durationMs` は 210,000（210 秒）なので、その長さで測った
- Node の起動だけ（`node -e 0`）で約 55 ms。WASM の `simcli replay` 全体（`bot` を含む）も 258〜292 ms で、計測用の `main` と変わらない
- WASM はネイティブの約 15〜20 倍遅いが、1 ラウンドの再シミュレーションは 0.1 秒に届かない。読み込みの約 80 ms は `islands.json` と全ステージの YAML の読み込みと検証で、埋め込みにしても大きくは変わらないと見込む
- ブラウザ（V8 の Liftoff→TurboFan）での WASM のコンパイルと起動は測っていない（ヘッドレスのブラウザでの計測は子 Issue の R4 で行う）。Node も V8 なので同じ程度と見込む（**見積もり**）

#### 記録と入力ログの大きさ

| ファイル | 生 | gzip -9 | brotli -q 11 |
|---|---|---|---|
| 記録 `simcli record --stage stage2 --bot steady --seed 1` | 180,567 B | 14,064 B | 9,426 B |
| 記録 `simcli record --stage stage1 --bot greedy --seed 1` | 202,191 B | 13,731 B | 8,698 B |
| 記録 `simcli record --stage demo-izu --bot greedy --seed 1` | 198,522 B | 13,388 B | 8,650 B |
| 入力ログ `stage2-steady-1.json` | 10,164 B | 932 B | 783 B |
| 入力ログ `stage1-greedy-1.json` | 5,801 B | 606 B | 524 B |
| 入力ログ `demo-izu-greedy-1.json` | 10,571 B | 972 B | 860 B |

#### コンテナの起動の時間と費用（見積もり。デプロイはしていない）

- 起動: ADR-0016 の T-007 のスパイク（M-1）の値。lite で p50 247 ms・p95 1112 ms・同時 3 件の最大 1706 ms、standard-1 で p50 230 ms・p95 396 ms。ADR-0016 の目安は「多くは 1〜3 秒」
- 費用: ADR-0018 の予約単価（lite で 30 分 約 0.0065 USD、standard-1 で約 0.040 USD）から、1 倍速で 210 秒流すと lite で約 0.0008 USD（0.0065 × 210 / 1800）。1 日の上限 1 USD では、ゲームとリプレイを合わせて lite で約 150 回分の予約しかない（予約は 30 分ぶんを取る）
- `cf deploy` は使っていない（Worker の権限外。計測もしていない）

#### やり直す手順

計測のコードはコミットしていない。やり直すときは、作業ツリーの中に一時的に置き、終わったら消す（`server/cmd/` の下に置くのは、`internal/` を import するため）。

```bash
M=.measure; mkdir -p "$M"                       # 作業ツリーの中の追跡しないディレクトリ
go -C server build -o "../$M/simcli" ./cmd/simcli
for s in stage2:steady stage1:greedy demo-izu:greedy; do
  "$M/simcli" record --data data --stage "${s%%:*}" --bot "${s##*:}" --seed 1 \
    --inputs-dir "$M/inputs" > "$M/rec-${s%%:*}.json"
done
for f in "$M"/rec-*.json "$M"/inputs/*.json; do
  echo "$f $(wc -c <"$f") $(gzip -9c "$f" | wc -c) $(brotli -c -q 11 "$f" | wc -c)"
done
time "$M/simcli" replay --data data --input "$M/inputs/stage2-steady-1.json" --invariants=false

# 計測用の main（下）を server/cmd/wasmprobe/main.go に置き、simcli の inputlog.go を同じディレクトリに写す
cp server/cmd/simcli/inputlog.go server/cmd/wasmprobe/
GOOS=js GOARCH=wasm go -C server build -o "../$M/probe.wasm" ./cmd/wasmprobe
cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" "$(go env GOROOT)/lib/wasm/wasm_exec_node.js" "$M/"
node "$M/wasm_exec_node.js" "$M/probe.wasm" "$PWD/data" "$PWD/$M/inputs/stage2-steady-1.json" 5
GOOS=js GOARCH=wasm go -C server build -o "../$M/simcli.wasm" ./cmd/simcli
node "$M/wasm_exec_node.js" "$M/simcli.wasm" replay --data "$PWD/data" \
  --input "$PWD/$M/inputs/stage2-steady-1.json" --invariants=false
rm -rf server/cmd/wasmprobe "$M"                # 消す
```

計測用の `main`（`server/cmd/wasmprobe/main.go`。`decodeInputLog` は写した `inputlog.go` のもの）:

```go
package main

import (
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/K-Kizuku/raft-game/server/internal/sim"
	"github.com/K-Kizuku/raft-game/server/internal/stage"
)

func main() { // wasmprobe DATA_DIR INPUT_LOG [N]
	data, input := os.Args[1], os.Args[2]
	n := 1
	if len(os.Args) > 3 {
		n, _ = strconv.Atoi(os.Args[3])
	}
	t0 := time.Now()
	f, err := os.Open(input)
	if err != nil {
		panic(err)
	}
	l, err := decodeInputLog(f)
	if err != nil {
		panic(err)
	}
	stages, err := stage.LoadDir(os.DirFS(data), "islands.json", "stages")
	if err != nil {
		panic(err)
	}
	var st *stage.Stage
	for _, s := range stages {
		if s.ID == l.StageID {
			st = s
		}
	}
	cfg, err := stage.SimConfig(st)
	if err != nil {
		panic(err)
	}
	load := time.Since(t0)
	for i := 0; i < n; i++ {
		t1 := time.Now()
		e, err := sim.Replay(cfg, st.Hash, sim.ReplayLog{Format: 1, StageHash: l.StageHash, Seed: l.Seed,
			Entries: l.Commands, Until: sim.SimTime(st.DurationMs)}, sim.Options{})
		if err != nil {
			panic(err)
		}
		h := e.TraceHash()
		fmt.Printf("load=%dms replay=%dms events=%d traceHash=sha256:%s\n", load.Milliseconds(),
			time.Since(t1).Milliseconds(), len(e.DrainEvents()), hex.EncodeToString(h[:]))
	}
}
```

`time.Now` は計測用の `main` の中だけで使う（`sim` の中では使わない。決定性の禁止事項は `sim.Engine` の中で動くパッケージが対象）。

## 決定

**案 A**。同じ Go のコード（`sim`・`stage`）を WebAssembly にしてブラウザで再シミュレーションし、出てきたイベント列を記録の形式（§13.5。`simcli replay --trace` と同じ。`bot` は空文字列）にして、web の記録の再生（今は開発用の `net/mock/record.ts`・`server.ts`）でゲーム画面に流す。再生の側は案 C と同じ部品を使う。

理由:

1. **FR-LOG-04 をそのまま満たす**のは A と B だけ。C は利用者の手元の入力ログを再シミュレーションできない（C3 は開発者向けの手順として残る）
2. **費用とコストガード**: A はサーバーの費用がほぼゼロで、コストガードを通す必要がない。B はリプレイのたびにコンテナを作り、1 日 1 USD の上限をゲームと取り合ううえ、アップロードの口と `CostGuard` の予約の種類が増えて、費用が急増する典型（ADR-0018 の背景）の攻撃面が広がる
3. **決定性**: 計測で、WASM の `traceHash` がネイティブの `simcli replay` と 3 ステージとも一致した。サーバーと同じパッケージを使うので、決定性のテスト（V-6）の上に乗る
4. **大きさと時間**は受け入れられる: brotli で約 2.0 MB を、リプレイの画面を開いたときだけ読む。再シミュレーションは Node で 0.1 秒に届かない。Workers の静的ファイルの 1 ファイルの上限（25 MiB）にも収まる
5. **作る量**: 新しいのはビルドの道と JS との薄い橋と画面で、`sim.Replay`・`simcli` の入力ログの読み込みと記録の組み立て・`protocol/simconv`・web の記録の再生とストアを使える。B より DO・Worker・コンテナの変更が少ない

選ばなかった案を後で足す道:

- **C1（CI で作った記録を配る）**: 「うまくいった回」のデモ再生や、WASM を読まずにすぐ見せたい場面のために、後から足せる。再生の部品（子 Issue の R3）は A と共通なので、記録のファイルを配るだけで済む
- **B（サーバーで再生）**: `node.detail` をリプレイで見せたい、またはブラウザの端末が遅すぎると分かったときに検討する。ただしコストガードの予約の種類と、アップロードの口の設計が先に要る
- **`node.detail`**: A のまま、WASM の Engine を時刻 t まで進めて `NodeDetail` を作る道がある（シークと合わせて後の子 Issue）

### 選んだ案の流れ

1. 利用者が入力ログ（FR-LOG-03 でダウンロードした JSON。§10.1）を、リプレイの画面（`/replay`）で選ぶ。R2 の共有（FR-LOG-06）では、共有の URL から入力ログを取得する
2. ブラウザが、WASM（`raft-replay.wasm`）と `wasm_exec.js` を静的ファイルとして読む（初回だけ。以後はキャッシュ）
3. WASM の中で、Go が入力ログを厳密に読み（`simcli` と同じ読み込み）、埋め込んだステージ定義（`-tags embeddata`）から `stage.SimConfig` で Config を作り、`sim.Replay` で再生する。`stageHash` が違えば断る
4. WASM は、記録（§13.5 の形式。`bot` は空）と、画面に要る `StageView`（プロトコル §5.1 の `stage`）と `RoundResult`（§5.5）を JS に返す。どれも既存の形式で、新しい形式を作らない
5. web の記録の再生（本番のビルドに入れる）が、記録をエンベロープにまとめてゲーム画面のストアに流す。一時停止・速度・「次の出来事まで」が使える。コマンド（船の操作）は受け付けない（読むだけ）
6. 終わったら、リプレイのリザルト（`RoundResult`）を出す

## 結果（この判断によって起きること）

- 良いこと:
  - リプレイにサーバーの費用がかからず、コストガード（G1〜G6）の対象を増やさない。署名（G3）にも触れない
  - 入力ログはブラウザの外に出ないので、任意のファイルをサーバーで処理する攻撃面が増えない
  - 同じ Go のコードなので、WASM の `traceHash` とネイティブの `simcli replay` の一致を CI で確かめられる（V-6 の延長）
  - 回線が無くても、`make demo-local` でも動く
- 悪いこと・受け入れるリスク:
  - リプレイの画面を開くと約 2〜3 MB を読む。遅い回線では数秒かかる（読み込み中の表示が要る）
  - 新しいビルドの道（WASM と `wasm_exec.js`。`wasm_exec.js` は Go の版と合わせる）を CI とデプロイに足す
  - 初版では `node.detail`（エンジニアモードの詳細）とシークを出さない
  - 遅い端末での起動の時間は測っていない（子 Issue の R4 で測る）

### コストガード（ADR-0018）

- A のリプレイはコンテナ・Durable Object を作らないので、`CostGuard` の予約・上限・アラームを通さない。WASM・JS の配信は静的ファイル（リクエストは無料・無制限）
- 静的ファイルなので Worker を通さない（`assets.runWorkerFirst` は `/api/*`・`/ws/*` だけ）。Worker の CPU（G5）・Workers Logs も増えない
- R2 の共有（FR-LOG-06）を作るときは、入力ログの読み出し（Worker → R2）が増える。読み出しは G1 の外なので、その設計（別の Issue）でレート制限と大きさの上限を決める

### ログの量

- リプレイはブラウザの中だけで動くので、Workers Logs は増えない。WASM の Go が標準出力・標準エラーに出すものは、ブラウザの `console` に出る（`wasm_exec.js`）。子 Issue で、入力ログの中身を `console` に出さない

### 公開リポジトリでのシークレット

- WASM と `wasm_exec.js` はビルドの生成物で、シークレットを含まない。ステージ定義は公開している `data/` のもの。生成物はコミットしない（`.gitignore`）

### `stageHash` の不一致

ステージ定義が変わると、古い入力ログの `stageHash` が今の定義と合わない。どの案でも起きる。

- **初版（決定）**: 再生を断る。WASM に埋め込んだのは、その版のステージ定義だけ。`sim.Replay` の `ErrReplayStage` を受けて、画面に「この入力ログは、今と違うステージ定義で記録されました。今の版では再生できません」と出す（入力ログの `stageId`・記録した `build` を添える）
- `build` だけが違い `stageHash` は同じとき: 再生を試みる（`internal.restore` と同じ扱い。ADR-0017 の 2026-11-07 の追記）。食い違えば（`ErrReplayDiverged`）、「版が違うため再現できませんでした」と出す
- 後で検討する案: (a) 古いステージ定義を `stageHash` ごとに残して WASM に埋め込む（Go のコードが変わると、同じ定義でも結果が変わりうるので、これだけでは足りない）、(b) 版ごとの WASM を R2 などに残し、入力ログの `build` で選ぶ。どちらも保存の量と運用が増えるので、初版では行わない

### R2 での共有（FR-LOG-06）とのつながり

ADR-0017 のとおり、R2 に置くのは入力ログの JSON（約 10 KB）で、記録やトレースではない。共有の URL を開いたブラウザは、入力ログを取得して、この ADR の WASM で再シミュレーションする。共有のたびにコンテナを起動しないので、R2 の共有の費用は保存と読み書きだけになる。共有の口（推測できない ID・書き込みの権限・レート制限・保存の期限）は FR-LOG-06 の設計で決める。

## 実装の分割（子 Issue の案）

変更予定ファイルは子 Issue どうしで重ならないように分け、重なるものは依存にした。`docs/03-design-doc.md` は R1・R2・R4 が別々の節（または同じ §10.4 の 1 文）を直すので、R1 → R2 → R4 の依存の順に入れる。既存の Issue と重なるファイル（`simcli` は #299、`App.tsx`・`ResultScreen.tsx` は #271）も依存にした。

| # | 題名 | やること | 主な変更予定ファイル（glob） | 依存 | AC の要点 | 目安 |
|---|---|---|---|---|---|---|
| R1 | リプレイの Go の部品を `internal/replaykit` に切り出す | `simcli` の入力ログの厳密な読み込み（`decodeInputLog`）と記録の組み立て（`encodeRecord`）、`internal/server` の `stageView`・`storyNames` を、I/O を持たないパッケージ `internal/replaykit` に移し、`simcli` と `server` はそれを呼ぶ。`bot` を import しない | `server/internal/replaykit/**`、`server/cmd/simcli/inputlog.go`、`server/cmd/simcli/record.go`、`server/cmd/simcli/replay.go`、`server/cmd/simcli/*_test.go`、`server/internal/server/session_view.go`、`server/internal/server/session_view_test.go`、`docs/03-design-doc.md`（§13.5 の 1 文） | #299（T-719。`simcli` の同じファイルを変える） | `simcli record`・`replay --trace` の出力が 1 バイトも変わらない（既存のテスト）。`server` の `snapshot` の `stage` が変わらない。`GOOS=js GOARCH=wasm go -C server build ./internal/replaykit/` が通る | 3h |
| R2 | WASM のエントリとビルドの道 | `server/cmd/replaywasm`（`//go:build js && wasm`。`syscall/js` で `raftReplay(inputLogJson)` を公開し、`{ ok, record, stage, result }` か `{ ok: false, code, message }` を返す。ステージ定義は `-tags embeddata`）。`make wasm`（`-trimpath -ldflags="-s -w"`。出力と `wasm_exec.js` を `web/public/replay/` に置き、コミットしない）。CI とデプロイのワークフローの正本にビルドの段を足す（取り込みは人） | `server/cmd/replaywasm/**`、`Makefile`、`scripts/build-replay-wasm.sh`、`.gitignore`、`.github/workflow-sources/*.yml`、`docs/03-design-doc.md`（§10.4 の 1 文） | R1 | `GOOS=js GOARCH=wasm go test`（Node の `go_js_wasm_exec`）で、testdata の入力ログ 3 本の `traceHash` がネイティブの `simcli replay` と一致する。`stageHash` が違えば `code: "stage_mismatch"`。WASM の大きさ（生・gzip）を CI のログに出し、25 MiB を超えたら失敗 | 4h |
| R3 | 記録の再生を本番のビルドに入れる | `web/src/net/mock/record.ts`・`server.ts` の中身を `web/src/net/replay/` に移し、`net/index.ts` から再輸出する。`net/mock/` の 2 ファイルは移した先を再輸出するだけにして、呼び出し元（テスト・`web/src/app/dev/`）は変えない。`StageView`・`RoundResult` を外から渡せる今の口を使い、`bot: ""` の記録を受け付ける。コマンド（船の操作）を `ack` の失敗にする（読むだけの）`AckPolicy` を足す | `web/src/net/replay/**`、`web/src/net/mock/**`、`web/src/net/index.ts` | なし | 既存の記録の再生のテスト（`net/mock/*.test.ts`・`net/playback.test.ts`）が変えずに通る。`simcli replay --trace` の形式（`bot` が空）の記録を読める。本番のビルド（`pnpm -C web build`）に入っても、開発用の固定の記録（`net/fixtures/`）は入らない | 3h |
| R4 | リプレイの画面（`/replay`） | ルート `/replay` と画面を足す。入力ログのファイルを選ぶ（1 MiB を超えれば断る）→ WASM を遅延して読む（読み込み中の表示）→ `raftReplay` → R3 の再生でゲーム画面（読むだけ）に流す → リザルト。誤り（形式・`stageHash` の不一致・版の食い違い）の文言。ヘッドレスのブラウザで、読み込みから最初の場面までの時間を測って PR に書く | `web/src/app/replay/**`、`web/src/replay/**`、`web/src/app/router/routes.ts`、`web/src/app/router/routes.test.ts`、`web/src/app/App.tsx`、`docs/03-design-doc.md`（§10.4・§13.6 の 1 行） | R2・R3、#271（`App.tsx` が重なる） | 入力ログを選ぶと、ゲーム画面で再生が始まり、一時停止・速度が使える。`stageHash` が違う入力ログでは、決めた文言を出して再生しない。WASM はリプレイの画面を開くまで読まない（ほかの画面の読み込みが増えない） | 4h |
| R5 | リザルトから「このラウンドをリプレイ」 | リザルト画面に、#271 の入力ログの取得（`fetchInputLog`）を使って、そのまま `/replay` で再生するボタンを足す（ダウンロードしなくても見られる） | `web/src/app/result/ReplayButton.tsx`、`web/src/app/result/ReplayButton.test.tsx`、`web/src/app/result/ResultScreen.tsx`、`web/src/app/result/ResultScreen.test.tsx` | R4、#271（T-714。`ResultScreen.tsx` が重なり、`fetchInputLog` を使う） | `inputLogAvailable` が `true` のときだけボタンを出す。押すと、取得した入力ログで `/replay` の再生が始まる。取得に失敗したら #271 と同じ文言 | 2h |

- シーク（任意の時刻へ飛ぶ）と、リプレイでの `node.detail`（WASM の Engine から作る）は、R4 の後に必要なら別に起票する（この ADR の範囲では初版に入れない）
- R2 の共有（FR-LOG-06）は別の設計の Issue。R4 の画面に「URL から入力ログを取得する」口を足すのは、その Issue で行う

## 差し替えるときの影響範囲

- 案 B に替える: `web/worker/`（DO のリプレイの種類・アップロードの口）、`web/worker/cost-guard.ts`（予約の種類）、`server/internal/server`（リプレイのモード）、プロトコル §2・§9、DesignDoc §10.4・§12。R4・R5 の画面は、再生の元を WASM から WS に替えるだけで残る
- 案 C に替える: R2（`server/cmd/replaywasm`）を外し、記録を配る道（CI か、ラウンドの終わりのコンテナから R2）を足す。R3 の再生と R4 の画面は残る
- いずれも `internal/sim` は変わらない

## 見直すきっかけ

- WASM が大きくなりすぎたとき（brotli で 5 MB を超える、または Workers の静的ファイルの上限に近づいた）→ TinyGo（Go 1.26 に対応したら）か、案 B・C1
- 遅い端末での再シミュレーションが数秒を超えると分かったとき → 計算を Web Worker に移す、または案 B
- リプレイで `node.detail` やシークが必要になったとき → WASM の Engine を時刻 t まで進める子 Issue を起票する
- ステージ定義の変更が多く、古い入力ログを再生できないことが問題になったとき → 「`stageHash` の不一致」の後で検討する案 (a)・(b)
