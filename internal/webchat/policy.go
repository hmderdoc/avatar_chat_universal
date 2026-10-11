package webchat

import (
	"encoding/base64"
	"encoding/json"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/hmderdoc/avatar_chat_universal/internal/chat"
	"github.com/hmderdoc/avatar_chat_universal/internal/inlineimg"
	"github.com/hmderdoc/avatar_chat_universal/internal/nickstyle"
)

// The policy every browser packet passes before it reaches a chat server,
// local or upstream. The chat protocol has no accounts, so anything a TCP
// client may do a browser could too; the front end narrows that to what
// the page does, and stamps identity itself:
//
//   - SOCKET PING is answered here, PONG dropped;
//   - SOCKET CLAIM {nick, key} comes first: the nickname is checked
//     against this server's name claims (claims.go) and, once granted, is
//     the connection's for good;
//   - chat QUERYs only: SUBSCRIBE, UNSUBSCRIBE, WRITE and WHO on
//     channels.<room>.messages, SLICE and PUSH on channels.<room>.history,
//     KEYS on public_channels, and (with CreateRooms) WRITE true on
//     public_channels.<room>;
//   - <room> is the default room or one in the public_channels registry,
//     so a browser can't read a member's private mailbox
//     (channels.<alias>.messages) by naming it as a room;
//   - private messages: the browser reads only its own web mailbox,
//     channels.<nick>@<origin>.{messages,history} (chat.MailboxFor), and
//     may WRITE a message to any other channels.<x>.messages and PUSH it
//     to channels.<x>.history, but never SUBSCRIBE, SLICE or WHO there:
//     no plain-name mailbox is ever readable from the page;
//   - every packet's nick and system, and every message's nick.name,
//     nick.host and nick.mailbox, are forced to the claimed nick,
//     Config.SystemName and its mailbox;
//   - a message's text is cleaned and capped at MaxMessage characters, its
//     time is the server's, TV tuner markers are refused, an avatar must
//     be a well-formed 120-byte one and a style is normalized.

// packet is the chat protocol's packet, as the policy rebuilds it.
type packet struct {
	Scope    string          `json:"scope,omitempty"`
	Func     string          `json:"func,omitempty"`
	Oper     string          `json:"oper,omitempty"`
	Location string          `json:"location,omitempty"`
	Data     json.RawMessage `json:"data,omitempty"`
	Lock     int             `json:"lock,omitempty"`
	Timeout  int             `json:"timeout,omitempty"`
	Nick     string          `json:"nick,omitempty"`
	System   string          `json:"system,omitempty"`
}

type policy struct {
	srv  *Server
	ip   string
	nick string // fixed by the CLAIM
}

// mailbox is the connection's own web mailbox, "" before the CLAIM.
func (p *policy) mailbox() string {
	if p.nick == "" {
		return ""
	}
	return chat.MailboxFor(p.nick, p.srv.cfg.SystemName)
}

var roomName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)

// maxNick is Synchronet's alias length (LEN_ALIAS).
const maxNick = 25

// filter returns the packet to forward (nil for none) and a reply for the
// browser (nil for none).
func (p *policy) filter(line []byte) (pass, reply []byte) {
	var pkt packet
	if err := json.Unmarshal(line, &pkt); err != nil {
		return nil, errorPacket("malformed JSON")
	}
	if pkt.Scope == "SOCKET" {
		switch pkt.Func {
		case "PING":
			b, _ := json.Marshal(packet{Scope: "SOCKET", Func: "PONG", Data: pkt.Data})
			return nil, b
		case "CLAIM":
			return nil, p.claim(pkt.Data)
		}
		return nil, nil
	}
	if pkt.Scope != "chat" || pkt.Func != "QUERY" {
		return nil, errorPacket("not allowed from the web page")
	}
	if why := p.check(&pkt); why != "" {
		return nil, errorPacket(why)
	}
	pkt.System = p.srv.cfg.SystemName
	pkt.Nick = p.nick
	pkt.Timeout = -1
	b, err := json.Marshal(pkt)
	if err != nil {
		return nil, errorPacket("could not encode the packet")
	}
	return b, nil
}

// check validates pkt in place and returns why it's refused, or "".
func (p *policy) check(pkt *packet) string {
	oper := strings.ToUpper(pkt.Oper)
	pkt.Oper = oper
	parts := strings.Split(pkt.Location, ".")
	switch {
	case pkt.Location == "public_channels":
		if oper != "KEYS" {
			return "not allowed: " + oper + " " + pkt.Location
		}
		pkt.Data, pkt.Lock = nil, chat.LockRead
		return ""
	case len(parts) == 2 && parts[0] == "public_channels":
		if oper != "WRITE" || !p.srv.cfg.CreateRooms {
			return "creating rooms is off here"
		}
		if !roomName.MatchString(parts[1]) {
			return "room names are letters, digits, - and _ (up to 32)"
		}
		if strings.EqualFold(parts[1], p.nick) {
			return "a room can't take your own name"
		}
		pkt.Data, pkt.Lock = json.RawMessage(`true`), chat.LockWrite
		p.srv.rooms.add(parts[1])
		return ""
	case len(parts) == 3 && parts[0] == "channels":
	default:
		return "not allowed: " + pkt.Location
	}
	room, leaf := parts[1], parts[2]
	if own := p.mailbox(); own != "" && strings.EqualFold(room, own) {
		return p.checkOwnMailbox(pkt, oper, leaf)
	}
	if !roomName.MatchString(room) || !p.srv.rooms.allowed(room) {
		return p.checkPM(pkt, oper, room, leaf)
	}
	switch {
	case leaf == "messages" && (oper == "SUBSCRIBE" || oper == "UNSUBSCRIBE" || oper == "WHO"):
		if p.nick == "" {
			return "claim a nickname first"
		}
		pkt.Data, pkt.Lock = nil, 0
	case leaf == "messages" && oper == "WRITE", leaf == "history" && oper == "PUSH":
		if p.nick == "" {
			return "join a room with a nickname first"
		}
		data, why := p.message(pkt.Data)
		if why != "" {
			return why
		}
		pkt.Data, pkt.Lock = data, chat.LockWrite
	case leaf == "history" && oper == "SLICE":
		return p.slice(pkt)
	default:
		return "not allowed: " + oper + " " + pkt.Location
	}
	return ""
}

// checkOwnMailbox: the browser's own web mailbox. It may follow its
// messages, read its history and push its sent copies there.
func (p *policy) checkOwnMailbox(pkt *packet, oper, leaf string) string {
	switch {
	case leaf == "messages" && (oper == "SUBSCRIBE" || oper == "UNSUBSCRIBE"):
		pkt.Data, pkt.Lock = nil, 0
	case leaf == "history" && oper == "SLICE":
		return p.slice(pkt)
	case leaf == "history" && oper == "PUSH":
		data, why := p.message(pkt.Data)
		if why != "" {
			return why
		}
		pkt.Data, pkt.Lock = data, chat.LockWrite
	default:
		return "not allowed: " + oper + " " + pkt.Location
	}
	return ""
}

// checkPM: any channel that isn't a public room or the browser's own
// mailbox is someone's mailbox. A message may be written there and pushed
// to its history (a PM); nothing there can be read.
func (p *policy) checkPM(pkt *packet, oper, target, leaf string) string {
	if !(leaf == "messages" && oper == "WRITE" || leaf == "history" && oper == "PUSH") {
		return "no public room named " + target
	}
	if p.nick == "" {
		return "claim a nickname first"
	}
	if !validTarget(target) {
		return "no such recipient"
	}
	data, why := p.message(pkt.Data)
	if why != "" {
		return why
	}
	pkt.Data, pkt.Lock = data, chat.LockWrite
	return ""
}

// validTarget: a mailbox name a PM may go to: a BBS alias (Synchronet
// allows most printable characters) or a web mailbox; no dot, which would
// split the location, and no controls.
func validTarget(s string) bool {
	if s == "" || len(s) > 80 || strings.TrimSpace(s) != s {
		return false
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f || r == '.' {
			return false
		}
	}
	return utf8.ValidString(s)
}

// slice clamps a SLICE's arguments.
func (p *policy) slice(pkt *packet) string {
	var args struct {
		Start int  `json:"start"`
		End   *int `json:"end,omitempty"`
	}
	if len(pkt.Data) > 0 && json.Unmarshal(pkt.Data, &args) != nil {
		return "bad SLICE arguments"
	}
	if args.Start < -500 {
		args.Start = -500
	}
	pkt.Data, _ = json.Marshal(args)
	pkt.Lock = chat.LockRead
	return ""
}

// claim handles SOCKET CLAIM {nick, key} and returns the reply, a SOCKET
// CLAIM packet with {ok, nick} or {ok: false, reason: "invalid" | "taken"
// | "limit" | "error" | "fixed"}.
func (p *policy) claim(raw json.RawMessage) []byte {
	var in struct {
		Nick string `json:"nick"`
		Key  string `json:"key"`
	}
	reply := func(ok bool, reason string) []byte {
		data, _ := json.Marshal(map[string]interface{}{"ok": ok, "nick": p.nick, "reason": reason})
		b, _ := json.Marshal(packet{Scope: "SOCKET", Func: "CLAIM", Data: data})
		return b
	}
	if json.Unmarshal(raw, &in) != nil {
		return reply(false, "invalid")
	}
	n, ok := cleanNick(in.Nick)
	if !ok {
		return reply(false, "invalid")
	}
	if p.nick != "" {
		if strings.EqualFold(n, p.nick) {
			return reply(true, "")
		}
		return reply(false, "fixed") // a new name needs a new connection
	}
	switch p.srv.claims.claim(n, in.Key, p.ip) {
	case claimOK:
		p.nick = n
		return reply(true, "")
	case claimTaken:
		return reply(false, "taken")
	case claimLimit:
		return reply(false, "limit")
	}
	return reply(false, "error")
}

// webNick is a message's nick as the policy rebuilds it.
type webNick struct {
	Name    string    `json:"name"`
	Host    string    `json:"host,omitempty"`
	Mailbox string    `json:"mailbox,omitempty"`
	Avatar  string    `json:"avatar,omitempty"`
	Style   *webStyle `json:"style,omitempty"`
}

// webStyle is a handle style as the page may send it: colours only.
type webStyle struct {
	Colors []string `json:"colors"`
}

// toNick is a PM's recipient as carried in private.to.
type toNick struct {
	Name    string `json:"name"`
	Host    string `json:"host,omitempty"`
	Mailbox string `json:"mailbox,omitempty"`
}

type webMessage struct {
	Nick    *webNick `json:"nick"`
	Str     string   `json:"str"`
	Time    int64    `json:"time"`
	Private *struct {
		To *toNick `json:"to"`
	} `json:"private,omitempty"`
}

func (p *policy) message(raw json.RawMessage) (json.RawMessage, string) {
	var in struct {
		Nick *struct {
			Avatar string           `json:"avatar"`
			Style  *nickstyle.Style `json:"style"`
		} `json:"nick"`
		Str     string `json:"str"`
		Private *struct {
			To *toNick `json:"to"`
		} `json:"private"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, "a message is {nick, str}"
	}
	text := cleanText(in.Str)
	if text == "" {
		return nil, "empty message"
	}
	if n := utf8.RuneCountInString(text); n > p.srv.cfg.MaxMessage {
		return nil, "message is too long"
	}
	if chat.IsTunerMarker(text) {
		return nil, "not allowed from the web page"
	}
	// The sender's own links count as seen, so their pictures and card
	// show for them too (a local server doesn't echo a write back).
	for _, u := range inlineimg.FindURLs(text) {
		p.srv.seen.add(u)
	}
	out := webMessage{
		Nick: &webNick{Name: p.nick, Host: p.srv.cfg.SystemName, Mailbox: p.mailbox()},
		Str:  text,
		Time: p.srv.now().UnixMilli(),
	}
	if in.Private != nil && in.Private.To != nil {
		to := in.Private.To
		name, host := cleanText(to.Name), cleanText(to.Host)
		if name != "" && utf8.RuneCountInString(name) <= 60 && utf8.RuneCountInString(host) <= 60 {
			t := &toNick{Name: name, Host: host}
			if chat.ValidMailbox(name, host, to.Mailbox) {
				t.Mailbox = to.Mailbox
			}
			out.Private = &struct {
				To *toNick `json:"to"`
			}{To: t}
		}
	}
	if in.Nick != nil {
		if validAvatar(in.Nick.Avatar) {
			out.Nick.Avatar = in.Nick.Avatar
		}
		// Colours only (UnmarshalJSON normalized them): a tag twists how
		// a name reads, so it never goes out from the page.
		if st := in.Nick.Style; st != nil && len(st.Colors) > 0 {
			out.Nick.Style = &webStyle{Colors: st.Colors}
		}
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil, "could not encode the message"
	}
	return b, ""
}

// cleanNick trims a requested nickname and checks it.
func cleanNick(s string) (string, bool) {
	s = strings.Join(strings.Fields(s), " ")
	if s == "" || utf8.RuneCountInString(s) > maxNick {
		return "", false
	}
	for _, r := range s {
		if !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == ' ' || r == '-' || r == '_') {
			return "", false
		}
	}
	return s, true
}

// cleanText drops the characters json-chat.js strips (form feed, CR, LF,
// bell) and other C0 controls, and trims.
func cleanText(s string) string {
	s = strings.ToValidUTF8(s, "")
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	return strings.TrimSpace(s)
}

// validAvatar: base64 of exactly 120 bytes (10x6 char/attr pairs).
func validAvatar(s string) bool {
	if s == "" || len(s) > 200 {
		return false
	}
	b, err := base64.StdEncoding.DecodeString(s)
	return err == nil && len(b) == 120
}
