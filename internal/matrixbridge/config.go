package matrixbridge

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Matrix MatrixConfig
	Chat   ChatConfig
	Bridge BridgeConfig
}

type MatrixConfig struct {
	// Homeserver base URL, e.g. https://matrix.org.
	Homeserver string
	// AccessToken is the bot's bearer token. The MATRIX_ACCESS_TOKEN env var
	// wins over a value written in the ini, so secrets can live outside config.
	AccessToken string
	// UserID is the bot's own mxid (@bot:server). Used to skip its own
	// messages so we don't echo our sends back into chat.
	UserID string
	// RoomID is the internal room id of the bound room, e.g. !abc:matrix.org.
	RoomID string
}

type ChatConfig struct {
	Host    string
	Port    int
	Nick    string
	System  string
	Channel string
}

type BridgeConfig struct {
	MatrixToChat         bool
	ChatToMatrix         bool
	IncludeAvatarImage   bool
	IncludeBitmapImage   bool
	IncludeMedia         bool
	IncludeAttachmentURL bool
	PublicBaseURL        string
	ChatToMatrixIgnore   []string
	ReconnectDelay       time.Duration
}

func DefaultConfig() *Config {
	return &Config{
		Matrix: MatrixConfig{
			AccessToken: os.Getenv("MATRIX_ACCESS_TOKEN"),
		},
		Chat: ChatConfig{
			Host:    "futureland.today",
			Port:    10088,
			Nick:    "Matrix",
			System:  "Matrix Bridge",
			Channel: "main",
		},
		Bridge: BridgeConfig{
			MatrixToChat:         true,
			ChatToMatrix:         true,
			IncludeAvatarImage:   false,
			IncludeBitmapImage:   true,
			IncludeMedia:         true,
			IncludeAttachmentURL: true,
			PublicBaseURL:        "",
			ReconnectDelay:       5 * time.Second,
		},
	}
}

func LoadConfig(path string) (*Config, error) {
	cfg := DefaultConfig()
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, err
	}
	defer f.Close()

	section := ""
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.Contains(line, "]") {
			section = strings.ToLower(strings.TrimSpace(line[1:strings.IndexByte(line, ']')]))
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(line[:eq]))
		val := strings.TrimSpace(line[eq+1:])
		setConfigValue(cfg, section, key, val)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("matrix bridge config: %v", err)
	}
	// The environment (including values loaded from .env) wins over a token
	// written in the ini, so secrets can live outside the committed config.
	if t := strings.TrimSpace(os.Getenv("MATRIX_ACCESS_TOKEN")); t != "" {
		cfg.Matrix.AccessToken = t
	}
	return cfg, nil
}

func setConfigValue(cfg *Config, section, key, val string) {
	switch section {
	case "matrix":
		switch key {
		case "homeserver":
			cfg.Matrix.Homeserver = strings.TrimRight(val, "/")
		case "access_token":
			cfg.Matrix.AccessToken = val
		case "user_id":
			cfg.Matrix.UserID = val
		case "room_id":
			cfg.Matrix.RoomID = val
		}
	case "chat", "json_chat", "json-chat":
		switch key {
		case "host":
			cfg.Chat.Host = val
		case "port":
			cfg.Chat.Port = atoiOr(val, cfg.Chat.Port)
		case "nick":
			cfg.Chat.Nick = val
		case "system":
			cfg.Chat.System = val
		case "channel":
			cfg.Chat.Channel = val
		}
	case "bridge":
		switch key {
		case "matrix_to_chat":
			cfg.Bridge.MatrixToChat = boolOr(val, cfg.Bridge.MatrixToChat)
		case "chat_to_matrix":
			cfg.Bridge.ChatToMatrix = boolOr(val, cfg.Bridge.ChatToMatrix)
		case "include_avatar_image":
			cfg.Bridge.IncludeAvatarImage = boolOr(val, cfg.Bridge.IncludeAvatarImage)
		case "include_bitmap_image":
			cfg.Bridge.IncludeBitmapImage = boolOr(val, cfg.Bridge.IncludeBitmapImage)
		case "include_media":
			cfg.Bridge.IncludeMedia = boolOr(val, cfg.Bridge.IncludeMedia)
		case "include_attachment_urls":
			cfg.Bridge.IncludeAttachmentURL = boolOr(val, cfg.Bridge.IncludeAttachmentURL)
		case "public_base_url":
			cfg.Bridge.PublicBaseURL = strings.TrimRight(val, "/")
		case "reconnect_delay_seconds":
			cfg.Bridge.ReconnectDelay = time.Duration(atoiOr(val, int(cfg.Bridge.ReconnectDelay/time.Second))) * time.Second
		}
	case "filter", "filters":
		switch key {
		case "chat_to_matrix_ignore", "chat_to_matrix_ignore_regex":
			cfg.Bridge.ChatToMatrixIgnore = append(cfg.Bridge.ChatToMatrixIgnore, val)
		}
	}
}

func atoiOr(s string, def int) int {
	if v, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
		return v
	}
	return def
}

func boolOr(s string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return def
	}
}
