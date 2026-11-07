//go:build js && wasm

package main

import (
	"fmt"
	"os"
	"syscall/js"

	"github.com/K-Kizuku/raft-game/server/internal/stage"
)

// main は埋め込んだステージ定義を 1 回だけ読み、globalThis.raftReplay を公開して止まる（doc.go）。
// 引数があるとき（Node での確かめ。os.Args は [WASM のパス, 引数…]）は runCheck を実行して終わる。
// ブラウザでは os.Args が ["js"] なので、このモードに入らない。
func main() {
	cat := embeddedCatalog()
	if len(os.Args) > 1 {
		os.Exit(runCheck(os.Args[1:], cat, build, os.Stdout, os.Stderr))
	}
	js.Global().Set("raftReplay", js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) != 1 || args[0].Type() != js.TypeString {
			return string(encodeFailure(&failure{code: codeBadInput, message: "引数は入力ログの文字列 1 つ"}, build))
		}
		// 大きさの判定（too_large）は handle が、入力ログを読む前にバイト数で行う。
		return string(handle([]byte(args[0].String()), cat, build))
	}))
	select {}
}

// embeddedCatalog は stage.Embedded() のステージ定義を読む。埋め込んでいない・読めないときは nil
// （raftReplay は呼ぶたびに internal を返す）。読めない理由は標準エラーに 1 回だけ出す。
func embeddedCatalog() *catalog {
	fsys, ok := stage.Embedded()
	if !ok {
		_, _ = fmt.Fprintln(os.Stderr, "raft-replay: ステージ定義を埋め込んでいない（-tags embeddata でビルドする）")
		return nil
	}
	cat, err := loadCatalog(fsys)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "raft-replay: %v\n", err)
		return nil
	}
	return cat
}
