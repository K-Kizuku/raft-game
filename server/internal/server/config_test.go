package server

import (
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"testing"
)

func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// CF-1: ParseConfig の既定値・優先順位・誤り。
func TestParseConfig(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
		env  map[string]string
		want Config
	}{
		{
			name: "single の既定値",
			args: []string{"--mode", "single"},
			want: Config{Mode: ModeSingle, Host: "", Port: 8080, MaxSessions: 16, LogLevel: slog.LevelInfo, AutoFerry: true},
		},
		{
			name: "standalone の既定値",
			env:  map[string]string{"MODE": "standalone"},
			want: Config{Mode: ModeStandalone, Host: "127.0.0.1", Port: 8080, MaxSessions: 16, LogLevel: slog.LevelInfo, AutoFerry: true},
		},
		{
			name: "環境変数",
			env: map[string]string{
				"MODE": "standalone", "HOST": "0.0.0.0", "PORT": "18080", "DATA_DIR": "/data",
				"ALLOWED_ORIGINS": "http://127.0.0.1:15173, http://localhost:15173", "DEMO_CONTROLS": "1",
				"MAX_SESSIONS": "256", "SIM_INVARIANTS": "1", "LOG_LEVEL": "debug",
			},
			want: Config{
				Mode: ModeStandalone, Host: "0.0.0.0", Port: 18080, DataDir: "/data",
				AllowedOrigins: []string{"http://127.0.0.1:15173", "http://localhost:15173"},
				DemoControls:   true, MaxSessions: 256, Invariants: true, LogLevel: slog.LevelDebug, AutoFerry: true,
			},
		},
		{
			name: "フラグが環境変数に勝つ",
			args: []string{
				"--mode", "single", "--host", "::1", "--port", "0", "--data", "d", "--allowed-origins", "https://a.example",
				"--demo-controls=false", "--max-sessions", "1", "--invariants=false", "--log-level", "error",
				"--auto-ferry=false",
			},
			env: map[string]string{
				"MODE": "standalone", "HOST": "x", "PORT": "1", "DATA_DIR": "e", "ALLOWED_ORIGINS": "http://b.example",
				"DEMO_CONTROLS": "1", "MAX_SESSIONS": "9", "SIM_INVARIANTS": "1", "LOG_LEVEL": "debug", "AUTO_FERRY": "1",
			},
			want: Config{
				Mode: ModeSingle, Host: "::1", Port: 0, DataDir: "d", AllowedOrigins: []string{"https://a.example"},
				MaxSessions: 1, LogLevel: slog.LevelError,
			},
		},
		{
			name: "環境変数の空は未設定と同じ",
			env: map[string]string{
				"MODE": "standalone", "HOST": "", "PORT": "", "DATA_DIR": "", "ALLOWED_ORIGINS": "",
				"DEMO_CONTROLS": "", "MAX_SESSIONS": "", "SIM_INVARIANTS": "", "LOG_LEVEL": "", "AUTO_FERRY": "",
			},
			want: Config{Mode: ModeStandalone, Host: "127.0.0.1", Port: 8080, MaxSessions: 16, LogLevel: slog.LevelInfo, AutoFerry: true},
		},
		{
			name: "DEMO_CONTROLS=0 と真偽値のフラグ",
			args: []string{"--demo-controls", "--invariants"},
			env:  map[string]string{"MODE": "standalone", "DEMO_CONTROLS": "0", "LOG_LEVEL": "warn"},
			want: Config{Mode: ModeStandalone, Host: "127.0.0.1", Port: 8080, DemoControls: true, Invariants: true,
				MaxSessions: 16, LogLevel: slog.LevelWarn, AutoFerry: true},
		},
		{
			name: "AUTO_FERRY=0 で自動航行を無効にする（T-724）",
			env:  map[string]string{"MODE": "standalone", "AUTO_FERRY": "0"},
			want: Config{Mode: ModeStandalone, Host: "127.0.0.1", Port: 8080, MaxSessions: 16, LogLevel: slog.LevelInfo},
		},
		{
			name: "--auto-ferry は AUTO_FERRY=0 に勝つ（T-724）",
			args: []string{"--mode", "standalone", "--auto-ferry"},
			env:  map[string]string{"AUTO_FERRY": "0"},
			want: Config{Mode: ModeStandalone, Host: "127.0.0.1", Port: 8080, MaxSessions: 16, LogLevel: slog.LevelInfo, AutoFerry: true},
		},
		{
			name: "ALLOWED_ORIGINS の読めない要素は無視する",
			args: []string{"--mode=standalone"},
			env: map[string]string{
				"ALLOWED_ORIGINS": " HTTP://Example.COM/path/ ,ftp://x, notaurl,,http://localhost:80,https://a.example:443,http://[::1]:5173,http://localhost:80",
			},
			want: Config{
				Mode: ModeStandalone, Host: "127.0.0.1", Port: 8080, MaxSessions: 16, LogLevel: slog.LevelInfo, AutoFerry: true,
				AllowedOrigins: []string{"http://example.com", "http://localhost", "https://a.example", "http://[::1]:5173"},
			},
		},
		{
			name: "ALLOWED_ORIGINS がすべて読めない",
			env:  map[string]string{"MODE": "standalone", "ALLOWED_ORIGINS": "x, ftp://y"},
			want: Config{Mode: ModeStandalone, Host: "127.0.0.1", Port: 8080, MaxSessions: 16, LogLevel: slog.LevelInfo, AutoFerry: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseConfig(tt.args, envOf(tt.env))
			if err != nil {
				t.Fatalf("ParseConfig: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseConfig =\n%+v\nwant\n%+v", got, tt.want)
			}
		})
	}
}

// CF-1: 起動の誤りはすべて ErrUsage。
func TestParseConfigErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
		env  map[string]string
	}{
		{"MODE がない", nil, nil},
		{"MODE=both", nil, map[string]string{"MODE": "both"}},
		{"--mode が不正", []string{"--mode", "Single"}, nil},
		{"--port 70000", []string{"--mode", "single", "--port", "70000"}, nil},
		{"--port -1", []string{"--mode", "single", "--port", "-1"}, nil},
		{"PORT=abc", nil, map[string]string{"MODE": "single", "PORT": "abc"}},
		{"位置引数", []string{"--mode", "single", "extra"}, nil},
		{"知らないフラグ", []string{"--mode", "single", "--nope"}, nil},
		{"-h", []string{"-h"}, nil},
		{"--help", []string{"--help"}, nil},
		{"DEMO_CONTROLS=yes", nil, map[string]string{"MODE": "standalone", "DEMO_CONTROLS": "yes"}},
		{"DEMO_CONTROLS=true", nil, map[string]string{"MODE": "standalone", "DEMO_CONTROLS": "true"}},
		{"SIM_INVARIANTS=2", nil, map[string]string{"MODE": "single", "SIM_INVARIANTS": "2"}},
		{"MAX_SESSIONS=0", nil, map[string]string{"MODE": "standalone", "MAX_SESSIONS": "0"}},
		{"--max-sessions 257", []string{"--mode", "standalone", "--max-sessions", "257"}, nil},
		{"LOG_LEVEL=trace", nil, map[string]string{"MODE": "single", "LOG_LEVEL": "trace"}},
		{"--demo-controls=x", []string{"--mode", "standalone", "--demo-controls=x"}, nil},
		{"AUTO_FERRY=true", nil, map[string]string{"MODE": "standalone", "AUTO_FERRY": "true"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseConfig(tt.args, envOf(tt.env))
			if !errors.Is(err, ErrUsage) {
				t.Fatalf("ParseConfig err = %v, want ErrUsage", err)
			}
			if !strings.Contains(err.Error(), "使い方") || !strings.Contains(err.Error(), "-mode") {
				t.Errorf("err に使い方がない: %v", err)
			}
		})
	}
}
