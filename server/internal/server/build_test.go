package server

import (
	"regexp"
	"runtime/debug"
	"testing"
)

// CF-2: BuildID。
func TestBuildID(t *testing.T) {
	t.Parallel()
	if got := BuildID("git:abc1234"); got != "git:abc1234" {
		t.Errorf("BuildID(git:abc1234) = %q", got)
	}
	if got := BuildID(""); !regexp.MustCompile(`^(git:[0-9a-f]{7}(-dirty)?|dev)$`).MatchString(got) {
		t.Errorf("BuildID(\"\") = %q", got)
	}
}

func TestBuildIDFromSettings(t *testing.T) {
	t.Parallel()
	rev := "3f2c1ab0123456789abcdef0123456789abcdef0"
	tests := []struct {
		settings []debug.BuildSetting
		want     string
	}{
		{nil, "dev"},
		{[]debug.BuildSetting{{Key: "vcs.revision", Value: rev}}, "git:3f2c1ab"},
		{[]debug.BuildSetting{{Key: "vcs.revision", Value: rev}, {Key: "vcs.modified", Value: "true"}}, "git:3f2c1ab-dirty"},
		{[]debug.BuildSetting{{Key: "vcs.modified", Value: "false"}, {Key: "vcs.revision", Value: rev}}, "git:3f2c1ab"},
		{[]debug.BuildSetting{{Key: "vcs.revision", Value: "abc"}}, "dev"},
	}
	for _, tt := range tests {
		if got := buildIDFromSettings(tt.settings); got != tt.want {
			t.Errorf("buildIDFromSettings(%v) = %q, want %q", tt.settings, got, tt.want)
		}
	}
}
