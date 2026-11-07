# 同梱の etcd-io/raft とパッチ

このディレクトリは、[etcd-io/raft](https://github.com/etcd-io/raft) のモジュール `go.etcd.io/raft/v3` を同梱したものです。`server/go.mod` の `replace go.etcd.io/raft/v3 => ./third_party/etcd-raft` で使っています。ライセンスは上流のまま Apache-2.0 です（[LICENSE](LICENSE)）。このファイルを除き、上流との違いは下の「外したファイル」と「当てたパッチ」だけです。

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
# Go のビルドとテストに使わない上流の開発用ファイルを外す（下の「外したファイル」）
rm -rf server/third_party/etcd-raft/{tla,scripts,.github,Makefile}
# このあとで下のパッチを raft.go に当て、この PATCHES.md を置き直す
```

## 外したファイル

Go のビルドとテスト（`go build`・`go vet`・`go test`）に使わない、上流の開発用のファイルは取り込みません。

| パス | 中身 | 外した理由 |
|---|---|---|
| `tla/` | TLA+ の仕様とモデル検査用のスクリプト（`validate.sh`・`validate-model.sh`） | Go から参照されない。CI の shell ジョブは `git ls-files -- '*.sh'` の全件に shellcheck をかけるので、上流のシェルスクリプトがエラーになる |
| `scripts/` | 上流の開発用スクリプト（`test.sh`・`genproto.sh` など） | 同上 |
| `Makefile` | `scripts/test.sh` を呼ぶだけの上流の開発用の入口 | `scripts/` を外すと動かない。このリポジトリでは使わない |
| `.github/` | 上流の GitHub Actions と Dependabot の設定 | サブディレクトリの中では GitHub に読まれず、`scripts/` を参照している |

外したあとで、同梱の中に `*.sh` が残っていないことを確かめます（`git ls-files -- 'server/third_party/etcd-raft/*.sh'` が空になる）。

## 上流との差分の確かめ方

[`scripts/raft/verify-patch.sh`](../../../scripts/raft/verify-patch.sh) で確かめます（T-003）。CI の shell ジョブが、そのテスト `scripts/raft/tests/run.sh` の中で毎回 `--with-tests` 付きで実行します。

```bash
bash scripts/raft/verify-patch.sh               # 上流との比較だけ
bash scripts/raft/verify-patch.sh --with-tests  # 比較が通ったら、同梱の上流のテストとパッチのテストも実行する
```

終了コードは 0 = 一致、1 = 差分がある、2 = 使い方の誤り・環境の不備（`go` がない・取得に失敗など）です。次の検査を行い、失敗したものをすべて出します。

1. **版**: `server/go.mod` の `require go.etcd.io/raft/v3` の版と、上の「取り込んだ版」の表の版が一致し、`replace go.etcd.io/raft/v3 => ./third_party/etcd-raft` がある
2. **上流の取得**: `go mod download -json` で上流の版を取得し、`h1:` ハッシュが上の表と一致する（`GOTOOLCHAIN=auto` なら Go 1.26 を自動で取得する）
3. **ファイルの一覧**: 上流のファイルから「外したファイル」を除いたものと、同梱の Git で追跡されているファイルから `PATCHES.md` を除いたものが一致する。外したはずのファイルが同梱に残っていないこと
4. **内容**: `raft.go` 以外のファイルが 1 バイトも違わない
5. **パッチ**: `raft.go` の変わった行（`+`・`-` の行）の並びが、下の「当てたパッチ」の差分と一致する（`@@` の行番号と文脈の行は比べない）
6. **上流の乱数の使い方**: 上流のテスト以外の Go のファイルで、`globalRand.` を使うのが `globalRand.Intn(r.electionTimeout)` の 1 行だけ

参考: 手で比べるときは次のとおりです。外したパスを除くと、差分は `PATCHES.md`（同梱側だけにある）と `raft.go`（下のパッチ）だけになります。

```bash
dir=$(go mod download -json go.etcd.io/raft/v3@v3.7.0 | python3 -c 'import json,sys; print(json.load(sys.stdin)["Dir"])')
diff -r -x tla -x scripts -x .github -x Makefile "$dir" server/third_party/etcd-raft
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
5. `go -C server build ./...`・`go -C server vet ./...`・`go -C server test ./...` と、同梱の上流のテスト `go -C server/third_party/etcd-raft test .` が通ることを確かめる。最後に `bash scripts/raft/verify-patch.sh --with-tests` が終了コード 0 で通ることを確かめる（手順 6 のあとでもう一度実行する）
6. この `PATCHES.md` の「取り込んだ版」の表とパッチの差分を更新し、ADR-0005 の差分も合わせる。「外したファイル」の表を変えたら、`scripts/raft/verify-patch.sh` の先頭の定数（`EXCLUDED_DIRS`・`EXCLUDED_FILES`・`VENDORED_ONLY`）も同じ PR で変える
7. 版を上げる変更は専用の PR にする
