package ui

import (
	"context"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
	"github.com/hmderdoc/avatar_chat_universal/internal/chat"
	"github.com/hmderdoc/avatar_chat_universal/internal/chatserver"
)

func startUITestServer(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	srv := chatserver.New(addr)
	ctx, cancel := context.WithCancel(context.Background())
	go srv.ListenAndServe(ctx)
	t.Cleanup(cancel)
	for i := 0; i < 100; i++ {
		if c, err := net.Dial("tcp", addr); err == nil {
			c.Close()
			return addr
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("server did not start")
	return ""
}

// Reproduces the App-layer flow: in a tuned room the lounge is active; switching
// to an untuned room turns it off; switching back must turn it on again.
func TestApp_LoungeReengagesOnSwitchBack(t *testing.T) {
	addr := startUITestServer(t)
	ctx := context.Background()

	alice := chat.NewSession(chat.NewClient(addr), &chat.Nick{Name: "alice"}, "rooma")
	if err := alice.Connect(ctx, 50); err != nil {
		t.Fatal(err)
	}
	defer alice.Close()
	if err := alice.SetTuner("127.0.0.1", 7601, "cam"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)

	bob := chat.NewSession(chat.NewClient(addr), &chat.Nick{Name: "bob"}, "rooma")
	if err := bob.Connect(ctx, 50); err != nil {
		t.Fatal(err)
	}
	defer bob.Close()

	app := NewApp(io.Discard, ansi.NewInput(strings.NewReader("")), bob, 80, 25, ansi.CharsetCP437)
	defer func() {
		if app.tvConsumer != nil {
			app.tvConsumer.Close()
		}
	}()

	app.tvSync()
	if !app.loungeActive() {
		t.Fatal("lounge not active in tuned room at start")
	}

	if err := bob.JoinChannel("roomb", 50); err != nil {
		t.Fatal(err)
	}
	app.tvSync()
	if app.loungeActive() {
		t.Fatal("lounge should be off in untuned room")
	}

	if err := bob.JoinChannel("rooma", 50); err != nil {
		t.Fatal(err)
	}
	app.tvSync()
	if !app.loungeActive() {
		t.Fatal("BUG: lounge did not re-engage on switching back to the tuned room")
	}
}
