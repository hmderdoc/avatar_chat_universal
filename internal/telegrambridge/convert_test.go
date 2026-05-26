package telegrambridge

import (
	"strings"
	"testing"

	"github.com/hmderdoc/avatar_chat_universal/internal/bridgecore"
	"github.com/hmderdoc/avatar_chat_universal/internal/chat"
)

func testBridge() *Bridge {
	cfg := DefaultConfig()
	cfg.Telegram.ChatID = "-1001234567890"
	return &Bridge{Config: cfg}
}

func TestTelegramMessageToChat_TextAndOrigin(t *testing.T) {
	b := testBridge()
	m := &tgMessage{
		From: &tgUser{ID: 1, FirstName: "Ada", LastName: "Lovelace"},
		Chat: &tgChat{ID: -1001234567890, Type: "supergroup"},
		Text: "hello bbs",
	}
	out, ok := b.telegramMessageToChat(m)
	if !ok {
		t.Fatal("expected message to convert")
	}
	if out.Str != "hello bbs" {
		t.Errorf("text = %q", out.Str)
	}
	if out.Nick.Name != "Ada Lovelace" {
		t.Errorf("name = %q", out.Nick.Name)
	}
	origin, ok := bridgecore.ParseHostMarker(out.Nick.Host)
	if !ok || !origin.Matches(b.origin()) {
		t.Errorf("host marker = %q, want telegram origin", out.Nick.Host)
	}
}

func TestTelegramMessageToChat_DropsBots(t *testing.T) {
	b := testBridge()
	m := &tgMessage{
		From: &tgUser{ID: 2, IsBot: true, Username: "somebot"},
		Chat: &tgChat{ID: -1001234567890},
		Text: "beep boop",
	}
	if _, ok := b.telegramMessageToChat(m); ok {
		t.Error("expected bot message to be dropped")
	}
}

func TestTelegramMessageToChat_PhotoAnnotatedNoURL(t *testing.T) {
	b := testBridge()
	m := &tgMessage{
		From:    &tgUser{ID: 1, FirstName: "Ada"},
		Chat:    &tgChat{ID: -1001234567890},
		Caption: "look at this",
		Photo:   []tgPhotoSize{{FileID: "secret-file-id", Width: 90, Height: 90}},
	}
	out, ok := b.telegramMessageToChat(m)
	if !ok {
		t.Fatal("expected message to convert")
	}
	if !strings.Contains(out.Str, "[photo]") {
		t.Errorf("expected photo annotation, got %q", out.Str)
	}
	if strings.Contains(out.Str, "secret-file-id") {
		t.Errorf("file id leaked into chat text: %q", out.Str)
	}
}

func TestTelegramMessageToChat_ChannelPostUsesTitle(t *testing.T) {
	b := testBridge()
	m := &tgMessage{
		Chat: &tgChat{ID: -1001234567890, Type: "channel", Title: "BBS News"},
		Text: "announcement",
	}
	out, ok := b.telegramMessageToChat(m)
	if !ok {
		t.Fatal("expected message to convert")
	}
	if out.Nick.Name != "BBS News" {
		t.Errorf("name = %q, want channel title", out.Nick.Name)
	}
}

func TestMatchChat(t *testing.T) {
	cases := []struct {
		configured string
		chat       *tgChat
		want       bool
	}{
		{"-1001234567890", &tgChat{ID: -1001234567890}, true},
		{"-1001234567890", &tgChat{ID: 999}, false},
		{"@bbschannel", &tgChat{Username: "bbschannel"}, true},
		{"@bbschannel", &tgChat{Username: "other"}, false},
		{"", &tgChat{ID: 1}, false},
		{"-100", nil, false},
	}
	for _, c := range cases {
		if got := matchChat(c.configured, c.chat); got != c.want {
			t.Errorf("matchChat(%q, %+v) = %v, want %v", c.configured, c.chat, got, c.want)
		}
	}
}

func TestHtmlLabel_SuffixesForeignOrigin(t *testing.T) {
	msg := &chat.Message{
		Nick: &chat.Nick{
			Name: "ircuser",
			Host: bridgecore.FormatHostMarker(bridgecore.NewOrigin("irc", "libera", "#avatarchat")),
		},
		Str: "hi",
	}
	got := htmlLabel(msg)
	if !strings.Contains(got, "<b>ircuser</b>") || !strings.Contains(got, "IRC:") {
		t.Errorf("htmlLabel = %q, want bold name + IRC origin tag", got)
	}
}

func TestHtmlLabel_EscapesName(t *testing.T) {
	msg := &chat.Message{Nick: &chat.Nick{Name: "a<b>&c"}, Str: "x"}
	got := htmlLabel(msg)
	if strings.Contains(got, "a<b>&c") {
		t.Errorf("htmlLabel did not escape name: %q", got)
	}
}

func TestIgnoredChatToTelegram(t *testing.T) {
	b := testBridge()
	b.Config.Bridge.ChatToTelegramIgnore = []string{`^System:`}
	msg := &chat.Message{Nick: &chat.Nick{Name: "System"}, Str: "node 3 logged in"}
	if !b.ignoredChatToTelegram(msg) {
		t.Error("expected System message to be ignored")
	}
	msg2 := &chat.Message{Nick: &chat.Nick{Name: "Ada"}, Str: "hello"}
	if b.ignoredChatToTelegram(msg2) {
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
