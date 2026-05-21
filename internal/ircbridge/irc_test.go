package ircbridge

import (
	"testing"

	"github.com/hmderdoc/avatar_chat_universal/internal/chat"
)

func TestParseIRCLinePrivmsg(t *testing.T) {
	msg := ParseIRCLine(":alice!u@example PRIVMSG #chat :hello world\r\n")
	if msg.Nick != "alice" || msg.Command != "PRIVMSG" {
		t.Fatalf("unexpected parsed message: %#v", msg)
	}
	if len(msg.Params) != 2 || msg.Params[0] != "#chat" || msg.Params[1] != "hello world" {
		t.Fatalf("unexpected params: %#v", msg.Params)
	}
}

func TestStripIRCFormatting(t *testing.T) {
	got := StripIRCFormatting("\x02bold\x02 \x0304,01red\x0f plain")
	if got != "bold red plain" {
		t.Fatalf("got %q", got)
	}
}

func TestStripChatControls(t *testing.T) {
	got := StripChatControls("\x01rred \x02jjoined")
	if got != "red joined" {
		t.Fatalf("got %q", got)
	}
}

func TestChatMessageToIRCSuppressesOnlyOwnIRCOrigin(t *testing.T) {
	b := &Bridge{Config: DefaultConfig()}
	b.Config.IRC.Host = "irc.libera.chat"
	b.Config.IRC.Channel = "#avatar"

	own := testChatMessage("alice", "IRC:irc.libera.chat/#avatar", "hello")
	if _, ok := b.chatMessageToIRC(own); ok {
		t.Fatal("own IRC origin should be suppressed")
	}

	other := testChatMessage("bob", "IRC:irc.efnet.org/#avatar", "hello")
	line, ok := b.chatMessageToIRC(other)
	if !ok {
		t.Fatal("different IRC origin should be forwarded")
	}
	if line != "<bob@irc.efnet.org/#avatar> hello" {
		t.Fatalf("line = %q", line)
	}
}

func testChatMessage(nick, host, text string) *chat.Message {
	return &chat.Message{
		Nick: &chat.Nick{Name: nick, Host: host},
		Str:  text,
	}
}
