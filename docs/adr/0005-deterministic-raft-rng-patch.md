# ADR-0005: 決定性のため etcd-io/raft に乱数源を注入する最小パッチを当てる

- 状態: Accepted（2026-11-06 にプロダクトオーナーが承認）
- 日付: 2026-11-01
- 決めた人: Claude
- 関連: 企画書 §10.5・§15-6、NFR-04、V-6、ADR-0002、ADR-0003、[DesignDoc §9](../03-design-doc.md)

## 背景

企画書は「同じ乱数シード・操作入力でシミュレーションを再現できる」ことを検証項目にしている（§15-6）。ところが `go.etcd.io/raft/v3` v3.7.0 は、選挙タイムアウトの乱数に **`crypto/rand`** を使っている。

```go
// raft.go（v3.7.0）
func (r *lockedRand) Intn(n int) int {
	r.mu.Lock()
	v, _ := rand.Int(rand.Reader, big.NewInt(int64(n)))   // crypto/rand
	r.mu.Unlock()
	return int(v.Int64())
}

var globalRand = &lockedRand{}
...
func (r *raft) resetRandomizedElectionTimeout() {
	r.randomizedElectionTimeout = r.electionTimeout + globalRand.Intn(r.electionTimeout)
}
```

外から乱数源を差し替える口はない。また `globalRand` はパッケージ全体で 1 つなので、仮に差し替えられても、1 プロセスで複数のセッションを並行に動かすとセッションどうしで乱数列を奪い合い、決定性が崩れる。

`globalRand` の利用箇所は `resetRandomizedElectionTimeout` の 1 か所だけであることをソースで確認した。

## 検討した選択肢

### 案 A: `Config` に乱数源を注入するフィールドを足す最小パッチ（ノードごとに注入）

- 長所: アルゴリズムには一切触れない（乱数の出どころだけを変える）。ノードごとに独立した乱数列にできるので、複数のセッションを並行に動かしても決定的
- 短所: ライブラリをフォーク（同梱）して管理する必要がある。上流の更新に追従するときにパッチを当て直す

### 案 B: パッチを当てず、「記録の再生」でリプレイする

- 長所: ライブラリをそのまま使える
- 短所: 再シミュレーションができず、「航路を変えたら結果がどう変わるか」の比較（企画書 §10.5）ができない。決定性のテスト（V-6）とシード固定のシミュレーションテストができない。デモの展開を固定できない

### 案 C: 選挙タイムアウトをゲーム側で管理する（`ElectionTick` を非常に大きくし、ゲーム側の決定的なタイマーで `Campaign()` を呼ぶ）

- 長所: ライブラリをそのまま使える
- 短所: 「Leader からの連絡でタイマーをリセットする」という Raft の選挙の規則をゲーム側で再実装することになり、「本物の Raft」から遠ざかる。CheckQuorum や PreVote のリース判定は `electionTimeout` を内部で使うので、整合を取るのが難しい

### 案 D: 全セッションの Raft の呼び出しをグローバルなロックで直列化し、`globalRand` を差し替える（`go:linkname` などで）

- 長所: パッチが不要
- 短所: 非公開の変数への `linkname` は Go 1.23 以降で制限されており脆い。並行性を失う

## 決定

案 A。`server/third_party/etcd-raft/` に v3.7.0 を同梱し、次のパッチを当てて `go.mod` の `replace` で使う。

```diff
--- raft.go.orig
+++ raft.go
@@ -288,6 +288,11 @@
 
 	// raft state tracer
 	TraceLogger TraceLogger
+
+	// RandIntn, if non-nil, is used instead of the package-level crypto/rand
+	// source to randomize the election timeout. It must return a value in
+	// [0, n). It allows deterministic simulation. (raft-game patch)
+	RandIntn func(n int) int
 }
 
 func (c *Config) validate() error {
@@ -421,6 +426,7 @@
 	randomizedElectionTimeout int
 	disableProposalForwarding bool
 	stepDownOnRemoval         bool
+	randIntn                  func(n int) int // raft-game patch
 
 	tick func()
 	step stepFunc
@@ -463,8 +469,12 @@
 		disableProposalForwarding:   c.DisableProposalForwarding,
 		disableConfChangeValidation: c.DisableConfChangeValidation,
 		stepDownOnRemoval:           c.StepDownOnRemoval,
+		randIntn:                    c.RandIntn,
 		traceLogger:                 c.TraceLogger,
 	}
+	if r.randIntn == nil { // raft-game patch
+		r.randIntn = globalRand.Intn
+	}
 
 	traceInitState(r)
 
@@ -2051,7 +2061,7 @@
 }
 
 func (r *raft) resetRandomizedElectionTimeout() {
-	r.randomizedElectionTimeout = r.electionTimeout + globalRand.Intn(r.electionTimeout)
+	r.randomizedElectionTimeout = r.electionTimeout + r.randIntn(r.electionTimeout)
 }
```

ゲーム側では、ノードごとに `rand.New(rand.NewPCG(seed, fnv64a("raft/<nodeID>"))).IntN` を渡す。

### 事前の検証（2026-11-01 に実施）

- 上記のパッチを v3.7.0 に当て、`go build ./...`・`go vet`・上流のテスト（`go test .`）がすべて通ることを確認した
- 3 ノードの `RawNode` を、30% のメッセージを（シード付きの乱数で）落とすネットワークで 3,000 ステップ動かし、適用されたエントリ・配達したメッセージ・最終状態をハッシュにした実験:

| 条件 | 3 回実行したときのハッシュ |
|---|---|
| パッチあり・同じシードの乱数を注入 | `8a09ce09…` / `8a09ce09…` / `8a09ce09…`（一致） |
| パッチあり・別のシード | `f14b5518…`（異なる展開） |
| 注入なし（`crypto/rand`） | `3ae31745…` / `ab1fa467…` / `b8bd5f9b…`（毎回異なる） |

- 乱数以外に非決定的な要素は見つからなかった（進捗の管理 `tracker.Visit` は ID をソートしてから走査している）
- 実験中に気づいた注意点: v3.7 の `Status` は `*pb.HardState` を埋め込んでおり、`st.Term` はポインタ。値は `st.HardState.GetTerm()` で読む

## 結果

- 良いこと: 決定性のテスト・シード固定のシミュレーションテスト・リプレイ・デモの展開の固定ができる
- 悪いこと: ライブラリを同梱して管理する。Apache-2.0 に従い、ライセンス本文・NOTICE・改変した旨（`PATCHES.md`）を同梱する必要がある

### 運用

- `server/third_party/etcd-raft/PATCHES.md` に、元の版（v3.7.0）・パッチの内容・理由を書く
- CI で、上流の v3.7.0 との差分がこのパッチだけであることをスクリプトで確認する（実装計画 T-003）
- 上流に「乱数源を注入できるようにする」提案（Issue / PR）を出すことも検討する。取り込まれれば `replace` を外せる

## 差し替えるときの影響範囲

`server/go.mod`（`replace`）、`server/third_party/etcd-raft/`、`server/internal/cluster`（`RandIntn` を渡している箇所）、決定性のテスト。

## 見直すきっかけ

- 上流が乱数源の注入に対応したとき（パッチをやめる）
- 決定性が不要だと判断したとき（案 B に戻す）
