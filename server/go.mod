module github.com/K-Kizuku/raft-game/server

go 1.26.0

require go.etcd.io/raft/v3 v3.7.0

require google.golang.org/protobuf v1.36.11

require go.yaml.in/yaml/v3 v3.0.6

require github.com/coder/websocket v1.8.15

// DTN-06（#163）: BPv7 の Bundle の CBOR（MIT。NOTICE）。float16 はその依存（MIT）。
require github.com/fxamacker/cbor/v2 v2.10.0

// DTN-07（#164）: Actor Host の Go の SQLite adapter（純 Go・BSD-3-Clause。NOTICE）。
// 下の indirect の modernc.org/*・go-humanize・uuid・go-isatty・go-strftime・bigfft・x/sys はその依存。
// modernc.org/sqlite が go 1.26.0 を求めるので、go の行は 1.26.0 になった（1.26 と同じ版）。
require modernc.org/sqlite v1.62.0

require (
	github.com/dustin/go-humanize v1.1.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/ncruces/go-strftime v1.1.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/x448/float16 v0.8.4 // indirect
	golang.org/x/sys v0.49.0 // indirect
	modernc.org/libc v1.80.1 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.12.1 // indirect
)

// ADR-0005: 選挙のタイムアウトの乱数を注入するパッチを当てた同梱の版を使う。
// 詳しくは third_party/etcd-raft/PATCHES.md。
replace go.etcd.io/raft/v3 => ./third_party/etcd-raft
