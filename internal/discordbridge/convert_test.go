package discordbridge

import (
	"fmt"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/hmderdoc/avatar_chat_universal/internal/bridgecore"
	"github.com/hmderdoc/avatar_chat_universal/internal/chat"
)

func TestChatMessageToDiscordSuppressesOwnOrigin(t *testing.T) {
	b := testBridge()
	msg := &chat.Message{
		Nick: &chat.Nick{Name: "alice", Host: bridgecore.FormatHostMarker(b.origin())},
		Str:  "hello",
	}
	if _, ok := b.chatMessageToDiscord(msg); ok {
		t.Fatal("own Discord origin should be suppressed")
	}
}

func TestChatMessageToDiscordForwardsOtherBridgeOrigin(t *testing.T) {
	b := testBridge()
	other := bridgecore.NewOrigin("irc", "irc.libera.chat", "#avatar")
	msg := &chat.Message{
		Nick: &chat.Nick{Name: "alice", Host: bridgecore.FormatHostMarker(other)},
		Str:  "hello *world*",
	}
	out, ok := b.chatMessageToDiscord(msg)
	if !ok {
		t.Fatal("other bridge origin should be forwarded")
	}
	if len(out.Embeds) == 0 || out.Embeds[0].Author == nil || !strings.Contains(out.Embeds[0].Author.Name, "alice -- IRC:irc.libera.chat/#avatar") {
		t.Fatalf("embed missing origin: %#v", out.Embeds)
	}
	if len(out.Embeds) == 0 || !strings.Contains(out.Embeds[0].Description, "*world*") {
		t.Fatalf("embed should preserve Discord markdown: %#v", out.Embeds)
	}
}

func TestDiscordMessageToChatIncludesAttachmentURLs(t *testing.T) {
	b := testBridge()
	msg, ok := b.discordMessageToChat(&discordgo.MessageCreate{
		Message: &discordgo.Message{
			ChannelID: "123",
			Content:   "hello",
			Author:    &discordgo.User{Username: "alice"},
			Attachments: []*discordgo.MessageAttachment{
				{Filename: "photo.png", URL: "https://cdn.example/photo.png"},
			},
		},
	})
	if !ok {
		t.Fatal("discord message should convert")
	}
	if !strings.Contains(msg.Str, "photo.png: https://cdn.example/photo.png") {
		t.Fatalf("missing attachment URL: %q", msg.Str)
	}
	if msg.Nick == nil || msg.Nick.Host != bridgecore.FormatHostMarker(b.origin()) {
		t.Fatalf("bad origin marker: %#v", msg.Nick)
	}
}

func TestChatMessageToDiscordEmbedsImageURL(t *testing.T) {
	b := testBridge()
	msg := &chat.Message{
		Nick: &chat.Nick{Name: "cinder", Host: "riot-grrrl.zine"},
		Str:  "Image complete! ![Cinder portrait](https://example.com/cinder.png) -- uploaded",
	}
	out, ok := b.chatMessageToDiscord(msg)
	if !ok {
		t.Fatal("message should convert")
	}
	if !hasEmbedImage(out, "https://example.com/cinder.png") {
		t.Fatalf("missing image embed: %#v", out.Embeds)
	}
	if strings.Contains(out.Embeds[0].Description, "https://example.com/cinder.png") {
		t.Fatalf("image URL should not be left in main description: %q", out.Embeds[0].Description)
	}
}

func TestChatMessageToDiscordShowsPlainHostOrigin(t *testing.T) {
	b := testBridge()
	msg := &chat.Message{
		Nick: &chat.Nick{Name: "iNK$tAiN", Host: "xerox-jan.zine"},
		Str:  "working on it",
	}
	out, ok := b.chatMessageToDiscord(msg)
	if !ok {
		t.Fatal("message should convert")
	}
	if len(out.Embeds) == 0 || out.Embeds[0].Author == nil || out.Embeds[0].Author.Name != "iNK$tAiN -- xerox-jan.zine" {
		t.Fatalf("bad author: %#v", out.Embeds)
	}
}

func TestChatMessageToDiscordEmbedsRelativeMediaPath(t *testing.T) {
	b := testBridge()
	b.Config.Bridge.PublicBaseURL = "https://futureland.today"
	msg := &chat.Message{
		Nick: &chat.Nick{Name: "cinder", Host: "riot-grrrl.zine"},
		Str:  "Image complete! ./api/files.ssjs?call=download-file&dir=originalcontent_imgs&file=portrait.png",
	}
	out, ok := b.chatMessageToDiscord(msg)
	if !ok {
		t.Fatal("message should convert")
	}
	want := "https://futureland.today/api/files.ssjs?call=download-file&dir=originalcontent_imgs&file=portrait.png"
	if !hasEmbedImage(out, want) {
		t.Fatalf("missing image embed: %#v", out.Embeds)
	}
}

func TestChatMessageToDiscordFilterSuppressesMatch(t *testing.T) {
	b := testBridge()
	b.Config.Bridge.ChatToDiscordIgnore = []string{`(?i)^BLOCKBRAIN:`}
	msg := &chat.Message{
		Nick: &chat.Nick{Name: "BLOCKBRAIN", Host: "system"},
		Str:  "sent an image",
	}
	if _, ok := b.chatMessageToDiscord(msg); ok {
		t.Fatal("filter should suppress matching message")
	}
}

func TestChatMessageToDiscordReuploadsImageBytes(t *testing.T) {
	b := testBridge()
	pngBytes := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0}
	var fetched string
	b.fetchMedia = func(rawURL string) ([]byte, error) {
		fetched = rawURL
		return pngBytes, nil
	}
	msg := &chat.Message{
		Nick: &chat.Nick{Name: "cinder", Host: "riot-grrrl.zine"},
		Str:  "Image complete! ![Cinder portrait](https://example.com/cinder.png) -- uploaded",
	}
	out, ok := b.chatMessageToDiscord(msg)
	if !ok {
		t.Fatal("message should convert")
	}
	if fetched != "https://example.com/cinder.png" {
		t.Fatalf("fetchMedia called with %q", fetched)
	}
	if !hasEmbedImage(out, "attachment://media-0.png") {
		t.Fatalf("image embed should reference the re-uploaded attachment: %#v", out.Embeds)
	}
	if len(out.Files) != 1 || out.Files[0].Name != "media-0.png" {
		t.Fatalf("expected one re-uploaded file media-0.png: %#v", out.Files)
	}
}

func TestChatMessageToDiscordFallsBackToURLWhenFetchFails(t *testing.T) {
	b := testBridge()
	b.fetchMedia = func(string) ([]byte, error) { return nil, fmt.Errorf("boom") }
	msg := &chat.Message{
		Nick: &chat.Nick{Name: "cinder", Host: "riot-grrrl.zine"},
		Str:  "![Cinder portrait](https://example.com/cinder.png)",
	}
	out, ok := b.chatMessageToDiscord(msg)
	if !ok {
		t.Fatal("message should convert")
	}
	if !hasEmbedImage(out, "https://example.com/cinder.png") {
		t.Fatalf("should fall back to remote URL embed: %#v", out.Embeds)
	}
	if len(out.Files) != 0 {
		t.Fatalf("no file should be attached on fetch failure: %#v", out.Files)
	}
}

func TestChatMessageToDiscordReuploadsAudioAsAttachment(t *testing.T) {
	b := testBridge()
	var fetched string
	b.fetchMedia = func(rawURL string) ([]byte, error) {
		fetched = rawURL
		return []byte("ID3fake-mp3-bytes"), nil
	}
	msg := &chat.Message{
		Nick: &chat.Nick{Name: "dj", Host: "riot-grrrl.zine"},
		Str:  "new track ![Demo](https://example.com/track.mp3)",
	}
	out, ok := b.chatMessageToDiscord(msg)
	if !ok {
		t.Fatal("message should convert")
	}
	if fetched != "https://example.com/track.mp3" {
		t.Fatalf("fetchMedia called with %q", fetched)
	}
	// No usable ID3 -> filename derived from the URL basename, not media-N.
	if len(out.Files) != 1 || out.Files[0].Name != "track.mp3" {
		t.Fatalf("expected standalone track.mp3 attachment: %#v", out.Files)
	}
	if out.Files[0].ContentType != "audio/mpeg" {
		t.Fatalf("expected audio/mpeg content type, got %q", out.Files[0].ContentType)
	}
	// Audio must NOT be referenced inside an embed image when there's no art.
	for _, e := range out.Embeds {
		if e != nil && e.Image != nil {
			t.Fatalf("audio without art should not produce an embed image: %#v", e)
		}
	}
}

func TestChatMessageToDiscordRichAudioCard(t *testing.T) {
	b := testBridge()
	png := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0}
	mp3 := buildMP3(map[string][]byte{
		"TIT2": append([]byte{0x00}, []byte("Fast Fart Philosopher")...),
		"TPE1": append([]byte{0x00}, []byte("mrodroid")...),
		"APIC": buildAPIC(png),
	})
	b.fetchMedia = func(string) ([]byte, error) { return mp3, nil }
	msg := &chat.Message{
		Nick: &chat.Nick{Name: "Hm Derdoc", Host: "futureland.today"},
		Str:  "https://futureland.today/api/files.ssjs?call=download-file&dir=originalcontent_mp3s&file=Fast_Fart_Philosopher_mrodroid.mp3",
	}
	out, ok := b.chatMessageToDiscord(msg)
	if !ok {
		t.Fatal("message should convert")
	}
	if len(out.Files) != 2 {
		t.Fatalf("expected mp3 + cover-art attachments, got %d: %#v", len(out.Files), out.Files)
	}
	if out.Files[0].Name != "mrodroid - Fast Fart Philosopher.mp3" {
		t.Fatalf("mp3 should be named from ID3 metadata, got %q", out.Files[0].Name)
	}
	if out.Files[1].Name != "media-1.png" {
		t.Fatalf("cover art name = %q", out.Files[1].Name)
	}
	if len(out.Embeds) != 1 {
		t.Fatalf("expected one consolidated embed, got %d: %#v", len(out.Embeds), out.Embeds)
	}
	e := out.Embeds[0]
	if e.Author == nil || !strings.Contains(e.Author.Name, "Hm Derdoc") {
		t.Fatalf("consolidated embed should carry the author header: %#v", e)
	}
	if e.Title != "Fast Fart Philosopher" {
		t.Fatalf("title should be the song title, got %q", e.Title)
	}
	if !strings.Contains(e.Description, "mrodroid") {
		t.Fatalf("description should include the artist, got %q", e.Description)
	}
	if e.Image == nil || e.Image.URL != "attachment://media-1.png" {
		t.Fatalf("album art should be a full image, got %#v", e.Image)
	}
}

func TestChatMessageToDiscordAudioFallsBackToLink(t *testing.T) {
	b := testBridge()
	b.fetchMedia = func(string) ([]byte, error) { return nil, fmt.Errorf("boom") }
	msg := &chat.Message{
		Nick: &chat.Nick{Name: "dj", Host: "riot-grrrl.zine"},
		Str:  "![Demo](https://example.com/track.mp3)",
	}
	out, ok := b.chatMessageToDiscord(msg)
	if !ok {
		t.Fatal("message should convert")
	}
	if len(out.Files) != 0 {
		t.Fatalf("no file should attach on fetch failure: %#v", out.Files)
	}
	found := false
	for _, e := range out.Embeds {
		if e != nil && strings.Contains(e.Description, "https://example.com/track.mp3") {
			found = true
		}
	}
	if !found {
		t.Fatalf("should fall back to a link embed: %#v", out.Embeds)
	}
}

// buildAPIC returns a minimal front-cover APIC frame body wrapping pic.
func buildAPIC(pic []byte) []byte {
	body := []byte{0x00} // text encoding: ISO-8859-1
	body = append(body, []byte("image/png")...)
	body = append(body, 0x00) // MIME terminator
	body = append(body, 0x03) // picture type: front cover
	body = append(body, 0x00) // empty description + terminator
	body = append(body, pic...)
	return body
}

// buildMP3 returns bytes beginning with a minimal ID3v2.3 tag containing the
// given frames (id -> frame body), followed by junk "audio".
func buildMP3(frames map[string][]byte) []byte {
	var all []byte
	for id, body := range frames {
		sz := len(body)
		f := []byte(id)
		f = append(f, byte(sz>>24), byte(sz>>16), byte(sz>>8), byte(sz)) // v2.3 size
		f = append(f, 0x00, 0x00)                                        // frame flags
		f = append(f, body...)
		all = append(all, f...)
	}
	ts := len(all)
	tag := []byte{'I', 'D', '3', 0x03, 0x00, 0x00}
	tag = append(tag,
		byte((ts>>21)&0x7f), byte((ts>>14)&0x7f), byte((ts>>7)&0x7f), byte(ts&0x7f)) // synchsafe size
	tag = append(tag, all...)
	tag = append(tag, []byte("....fake audio frames....")...)
	return tag
}

func testBridge() *Bridge {
	cfg := DefaultConfig()
	cfg.Discord.GuildID = "guild"
	cfg.Discord.ChannelID = "123"
	return &Bridge{Config: cfg}
}

func hasEmbedImage(out *discordgo.MessageSend, want string) bool {
	for _, embed := range out.Embeds {
		if embed != nil && embed.Image != nil && embed.Image.URL == want {
			return true
		}
	}
	return false
}
