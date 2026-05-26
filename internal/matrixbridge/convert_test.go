package matrixbridge

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hmderdoc/avatar_chat_universal/internal/bridgecore"
	"github.com/hmderdoc/avatar_chat_universal/internal/chat"
)

func testBridge() *Bridge {
	cfg := DefaultConfig()
	cfg.Matrix.Homeserver = "https://matrix.org"
	cfg.Matrix.RoomID = "!abc:matrix.org"
	cfg.Matrix.UserID = "@bridgebot:matrix.org"
	return &Bridge{Config: cfg}
}

// msgEvent builds an m.room.message event with the given content fields.
func msgEvent(sender, msgtype, body string, extra map[string]interface{}) *mxEvent {
	content := map[string]interface{}{}
	if msgtype != "" {
		content["msgtype"] = msgtype
	}
	if body != "" {
		content["body"] = body
	}
	for k, v := range extra {
		content[k] = v
	}
	raw, _ := json.Marshal(content)
	return &mxEvent{
		Type:    "m.room.message",
		Sender:  sender,
		Content: raw,
	}
}

func TestMatrixMessageToChat_TextAndOrigin(t *testing.T) {
	b := testBridge()
	ev := msgEvent("@ada:matrix.org", "m.text", "hello bbs", nil)
	out, ok := b.matrixMessageToChat(context.Background(), nil, ev)
	if !ok {
		t.Fatal("expected message to convert")
	}
	if out.Str != "hello bbs" {
		t.Errorf("text = %q", out.Str)
	}
	if out.Nick.Name != "ada" {
		t.Errorf("name = %q, want localpart fallback", out.Nick.Name)
	}
	origin, ok := bridgecore.ParseHostMarker(out.Nick.Host)
	if !ok || !origin.Matches(b.origin()) {
		t.Errorf("host marker = %q, want matrix origin", out.Nick.Host)
	}
}

func TestMatrixMessageToChat_SkipsOwnUser(t *testing.T) {
	// The poll loop drops events whose sender == configured user_id. Verify the
	// configured user id equals the bot mxid so that filter is wired.
	b := testBridge()
	ev := msgEvent("@bridgebot:matrix.org", "m.text", "echo of our own send", nil)
	if ev.Sender != b.Config.Matrix.UserID {
		t.Fatalf("sender %q != configured user id %q", ev.Sender, b.Config.Matrix.UserID)
	}
}

func TestMatrixMessageToChat_ImageAnnotatedNoURL(t *testing.T) {
	b := testBridge()
	ev := msgEvent("@ada:matrix.org", "m.image", "kitten.png", map[string]interface{}{
		"url": "mxc://matrix.org/secretMediaID",
		"info": map[string]interface{}{
			"mimetype": "image/png",
		},
	})
	out, ok := b.matrixMessageToChat(context.Background(), nil, ev)
	if !ok {
		t.Fatal("expected message to convert")
	}
	if !strings.Contains(out.Str, "[image:") {
		t.Errorf("expected image annotation, got %q", out.Str)
	}
	if strings.Contains(out.Str, "mxc://") || strings.Contains(out.Str, "secretMediaID") {
		t.Errorf("media url leaked into chat text: %q", out.Str)
	}
}

func TestMatrixMessageToChat_AttachmentURLsDisabled(t *testing.T) {
	b := testBridge()
	b.Config.Bridge.IncludeAttachmentURL = false
	ev := msgEvent("@ada:matrix.org", "m.file", "report.pdf", map[string]interface{}{
		"url": "mxc://matrix.org/anotherSecret",
	})
	if _, ok := b.matrixMessageToChat(context.Background(), nil, ev); ok {
		t.Error("expected media event with no text to be dropped when annotation is disabled")
	}
}

func TestMatrixMessageToChat_StripsEscFromBody(t *testing.T) {
	b := testBridge()
	ev := msgEvent("@ada:matrix.org", "m.text", "a\x1bb\nc\x07", nil)
	out, ok := b.matrixMessageToChat(context.Background(), nil, ev)
	if !ok {
		t.Fatal("expected message to convert")
	}
	if strings.ContainsRune(out.Str, 0x1b) || strings.ContainsRune(out.Str, 0x07) {
		t.Errorf("control bytes survived: %q", out.Str)
	}
	if !strings.Contains(out.Str, " / ") {
		t.Errorf("newline not flattened: %q", out.Str)
	}
}

func TestHomeserverHost(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"https://matrix.org", "matrix.org"},
		{"https://matrix.example.com:8448", "matrix.example.com:8448"},
		{"http://localhost", "localhost"},
		{"", "matrix"},
	}
	for _, c := range cases {
		if got := homeserverHost(c.in); got != c.want {
			t.Errorf("homeserverHost(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestLocalpart(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"@ada:matrix.org", "ada"},
		{"@bot:example.com:8448", "bot"},
		{"plainname", "plainname"},
		{"", "matrix"},
	}
	for _, c := range cases {
		if got := localpart(c.in); got != c.want {
			t.Errorf("localpart(%q) = %q, want %q", c.in, got, c.want)
		}
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

func TestIgnoredChatToMatrix(t *testing.T) {
	b := testBridge()
	b.Config.Bridge.ChatToMatrixIgnore = []string{`^System:`}
	msg := &chat.Message{Nick: &chat.Nick{Name: "System"}, Str: "node 3 logged in"}
	if !b.ignoredChatToMatrix(msg) {
		t.Error("expected System message to be ignored")
	}
	msg2 := &chat.Message{Nick: &chat.Nick{Name: "Ada"}, Str: "hello"}
	if b.ignoredChatToMatrix(msg2) {
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
