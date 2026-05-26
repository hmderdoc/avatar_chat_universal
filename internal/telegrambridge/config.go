package telegrambridge

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Telegram TelegramConfig
	Chat     ChatConfig
	Bridge   BridgeConfig
}

type TelegramConfig struct {
	Token string
	// ChatID is the target group/channel. Accepts a numeric id
	// (e.g. -1001234567890) or an @channelusername.
	ChatID string
	// PollTimeout is the getUpdates long-poll window in seconds.
	PollTimeout int
}

type ChatConfig struct {
	Host    string
	Port    int
	Nick    string
	System  string
	Channel string
}

type BridgeConfig struct {
	TelegramToChat       bool
	ChatToTelegram       bool
	IncludeAvatarImage   bool
	IncludeBitmapImage   bool
	IncludeMedia         bool
	IncludeAttachmentURL bool
	PublicBaseURL        string
	ChatToTelegramIgnore []string
	ReconnectDelay       time.Duration
}

func DefaultConfig() *Config {
	return &Config{
		Telegram: TelegramConfig{
			Token:       os.Getenv("TELEGRAM_BOT_TOKEN"),
			PollTimeout: 30,
		},
		Chat: ChatConfig{
			Host:    "futureland.today",
			Port:    10088,
			Nick:    "Telegram",
			System:  "Telegram Bridge",
			Channel: "main",
		},
		Bridge: BridgeConfig{
			TelegramToChat:       true,
			ChatToTelegram:       true,
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
		return nil, fmt.Errorf("telegram bridge config: %v", err)
	}
	// The environment (including values loaded from .env) wins over a token
	// written in the ini, so secrets can live outside the committed config.
	if t := strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN")); t != "" {
		cfg.Telegram.Token = t
	}
	return cfg, nil
}

func setConfigValue(cfg *Config, section, key, val string) {
	switch section {
	case "telegram":
		switch key {
		case "token":
			cfg.Telegram.Token = val
		case "chat_id":
			cfg.Telegram.ChatID = val
		case "poll_timeout_seconds":
			cfg.Telegram.PollTimeout = atoiOr(val, cfg.Telegram.PollTimeout)
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
		case "telegram_to_chat":
			cfg.Bridge.TelegramToChat = boolOr(val, cfg.Bridge.TelegramToChat)
		case "chat_to_telegram":
			cfg.Bridge.ChatToTelegram = boolOr(val, cfg.Bridge.ChatToTelegram)
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
		case "chat_to_telegram_ignore", "chat_to_telegram_ignore_regex":
			cfg.Bridge.ChatToTelegramIgnore = append(cfg.Bridge.ChatToTelegramIgnore, val)
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
