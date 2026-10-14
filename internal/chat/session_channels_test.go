package chat_test

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/hmderdoc/avatar_chat_universal/internal/chat"
	"github.com/hmderdoc/avatar_chat_universal/internal/chatserver"
)

func startServer(t *testing.T) string {
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

func connect(t *testing.T, addr, name, channel string) *chat.Session {
	t.Helper()
	s := chat.NewSession(chat.NewClient(addr), &chat.Nick{Name: name}, channel)
	if err := s.Connect(context.Background(), 50); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// cycleUntil drains s until cond holds or a second passes.
func cycleUntil(s *chat.Session, cond func() bool) bool {
	for i := 0; i < 100; i++ {
		s.Cycle()
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func transcriptHas(s *chat.Session, text string) bool {
	for _, m := range s.Messages() {
		if m.Nick != nil && m.Str == text {
			return true
		}
	}
	return false
}

func TestSession_BackgroundChannelCountsUnread(t *testing.T) {
	addr := startServer(t)
	alice := connect(t, addr, "alice", "main")
	bob := connect(t, addr, "bob", "games")

	// alice joins games, then goes back to main; games stays joined.
	if err := alice.JoinChannel("games", 20); err != nil {
		t.Fatal(err)
	}
	if err := alice.JoinChannel("main", 20); err != nil {
		t.Fatal(err)
	}
	if got := alice.Joined(); len(got) != 2 {
		t.Fatalf("joined = %v, want main and games", got)
	}

	var background []string
	alice.OnBackground = func(ch string, m *chat.Message) { background = append(background, ch+":"+m.Str) }

	time.Sleep(50 * time.Millisecond)
	if err := bob.Send("anyone for chess"); err != nil {
		t.Fatal(err)
	}
	if !cycleUntil(alice, func() bool { return alice.Unread("games") == 1 }) {
		t.Fatalf("games unread = %d, want 1", alice.Unread("games"))
	}
	if transcriptHas(alice, "anyone for chess") {
		t.Fatal("a background message leaked into the viewed transcript")
	}
	if len(background) != 1 || background[0] != "games:anyone for chess" {
		t.Fatalf("OnBackground calls = %v", background)
	}
	if alice.UnreadTotal() != 1 {
		t.Fatalf("UnreadTotal = %d, want 1", alice.UnreadTotal())
	}

	// Switching there clears the count and the history shows the message.
	if err := alice.JoinChannel("GAMES", 20); err != nil {
		t.Fatal(err)
	}
	if alice.Channel != "games" {
		t.Fatalf("channel = %q, want the joined spelling games", alice.Channel)
	}
	if alice.Unread("games") != 0 {
		t.Fatal("unread not cleared on switch")
	}
	if !transcriptHas(alice, "anyone for chess") {
		t.Fatal("history reload missed the message")
	}

	// /part goes back to main and drops games.
	next, err := alice.PartCurrent(20)
	if err != nil {
		t.Fatal(err)
	}
	if next != "main" || alice.Channel != "main" {
		t.Fatalf("PartCurrent -> %q (channel %q), want main", next, alice.Channel)
	}
	if got := alice.Joined(); len(got) != 1 || got[0] != "main" {
		t.Fatalf("joined after part = %v", got)
	}
	// Parting the last channel is the caller's call.
	if next, _ := alice.PartCurrent(20); next != "" {
		t.Fatalf("PartCurrent on the only channel = %q, want empty", next)
	}
}

func TestSession_PresenceNoticesOnlyForViewedChannel(t *testing.T) {
	addr := startServer(t)
	alice := connect(t, addr, "alice", "main")
	if err := alice.JoinChannel("games", 20); err != nil {
		t.Fatal(err)
	}
	if err := alice.JoinChannel("main", 20); err != nil {
		t.Fatal(err)
	}
	alice.Cycle()
	before := len(alice.Messages())
	connect(t, addr, "carol", "games")
	time.Sleep(100 * time.Millisecond)
	alice.Cycle()
	for _, m := range alice.Messages()[before:] {
		if strings.Contains(m.Str, "carol") {
			t.Fatalf("join notice from a background channel shown: %q", m.Str)
		}
	}
}

func TestSession_RoomsListsRegistryAndJoined(t *testing.T) {
	addr := startServer(t)
	alice := connect(t, addr, "alice", "main")
	connect(t, addr, "bob", "lounge")

	reg := chat.NewClient(addr)
	if err := reg.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer reg.Close()
	for _, name := range []string{"lounge", "motd", "art"} {
		if err := reg.Write("chat", "public_channels."+name, true, chat.LockWrite); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(50 * time.Millisecond)

	rooms := alice.Rooms()
	var names []string
	for _, r := range rooms {
		names = append(names, r.Name)
	}
	if strings.Join(names, ",") != "main,art,lounge" {
		t.Fatalf("rooms = %v, want main (current) then art, lounge; motd hidden", names)
	}
	if !rooms[0].Current || !rooms[0].Joined || rooms[0].Users != 1 {
		t.Fatalf("main row = %+v", rooms[0])
	}
	if rooms[2].Joined || rooms[2].Users != 1 {
		t.Fatalf("lounge row = %+v, want not joined with bob counted", rooms[2])
	}
}

func TestMentionsName(t *testing.T) {
	for _, tc := range []struct {
		text string
		want bool
	}{
		{"hey Beta Tester!", true},
		{"@beta tester you there", true},
		{"BETA TESTER", true},
		{"betatester", false},
		{"Beta Testers", false},
		{"xBeta Tester", false},
		{"ask beta tester2", false},
		{"(beta tester)", true},
		{"", false},
	} {
		if got := chat.MentionsName(tc.text, "Beta Tester"); got != tc.want {
			t.Errorf("MentionsName(%q) = %v, want %v", tc.text, got, tc.want)
		}
	}
	if chat.MentionsName("anything", "") {
		t.Error("empty name matched")
	}
	// A later whole-word occurrence counts even after an embedded one.
	if !chat.MentionsName("alicex and alice", "alice") {
		t.Error("missed the second occurrence")
	}
}

// A member's PMs live in channels.<alias>, so /join <alias> must not open
// them as a room, whether or not the joiner has seen that name talk.
func TestSession_JoinRefusesAMailbox(t *testing.T) {
	addr := startServer(t)
	alice := connect(t, addr, "alice", "main")
	// Bob's door, nick set as main.go does, is subscribed to channels.Bob.messages.
	c := chat.NewClient(addr)
	c.Nick = "Bob"
	bob := chat.NewSession(c, &chat.Nick{Name: "Bob"}, "main")
	if err := bob.Connect(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { bob.Close() })
	if err := alice.JoinChannel("Bob", 50); err == nil || !strings.Contains(err.Error(), "mailbox") {
		t.Fatalf("joining a member's mailbox: err = %v", err)
	}
	if err := alice.JoinChannel("a.b", 50); err == nil {
		t.Fatal("joined a name with a dot")
	}
	if err := alice.JoinChannel("lobby", 50); err != nil {
		t.Fatalf("joining a new room: %v", err)
	}
}

// A web guest who takes a door user's name is not that user: the door user
// doesn't see the guest's lines as their own, and a PM to the name still
// reaches the door user, not the guest's web mailbox.
func TestSession_WebGuestCantTakeADoorUsersName(t *testing.T) {
	addr := startServer(t)
	alice := connect(t, addr, "alice", "main")
	alice.Self.Host = "AliceBBS"
	bob := connect(t, addr, "bob", "main")
	if err := alice.Send("the real alice"); err != nil {
		t.Fatal(err)
	}
	if !cycleUntil(bob, func() bool { return transcriptHas(bob, "the real alice") }) {
		t.Fatal("alice's line not delivered")
	}
	guest := chat.NewSession(chat.NewClient(addr), &chat.Nick{Name: "alice", Host: "Web", Mailbox: chat.MailboxFor("alice", "Web")}, "main")
	if err := guest.Connect(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { guest.Close() })
	if err := guest.Send("i am alice too"); err != nil {
		t.Fatal(err)
	}
	if !cycleUntil(bob, func() bool { return transcriptHas(bob, "i am alice too") }) {
		t.Fatal("guest line not delivered to bob")
	}
	if !cycleUntil(alice, func() bool { return transcriptHas(alice, "i am alice too") }) {
		t.Fatal("the door user took the guest's line for their own echo")
	}
	if err := bob.SendPrivate("alice", "for the real one"); err != nil {
		t.Fatal(err)
	}
	if !cycleUntil(alice, func() bool { return transcriptHas(alice, "for the real one") }) {
		t.Fatal("PM to the name went to the web guest")
	}
	if chat.SameUser(alice.Self, &chat.Nick{Name: "Alice", Host: "Web", Mailbox: chat.MailboxFor("alice", "Web")}) {
		t.Fatal("a web nick counted as the door user")
	}
	if !chat.SameUser(alice.Self, &chat.Nick{Name: "Alice", Host: "alicebbs"}) {
		t.Fatal("own nick not recognised")
	}
}

// A PM to a name nobody is reading says so.
func TestSession_PMToOfflineNameSaysSo(t *testing.T) {
	addr := startServer(t)
	alice := connect(t, addr, "alice", "main")
	if err := alice.SendPrivate("nobody", "hello?"); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range alice.Messages() {
		if m.Nick == nil && strings.Contains(m.Str, "nobody isn't online") {
			found = true
		}
	}
	if !found {
		t.Fatal("no offline notice")
	}
}
