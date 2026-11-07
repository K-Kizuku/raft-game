// Package raftcheck は、同梱の etcd-raft（server/third_party/etcd-raft）に
// ADR-0005 のパッチ（Config.RandIntn）が当たっていて、ビルドと利用ができることを
// 確かめるための最小のパッケージ。テストだけを持つ（T-002）。
//
// patch_test.go（T-003）は、パッチの性質を確かめる: RandIntn が nil のときに
// 上流の既定の乱数源へ切り戻ること、タイムアウトをリセットするたびに RandIntn が
// 呼ばれること、RandIntn がノードごとに独立していること、シード付きの乱数で
// 展開が再現されること。上流との差分がパッチだけであることは
// scripts/raft/verify-patch.sh で確かめる。
package raftcheck
