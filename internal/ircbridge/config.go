package ircbridge

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type BitmapMode string

const (
	BitmapFilter   BitmapMode = "filter"
	BitmapAnnounce BitmapMode = "announce"
	BitmapDump     BitmapMode = "dump"
)

type Config struct {
	IRC    IRCConfig
	Chat   ChatConfig
	Bridge BridgeConfig
}

type IRCConfig struct {
	Host             string
	Port             int
	TLS              bool
	Password         string
	Nick             string
	Username         string
	Realname         string
	Channel          string
	NickServPassword string
}

type ChatConfig struct {
	Host    string
	Port    int
	Nick    string
	System  string
	Channel string
}

type BridgeConfig struct {
	IRCToChat          bool
	ChatToIRC          bool
	StripIRCFormatting bool
	BitmapMode         BitmapMode
	ReconnectDelay     time.Duration
}

func DefaultConfig() *Config {
	return &Config{
		IRC: IRCConfig{
			Host:     "irc.libera.chat",
			Port:     6697,
			TLS:      true,
			Nick:     "AvatarBridge",
			Username: "avatarbridge",
			Realname: "Avatar Chat IRC Bridge",
			Channel:  "#avatar-chat",
		},
		Chat: ChatConfig{
			Host:    "futureland.today",
			Port:    10088,
			Nick:    "IRC",
			System:  "IRC Bridge",
			Channel: "main",
		},
		Bridge: BridgeConfig{
			IRCToChat:          true,
			ChatToIRC:          true,
			StripIRCFormatting: true,
			BitmapMode:         BitmapAnnounce,
			ReconnectDelay:     5 * time.Second,
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
		return nil, fmt.Errorf("bridge config: %v", err)
	}
	return cfg, nil
}

func setConfigValue(cfg *Config, section, key, val string) {
	switch section {
	case "irc":
		switch key {
		case "host":
			cfg.IRC.Host = val
		case "port":
			cfg.IRC.Port = atoiOr(val, cfg.IRC.Port)
		case "tls":
			cfg.IRC.TLS = boolOr(val, cfg.IRC.TLS)
		case "password":
			cfg.IRC.Password = val
		case "nick":
			cfg.IRC.Nick = val
		case "username":
			cfg.IRC.Username = val
		case "realname":
			cfg.IRC.Realname = val
		case "channel":
			cfg.IRC.Channel = val
		case "nickserv_password":
			cfg.IRC.NickServPassword = val
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
		case "irc_to_chat":
			cfg.Bridge.IRCToChat = boolOr(val, cfg.Bridge.IRCToChat)
		case "chat_to_irc":
			cfg.Bridge.ChatToIRC = boolOr(val, cfg.Bridge.ChatToIRC)
		case "strip_irc_formatting":
			cfg.Bridge.StripIRCFormatting = boolOr(val, cfg.Bridge.StripIRCFormatting)
		case "bitmap_mode":
			cfg.Bridge.BitmapMode = parseBitmapMode(val, cfg.Bridge.BitmapMode)
		case "reconnect_delay_seconds":
			cfg.Bridge.ReconnectDelay = time.Duration(atoiOr(val, int(cfg.Bridge.ReconnectDelay/time.Second))) * time.Second
		}
	}
}

func parseBitmapMode(s string, def BitmapMode) BitmapMode {
	switch BitmapMode(strings.ToLower(strings.TrimSpace(s))) {
	case BitmapFilter, BitmapAnnounce, BitmapDump:
		return BitmapMode(strings.ToLower(strings.TrimSpace(s)))
	default:
		return def
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
