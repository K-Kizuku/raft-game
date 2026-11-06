// Package verify は、Merge Gate が CI の失敗した PR を止めることを確かめるための検証用のパッケージ（#7）。
// この PR はマージしない。
package verify

import "testing"

// TestIntentionalFailure は必ず失敗する。CI（go-tools・ci-ok）を失敗させるためのテスト。
func TestIntentionalFailure(t *testing.T) {
	t.Fatal("intentional failure for merge-gate verification (#7)")
}
