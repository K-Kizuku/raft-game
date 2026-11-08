package main

import "runtime/debug"

// buildID は "git:<コミットの先頭 7 桁>"（作業ツリーに変更があれば末尾に "-dirty"）。分からなければ "dev"。
// 入力ログ・記録の build に入れるだけで、Engine に渡さない（トレースに入らない）。
func buildID() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	var rev string
	var dirty bool
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if rev == "" {
		return "dev"
	}
	if len(rev) > 7 {
		rev = rev[:7]
	}
	id := "git:" + rev
	if dirty {
		id += "-dirty"
	}
	return id
}
