package telegrambridge

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"strings"
	"time"

	"github.com/hmderdoc/avatar_chat_universal/internal/bridgecore"
	"github.com/hmderdoc/avatar_chat_universal/internal/bridgemedia"
	"github.com/hmderdoc/avatar_chat_universal/internal/chat"
)

type Bridge struct {
	Config *Config
	Logger *log.Logger

	// fetchMedia downloads a remote media URL so it can be re-uploaded as a
	// native Telegram attachment. Left nil in tests; defaulted in Run.
	fetchMedia bridgemedia.Fetcher
}

// maxMediaUploadBytes caps re-uploaded media. Telegram's bot photo limit is
// ~10 MiB; generated BBS images are far smaller.
const maxMediaUploadBytes = 10 << 20

func (b *Bridge) Run(ctx context.Context) error {
	if b.Logger == nil {
		b.Logger = log.Default()
	}
	if b.fetchMedia == nil {
		b.fetchMedia = bridgemedia.NewHTTPFetcher(maxMediaUploadBytes)
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
	if b.Config == nil {
		return fmt.Errorf("telegram bridge: nil config")
	}
	if b.Config.Telegram.Token == "" {
		return fmt.Errorf("telegram bridge: missing bot token")
	}
	if b.Config.Telegram.ChatID == "" {
		return fmt.Errorf("telegram bridge: missing chat_id")
	}

	// Bound both pumps to this attempt: when one returns, cancel the other so
	// the long-poll goroutine doesn't leak across reconnects.
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	chatClient := chat.NewClient(net.JoinHostPort(b.Config.Chat.Host, fmt.Sprintf("%d", b.Config.Chat.Port)))
	chatClient.Nick = b.Config.Chat.Nick
	chatClient.System = b.Config.Chat.System
	b.Logger.Printf("bridge: connecting to Avatar Chat at %s:%d channel=%s", b.Config.Chat.Host, b.Config.Chat.Port, b.Config.Chat.Channel)
	if err := chatClient.Connect(runCtx); err != nil {
		return err
	}
	defer chatClient.Close()
	chatLoc := locMessages(b.Config.Chat.Channel)
	if err := chatClient.Subscribe("chat", chatLoc); err != nil {
		return err
	}
	defer chatClient.Unsubscribe("chat", chatLoc)

	tg := newTGClient(b.Config.Telegram.Token)
	b.Logger.Printf("bridge: polling Telegram chat_id=%s", b.Config.Telegram.ChatID)

	errc := make(chan error, 2)
	go func() { errc <- b.pollTelegram(runCtx, tg, chatClient) }()
	go func() { errc <- b.forwardChat(runCtx, chatClient, tg) }()

	select {
	case <-ctx.Done():
		return nil
	case err := <-errc:
		return err
	}
}

// pollTelegram long-polls getUpdates and forwards qualifying messages into
// Avatar Chat. Transient errors are logged and retried; it only returns when
// the context is cancelled.
func (b *Bridge) pollTelegram(ctx context.Context, tg *tgClient, chatClient *chat.Client) error {
	var offset int64
	delay := b.Config.Bridge.ReconnectDelay
	if delay <= 0 {
		delay = 5 * time.Second
	}
	for {
		if ctx.Err() != nil {
			return nil
		}
		updates, err := tg.getUpdates(ctx, offset, b.Config.Telegram.PollTimeout)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			b.Logger.Printf("bridge: telegram getUpdates failed: %v", err)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(delay):
			}
			continue
		}
		for _, u := range updates {
			if u.UpdateID >= offset {
				offset = u.UpdateID + 1
			}
			if !b.Config.Bridge.TelegramToChat {
				continue
			}
			msg := u.effective()
			if msg == nil || !matchChat(b.Config.Telegram.ChatID, msg.Chat) {
				continue
			}
			out, ok := b.telegramMessageToChat(msg)
			if !ok {
				continue
			}
			if err := chatClient.Write("chat", locMessages(b.Config.Chat.Channel), out, chat.LockWrite); err != nil {
				b.Logger.Printf("bridge: telegram->chat write failed: %v", err)
				continue
			}
			_ = chatClient.Push("chat", locHistory(b.Config.Chat.Channel), out, chat.LockWrite)
		}
	}
}

func (b *Bridge) forwardChat(ctx context.Context, c *chat.Client, tg *tgClient) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case pkt, ok := <-c.Updates():
			if !ok {
				return fmt.Errorf("chat: update stream closed")
			}
			if !b.Config.Bridge.ChatToTelegram {
				continue
			}
			if strings.ToUpper(pkt.Oper) != "WRITE" || pkt.Location != locMessages(b.Config.Chat.Channel) {
				continue
			}
			var msg chat.Message
			if err := json.Unmarshal(pkt.Data, &msg); err != nil {
				continue
			}
			if err := b.sendChatMessage(ctx, tg, &msg); err != nil {
				b.Logger.Printf("bridge: chat->telegram send failed: %v", err)
			}
		}
	}
}

func (b *Bridge) origin() bridgecore.Origin {
	if b == nil || b.Config == nil {
		return bridgecore.Origin{}
	}
	return bridgecore.NewOrigin("telegram", "telegram", b.Config.Telegram.ChatID)
}

func locMessages(channel string) string { return "channels." + channel + ".messages" }
func locHistory(channel string) string  { return "channels." + channel + ".history" }
func nowMs() int64                      { return time.Now().UnixNano() / 1000000 }
