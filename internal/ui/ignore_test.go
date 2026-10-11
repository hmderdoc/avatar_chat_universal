package ui

import (
	"io"
	"strings"
	"testing"

	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
	"github.com/hmderdoc/avatar_chat_universal/internal/chat"
	"github.com/hmderdoc/avatar_chat_universal/internal/ignore"
)

func TestIgnoreCommands(t *testing.T) {
	sess := chat.NewSession(nil, &chat.Nick{Name: "alice"}, "main")
	app := NewApp(io.Discard, ansi.NewInput(strings.NewReader("")), sess, 80, 25, ansi.CharsetCP437)
	var stored string
	app.Ignores = &ignore.List{
		Load: func() string { return stored },
		Save: func(v string) error { stored = v; return nil },
	}
	sess.Ignore = app.Ignores.Hidden

	app.handleSlashCommand("/ignore")
	if !strings.Contains(lastNotice(app), "ignoring nobody") {
		t.Fatalf("empty list: %q", lastNotice(app))
	}
	app.handleSlashCommand("/ignore alice")
	if !strings.Contains(lastNotice(app), "that's you") {
		t.Fatalf("self ignore: %q", lastNotice(app))
	}
	app.handleSlashCommand("/ignore Bob")
	if !sess.Ignore("bob", "") || !strings.Contains(stored, "bob") {
		t.Fatalf("not ignored, stored %q", stored)
	}
	app.handleSlashCommand("/ignore")
	if !strings.Contains(lastNotice(app), "ignoring: Bob") {
		t.Fatalf("list: %q", lastNotice(app))
	}
	if !app.muted(&chat.Message{Nick: &chat.Nick{Name: "BOB"}, Str: "alice!"}) {
		t.Fatal("an ignored mention would still ring")
	}
	app.handleSlashCommand("/unignore bob")
	if sess.Ignore("bob", "") || stored != "" {
		t.Fatalf("still ignored, stored %q", stored)
	}
}
