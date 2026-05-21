package discordbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/hmderdoc/avatar_chat_universal/internal/bridgecore"
	"github.com/hmderdoc/avatar_chat_universal/internal/bridgemedia"
	"github.com/hmderdoc/avatar_chat_universal/internal/chat"
)

type Bridge struct {
	Config *Config
	Logger *log.Logger

	// fetchMedia downloads a remote media URL so it can be re-uploaded as a
	// native Discord attachment. Left nil in tests; defaulted in Run.
	fetchMedia bridgemedia.Fetcher
}

// maxMediaUploadBytes caps re-uploaded image size. 8 MiB is the Discord upload
// limit for unboosted guilds; generated BBS images are well under this.
const maxMediaUploadBytes = 8 << 20

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
		return fmt.Errorf("discord bridge: nil config")
	}
	if b.Config.Discord.Token == "" {
		return fmt.Errorf("discord bridge: missing bot token")
	}
	if b.Config.Discord.ChannelID == "" {
		return fmt.Errorf("discord bridge: missing channel_id")
	}

	chatClient := chat.NewClient(net.JoinHostPort(b.Config.Chat.Host, fmt.Sprintf("%d", b.Config.Chat.Port)))
	chatClient.Nick = b.Config.Chat.Nick
	chatClient.System = b.Config.Chat.System
	b.Logger.Printf("bridge: connecting to Avatar Chat at %s:%d channel=%s", b.Config.Chat.Host, b.Config.Chat.Port, b.Config.Chat.Channel)
	if err := chatClient.Connect(ctx); err != nil {
		return err
	}
	defer chatClient.Close()
	chatLoc := locMessages(b.Config.Chat.Channel)
	if err := chatClient.Subscribe("chat", chatLoc); err != nil {
		return err
	}
	defer chatClient.Unsubscribe("chat", chatLoc)

	discord, err := discordgo.New("Bot " + b.Config.Discord.Token)
	if err != nil {
		return err
	}
	discord.Identify.Intents = discordgo.IntentsGuilds | discordgo.IntentsGuildMessages | discordgo.IntentsMessageContent

	errc := make(chan error, 2)
	discord.AddHandler(func(s *discordgo.Session, r *discordgo.Ready) {
		user := "<unknown>"
		if r.User != nil {
			user = r.User.String()
		}
		b.Logger.Printf("bridge: discord ready as %s guilds=%d session=%s", user, len(r.Guilds), r.SessionID)
	})
	discord.AddHandler(func(s *discordgo.Session, m *discordgo.MessageCreate) {
		if !b.Config.Bridge.DiscordToChat {
			return
		}
		msg, ok := b.discordMessageToChat(m)
		if !ok {
			return
		}
		if err := chatClient.Write("chat", locMessages(b.Config.Chat.Channel), msg, chat.LockWrite); err != nil {
			b.Logger.Printf("bridge: discord->chat write failed: %v", err)
			return
		}
		_ = chatClient.Push("chat", locHistory(b.Config.Chat.Channel), msg, chat.LockWrite)
	})

	b.Logger.Printf("bridge: connecting to Discord channel_id=%s guild_id=%s", b.Config.Discord.ChannelID, b.Config.Discord.GuildID)
	if err := discord.Open(); err != nil {
		return err
	}
	defer discord.Close()
	if ch, err := discord.Channel(b.Config.Discord.ChannelID); err != nil {
		b.Logger.Printf("bridge: discord channel lookup failed for %s: %v", b.Config.Discord.ChannelID, err)
	} else {
		b.Logger.Printf("bridge: discord channel ready name=%s guild_id=%s", ch.Name, ch.GuildID)
		if b.Config.Discord.GuildID != "" && ch.GuildID != "" && ch.GuildID != b.Config.Discord.GuildID {
			b.Logger.Printf("bridge: warning: configured guild_id=%s does not match channel guild_id=%s", b.Config.Discord.GuildID, ch.GuildID)
		}
	}

	go func() { errc <- b.forwardChat(ctx, chatClient, discord) }()

	select {
	case <-ctx.Done():
		return nil
	case err := <-errc:
		return err
	}
}

func (b *Bridge) forwardChat(ctx context.Context, c *chat.Client, discord *discordgo.Session) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case pkt, ok := <-c.Updates():
			if !ok {
				return fmt.Errorf("chat: update stream closed")
			}
			if !b.Config.Bridge.ChatToDiscord {
				continue
			}
			if strings.ToUpper(pkt.Oper) != "WRITE" || pkt.Location != locMessages(b.Config.Chat.Channel) {
				continue
			}
			var msg chat.Message
			if err := json.Unmarshal(pkt.Data, &msg); err != nil {
				continue
			}
			out, ok := b.chatMessageToDiscord(&msg)
			if !ok {
				continue
			}
			if err := b.sendDiscordMessage(discord, out); err != nil {
				b.Logger.Printf("bridge: chat->discord send failed: %v", err)
			}
		}
	}
}

func (b *Bridge) sendDiscordMessage(discord *discordgo.Session, out *discordgo.MessageSend) error {
	if _, err := discord.ChannelMessageSendComplex(b.Config.Discord.ChannelID, out); err != nil {
		fallback := plainFallback(out)
		if fallback == "" {
			return err
		}
		if _, retryErr := discord.ChannelMessageSendComplex(b.Config.Discord.ChannelID, &discordgo.MessageSend{
			Content:         fallback,
			AllowedMentions: &discordgo.MessageAllowedMentions{Parse: []discordgo.AllowedMentionType{}},
		}); retryErr != nil {
			return fmt.Errorf("%v; fallback failed: %v", err, retryErr)
		}
		b.Logger.Printf("bridge: chat->discord rich send failed, sent plain fallback: %v", err)
	}
	return nil
}

func plainFallback(out *discordgo.MessageSend) string {
	if out == nil {
		return ""
	}
	if strings.TrimSpace(out.Content) != "" {
		return strings.TrimSpace(out.Content)
	}
	var parts []string
	for _, embed := range out.Embeds {
		if embed == nil {
			continue
		}
		if embed.Author != nil && strings.TrimSpace(embed.Author.Name) != "" {
			parts = append(parts, "**"+embed.Author.Name+"**")
		}
		if strings.TrimSpace(embed.Description) != "" {
			parts = append(parts, strings.TrimSpace(embed.Description))
		}
		if embed.Image != nil && strings.TrimSpace(embed.Image.URL) != "" && !strings.HasPrefix(embed.Image.URL, "attachment://") {
			parts = append(parts, strings.TrimSpace(embed.Image.URL))
		}
		if strings.TrimSpace(embed.URL) != "" {
			parts = append(parts, strings.TrimSpace(embed.URL))
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func (b *Bridge) origin() bridgecore.Origin {
	if b == nil || b.Config == nil {
		return bridgecore.Origin{}
	}
	network := b.Config.Discord.GuildID
	if network == "" {
		network = "discord"
	}
	return bridgecore.NewOrigin("discord", network, b.Config.Discord.ChannelID)
}

func locMessages(channel string) string { return "channels." + channel + ".messages" }
func locHistory(channel string) string  { return "channels." + channel + ".history" }
func nowMs() int64                      { return time.Now().UnixNano() / 1000000 }
