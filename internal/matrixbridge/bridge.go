package matrixbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/url"
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
	// native Matrix attachment. Left nil in tests; defaulted in Run.
	fetchMedia bridgemedia.Fetcher
}

// maxMediaUploadBytes caps re-uploaded media. Generated BBS images are small;
// this keeps an oversize linked file from buffering unboundedly.
const maxMediaUploadBytes = 10 << 20

// syncTimeoutMs is the /sync long-poll window. The HTTP call is bounded a bit
// larger than this in the client.
const syncTimeoutMs = 30000

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
		return fmt.Errorf("matrix bridge: nil config")
	}
	if b.Config.Matrix.Homeserver == "" {
		return fmt.Errorf("matrix bridge: missing homeserver")
	}
	if b.Config.Matrix.AccessToken == "" {
		return fmt.Errorf("matrix bridge: missing access token")
	}
	if b.Config.Matrix.RoomID == "" {
		return fmt.Errorf("matrix bridge: missing room_id")
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

	mx := newMXClient(b.Config.Matrix.Homeserver, b.Config.Matrix.AccessToken)
	b.Logger.Printf("bridge: syncing Matrix room=%s", b.Config.Matrix.RoomID)

	errc := make(chan error, 2)
	go func() { errc <- b.pollMatrix(runCtx, mx, chatClient) }()
	go func() { errc <- b.forwardChat(runCtx, chatClient, mx) }()

	select {
	case <-ctx.Done():
		return nil
	case err := <-errc:
		return err
	}
}

// pollMatrix long-polls /sync and forwards qualifying room messages into Avatar
// Chat. Transient errors are logged and retried; it only returns when the
// context is cancelled. The first sync (no since token) is an initial sync used
// only to obtain next_batch; its events are discarded so we don't replay room
// history. Forwarding begins on subsequent syncs.
func (b *Bridge) pollMatrix(ctx context.Context, mx *mxClient, chatClient *chat.Client) error {
	var since string
	primed := false
	delay := b.Config.Bridge.ReconnectDelay
	if delay <= 0 {
		delay = 5 * time.Second
	}
	for {
		if ctx.Err() != nil {
			return nil
		}
		events, next, err := mx.sync(ctx, since, b.Config.Matrix.RoomID, syncTimeoutMs)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			b.Logger.Printf("bridge: matrix sync failed: %v", err)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(delay):
			}
			continue
		}
		since = next
		if !primed {
			// Discard the initial sync's backlog; only forward live events.
			primed = true
			continue
		}
		for _, ev := range events {
			if !b.Config.Bridge.MatrixToChat {
				continue
			}
			if ev.Type != "m.room.message" {
				continue
			}
			// Skip our own messages to prevent echo of our own sends.
			if b.Config.Matrix.UserID != "" && ev.Sender == b.Config.Matrix.UserID {
				continue
			}
			out, ok := b.matrixMessageToChat(ctx, mx, &ev)
			if !ok {
				continue
			}
			if err := chatClient.Write("chat", locMessages(b.Config.Chat.Channel), out, chat.LockWrite); err != nil {
				b.Logger.Printf("bridge: matrix->chat write failed: %v", err)
				continue
			}
			_ = chatClient.Push("chat", locHistory(b.Config.Chat.Channel), out, chat.LockWrite)
		}
	}
}

func (b *Bridge) forwardChat(ctx context.Context, c *chat.Client, mx *mxClient) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case pkt, ok := <-c.Updates():
			if !ok {
				return fmt.Errorf("chat: update stream closed")
			}
			if !b.Config.Bridge.ChatToMatrix {
				continue
			}
			if strings.ToUpper(pkt.Oper) != "WRITE" || pkt.Location != locMessages(b.Config.Chat.Channel) {
				continue
			}
			var msg chat.Message
			if err := json.Unmarshal(pkt.Data, &msg); err != nil {
				continue
			}
			if err := b.sendChatMessage(ctx, mx, &msg); err != nil {
				b.Logger.Printf("bridge: chat->matrix send failed: %v", err)
			}
		}
	}
}

func (b *Bridge) origin() bridgecore.Origin {
	if b == nil || b.Config == nil {
		return bridgecore.Origin{}
	}
	return bridgecore.NewOrigin("matrix", homeserverHost(b.Config.Matrix.Homeserver), b.Config.Matrix.RoomID)
}

// homeserverHost extracts the host portion of the homeserver URL for the origin
// network field, e.g. "matrix.org" from "https://matrix.org".
func homeserverHost(homeserver string) string {
	homeserver = strings.TrimSpace(homeserver)
	if homeserver == "" {
		return "matrix"
	}
	if u, err := url.Parse(homeserver); err == nil && u.Host != "" {
		return u.Host
	}
	// Fall back to stripping a scheme prefix and trailing path manually.
	s := homeserver
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexByte(s, '/'); i >= 0 {
		s = s[:i]
	}
	if s == "" {
		return "matrix"
	}
	return s
}

func locMessages(channel string) string { return "channels." + channel + ".messages" }
func locHistory(channel string) string  { return "channels." + channel + ".history" }
func nowMs() int64                       { return time.Now().UnixNano() / 1000000 }
