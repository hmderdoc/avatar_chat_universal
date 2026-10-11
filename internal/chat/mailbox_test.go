package chat

import "testing"

func TestMailbox(t *testing.T) {
	if got := Origin("futureland.today"); got != "futureland_today" {
		t.Fatalf("Origin = %q", got)
	}
	if got := MailboxFor("alice", "Web BBS"); got != "alice@Web_BBS" {
		t.Fatalf("MailboxFor = %q", got)
	}
	cases := []struct {
		name, host, mailbox string
		ok                  bool
	}{
		{"alice", "web.example", "alice@web_example", true},
		{"alice", "web.example", "ALICE@WEB_EXAMPLE", true},
		{"alice", "web.example", "", false},
		{"alice", "web.example", "bob@web_example", false},   // someone else's
		{"alice", "web.example", "alice@other", false},       // another origin
		{"alice", "web.example", "alice@web.example", false}, // a dot
		{"alice", "web.example", "Hm Derdoc", false},         // a plain-name mailbox
		{"", "web.example", "@web_example", false},
	}
	for _, c := range cases {
		if got := ValidMailbox(c.name, c.host, c.mailbox); got != c.ok {
			t.Errorf("ValidMailbox(%q, %q, %q) = %v", c.name, c.host, c.mailbox, got)
		}
	}
	if PMTarget("alice", "web.example", "alice@web_example") != "alice@web_example" ||
		PMTarget("alice", "web.example", "Hm Derdoc") != "alice" {
		t.Fatal("PMTarget")
	}
}
