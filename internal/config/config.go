package config

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Host             string
	Port             int
	DefaultChannel  string
	MaxHistory       int
	PollDelayMs      int
	ReconnectDelayMs int
	InputMaxLength   int

	BBSID  string
	Sysop  string

	SysopAvatarsDir string
	OutputCharset   string // "cp437" (default) or "utf8"

	ScreenCols int // 0 = auto-detect, then fall back to 80
	ScreenRows int // 0 = auto-detect, then fall back to 24

	MotdChannel string // chat channel name where MOTD messages are pushed

	IdleEnabled        bool
	IdleTimeoutSeconds int
	IdleSwitchInterval int      // seconds between animation rotations
	IdleFPS            int      // frames-per-second cap for animation ticks
	IdleRandom         bool     // pick next animation randomly vs in sequence order
	IdleSequence       []string // ordered playback list (when IdleRandom = false)
	IdleDisable        []string // animations to skip (matches config name, e.g. "fireworks")

	FigletMessages []string // pipe-separated in INI; rotated through by figlet_message
	FigletColors   bool     // enable rainbow color cycling on figlet text
	FigletMove     bool     // enable bouncing motion (vs static centered)

	SplashTimeoutSeconds int // auto-dismiss splash after N seconds; user can press a key to skip earlier. 0 = skip splash entirely.

	AnsiGalleryDir       string // directory of .ans/.bin files for the ansi_gallery idle animation; empty = disable
	IdleInterleaveAnsi   bool   // insert a single piece of gallery art between every procedural idle animation

	Theme string // theme name (resolved to themes/<name>.ini) or full .ini path

	Raw map[string]string
}

func Default() *Config {
	return &Config{
		Host:               "futureland.today",
		Port:               10088,
		DefaultChannel:     "main",
		MaxHistory:         200,
		PollDelayMs:        25,
		ReconnectDelayMs:   3000,
		InputMaxLength:     500,
		IdleEnabled:        true,
		IdleTimeoutSeconds: 180,
		IdleSwitchInterval: 60,
		IdleFPS:            8,
		IdleRandom:         true,
		IdleInterleaveAnsi: true,
		FigletColors:         true,
		FigletMove:           true,
		FigletMessages:       []string{"Avatar Chat"},
		MotdChannel:          "motd",
		OutputCharset:        "cp437",
		SplashTimeoutSeconds: 5,
		Theme:                "futurewave",
		Raw:                  map[string]string{},
	}
}

func Load(path string) (*Config, error) {
	cfg := Default()
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, ";") || strings.HasPrefix(trim, "#") {
			continue
		}
		eq := strings.IndexByte(trim, '=')
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(trim[:eq])
		val := strings.TrimSpace(trim[eq+1:])
		cfg.Raw[key] = val

		switch key {
		case "host":
			cfg.Host = val
		case "port":
			cfg.Port = atoiOr(val, cfg.Port)
		case "default_channel":
			cfg.DefaultChannel = val
		case "max_history":
			cfg.MaxHistory = atoiOr(val, cfg.MaxHistory)
		case "poll_delay_ms":
			cfg.PollDelayMs = atoiOr(val, cfg.PollDelayMs)
		case "reconnect_delay_ms":
			cfg.ReconnectDelayMs = atoiOr(val, cfg.ReconnectDelayMs)
		case "input_max_length":
			cfg.InputMaxLength = atoiOr(val, cfg.InputMaxLength)
		case "bbs_id":
			cfg.BBSID = val
		case "sysop":
			cfg.Sysop = val
		case "sysop_avatars_dir":
			cfg.SysopAvatarsDir = val
		case "output_charset":
			cfg.OutputCharset = strings.ToLower(val)
		case "screen_cols":
			cfg.ScreenCols = atoiOr(val, cfg.ScreenCols)
		case "screen_rows":
			cfg.ScreenRows = atoiOr(val, cfg.ScreenRows)
		case "motd_channel":
			cfg.MotdChannel = val
		case "idle_enabled":
			cfg.IdleEnabled = parseBool(val, cfg.IdleEnabled)
		case "idle_timeout_seconds":
			cfg.IdleTimeoutSeconds = atoiOr(val, cfg.IdleTimeoutSeconds)
		case "idle_switch_interval":
			cfg.IdleSwitchInterval = atoiOr(val, cfg.IdleSwitchInterval)
		case "idle_fps":
			cfg.IdleFPS = atoiOr(val, cfg.IdleFPS)
		case "idle_random":
			cfg.IdleRandom = parseBool(val, cfg.IdleRandom)
		case "idle_sequence":
			cfg.IdleSequence = splitCSV(val)
		case "idle_disable":
			cfg.IdleDisable = splitCSV(val)
		case "idle_figlet_messages":
			cfg.FigletMessages = splitPipe(val)
		case "idle_figlet_colors":
			cfg.FigletColors = parseBool(val, cfg.FigletColors)
		case "idle_figlet_move":
			cfg.FigletMove = parseBool(val, cfg.FigletMove)
		case "splash_timeout_seconds":
			cfg.SplashTimeoutSeconds = atoiOr(val, cfg.SplashTimeoutSeconds)
		case "ansi_gallery_dir":
			cfg.AnsiGalleryDir = val
		case "idle_interleave_ansi":
			cfg.IdleInterleaveAnsi = parseBool(val, cfg.IdleInterleaveAnsi)
		case "theme":
			cfg.Theme = val
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	return cfg, nil
}

func atoiOr(s string, def int) int {
	if v, err := strconv.Atoi(s); err == nil {
		return v
	}
	return def
}

// splitPipe parses a pipe-separated list. Unlike splitCSV, tokens preserve
// their original casing (figlet messages care about it).
func splitPipe(s string) []string {
	parts := strings.Split(s, "|")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		t := strings.TrimSpace(p)
		if t != "" {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// splitCSV parses a comma-separated list of trimmed lowercase tokens.
// Empty or whitespace-only inputs yield nil (not []string{""}) so callers
// can use len(...) == 0 as an "unset" check.
func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		t := strings.ToLower(strings.TrimSpace(p))
		if t != "" {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func parseBool(s string, def bool) bool {
	switch strings.ToLower(s) {
	case "true", "yes", "on", "1":
		return true
	case "false", "no", "off", "0":
		return false
	}
	return def
}
