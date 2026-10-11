package chat_test

import (
	"context"
	"testing"
	"time"

	"github.com/hmderdoc/avatar_chat_universal/internal/chat"
)

func connectAs(t *testing.T, addr string, self *chat.Nick, channel string) *chat.Session {
	t.Helper()
	s := chat.NewSession(chat.NewClient(addr), self, channel)
	if err := s.Connect(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func privateHas(s *chat.Session, text string) bool {
	for _, m := range s.Messages() {
		if m.Nick != nil && m.Private && m.Str == text {
			return true
		}
	}
	return false
}

// A BBS user on two clients of their board gets one PM in both (their
// plain-name mailbox, unchanged).
func TestSession_PMReachesEveryClientOfABBSUser(t *testing.T) {
	addr := startServer(t)
	door := connect(t, addr, "alice", "main")
	web := connect(t, addr, "alice", "main")
	bob := connect(t, addr, "bob", "main")
	if err := bob.SendPrivate("alice", "one pm"); err != nil {
		t.Fatal(err)
	}
	for _, s := range []*chat.Session{door, web} {
		if !cycleUntil(s, func() bool { return privateHas(s, "one pm") }) {
			t.Fatal("a client of alice missed the PM")
		}
	}
}

// /msg and /r reach a standalone web user's name@origin mailbox, not the
// plain-name one; a forged mailbox is ignored.
func TestSession_PMToWebUserMailbox(t *testing.T) {
	addr := startServer(t)
	alice := connect(t, addr, "alice", "main")
	// What the bundled server stamps on its web user's packets.
	webby := connectAs(t, addr, &chat.Nick{Name: "Webby", Host: "web.example", Mailbox: chat.MailboxFor("Webby", "web.example")}, "main")
	// Watchers: the web user's mailbox, and the plain-name one nobody should get.
	inbox := connectAs(t, addr, &chat.Nick{Name: chat.MailboxFor("Webby", "web.example")}, "lobby")
	plain := connectAs(t, addr, &chat.Nick{Name: "Webby"}, "lobby")

	if err := webby.Send("hi from the web"); err != nil {
		t.Fatal(err)
	}
	if !cycleUntil(alice, func() bool { return transcriptHas(alice, "hi from the web") }) {
		t.Fatal("alice never saw webby")
	}
	if err := alice.SendPrivate("webby", "by msg"); err != nil {
		t.Fatal(err)
	}
	if !cycleUntil(inbox, func() bool { return privateHas(inbox, "by msg") }) {
		t.Fatal("/msg didn't reach the web mailbox")
	}

	// /r after a PM from the web user.
	if err := webby.SendPrivate("alice", "pm from webby"); err != nil {
		t.Fatal(err)
	}
	if !cycleUntil(alice, func() bool { return privateHas(alice, "pm from webby") }) {
		t.Fatal("alice never got webby's PM")
	}
	if err := alice.ReplyPrivate("by reply"); err != nil {
		t.Fatal(err)
	}
	if !cycleUntil(inbox, func() bool { return privateHas(inbox, "by reply") }) {
		t.Fatal("/r didn't reach the web mailbox")
	}
	time.Sleep(50 * time.Millisecond)
	for i := 0; i < 10; i++ {
		plain.Cycle()
	}
	if privateHas(plain, "by msg") || privateHas(plain, "by reply") {
		t.Fatal("a PM to the web user landed in the plain-name mailbox")
	}
	// The echo names the plain recipient, never the mailbox.
	for _, m := range alice.Messages() {
		if m.Private && m.Nick != nil && m.Nick.Name == "alice" && (m.Str == "->webby: by msg" || m.Str == "->Webby: by reply") {
			continue
		}
		if m.Str != "" && containsMailbox(m.Str) {
			t.Fatalf("a mailbox string shown: %q", m.Str)
		}
	}
}

func containsMailbox(s string) bool {
	for i := 0; i+len("@web_example") <= len(s); i++ {
		if s[i:i+len("@web_example")] == "@web_example" {
			return true
		}
	}
	return false
}

func TestSession_ForgedMailboxIgnored(t *testing.T) {
	addr := startServer(t)
	alice := connect(t, addr, "alice", "main")
	bob := connect(t, addr, "bob", "lobby")
	mallory := connectAs(t, addr, &chat.Nick{Name: "Mallory", Host: "evil.example", Mailbox: "bob"}, "main")
	if err := mallory.Send("reply to me"); err != nil {
		t.Fatal(err)
	}
	if !cycleUntil(alice, func() bool { return transcriptHas(alice, "reply to me") }) {
		t.Fatal("alice never saw mallory")
	}
	if err := alice.SendPrivate("Mallory", "not for bob"); err != nil {
		t.Fatal(err)
	}
	if !cycleUntil(mallory, func() bool { return privateHas(mallory, "not for bob") }) {
		t.Fatal("the PM didn't fall back to the plain name")
	}
	for i := 0; i < 10; i++ {
		bob.Cycle()
	}
	if privateHas(bob, "not for bob") {
		t.Fatal("a forged mailbox redirected the PM to bob")
	}
}
