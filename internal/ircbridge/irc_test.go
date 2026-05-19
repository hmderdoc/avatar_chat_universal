package ircbridge

import "testing"

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
