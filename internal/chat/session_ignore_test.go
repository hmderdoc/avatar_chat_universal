package chat_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/hmderdoc/avatar_chat_universal/internal/chat"
)

func ignoreBob(sender, text string) bool {
	return strings.EqualFold(sender, "bob") || strings.HasPrefix(text, "<bob> ")
}

func TestSession_IgnoreDropsLiveHistoryAndPMs(t *testing.T) {
	addr := startServer(t)
	bob := connect(t, addr, "bob", "main")
	carol := connect(t, addr, "carol", "main")
	if err := bob.Send("before alice came"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)

	alice := chat.NewSession(chat.NewClient(addr), &chat.Nick{Name: "alice"}, "main")
	alice.Ignore = ignoreBob
	if err := alice.Connect(context.Background(), 50); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { alice.Close() })
	if transcriptHas(alice, "before alice came") {
		t.Fatal("ignored line loaded from history")
	}

	_ = bob.Send("live from bob")
	_ = bob.SendPrivate("alice", "pm from bob")
	_ = carol.Send("<bob> relayed through a bridge")
	_ = carol.Send("carol here")
	if !cycleUntil(alice, func() bool { return transcriptHas(alice, "carol here") }) {
		t.Fatal("carol's line never arrived")
	}
	for _, s := range []string{"live from bob", "pm from bob", "<bob> relayed through a bridge"} {
		if transcriptHas(alice, s) {
			t.Errorf("%q shown", s)
		}
	}
	if alice.LastReplyTarget() == "bob" {
		t.Error("an ignored PM set the /r target")
	}
}

func TestSession_IgnoreSkipsBackgroundUnreadAndRefilters(t *testing.T) {
	addr := startServer(t)
	alice := connect(t, addr, "alice", "main")
	bob := connect(t, addr, "bob", "games")
	if err := alice.JoinChannel("games", 20); err != nil {
		t.Fatal(err)
	}
	_ = bob.Send("shown before the ignore")
	if !cycleUntil(alice, func() bool { return transcriptHas(alice, "shown before the ignore") }) {
		t.Fatal("line never arrived")
	}
	// Ignoring later drops what's already on screen.
	alice.Ignore = ignoreBob
	alice.Refilter()
	if transcriptHas(alice, "shown before the ignore") {
		t.Fatal("Refilter kept the line")
	}
	if err := alice.JoinChannel("main", 20); err != nil {
		t.Fatal(err)
	}
	_ = bob.Send("in games while alice is away")
	time.Sleep(100 * time.Millisecond)
	for i := 0; i < 20; i++ {
		alice.Cycle()
	}
	if n := alice.Unread("games"); n != 0 {
		t.Fatalf("unread games = %d, want 0 for an ignored sender", n)
	}
}
