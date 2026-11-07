package main

import (
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// cgroupInfo はコンテナの中から見た CPU とメモリの上限（M-14）。読めなければ null。
type cgroupInfo struct {
	CPUMax    *string `json:"cpuMax"`
	MemoryMax *string `json:"memoryMax"`
}

type envInfo struct {
	MODE *string `json:"MODE"`
	PORT *string `json:"PORT"`
}

type infoResponse struct {
	GoVersion  string     `json:"goVersion"`
	GOOS       string     `json:"goos"`
	GOARCH     string     `json:"goarch"`
	NumCPU     int        `json:"numCPU"`
	GOMAXPROCS int        `json:"gomaxprocs"`
	Cgroup     cgroupInfo `json:"cgroup"`
	Env        envInfo    `json:"env"`
	StartedAt  string     `json:"startedAt"`
	UptimeMs   int64      `json:"uptimeMs"`
	Hostname   *string    `json:"hostname"`
}

func (s *server) handleInfo(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.info())
}

func (s *server) info() infoResponse {
	var host *string
	if h, err := s.opts.hostname(); err == nil {
		host = &h
	}
	return infoResponse{
		GoVersion:  runtime.Version(),
		GOOS:       runtime.GOOS,
		GOARCH:     runtime.GOARCH,
		NumCPU:     runtime.NumCPU(),
		GOMAXPROCS: runtime.GOMAXPROCS(0),
		Cgroup:     readCgroup(s.opts.cgroupRoot),
		Env:        envInfo{MODE: envOrNil(s.opts.getenv, "MODE"), PORT: envOrNil(s.opts.getenv, "PORT")},
		StartedAt:  s.opts.startedAt.UTC().Format(time.RFC3339Nano),
		UptimeMs:   time.Since(s.opts.startedAt).Milliseconds(),
		Hostname:   host,
	}
}

func envOrNil(getenv func(string) string, key string) *string {
	v := getenv(key)
	if v == "" {
		return nil
	}
	return &v
}

// readCgroup は cgroup v2 の cpu.max・memory.max を読む。なければ v1 の値を v2 と同じ書式にする。
// どちらもなければ null。
func readCgroup(root string) cgroupInfo {
	var out cgroupInfo
	if v, ok := readTrim(filepath.Join(root, "cpu.max")); ok {
		out.CPUMax = &v
	} else if quota, ok := readTrim(filepath.Join(root, "cpu", "cpu.cfs_quota_us")); ok {
		if period, ok := readTrim(filepath.Join(root, "cpu", "cpu.cfs_period_us")); ok {
			if quota == "-1" {
				quota = "max"
			}
			v := quota + " " + period
			out.CPUMax = &v
		}
	}
	if v, ok := readTrim(filepath.Join(root, "memory.max")); ok {
		out.MemoryMax = &v
	} else if v, ok := readTrim(filepath.Join(root, "memory", "memory.limit_in_bytes")); ok {
		out.MemoryMax = &v
	}
	return out
}

func readTrim(path string) (string, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", false // ない・読めない（cgroup v1 と v2 の違い・権限）は同じく null にする
	}
	return strings.TrimSpace(string(b)), true
}
