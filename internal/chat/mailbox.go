package chat

import "strings"

// Web mailboxes (WEB.md "Private messages"). A standalone web user, one on
// the bundled server's page with no BBS login, carries an extra nick field
// naming where PMs to them go:
//
//	{"name": "alice", "host": "web.example", "mailbox": "alice@web_example"}
//
// The bundled server stamps it and lets a browser read only its own
// mailbox, so a web user who types someone's name never sees their PMs.
// BBS users carry no mailbox and keep the plain-name one
// (channels.<name>.messages) every client of their board reads.
//
// A client honours the field only when it is exactly MailboxFor(name,
// host), compared without case; anything else (a forged mailbox naming
// someone else's, one with a dot) is ignored and the PM goes to the plain
// name as before.

// Origin is host made safe for a location path: every character outside
// [A-Za-z0-9_-] becomes '_' (so "futureland.today" is "futureland_today";
// a dot would split the path).
func Origin(host string) string {
	var b strings.Builder
	for i := 0; i < len(host); i++ {
		c := host[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' {
			b.WriteByte(c)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

// MailboxFor is a web user's mailbox: name@Origin(host).
func MailboxFor(name, host string) string {
	return name + "@" + Origin(host)
}

// ValidMailbox reports whether mailbox is the one name on host may carry.
func ValidMailbox(name, host, mailbox string) bool {
	if mailbox == "" || name == "" || strings.Contains(mailbox, ".") {
		return false
	}
	return strings.EqualFold(mailbox, MailboxFor(name, host))
}

// PMTarget is the mailbox a PM to this sender goes to: their valid web
// mailbox, else their plain name.
func PMTarget(name, host, mailbox string) string {
	if ValidMailbox(name, host, mailbox) {
		return mailbox
	}
	return name
}
