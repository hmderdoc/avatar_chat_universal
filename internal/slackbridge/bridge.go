package slackbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/hmderdoc/avatar_chat_universal/internal/bridgecore"
	"github.com/hmderdoc/avatar_chat_universal/internal/bridgemedia"
	"github.com/hmderdoc/avatar_chat_universal/internal/chat"
)

type Bridge struct {
	Config *Config
	Logger *log.Logger

	// fetchMedia downloads a remote media URL so it can be re-uploaded as a
	// native Slack attachment. Left nil in tests; defaulted in Run.
	fetchMedia bridgemedia.Fetcher

	// team is the resolved Slack workspace id used as the origin network. It is
	// learned from the first inbound event's team_id; until then origin() uses
	// "slack". Both the marker we emit and the one we dedup against come from
	// origin(), so a late update only affects messages seen after it. It is read
	// by forwardChat and written by pollSlack, so access is guarded.
	teamMu sync.Mutex
	team   string
}

// maxMediaUploadBytes caps re-uploaded media. Generated BBS images are far
// smaller; this is a sanity ceiling.
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
		return fmt.Errorf("slack bridge: nil config")
	}
	if b.Config.Slack.AppToken == "" {
		return fmt.Errorf("slack bridge: missing app token")
	}
	if b.Config.Slack.BotToken == "" {
		return fmt.Errorf("slack bridge: missing bot token")
	}
	if b.Config.Slack.ChannelID == "" {
		return fmt.Errorf("slack bridge: missing channel_id")
	}

	// Bound both pumps to this attempt: when one returns, cancel the other so
	// the socket-read goroutine doesn't leak across reconnects.
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

	sc := newSlackClient(b.Config.Slack.AppToken, b.Config.Slack.BotToken)
	if err := sc.open(runCtx); err != nil {
		return err
	}
	defer sc.close()
	b.Logger.Printf("bridge: connected to Slack socket, channel_id=%s", b.Config.Slack.ChannelID)

	errc := make(chan error, 2)
	go func() { errc <- b.pollSlack(runCtx, sc, chatClient) }()
	go func() { errc <- b.forwardChat(runCtx, chatClient, sc) }()

	select {
	case <-ctx.Done():
		return nil
	case err := <-errc:
		return err
	}
}

// pollSlack reads Socket Mode envelopes, acks them, and forwards qualifying
// message events into Avatar Chat. It returns an error (closed socket or a
// Slack "disconnect" envelope) to trigger the outer reconnect loop.
func (b *Bridge) pollSlack(ctx context.Context, sc *slackClient, chatClient *chat.Client) error {
	for {
		if ctx.Err() != nil {
			return nil
		}
		env, err := sc.readEnvelope()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("slack: read socket: %v", err)
		}

		switch env.Type {
		case "hello":
			continue
		case "disconnect":
			// Slack is about to drop the socket; return so the outer loop
			// re-opens a fresh connection.
			return fmt.Errorf("slack: socket disconnect (reason=%s)", env.Reason)
		default:
			// Ack any envelope that carries an id (events_api, slash_commands,
			// interactive) so Slack doesn't drop us.
			if env.EnvelopeID != "" {
				if err := sc.ack(env.EnvelopeID); err != nil {
					b.Logger.Printf("bridge: slack ack failed: %v", err)
				}
			}
		}

		if env.Type != "events_api" || len(env.Payload) == 0 {
			continue
		}
		if !b.Config.Bridge.SlackToChat {
			continue
		}
		var payload slackEventPayload
		if err := json.Unmarshal(env.Payload, &payload); err != nil {
			continue
		}
		ev := payload.Event
		if ev.Channel != b.Config.Slack.ChannelID {
			continue
		}
		out, ok := b.slackMessageToChat(ctx, sc, &payload)
		if !ok {
			continue
		}
		if err := chatClient.Write("chat", locMessages(b.Config.Chat.Channel), out, chat.LockWrite); err != nil {
			b.Logger.Printf("bridge: slack->chat write failed: %v", err)
			continue
		}
		_ = chatClient.Push("chat", locHistory(b.Config.Chat.Channel), out, chat.LockWrite)
	}
}

func (b *Bridge) forwardChat(ctx context.Context, c *chat.Client, sc *slackClient) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case pkt, ok := <-c.Updates():
			if !ok {
				return fmt.Errorf("chat: update stream closed")
			}
			if !b.Config.Bridge.ChatToSlack {
				continue
			}
			if strings.ToUpper(pkt.Oper) != "WRITE" || pkt.Location != locMessages(b.Config.Chat.Channel) {
				continue
			}
			var msg chat.Message
			if err := json.Unmarshal(pkt.Data, &msg); err != nil {
				continue
			}
			if err := b.sendChatMessage(ctx, sc, &msg); err != nil {
				b.Logger.Printf("bridge: chat->slack send failed: %v", err)
			}
		}
	}
}

func (b *Bridge) origin() bridgecore.Origin {
	if b == nil || b.Config == nil {
		return bridgecore.Origin{}
	}
	return bridgecore.NewOrigin("slack", b.teamID(), b.Config.Slack.ChannelID)
}

// teamID picks the workspace id for the origin, falling back to "slack".
func (b *Bridge) teamID() string {
	if b != nil {
		b.teamMu.Lock()
		t := strings.TrimSpace(b.team)
		b.teamMu.Unlock()
		if t != "" {
			return t
		}
	}
	return "slack"
}

// setTeam records the workspace id learned from an inbound event.
func (b *Bridge) setTeam(id string) {
	id = strings.TrimSpace(id)
	if id == "" {
		return
	}
	b.teamMu.Lock()
	b.team = id
	b.teamMu.Unlock()
}

func locMessages(channel string) string { return "channels." + channel + ".messages" }
func locHistory(channel string) string  { return "channels." + channel + ".history" }
func nowMs() int64                      { return time.Now().UnixNano() / 1000000 }
