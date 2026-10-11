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

	Theme      string // theme name (resolved to themes/<name>.ini) or full .ini path
	UserThemes bool   // users may pick another themes/<name>.ini with /theme (default on)

	Mouse              bool // ask the terminal for mouse reports (default on; /mouse per user)
	ResizeProbeSeconds int  // re-check the screen size this often (default 5; 0 = startup only)

	// TV lounge mode (telnetvision). See internal/ui/tvlounge.go.
	TVColor      string // "auto" (default: ask the terminal), "truecolor" or "16"
	TVPopupSecs  int    // how long a comment lingers over the video (5-15, default 10)
	TVAvatars    bool   // show avatars in popups (default off; they compete with video)
	// TVAudio plays the lounge's sound through SyncTERM Audio APCs:
	// "auto" (default: only if the terminal answers the libsndfile probe),
	// "on" (skip the probe) or "off".
	TVAudio string
	// AudioLinks plays audio links (/play) the same way; same values.
	AudioLinks string
	// AudioMaxMB caps a downloaded audio link (default 20).
	AudioMaxMB int

	// Video links (/play) through the host's ffmpeg. See CONFIG.md
	// "Video links". Video is "auto" (default: on when ffmpeg and ffprobe
	// are found) or "off". The tool paths default to a PATH lookup;
	// VideoYtDlp "off" turns page links (YouTube, Vimeo) off.
	Video                string
	VideoFFmpeg          string
	VideoFFprobe         string
	VideoYtDlp           string
	VideoSites           []string // hosts whose page links go to yt-dlp
	VideoExtractors      []string // yt-dlp extractors those hosts need
	VideoMaxMB           int      // per download, default 100
	VideoMaxSeconds      int      // per playback, default 600
	VideoMaxConcurrent   int      // playbacks at once on the host, default 2
	VideoDownloadSeconds int      // per download, default 180

	// Bell sends BEL (0x07) when a PM or a mention of your name arrives,
	// at most once every few seconds. Default on.
	Bell bool

	// Inline images from links in chat. See CONFIG.md "Inline images".
	Images          bool     // images = on (default) | off
	ImageSixel      string   // "auto" (default: ask the terminal), "on", "off"
	ImageMaxMB      int      // per-download cap, default 5
	ImageCacheDir   string   // default <data>/imagecache
	ImageCacheMB    int      // disk cache cap, default 50; 0 keeps nothing
	NSFWKeywords    []string // words that veil a picture until /nsfw
	NSFWKeywordsSet bool     // nsfw_keywords was given (an empty list disables the veil)

	// Link previews: a card (site, title, summary, thumbnail) under the
	// first plain link of a message, fetched with the image guards.
	LinkPreviews        bool // link_previews = true (default) | false
	LinkPreviewTTLHours int  // how long a fetched card is reused, default 24

	// Chat attachments: /upload receives a file by Zmodem and posts it to
	// this system's media server (avatar_chat_server -media-addr). Empty
	// UploadURL turns /upload off. See MEDIA.md.
	UploadURL   string // e.g. https://bbs.example.org:10089/upload
	UploadMaxMB int    // largest file the door will receive, default 20

	// Avatar and handle-style storage. See CONFIG.md "Avatar storage".
	AvatarStore       string // "auto" (default), "synchronet" or "local"
	SynchronetDataDir string // explicit Synchronet data dir; overrides detection

	// SharedIgnoreFile is a shell's per-user prefs JSON whose ignore lists
	// the door also honours, read-only (Synchronet only). See CONFIG.md.
	SharedIgnoreFile string

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
		UserThemes:           true,
		Mouse:                true,
		ResizeProbeSeconds:   5,
		TVColor:              "auto",
		TVAudio:              "auto",
		AudioLinks:           "auto",
		AudioMaxMB:           20,
		Video:                "auto",
		VideoSites:           []string{"youtube.com", "youtu.be", "vimeo.com"},
		VideoExtractors:      []string{"youtube", "vimeo"},
		VideoMaxMB:           100,
		VideoMaxSeconds:      600,
		VideoMaxConcurrent:   2,
		VideoDownloadSeconds: 180,
		TVPopupSecs:          10,
		AvatarStore:          "auto",
		Bell:                 true,
		Images:               true,
		ImageSixel:           "auto",
		ImageMaxMB:           5,
		ImageCacheMB:         50,
		LinkPreviews:         true,
		LinkPreviewTTLHours:  24,
		UploadMaxMB:          20,
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
		case "user_themes":
			cfg.UserThemes = parseBool(val, cfg.UserThemes)
		case "mouse":
			cfg.Mouse = parseBool(val, cfg.Mouse)
		case "resize_probe_seconds":
			cfg.ResizeProbeSeconds = atoiOr(val, cfg.ResizeProbeSeconds)
		case "tv_color":
			cfg.TVColor = val
		case "tv_audio":
			cfg.TVAudio = audioMode(val, cfg.TVAudio)
		case "audio_links":
			cfg.AudioLinks = audioMode(val, cfg.AudioLinks)
		case "audio_max_mb":
			cfg.AudioMaxMB = atoiOr(val, cfg.AudioMaxMB)
		case "video":
			if audioMode(val, "auto") == "off" {
				cfg.Video = "off"
			} else {
				cfg.Video = "auto"
			}
		case "video_ffmpeg":
			cfg.VideoFFmpeg = val
		case "video_ffprobe":
			cfg.VideoFFprobe = val
		case "video_ytdlp":
			cfg.VideoYtDlp = val
		case "video_sites":
			cfg.VideoSites = splitCSV(val)
		case "video_extractors":
			cfg.VideoExtractors = splitCSV(val)
		case "video_max_mb":
			cfg.VideoMaxMB = atoiOr(val, cfg.VideoMaxMB)
		case "video_max_seconds":
			cfg.VideoMaxSeconds = atoiOr(val, cfg.VideoMaxSeconds)
		case "video_max_concurrent":
			cfg.VideoMaxConcurrent = atoiOr(val, cfg.VideoMaxConcurrent)
		case "video_download_seconds":
			cfg.VideoDownloadSeconds = atoiOr(val, cfg.VideoDownloadSeconds)
		case "tv_popup_seconds":
			cfg.TVPopupSecs = atoiOr(val, cfg.TVPopupSecs)
		case "tv_avatars":
			cfg.TVAvatars = parseBool(val, cfg.TVAvatars)
		case "bell":
			cfg.Bell = parseBool(val, cfg.Bell)
		case "images":
			cfg.Images = parseBool(val, cfg.Images)
		case "image_sixel":
			cfg.ImageSixel = strings.ToLower(val)
		case "image_max_mb":
			cfg.ImageMaxMB = atoiOr(val, cfg.ImageMaxMB)
		case "image_cache_dir":
			cfg.ImageCacheDir = val
		case "image_cache_mb":
			cfg.ImageCacheMB = atoiOr(val, cfg.ImageCacheMB)
		case "link_previews":
			cfg.LinkPreviews = parseBool(val, cfg.LinkPreviews)
		case "link_preview_ttl_hours":
			cfg.LinkPreviewTTLHours = atoiOr(val, cfg.LinkPreviewTTLHours)
		case "nsfw_keywords":
			cfg.NSFWKeywordsSet = true
			if strings.EqualFold(val, "none") {
				cfg.NSFWKeywords = nil
			} else {
				cfg.NSFWKeywords = splitCSV(val)
			}
		case "upload_url":
			cfg.UploadURL = val
		case "upload_max_mb":
			cfg.UploadMaxMB = atoiOr(val, cfg.UploadMaxMB)
		case "avatar_store":
			if val != "" {
				cfg.AvatarStore = strings.ToLower(val)
			}
		case "synchronet_data_dir":
			cfg.SynchronetDataDir = val
		case "shared_ignore_file":
			cfg.SharedIgnoreFile = val
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("config: %v", err)
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

// audioMode reads auto|on|off (true/false count as on/off); anything else
// keeps def.
func audioMode(val, def string) string {
	switch strings.ToLower(strings.TrimSpace(val)) {
	case "auto":
		return "auto"
	case "on", "true", "yes", "1":
		return "on"
	case "off", "false", "no", "0":
		return "off"
	}
	return def
}
