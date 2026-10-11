package ui

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
	"github.com/hmderdoc/avatar_chat_universal/internal/chat"
)

func uploadApp(t *testing.T) *App {
	t.Helper()
	addr := startUITestServer(t)
	sess := chat.NewSession(chat.NewClient(addr), &chat.Nick{Name: "alice"}, "main")
	if err := sess.Connect(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sess.Close() })
	return NewApp(io.Discard, ansi.NewInput(strings.NewReader("")), sess, 80, 25, ansi.CharsetCP437)
}

func lastNotice(a *App) string {
	msgs := a.Session.Messages()
	if len(msgs) == 0 {
		return ""
	}
	return msgs[len(msgs)-1].Str
}

// /upload puts the returned link in the input line and sends nothing.
func TestUploadPutsLinkInInputLine(t *testing.T) {
	a := uploadApp(t)
	const link = "https://bbs.example.org/chatmedia/2026-10-10/0123456789abcdef01234567.png"
	a.OnMediaUpload = func() (string, int, error) { return link, 7, nil }
	before := len(a.Session.Messages())
	if _, handled := a.handleSlashCommand("/upload"); !handled {
		t.Fatal("/upload not handled")
	}
	if a.inputLine.Value() != link {
		t.Fatalf("input line = %q", a.inputLine.Value())
	}
	if n := lastNotice(a); !strings.Contains(n, "press Enter") || !strings.Contains(n, "7 days") {
		t.Fatalf("notice = %q", n)
	}
	for _, m := range a.Session.Messages()[before:] {
		if strings.Contains(m.Str, link) {
			t.Fatal("the link was sent before the user pressed Enter")
		}
	}
}

func TestUploadErrorsAndUnconfigured(t *testing.T) {
	a := uploadApp(t)
	a.handleSlashCommand("/upload")
	if n := lastNotice(a); !strings.Contains(n, "not set up") {
		t.Fatalf("unconfigured notice = %q", n)
	}
	a.OnMediaUpload = func() (string, int, error) { return "", 0, errors.New("unsupported file type") }
	a.handleSlashCommand("/upload")
	if n := lastNotice(a); !strings.Contains(n, "unsupported file type") || a.inputLine.Value() != "" {
		t.Fatalf("error notice = %q, input %q", n, a.inputLine.Value())
	}
}
