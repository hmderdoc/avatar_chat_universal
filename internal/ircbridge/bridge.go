package ircbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"strings"
	"time"

	"github.com/hmderdoc/avatar_chat_universal/internal/bitmap"
	"github.com/hmderdoc/avatar_chat_universal/internal/chat"
)

const ircHostMarker = "IRC:"

type Bridge struct {
	Config *Config
	Logger *log.Logger
}

func (b *Bridge) Run(ctx context.Context) error {
	if b.Logger == nil {
		b.Logger = log.Default()
	}
	for {
		err := b.runOnce(ctx)
		if ctx.Err() != nil {
			return nil
		}
		b.Logger.Printf("bridge: disconnected: %v", err)
		delay := b.Config.Bridge.ReconnectDelay
		if delay <= 0 {
			delay = 5 * time.Second
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(delay):
		}
	}
}

func (b *Bridge) runOnce(ctx context.Context) error {
	cfg := b.Config
	chatClient := chat.NewClient(net.JoinHostPort(cfg.Chat.Host, fmt.Sprintf("%d", cfg.Chat.Port)))
	chatClient.Nick = cfg.Chat.Nick
	chatClient.System = cfg.Chat.System
	if err := chatClient.Connect(ctx); err != nil {
		return err
	}
	defer chatClient.Close()
	chatLoc := locMessages(cfg.Chat.Channel)
	if err := chatClient.Subscribe("chat", chatLoc); err != nil {
		return err
	}
	defer chatClient.Unsubscribe("chat", chatLoc)

	irc := NewIRCClient(cfg.IRC)
	if err := irc.Connect(ctx); err != nil {
		return err
	}
	defer irc.Close()

	ircEvents := make(chan IRCMessage, 128)
	errc := make(chan error, 2)
	go func() { errc <- irc.ReadLoop(ctx, ircEvents) }()
	go func() { errc <- b.forwardChat(ctx, chatClient, irc) }()

	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-errc:
			return err
		case msg, ok := <-ircEvents:
			if !ok {
				return fmt.Errorf("irc: event stream closed")
			}
			if cfg.Bridge.IRCToChat {
				if err := b.forwardIRCMessage(chatClient, msg); err != nil {
					b.Logger.Printf("bridge: irc->chat failed: %v", err)
				}
			}
		}
	}
}

func (b *Bridge) forwardIRCMessage(c *chat.Client, msg IRCMessage) error {
	if msg.Command != "PRIVMSG" || len(msg.Params) < 2 {
		return nil
	}
	target, text := msg.Params[0], msg.Params[1]
	if !strings.EqualFold(target, b.Config.IRC.Channel) {
		return nil
	}
	if strings.EqualFold(msg.Nick, b.Config.IRC.Nick) {
		return nil
	}
	if b.Config.Bridge.StripIRCFormatting {
		text = StripIRCFormatting(text)
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	var out *chat.Message
	if action, ok := parseCTCPAction(text); ok {
		out = &chat.Message{
			Nick: &chat.Nick{
				Name: msg.Nick,
				Host: ircHostMarker + b.Config.IRC.Host + "/" + b.Config.IRC.Channel,
			},
			Str:  "* " + action,
			Time: nowMs(),
		}
	} else {
		out = &chat.Message{
			Nick: &chat.Nick{
				Name: msg.Nick,
				Host: ircHostMarker + b.Config.IRC.Host + "/" + b.Config.IRC.Channel,
			},
			Str:  text,
			Time: nowMs(),
		}
	}
	if err := c.Write("chat", locMessages(b.Config.Chat.Channel), out, chat.LockWrite); err != nil {
		return err
	}
	_ = c.Push("chat", locHistory(b.Config.Chat.Channel), out, chat.LockWrite)
	return nil
}

func (b *Bridge) forwardChat(ctx context.Context, c *chat.Client, irc *IRCClient) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case pkt, ok := <-c.Updates():
			if !ok {
				return fmt.Errorf("chat: update stream closed")
			}
			if !b.Config.Bridge.ChatToIRC {
				continue
			}
			if strings.ToUpper(pkt.Oper) != "WRITE" || pkt.Location != locMessages(b.Config.Chat.Channel) {
				continue
			}
			var msg chat.Message
			if err := json.Unmarshal(pkt.Data, &msg); err != nil {
				continue
			}
			line, ok := b.chatMessageToIRC(&msg)
			if !ok {
				continue
			}
			if err := irc.Privmsg(b.Config.IRC.Channel, line); err != nil {
				return err
			}
		}
	}
}

func (b *Bridge) chatMessageToIRC(msg *chat.Message) (string, bool) {
	if msg == nil || msg.Str == "" {
		return "", false
	}
	if msg.Nick != nil && strings.HasPrefix(msg.Nick.Host, ircHostMarker) {
		return "", false
	}
	if bitmap.IsBitmap(msg.Str) {
		switch b.Config.Bridge.BitmapMode {
		case BitmapFilter:
			return "", false
		case BitmapDump:
			return msg.Str, true
		default:
			sender := nickName(msg)
			if img, err := bitmap.Parse(msg.Str); err == nil {
				return fmt.Sprintf("[image from %s: %dx%d omitted]", sender, img.Width, img.Height), true
			}
			return fmt.Sprintf("[image from %s omitted]", sender), true
		}
	}
	text := StripChatControls(msg.Str)
	if msg.Nick == nil || msg.Nick.Name == "" {
		return "* " + text, true
	}
	return fmt.Sprintf("<%s> %s", msg.Nick.Name, text), true
}

func nickName(msg *chat.Message) string {
	if msg != nil && msg.Nick != nil && msg.Nick.Name != "" {
		return msg.Nick.Name
	}
	return "unknown"
}

func locMessages(channel string) string { return "channels." + channel + ".messages" }
func locHistory(channel string) string  { return "channels." + channel + ".history" }
func nowMs() int64                      { return time.Now().UnixNano() / 1000000 }

func parseCTCPAction(text string) (string, bool) {
	if strings.HasPrefix(text, "\x01ACTION ") && strings.HasSuffix(text, "\x01") {
		return strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(text, "\x01ACTION "), "\x01")), true
	}
	return "", false
}

func StripIRCFormatting(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case 0x02, 0x0f, 0x16, 0x1d, 0x1f:
			continue
		case 0x03:
			for j := 0; j < 2 && i+1 < len(s) && isDigit(s[i+1]); j++ {
				i++
			}
			if i+1 < len(s) && s[i+1] == ',' {
				i++
				for j := 0; j < 2 && i+1 < len(s) && isDigit(s[i+1]); j++ {
					i++
				}
			}
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

func StripChatControls(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case 0x01:
			if i+1 < len(s) {
				i++
			}
		case 0x02:
			if i+1 < len(s) {
				i++
			}
		default:
			b.WriteByte(s[i])
		}
	}
	return strings.TrimSpace(b.String())
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }
