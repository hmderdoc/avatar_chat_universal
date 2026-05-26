package slackbridge

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Slack  SlackConfig
	Chat   ChatConfig
	Bridge BridgeConfig
}

type SlackConfig struct {
	// AppToken is the app-level token (xapp-...) used to open the Socket Mode
	// websocket. Env SLACK_APP_TOKEN wins over the ini value.
	AppToken string
	// BotToken is the bot token (xoxb-...) used for Web API calls. Env
	// SLACK_BOT_TOKEN wins over the ini value.
	BotToken string
	// ChannelID is the target channel (e.g. C0123ABCD).
	ChannelID string
}

type ChatConfig struct {
	Host    string
	Port    int
	Nick    string
	System  string
	Channel string
}

type BridgeConfig struct {
	SlackToChat          bool
	ChatToSlack          bool
	IncludeAvatarImage   bool
	IncludeBitmapImage   bool
	IncludeMedia         bool
	IncludeAttachmentURL bool
	PublicBaseURL        string
	ChatToSlackIgnore    []string
	ReconnectDelay       time.Duration
}

func DefaultConfig() *Config {
	return &Config{
		Slack: SlackConfig{
			AppToken: os.Getenv("SLACK_APP_TOKEN"),
			BotToken: os.Getenv("SLACK_BOT_TOKEN"),
		},
		Chat: ChatConfig{
			Host:    "futureland.today",
			Port:    10088,
			Nick:    "Slack",
			System:  "Slack Bridge",
			Channel: "main",
		},
		Bridge: BridgeConfig{
			SlackToChat:          true,
			ChatToSlack:          true,
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
		return nil, fmt.Errorf("slack bridge config: %v", err)
	}
	// The environment (including values loaded from .env) wins over tokens
	// written in the ini, so secrets can live outside the committed config.
	if t := strings.TrimSpace(os.Getenv("SLACK_APP_TOKEN")); t != "" {
		cfg.Slack.AppToken = t
	}
	if t := strings.TrimSpace(os.Getenv("SLACK_BOT_TOKEN")); t != "" {
		cfg.Slack.BotToken = t
	}
	return cfg, nil
}

func setConfigValue(cfg *Config, section, key, val string) {
	switch section {
	case "slack":
		switch key {
		case "app_token":
			cfg.Slack.AppToken = val
		case "bot_token":
			cfg.Slack.BotToken = val
		case "channel_id":
			cfg.Slack.ChannelID = val
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
		case "slack_to_chat":
			cfg.Bridge.SlackToChat = boolOr(val, cfg.Bridge.SlackToChat)
		case "chat_to_slack":
			cfg.Bridge.ChatToSlack = boolOr(val, cfg.Bridge.ChatToSlack)
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
		case "chat_to_slack_ignore", "chat_to_slack_ignore_regex":
			cfg.Bridge.ChatToSlackIgnore = append(cfg.Bridge.ChatToSlackIgnore, val)
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
