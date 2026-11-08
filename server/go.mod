module github.com/K-Kizuku/raft-game/server

go 1.26

require go.etcd.io/raft/v3 v3.7.0

require google.golang.org/protobuf v1.36.11

require go.yaml.in/yaml/v3 v3.0.6

require github.com/coder/websocket v1.8.15

// DTN-06（#163）: BPv7 の Bundle の CBOR（MIT。NOTICE）。float16 はその依存（MIT）。
require github.com/fxamacker/cbor/v2 v2.10.0

require github.com/x448/float16 v0.8.4 // indirect

// ADR-0005: 選挙のタイムアウトの乱数を注入するパッチを当てた同梱の版を使う。
// 詳しくは third_party/etcd-raft/PATCHES.md。
replace go.etcd.io/raft/v3 => ./third_party/etcd-raft
