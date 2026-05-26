package slackbridge

import (
	"context"
	"strings"
	"testing"

	"github.com/hmderdoc/avatar_chat_universal/internal/bridgecore"
	"github.com/hmderdoc/avatar_chat_universal/internal/chat"
)

func testBridge() *Bridge {
	cfg := DefaultConfig()
	cfg.Slack.ChannelID = "C0123ABCD"
	return &Bridge{Config: cfg}
}

func TestSlackMessageToChat_TextAndOrigin(t *testing.T) {
	b := testBridge()
	payload := &slackEventPayload{
		TeamID: "T999",
		Event: slackEventInner{
			Type:    "message",
			Channel: "C0123ABCD",
			User:    "U12345",
			Text:    "hello bbs",
		},
	}
	// sc is nil: name falls back to the raw user id, no network used.
	out, ok := b.slackMessageToChat(context.Background(), nil, payload)
	if !ok {
		t.Fatal("expected message to convert")
	}
	if out.Str != "hello bbs" {
		t.Errorf("text = %q", out.Str)
	}
	if out.Nick.Name != "U12345" {
		t.Errorf("name = %q", out.Nick.Name)
	}
	origin, ok := bridgecore.ParseHostMarker(out.Nick.Host)
	if !ok || !origin.Matches(b.origin()) {
		t.Errorf("host marker = %q, want slack origin", out.Nick.Host)
	}
	if origin.Network != "t999" {
		t.Errorf("origin network = %q, want team id", origin.Network)
	}
}

func TestSlackMessageToChat_DropsBots(t *testing.T) {
	b := testBridge()
	payload := &slackEventPayload{
		Event: slackEventInner{
			Type:    "message",
			Channel: "C0123ABCD",
			BotID:   "B0001",
			Text:    "beep boop",
		},
	}
	if _, ok := b.slackMessageToChat(context.Background(), nil, payload); ok {
		t.Error("expected bot message to be dropped")
	}
}

func TestSlackMessageToChat_DropsSubtypes(t *testing.T) {
	b := testBridge()
	for _, sub := range []string{"bot_message", "message_changed", "message_deleted", "channel_join"} {
		payload := &slackEventPayload{
			Event: slackEventInner{
				Type:    "message",
				Channel: "C0123ABCD",
				Subtype: sub,
				User:    "U1",
				Text:    "x",
			},
		}
		if _, ok := b.slackMessageToChat(context.Background(), nil, payload); ok {
			t.Errorf("expected subtype %q to be dropped", sub)
		}
	}
}

func TestSlackMessageToChat_UnescapesMarkup(t *testing.T) {
	b := testBridge()
	payload := &slackEventPayload{
		Event: slackEventInner{
			Type:    "message",
			Channel: "C0123ABCD",
			User:    "U1",
			Text:    "see <http://example.com|the site> &amp; <http://x>",
		},
	}
	out, ok := b.slackMessageToChat(context.Background(), nil, payload)
	if !ok {
		t.Fatal("expected message to convert")
	}
	if !strings.Contains(out.Str, "the site") {
		t.Errorf("link label not used: %q", out.Str)
	}
	if strings.Contains(out.Str, "<http://example.com") || strings.Contains(out.Str, "|") {
		t.Errorf("slack link markup survived: %q", out.Str)
	}
	if !strings.Contains(out.Str, "&") || strings.Contains(out.Str, "&amp;") {
		t.Errorf("entity not unescaped: %q", out.Str)
	}
	if !strings.Contains(out.Str, "http://x") {
		t.Errorf("bare link not unwrapped: %q", out.Str)
	}
}

func TestSlackMessageToChat_FileAnnotatedNoURL(t *testing.T) {
	b := testBridge()
	payload := &slackEventPayload{
		Event: slackEventInner{
			Type:    "message",
			Channel: "C0123ABCD",
			User:    "U1",
			Text:    "look at this",
			Files: []slackFile{{
				Name:       "secret.png",
				Mimetype:   "image/png",
				URLPrivate: "https://files.slack.com/secret-private-url",
			}},
		},
	}
	out, ok := b.slackMessageToChat(context.Background(), nil, payload)
	if !ok {
		t.Fatal("expected message to convert")
	}
	if !strings.Contains(out.Str, "[file: secret.png]") {
		t.Errorf("expected file annotation, got %q", out.Str)
	}
	if strings.Contains(out.Str, "files.slack.com") || strings.Contains(out.Str, "secret-private-url") {
		t.Errorf("url_private leaked into chat text: %q", out.Str)
	}
}

func TestSlackMessageToChat_WrongChannelIgnoredByCaller(t *testing.T) {
	// slackMessageToChat itself does not gate on channel (the poller does), but a
	// non-message event type must still be dropped here.
	b := testBridge()
	payload := &slackEventPayload{
		Event: slackEventInner{Type: "reaction_added", Channel: "C0123ABCD", User: "U1", Text: "x"},
	}
	if _, ok := b.slackMessageToChat(context.Background(), nil, payload); ok {
		t.Error("expected non-message event to be dropped")
	}
}

func TestAuthorLabel_SuffixesForeignOrigin(t *testing.T) {
	msg := &chat.Message{
		Nick: &chat.Nick{
			Name: "ircuser",
			Host: bridgecore.FormatHostMarker(bridgecore.NewOrigin("irc", "libera", "#avatarchat")),
		},
		Str: "hi",
	}
	got := authorLabel(msg)
	if !strings.HasPrefix(got, "ircuser -- IRC:") {
		t.Errorf("authorLabel = %q, want IRC origin suffix", got)
	}
}

func TestIgnoredChatToSlack(t *testing.T) {
	b := testBridge()
	b.Config.Bridge.ChatToSlackIgnore = []string{`^System:`}
	msg := &chat.Message{Nick: &chat.Nick{Name: "System"}, Str: "node 3 logged in"}
	if !b.ignoredChatToSlack(msg) {
		t.Error("expected System message to be ignored")
	}
	msg2 := &chat.Message{Nick: &chat.Nick{Name: "Ada"}, Str: "hello"}
	if b.ignoredChatToSlack(msg2) {
		t.Error("did not expect Ada message to be ignored")
	}
}

func TestSanitizeText_StripsControlsAndEsc(t *testing.T) {
	got := sanitizeText("a\x1bb\nc\x07")
	if strings.ContainsRune(got, 0x1b) || strings.ContainsRune(got, 0x07) {
		t.Errorf("control bytes survived: %q", got)
	}
	if !strings.Contains(got, " / ") {
		t.Errorf("newline not flattened: %q", got)
	}
}
