//go:build !embeddata

package stage

import "io/fs"

// Embedded は、-tags embeddata で埋め込んだステージ定義を返す。タグなしのビルドでは
// 埋め込んでいないので、常に (nil, false)（ADR-0025）。
func Embedded() (fs.FS, bool) { return nil, false }
