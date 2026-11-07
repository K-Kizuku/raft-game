//go:build embeddata

package stage

import (
	"embed"
	"io/fs"
)

// embeddedFS は、make sync-data で data/ からコピーしたステージ定義（ADR-0025）。
// all: を付けるのは、stages/ に .gitkeep しかないときにもディレクトリを埋め込むため。
//
//go:embed all:embedded
var embeddedFS embed.FS

// Embedded は、make sync-data でコピーして -tags embeddata で埋め込んだステージ定義を返す
// （islands.json と stages/ を根に持つ fs.FS）。失敗なら (nil, false)。
func Embedded() (fs.FS, bool) {
	sub, err := fs.Sub(embeddedFS, "embedded")
	if err != nil {
		return nil, false
	}
	return sub, true
}
