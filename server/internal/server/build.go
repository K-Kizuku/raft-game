package server

import "runtime/debug"

// BuildID は、ldflags で入れた値があればそれを、なければ "git:<コミットの先頭 7 桁>"
// （vcs.modified なら末尾に "-dirty"）を、どちらもなければ "dev" を返す。
// /healthz の build、snapshot の session.build、ログの build に使う。Engine には渡さない。
func BuildID(ldflag string) string {
	if ldflag != "" {
		return ldflag
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	return buildIDFromSettings(info.Settings)
}

// buildIDFromSettings は、ビルドの情報の vcs.revision・vcs.modified から識別子を作る。
func buildIDFromSettings(settings []debug.BuildSetting) string {
	var rev string
	var modified bool
	for _, s := range settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	if len(rev) < 7 {
		return "dev"
	}
	id := "git:" + rev[:7]
	if modified {
		id += "-dirty"
	}
	return id
}
