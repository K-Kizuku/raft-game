package sim

import (
	"hash/fnv"
	"math/rand/v2"
	"strconv"
)

// 乱数列の用途の名前（§9.1）。
const (
	StreamRequests = "requests" // 依頼の発生時刻・種類
	StreamEvents   = "events"   // 台本の「この時間帯のどこか」の時刻
	StreamDrift    = "drift"    // 漂流瓶で複製する手紙
)

// Sub は、ラウンドのシードと用途の名前から独立した乱数列を作る。
// 呼ぶたびに新しい乱数列を作る（同じ引数で 2 回呼ぶと、同じ列が 2 本できる）。
// 名前が空文字列でも動く。
func Sub(seed uint64, name string) *rand.Rand {
	return rand.New(rand.NewPCG(seed, fnv64a(name)))
}

// RaftStreamName は、ノードごとの選挙のタイムアウトの乱数列の名前 "raft/<nodeID>"（10 進数）。
func RaftStreamName(nodeID uint64) string {
	return "raft/" + strconv.FormatUint(nodeID, 10)
}

// RaftRandIntn は raft.Config.RandIntn（ADR-0005 のパッチ）に渡す関数を返す。
// Sub(seed, RaftStreamName(nodeID)).IntN と同じ。返す関数は n <= 0 で panic する。
// ノードを作るときに 1 回だけ呼ぶ。
func RaftRandIntn(seed, nodeID uint64) func(n int) int {
	return Sub(seed, RaftStreamName(nodeID)).IntN
}

// fnv64a は文字列の FNV-1a（64 ビット）のハッシュ値を返す。
func fnv64a(s string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s)) // hash.Hash の Write はエラーを返さない
	return h.Sum64()
}
