//go:build !(js && wasm)

package main

import (
	"fmt"
	"os"
)

// main はネイティブのビルドの代わり（go build ./...・go vet ./... を通すため）。WASM としてだけ動く。
func main() {
	_, _ = fmt.Fprintln(os.Stderr, "raft-replay は GOOS=js GOARCH=wasm でビルドする（scripts/build-replay-wasm.sh）")
	os.Exit(2)
}
