# 同梱の etcd-io/raft とパッチ

このディレクトリは、[etcd-io/raft](https://github.com/etcd-io/raft) のモジュール `go.etcd.io/raft/v3` を同梱したものです。`server/go.mod` の `replace go.etcd.io/raft/v3 => ./third_party/etcd-raft` で使っています。ライセンスは上流のまま Apache-2.0 です（[LICENSE](LICENSE)）。このファイルを除き、上流との違いは下のパッチだけです。

## 取り込んだ版

| 項目 | 値 |
|---|---|
| モジュール | `go.etcd.io/raft/v3` |
| 版 | `v3.7.0` |
| 上流のコミット | `b867cf13f6bc0dae21204302df97bc2355c3af55`（タグ `v3.7.0`） |
| モジュールの `h1:` ハッシュ | `h1:BGzlwx07bLv8PW6OU5HObuz1y4hlPZUXA07pM1mPUh4=` |
| 取り込んだ日 | 2026-11-07（T-002, #10） |

`go.mod`・`go.sum` は上流のまま（`module go.etcd.io/raft/v3`）です。

## 取り込み方

Go のモジュールのキャッシュにあるファイルを、そのままコピーします。キャッシュのファイルは読み取り専用なので、コピーしたあとで書き込み権限を付けます。

```bash
# リポジトリのルートで実行する（Go 1.26 が必要。GOTOOLCHAIN=auto なら自動で取得される）
dir=$(go mod download -json go.etcd.io/raft/v3@v3.7.0 | python3 -c 'import json,sys; print(json.load(sys.stdin)["Dir"])')
rm -rf server/third_party/etcd-raft
cp -r "$dir" server/third_party/etcd-raft
chmod -R u+w server/third_party/etcd-raft
# このあとで下のパッチを raft.go に当て、この PATCHES.md を置き直す
```

## 当てたパッチ

[ADR-0005](../../../docs/adr/0005-deterministic-raft-rng-patch.md) の差分そのままです。変更したファイルは `raft.go` だけです。

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

## パッチの理由

v3.7.0 は、選挙のタイムアウトの乱数にパッケージ全体で 1 つの `globalRand`（`crypto/rand`）を使っており、外から差し替える口がありません。これでは、同じシードと同じ操作から同じ展開を再現できません（NFR-04・V-6）。パッチは乱数の出どころだけを `Config.RandIntn` に変え、Raft のアルゴリズムには触れません。`RandIntn` が `nil` のときは上流と同じ `globalRand.Intn` を使います。ゲーム側はノードごとに独立した乱数列（シード付きの PCG）を渡すので、1 プロセスで複数のセッションを並行に動かしても決定的です。詳しくは ADR-0005 を参照してください。

## 上流を更新するときの手順

1. 新しい版のリリースノートを読み、`resetRandomizedElectionTimeout` と `globalRand` の使い方が変わっていないかを確かめる（上流が乱数源の注入に対応していれば、パッチと `replace` をやめることを検討する）
2. 上の「取り込み方」の版を新しい版に変えて取り込み直す
3. 上のパッチを `raft.go` に当て直す。行番号がずれたら内容で合わせる。`globalRand` の利用箇所が `resetRandomizedElectionTimeout` 以外にも増えていたら、止めて ADR を書く
4. `server/go.mod` の `require` の版を上げ、`go -C server mod tidy` を実行する
5. `go -C server build ./...`・`go -C server vet ./...`・`go -C server test ./...` と、同梱の上流のテスト `go -C server/third_party/etcd-raft test .` が通ることを確かめる
6. この `PATCHES.md` の「取り込んだ版」の表とパッチの差分を更新し、ADR-0005 の差分も合わせる
7. 版を上げる変更は専用の PR にする
