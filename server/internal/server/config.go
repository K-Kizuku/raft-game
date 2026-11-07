package server

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
)

// Mode は起動のモード（DesignDoc §12.1）。
type Mode string

const (
	ModeSingle     Mode = "single"
	ModeStandalone Mode = "standalone"
)

// Config は起動の設定。
type Config struct {
	Mode           Mode
	Host           string   // 待ち受けるホスト
	Port           int      // 0 なら空いているポート（テスト用）
	DataDir        string   // "" なら埋め込み（stage.Embedded）
	AllowedOrigins []string // standalone だけ。ALLOWED_ORIGINS を読んだもの（有効な要素だけ）
	DemoControls   bool     // standalone だけ。true なら demo: true のセッションを作れる
	MaxSessions    int      // standalone だけ
	Invariants     bool     // sim.Options.CheckInvariants
	LogLevel       slog.Level
	Build          string // BuildID の結果。ParseConfig は設定しない（main が入れる）
}

// ErrUsage は起動の設定の誤り（main は終了コード 2 にする）。
var ErrUsage = errors.New("server: usage")

// 既定値。
const (
	defaultPort           = 8080
	defaultMaxSessions    = 16
	maxMaxSessions        = 256
	defaultStandaloneHost = "127.0.0.1"
)

// ParseConfig はフラグ args と環境変数（getenv）から設定を作る。フラグが環境変数より優先する。
// 環境変数の値が "" のときは未設定と同じに扱う。
// 誤りは ErrUsage を包んだエラー（main は終了コード 2 にする）。-h・--help も ErrUsage を包み、
// エラーの文に使い方を含める。
func ParseConfig(args []string, getenv func(string) string) (Config, error) {
	fs := flag.NewFlagSet("server", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var (
		mode           = fs.String("mode", "", "single か standalone（MODE。必須）")
		host           = fs.String("host", "", "待ち受けるホスト（HOST。既定: single は全アドレス、standalone は 127.0.0.1）")
		port           = fs.String("port", "", "待ち受けるポート 0〜65535（PORT。既定 8080）")
		dataDir        = fs.String("data", "", "ステージ定義のディレクトリ（DATA_DIR。既定: 埋め込み）")
		allowedOrigins = fs.String("allowed-origins", "", "許可する Origin のカンマ区切り（ALLOWED_ORIGINS。standalone だけ）")
		demoControls   = fs.Bool("demo-controls", false, "デモ操作を有効にする（DEMO_CONTROLS=1。standalone だけ）")
		maxSessions    = fs.String("max-sessions", "", "セッションの数の上限 1〜256（MAX_SESSIONS。既定 16。standalone だけ）")
		invariants     = fs.Bool("invariants", false, "Engine の不変条件を検査する（SIM_INVARIANTS=1）")
		logLevel       = fs.String("log-level", "", "debug・info・warn・error（LOG_LEVEL。既定 info）")
	)
	usageErr := func(err error) error {
		return fmt.Errorf("%w: %v\n%s", ErrUsage, err, usage(fs))
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return Config{}, fmt.Errorf("%w: %w\n%s", ErrUsage, err, usage(fs))
		}
		return Config{}, usageErr(err)
	}
	if fs.NArg() > 0 {
		return Config{}, usageErr(fmt.Errorf("位置引数は受け付けない: %q", fs.Arg(0)))
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })

	// value は、フラグを指定していればその値を、なければ環境変数の値を返す。
	value := func(name, env string, flagValue string) (string, bool) {
		if set[name] {
			return flagValue, true
		}
		if v := getenv(env); v != "" {
			return v, true
		}
		return "", false
	}
	// boolValue は、フラグがあればその値、なければ環境変数（1 だけが真、0 は偽、ほかは誤り）。
	boolValue := func(name, env string, flagValue bool) (bool, error) {
		if set[name] {
			return flagValue, nil
		}
		switch v := getenv(env); v {
		case "", "0":
			return false, nil
		case "1":
			return true, nil
		default:
			return false, fmt.Errorf("%s は 1・0 のどれか（値: %q）", env, v)
		}
	}

	var cfg Config
	m, _ := value("mode", "MODE", *mode)
	switch Mode(m) {
	case ModeSingle, ModeStandalone:
		cfg.Mode = Mode(m)
	case "":
		return Config{}, usageErr(errors.New("--mode（MODE）がない: single か standalone を指定する"))
	default:
		return Config{}, usageErr(fmt.Errorf("--mode（MODE）は single か standalone（値: %q）", m))
	}

	if h, ok := value("host", "HOST", *host); ok {
		cfg.Host = h
	} else if cfg.Mode == ModeStandalone {
		cfg.Host = defaultStandaloneHost
	}

	cfg.Port = defaultPort
	if p, ok := value("port", "PORT", *port); ok {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || n > 65535 {
			return Config{}, usageErr(fmt.Errorf("--port（PORT）は 0〜65535 の整数（値: %q）", p))
		}
		cfg.Port = n
	}

	cfg.DataDir, _ = value("data", "DATA_DIR", *dataDir)

	if o, ok := value("allowed-origins", "ALLOWED_ORIGINS", *allowedOrigins); ok {
		cfg.AllowedOrigins = parseAllowedOrigins(o)
	}

	var err error
	if cfg.DemoControls, err = boolValue("demo-controls", "DEMO_CONTROLS", *demoControls); err != nil {
		return Config{}, usageErr(err)
	}
	if cfg.Invariants, err = boolValue("invariants", "SIM_INVARIANTS", *invariants); err != nil {
		return Config{}, usageErr(err)
	}

	cfg.MaxSessions = defaultMaxSessions
	if s, ok := value("max-sessions", "MAX_SESSIONS", *maxSessions); ok {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > maxMaxSessions {
			return Config{}, usageErr(fmt.Errorf("--max-sessions（MAX_SESSIONS）は 1〜%d の整数（値: %q）", maxMaxSessions, s))
		}
		cfg.MaxSessions = n
	}

	cfg.LogLevel = slog.LevelInfo
	if l, ok := value("log-level", "LOG_LEVEL", *logLevel); ok {
		switch l {
		case "debug":
			cfg.LogLevel = slog.LevelDebug
		case "info":
			cfg.LogLevel = slog.LevelInfo
		case "warn":
			cfg.LogLevel = slog.LevelWarn
		case "error":
			cfg.LogLevel = slog.LevelError
		default:
			return Config{}, usageErr(fmt.Errorf("--log-level（LOG_LEVEL）は debug・info・warn・error のどれか（値: %q）", l))
		}
	}
	return cfg, nil
}

// usage は使い方の文を返す。
func usage(fs *flag.FlagSet) string {
	var b bytes.Buffer
	b.WriteString("使い方: server --mode single|standalone [フラグ]\n")
	fs.SetOutput(&b)
	fs.PrintDefaults()
	fs.SetOutput(io.Discard)
	return strings.TrimRight(b.String(), "\n")
}

// parseAllowedOrigins は ALLOWED_ORIGINS（カンマ区切り）を読む（プロトコル §2 の「Origin の規則」）。
// 各要素の前後の空白を除き、http:・https: の URL として読めるものだけを、そのオリジン
// （スキームとホストを小文字にし、既定のポートとパスを落としたもの）として使う。読めない要素は無視する。
func parseAllowedOrigins(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if o, ok := originOf(strings.TrimSpace(part)); ok && !contains(out, o) {
			out = append(out, o)
		}
	}
	return out
}

// originOf は URL の文字列 s のオリジンを返す（ブラウザの URL.origin と同じ形）。
func originOf(s string) (string, bool) {
	if s == "" {
		return "", false
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", false
	}
	scheme := strings.ToLower(u.Scheme)
	if (scheme != "http" && scheme != "https") || u.Host == "" || u.Opaque != "" {
		return "", false
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return "", false
	}
	if strings.Contains(host, ":") { // IPv6
		host = "[" + host + "]"
	}
	defaultPort := (scheme == "http" && u.Port() == "80") || (scheme == "https" && u.Port() == "443")
	if p := u.Port(); p != "" && !defaultPort {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || n > 65535 {
			return "", false
		}
		host += ":" + strconv.Itoa(n)
	}
	return scheme + "://" + host, true
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
