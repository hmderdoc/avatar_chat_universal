package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hmderdoc/avatar_chat_universal/internal/bitmap"
	"github.com/hmderdoc/avatar_chat_universal/internal/nickstyle"
)

// BitmapEntry is one received-image record. Stored separately from the
// transcript (a notice appears in chat, but the raw [BITMAP|...] envelope
// is suppressed) and surfaced via the /img viewer modal.
type BitmapEntry struct {
	Image  *bitmap.Image
	Time   int64
	Source string // "main" for public; sender's name for private mailbox
	Sender string
	Viewed bool
}

// Session is a higher-level chat client that manages one channel of state on
// top of a Client. It subscribes, loads recent history, sends messages, and
// dispatches incoming updates to the caller via the OnMessage callback.
type Session struct {
	Client      *Client
	Self        *Nick
	Channel     string
	MotdChannel string // server channel name to pull MOTD from (default "motd")
	MaxBuffer   int    // cap on retained history

	OnMessage func(*Message) // called from Cycle for each incoming/local msg
	OnNotice  func(string)   // called for join/leave/ping notices
	// OnBackground is called for a message that arrives on a joined channel
	// other than the one being viewed. The message is counted as unread for
	// that channel and is NOT added to the transcript.
	OnBackground func(channel string, m *Message)
	// Ignore, when set, reports whether a line from sender (with body
	// text) is from someone the user ignores. Such lines, their art and
	// their join/leave notices are dropped, live and from history.
	Ignore func(sender, text string) bool

	mu          sync.Mutex
	messages    []*Message
	avatarCache map[string]string // lowercase nick -> base64 avatar
	hostCache   map[string]string // lowercase nick -> bbs/system name
	styleCache  map[string]*nickstyle.Style // lowercase nick -> last handle style seen
	nickNames   map[string]string // lowercase nick -> original casing
	// peers is the last nick seen for each name (lowercase), with its host
	// and only a valid web mailbox, so a PM by name reaches a standalone web
	// user's name@origin mailbox. lastReplyNick is the sender of the most
	// recent PM, for /r.
	peers         map[string]Nick
	lastReplyNick *Nick

	bitmapQueue    []*BitmapEntry
	bitmapMaxQueue int

	motdText      string // current MOTD line, rotated through the header
	motdTimestamp int64
	userCount     int       // last-known size of current channel
	lastReply     string    // sender of the most recent received PM (for /r)
	tuner         *Tuner    // active TV-lounge tuner for the channel (nil = off)

	// joined lists every channel we stay subscribed to, in join order; the
	// viewed one (Channel) is always among them. unread counts messages
	// that arrived on the others since they were last viewed (lowercase
	// channel name -> count).
	joined []string
	unread map[string]int
}

func NewSession(client *Client, self *Nick, channel string) *Session {
	return &Session{
		Client:         client,
		Self:           self,
		Channel:        channel,
		MotdChannel:    "motd",
		MaxBuffer:      500,
		avatarCache:    map[string]string{},
		hostCache:      map[string]string{},
		styleCache:     map[string]*nickstyle.Style{},
		nickNames:      map[string]string{},
		peers:          map[string]Nick{},
		bitmapMaxQueue: 20,
		joined:         []string{channel},
		unread:         map[string]int{},
	}
}

// peerOf is n as a PM target: name, host and its mailbox only when valid
// (a forged one is dropped, so the PM goes to the plain name).
func peerOf(n *Nick) Nick {
	p := Nick{Name: n.Name, Host: n.Host}
	if ValidMailbox(n.Name, n.Host, n.Mailbox) {
		p.Mailbox = n.Mailbox
	}
	return p
}

// ignored reports whether m is from someone the user ignores.
func (s *Session) ignored(m *Message) bool {
	if s.Ignore == nil || m == nil || m.Nick == nil || s.isSelf(m.Nick) {
		return false
	}
	return s.Ignore(m.Nick.Name, m.Str)
}

// Refilter drops already-shown lines from anyone now ignored, after the
// list changed.
func (s *Session) Refilter() {
	if s.Ignore == nil {
		return
	}
	s.mu.Lock()
	kept := s.messages[:0]
	for _, m := range s.messages {
		if m.Nick != nil && !s.isSelf(m.Nick) && s.Ignore(m.Nick.Name, m.Str) {
			continue
		}
		kept = append(kept, m)
	}
	s.messages = kept
	s.mu.Unlock()
}

// ignoredName is ignored for a bare handle (join/leave notices).
func (s *Session) ignoredName(name string) bool {
	return s.Ignore != nil && s.Ignore(name, "")
}

// HasActiveEffect reports whether any recent notice has a sweep effect
// that should still be re-rendered. The App polls this each tick to keep
// the dirty flag set during animations.
func (s *Session) HasActiveEffect() bool {
	const animMs = 1800
	now := time.Now().UnixNano() / 1000000
	s.mu.Lock()
	defer s.mu.Unlock()
	// Walk the most recent slice; effects are typically the newest msgs.
	limit := 30
	if limit > len(s.messages) {
		limit = len(s.messages)
	}
	for i := len(s.messages) - 1; i >= len(s.messages)-limit; i-- {
		m := s.messages[i]
		if m == nil {
			continue
		}
		if m.Nick != nil && m.Nick.Name != "" {
			continue // not a notice
		}
		if len(m.Str) >= 2 && m.Str[0] == 0x02 {
			if now-m.Time < animMs {
				return true
			}
		}
	}
	return false
}

// MotdText returns the latest MOTD line (call RefreshMotd to update).
func (s *Session) MotdText() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.motdText
}

// UserCount returns the cached size of the current channel.
func (s *Session) UserCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.userCount
}

// LastReplyTarget returns the nick of the most recent person to PM us, used
// by the /r command to default the recipient.
func (s *Session) LastReplyTarget() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastReply
}

// RefreshMotd pulls the most-recent message from the configured MOTD
// channel's history and stores it for the header to display. Cheap if no
// new message; safe to call frequently from a poll loop.
func (s *Session) RefreshMotd() {
	if s.Client == nil || s.MotdChannel == "" {
		return
	}
	var hist []*Message
	loc := s.locHistory(s.MotdChannel)
	if err := s.Client.Slice("chat", loc, -10, nil, LockRead, &hist); err != nil {
		return
	}
	for i := len(hist) - 1; i >= 0; i-- {
		m := hist[i]
		if m == nil || m.Str == "" {
			continue
		}
		s.mu.Lock()
		s.motdText = strings.TrimSpace(m.Str)
		s.motdTimestamp = m.Time
		s.mu.Unlock()
		return
	}
}

// Bitmaps returns a snapshot of all queued bitmap images (oldest first).
func (s *Session) Bitmaps() []*BitmapEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*BitmapEntry, len(s.bitmapQueue))
	copy(out, s.bitmapQueue)
	return out
}

// UnviewedBitmaps returns the count of bitmaps the user hasn't opened yet.
func (s *Session) UnviewedBitmaps() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, b := range s.bitmapQueue {
		if !b.Viewed {
			n++
		}
	}
	return n
}

// MarkBitmapViewed flags one queue entry as seen so the unread count
// reflects only genuinely-new images.
func (s *Session) MarkBitmapViewed(idx int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if idx >= 0 && idx < len(s.bitmapQueue) {
		s.bitmapQueue[idx].Viewed = true
	}
}

// appendBitmap pushes a decoded bitmap into the queue, evicting the
// oldest if we exceed the cap.
func (s *Session) appendBitmap(e *BitmapEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bitmapQueue = append(s.bitmapQueue, e)
	if s.bitmapMaxQueue > 0 && len(s.bitmapQueue) > s.bitmapMaxQueue {
		drop := len(s.bitmapQueue) - s.bitmapMaxQueue
		s.bitmapQueue = s.bitmapQueue[drop:]
	}
}

// CachedAvatars returns base64-encoded avatars for every user we've seen
// (including ourselves, if our Self.Avatar is set). Used to populate the
// avatars_float idle animation with real user portraits.
func (s *Session) CachedAvatars() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.avatarCache)+1)
	if s.Self != nil && s.Self.Avatar != "" {
		out = append(out, s.Self.Avatar)
	}
	for _, b64 := range s.avatarCache {
		if b64 != "" {
			out = append(out, b64)
		}
	}
	return out
}

// AvatarEntry pairs a nick with its base64-encoded avatar.
type AvatarEntry struct {
	Name   string
	Base64 string
}

// CachedAvatarsWithNames is like CachedAvatars but preserves which user
// each avatar belongs to. Used by avatars_float so colliding sprites can
// greet each other by name.
func (s *Session) CachedAvatarsWithNames() []AvatarEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]AvatarEntry, 0, len(s.avatarCache)+1)
	if s.Self != nil && s.Self.Avatar != "" {
		out = append(out, AvatarEntry{Name: s.Self.Name, Base64: s.Self.Avatar})
	}
	for key, b64 := range s.avatarCache {
		if b64 == "" {
			continue
		}
		name := s.nickNames[key]
		if name == "" {
			name = key
		}
		// Skip duplicate of self that may be in the cache too.
		if s.Self != nil && strings.EqualFold(name, s.Self.Name) {
			continue
		}
		out = append(out, AvatarEntry{Name: name, Base64: b64})
	}
	return out
}

// KnownNicks returns every distinct nickname we've seen (from messages we
// received, /who responses, etc.) in their original casing. Used to drive
// tab completion in the input line.
func (s *Session) KnownNicks() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.nickNames))
	for _, name := range s.nickNames {
		out = append(out, name)
	}
	return out
}

// AvatarFor returns the base64-encoded avatar last seen for the given nick,
// or "" if we haven't seen one. Used by the roster modal to show portraits
// of users in the channel.
func (s *Session) AvatarFor(nick string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.avatarCache[strings.ToLower(nick)]
}

// HostFor returns the BBS/system name last seen for the given nick.
func (s *Session) HostFor(nick string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hostCache[strings.ToLower(nick)]
}

// rememberNick captures avatar + host info from a sender so the roster
// modal can show their portrait even though the server's WHO only returns
// names.
func (s *Session) rememberNick(n *Nick) {
	if n == nil || n.Name == "" {
		return
	}
	key := strings.ToLower(n.Name)
	s.mu.Lock()
	// A web guest using the name of a BBS user we know doesn't take over
	// that name: its avatar, host and style stay the BBS user's, and a PM
	// to the name still reaches the BBS user.
	if prev, ok := s.peers[key]; ok && prev.Mailbox == "" && peerOf(n).Mailbox != "" {
		s.mu.Unlock()
		return
	}
	s.nickNames[key] = n.Name
	if n.Avatar != "" {
		s.avatarCache[key] = n.Avatar
	}
	if n.Host != "" {
		s.hostCache[key] = n.Host
	}
	// Every message carries the sender's current style (or none), so the
	// latest one wins, including a style that was cleared.
	s.styleCache[key] = n.Style
	s.peers[key] = peerOf(n)
	s.mu.Unlock()
}

// StyleFor returns the handle style last seen on a message from nick (our
// own from Self), or nil. Used by the roster modal.
func (s *Session) StyleFor(nick string) *nickstyle.Style {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Self != nil && strings.EqualFold(nick, s.Self.Name) {
		return s.Self.Style
	}
	return s.styleCache[strings.ToLower(nick)]
}

// Connect dials the server, subscribes to the configured channel, and loads
// recent history. Also subscribes to our own mailbox channel
// (channels.<self.name>.messages) so we receive private messages addressed
// to us, matching the JS chat's connect() at /sbbs/repo/exec/load/json-chat.js:48.
func (s *Session) Connect(ctx context.Context, historyCount int) error {
	if err := s.Client.Connect(ctx); err != nil {
		return err
	}
	loc := s.locMessages(s.Channel)
	if err := s.Client.Subscribe("chat", loc); err != nil {
		return fmt.Errorf("subscribe %s: %v", loc, err)
	}
	if s.Self != nil && s.Self.Name != "" {
		_ = s.Client.Subscribe("chat", s.locMessages(s.Self.Name))
	}
	if historyCount > 0 {
		var hist []*Message
		histLoc := s.locHistory(s.Channel)
		if err := s.Client.Slice("chat", histLoc, -historyCount, nil, LockRead, &hist); err != nil {
			// Treat history failure as non-fatal; we just lose backlog.
			if s.OnNotice != nil {
				s.OnNotice(fmt.Sprintf("history load failed: %v", err))
			}
		} else {
			var lastTime int64
			for _, m := range hist {
				if m == nil {
					continue
				}
				s.rememberNick(m.Nick)
				if s.ignored(m) {
					continue
				}
				// A tuner marker in history sets the room's current TV state
				// quietly (last one wins), so joining a tuned room enters the
				// lounge without replaying old tune/off notices.
				if s.maybeTuner(m, false) {
					continue
				}
				// History BITMAP envelopes get queued and shown as a notice,
				// just like ones that arrive live. Mark them Viewed so they
				// don't inflate the unread count when the user opens the door.
				if bitmap.IsBitmap(m.Str) {
					if img, err := bitmap.Parse(m.Str); err == nil {
						sender := ""
						if m.Nick != nil {
							sender = m.Nick.Name
						}
						s.appendBitmap(&BitmapEntry{
							Image:  img,
							Time:   m.Time,
							Source: s.Channel,
							Sender: sender,
							Viewed: true,
						})
						s.appendNotice(fmt.Sprintf("\x01R%s sent an image (%dx%d).\x01W /img to view", sender, img.Width, img.Height))
						if m.Time > lastTime {
							lastTime = m.Time
						}
						continue
					}
				}
				s.append(m)
				if m.Time > lastTime {
					lastTime = m.Time
				}
			}
			if lastTime > 0 {
				s.appendNotice(time.Unix(0, (lastTime) * 1000000).Format("\x01mLast msg:\x01M 15:04 on 01/02/2006"))
			}
		}
	}
	// Durable TV state wins over anything inferred from message history.
	s.loadTunerState(s.Channel)
	// Initial roster surfaced as a notice so users see who's online without
	// having to /who.
	s.announceRoster(s.Channel)
	s.RefreshMotd()
	return nil
}

// announceRoster fetches the WHO list for `channel`, dedupes it
// case-insensitively (matching the /who modal), updates the avatar/host
// caches, sets userCount, and pushes a colored "Users in <chan>: ..."
// notice into the transcript: labels and commas in dark gray, names in
// light gray.
func (s *Session) announceRoster(channel string) {
	if s.Client == nil {
		return
	}
	entries, err := s.Client.Who("chat", s.locMessages(channel))
	if err != nil {
		return
	}
	seen := map[string]bool{}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.Nick == "" {
			continue
		}
		key := strings.ToLower(e.Nick)
		if seen[key] {
			continue
		}
		seen[key] = true
		names = append(names, e.Nick)
		s.mu.Lock()
		s.nickNames[key] = e.Nick
		if e.System != "" {
			s.hostCache[key] = e.System
		}
		s.mu.Unlock()
	}
	// Alphabetical, case-insensitive — matches the roster modal's order.
	sort.Slice(names, func(i, j int) bool {
		return strings.ToLower(names[i]) < strings.ToLower(names[j])
	})
	s.mu.Lock()
	s.userCount = len(names)
	s.mu.Unlock()
	if len(names) == 0 {
		return
	}
	var b strings.Builder
	b.WriteString("\x01nUsers in ")
	b.WriteString(channel)
	b.WriteString(":")
	for i, n := range names {
		if i > 0 {
			b.WriteString("\x01n,")
		}
		b.WriteString(" \x01w")
		b.WriteString(n)
	}
	s.appendNotice(b.String())
}

// IsAlive reports whether the underlying chat connection is currently up.
func (s *Session) IsAlive() bool {
	if s.Client == nil {
		return false
	}
	return s.Client.IsAlive()
}

// Redial drops the current TCP connection and dials again. It only touches
// the socket, so it may run off the main loop while the screen stays live;
// call Resume on the main loop once it succeeds.
func (s *Session) Redial(ctx context.Context) error {
	return s.Client.Reconnect(ctx)
}

// Resume re-subscribes to every joined channel and our personal mailbox
// after Redial, shows what was said in the viewed channel while we were
// gone, and refreshes the roster. lostAt is when the drop was noticed; it
// bounds the catch-up when none of our messages are in the recent history.
// The local message buffer is preserved so the user doesn't lose history.
func (s *Session) Resume(lostAt time.Time) error {
	for _, ch := range s.Joined() {
		if err := s.Client.Subscribe("chat", s.locMessages(ch)); err != nil {
			return fmt.Errorf("resubscribe %s: %v", ch, err)
		}
	}
	if s.Self != nil && s.Self.Name != "" {
		_ = s.Client.Subscribe("chat", s.locMessages(s.Self.Name))
	}
	s.catchUp(lostAt)
	s.announceRoster(s.Channel)
	return nil
}

// mailboxCount is how much of our mailbox history LoadMailbox reads.
const mailboxCount = 50

// LoadMailbox shows the private messages that reached our mailbox's
// history after since (epoch ms): the ones sent while no door of ours was
// running. It returns how many it showed.
func (s *Session) LoadMailbox(since int64) int {
	if s.Self == nil || s.Self.Name == "" {
		return 0
	}
	var hist []*Message
	if err := s.Client.Slice("chat", s.locHistory(s.Self.Name), -mailboxCount, nil, LockRead, &hist); err != nil {
		return 0
	}
	var waiting []*Message
	for _, m := range hist {
		if m != nil && m.Nick != nil && !s.isSelf(m.Nick) && m.Time > since {
			waiting = append(waiting, m)
		}
	}
	if len(waiting) == 0 {
		return 0
	}
	what := "private messages"
	if len(waiting) == 1 {
		what = "private message"
	}
	s.appendNotice(fmt.Sprintf("\x01y%d %s while you were away:", len(waiting), what))
	for _, m := range waiting {
		s.handleUpdate(&Packet{Oper: "WRITE", Location: s.locMessages(s.Self.Name), Data: mustJSON(m)})
	}
	return len(waiting)
}

// catchUpCount is how much of the viewed channel's history Resume reads.
const catchUpCount = 100

// msgKey identifies a message across the live feed and history.
func msgKey(m *Message) string {
	name := ""
	if m.Nick != nil {
		name = m.Nick.Name
	}
	return fmt.Sprintf("%s\x00%d\x00%s", name, m.Time, m.Str)
}

// catchUp shows the messages that reached the viewed channel's history
// after the newest one we already have, or, when none of ours is in that
// stretch, the ones sent since lostAt.
func (s *Session) catchUp(lostAt time.Time) {
	var hist []*Message
	if err := s.Client.Slice("chat", s.locHistory(s.Channel), -catchUpCount, nil, LockRead, &hist); err != nil {
		s.appendNotice(fmt.Sprintf("couldn't load missed messages: %v", err))
		return
	}
	have := map[string]bool{}
	s.mu.Lock()
	for _, m := range s.messages {
		if m.Nick != nil {
			have[msgKey(m)] = true
		}
	}
	s.mu.Unlock()
	start := -1
	for i, m := range hist {
		if m != nil && have[msgKey(m)] {
			start = i + 1
		}
	}
	since := lostAt.UnixNano() / 1000000
	for i, m := range hist {
		if m == nil || have[msgKey(m)] {
			continue
		}
		if start >= 0 && i < start || start < 0 && m.Time < since {
			continue
		}
		s.handleUpdate(&Packet{Oper: "WRITE", Location: s.locMessages(s.Channel), Data: mustJSON(m)})
	}
}

func mustJSON(v interface{}) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// Close unsubscribes and disconnects.
func (s *Session) Close() error {
	if s.Client == nil {
		return nil
	}
	for _, ch := range s.Joined() {
		_ = s.Client.Unsubscribe("chat", s.locMessages(ch))
	}
	return s.Client.Close()
}

// Send broadcasts a chat message to the current channel. Our avatar (if set
// on Self.Avatar) is attached so receiving clients can render it.
func (s *Session) Send(text string) error {
	text = sanitizeText(text)
	if text == "" {
		return nil
	}
	msg := &Message{
		Nick: s.Self,
		Str:  text,
		Time: time.Now().UnixNano() / 1000000,
	}
	if err := s.Client.Write("chat", s.locMessages(s.Channel), msg, LockWrite); err != nil {
		return err
	}
	if err := s.Client.Push("chat", s.locHistory(s.Channel), msg, LockWrite); err != nil {
		// Push is best-effort; we already broadcast.
		if s.OnNotice != nil {
			s.OnNotice(fmt.Sprintf("history push failed: %v", err))
		}
	}
	s.append(msg)
	if s.OnMessage != nil {
		s.OnMessage(msg)
	}
	return nil
}

// SendPrivate addresses a message to a specific user's mailbox. Mirrors the
// JS door's /private flow at avatar_chat.js:2780-2782:
//   - WRITE to the recipient's messages channel (live delivery)
//   - PUSH to the recipient's history (so they see it later)
//   - locally append with Private=true so the sender sees their own PM
func (s *Session) SendPrivate(recipient, text string) error {
	if recipient == "" {
		return nil
	}
	s.mu.Lock()
	p, ok := s.peers[strings.ToLower(recipient)]
	s.mu.Unlock()
	if !ok {
		p = Nick{Name: recipient}
	}
	return s.sendPrivateTo(recipient, p, text)
}

// ReplyPrivate answers the most recent PM (/r): to that sender's mailbox
// when they are a standalone web user, else their plain name.
func (s *Session) ReplyPrivate(text string) error {
	s.mu.Lock()
	p := s.lastReplyNick
	name := s.lastReply
	s.mu.Unlock()
	if p == nil {
		return s.SendPrivate(name, text)
	}
	return s.sendPrivateTo(p.Name, *p, text)
}

// sendPrivateTo sends to peer: its valid mailbox (a standalone web user),
// else the plain-name mailbox `recipient` as typed. The local echo names
// the plain recipient; a mailbox string is never shown.
func (s *Session) sendPrivateTo(recipient string, peer Nick, text string) error {
	text = sanitizeText(text)
	if text == "" || recipient == "" {
		return nil
	}
	dest := recipient
	if ValidMailbox(peer.Name, peer.Host, peer.Mailbox) {
		dest = peer.Mailbox
	}
	msg := &Message{
		Nick: s.Self,
		Str:  text,
		Time: time.Now().UnixNano() / 1000000,
	}
	loc := s.locMessages(dest)
	if err := s.Client.Write("chat", loc, msg, LockWrite); err != nil {
		return err
	}
	_ = s.Client.Push("chat", s.locHistory(dest), msg, LockWrite)

	// Local view: a PM from us, addressed to <recipient>. We tweak the
	// displayed string to show the destination so the sender knows where
	// it went, since they don't see anything in the public channel.
	local := *msg
	local.Private = true
	local.Str = "->" + recipient + ": " + text
	s.append(&local)
	if s.OnMessage != nil {
		s.OnMessage(&local)
	}
	// Nobody reading that mailbox: say so, so a PM to a mistyped or
	// offline name isn't taken as delivered.
	if who, err := s.Client.Who("chat", loc); err == nil && len(who) == 0 {
		s.appendNotice(fmt.Sprintf("%s isn't online; they'll see it next time they're here.", recipient))
	}
	return nil
}

// Action broadcasts a /me-style action to the current channel. Matches the
// JS chat's action() shape (json-chat.js:205-211): nick=nil so the message
// renders as a notice, str="<sender-name> <action>".
func (s *Session) Action(text string) error {
	text = sanitizeText(text)
	if text == "" || s.Self == nil {
		return nil
	}
	msg := &Message{
		Nick: nil,
		Str:  s.Self.Name + " " + text,
		Time: time.Now().UnixNano() / 1000000,
	}
	if err := s.Client.Write("chat", s.locMessages(s.Channel), msg, LockWrite); err != nil {
		return err
	}
	if err := s.Client.Push("chat", s.locHistory(s.Channel), msg, LockWrite); err != nil {
		if s.OnNotice != nil {
			s.OnNotice(fmt.Sprintf("history push failed: %v", err))
		}
	}
	s.append(msg)
	if s.OnMessage != nil {
		s.OnMessage(msg)
	}
	return nil
}

// Cycle drains one batch of pending updates from the underlying client and
// dispatches them via OnMessage / OnNotice. Call this from your main poll
// loop, e.g. every 25-50ms.
func (s *Session) Cycle() {
	for {
		select {
		case pkt, ok := <-s.Client.Updates():
			if !ok {
				return
			}
			s.handleUpdate(pkt)
		default:
			return
		}
	}
}

func (s *Session) handleUpdate(pkt *Packet) {
	// Locations look like "channels.<name>.messages".
	parts := strings.Split(pkt.Location, ".")
	if len(parts) < 3 || parts[0] != "channels" {
		return
	}
	channel := parts[1]
	leaf := parts[2]

	// A joined channel we're not looking at: count it, don't show it.
	if !strings.EqualFold(channel, s.Channel) && !s.isSelfName(channel) {
		s.handleBackground(channel, leaf, pkt)
		return
	}

	switch strings.ToUpper(pkt.Oper) {
	case "WRITE":
		if leaf != "messages" {
			return
		}
		var msg Message
		if err := json.Unmarshal(pkt.Data, &msg); err != nil {
			return
		}
		// Mark messages arriving on our own mailbox channel as private.
		if s.Self != nil && strings.EqualFold(channel, s.Self.Name) {
			msg.Private = true
		}
		// Don't echo our own messages back — Send already appended.
		if msg.Nick != nil && s.Self != nil && msg.Nick.Name == s.Self.Name && msg.Time > 0 {
			if s.recentSelf(&msg) {
				s.rememberNick(msg.Nick)
				return
			}
		}
		s.rememberNick(msg.Nick)
		if s.ignored(&msg) {
			return
		}

		// TVTUNER control markers: apply (enter/exit lounge) + notice, and
		// suppress the raw marker from the transcript.
		if s.maybeTuner(&msg, true) {
			return
		}

		// Track most recent PM sender so /r works.
		if msg.Private && msg.Nick != nil && !s.isSelf(msg.Nick) {
			p := peerOf(msg.Nick)
			s.mu.Lock()
			s.lastReply = msg.Nick.Name
			s.lastReplyNick = &p
			s.mu.Unlock()
		}

		// BITMAP envelopes: queue the decoded image, push a placeholder
		// notice into the transcript, and skip appending the raw text.
		if bitmap.IsBitmap(msg.Str) {
			if img, err := bitmap.Parse(msg.Str); err == nil {
				sender := ""
				if msg.Nick != nil {
					sender = msg.Nick.Name
				}
				s.appendBitmap(&BitmapEntry{
					Image:  img,
					Time:   msg.Time,
					Source: channel,
					Sender: sender,
				})
				notice := fmt.Sprintf("\x01R%s sent an image (%dx%d).\x01W /img to view", sender, img.Width, img.Height)
				s.appendNotice(notice)
				return
			}
		}

		s.append(&msg)
		if s.OnMessage != nil {
			s.OnMessage(&msg)
		}
	case "SUBSCRIBE":
		var info struct {
			Nick   string `json:"nick"`
			System string `json:"system"`
		}
		_ = json.Unmarshal(pkt.Data, &info)
		if info.Nick != "" && !s.isSelfPresence(info.Nick, info.System) {
			// \x02j marks this notice for the green-glow L→R sweep
			// effect that the transcript renderer animates for ~2s.
			if !s.ignoredName(info.Nick) {
				s.appendNotice("\x02j" + info.Nick + " is here.")
			}
			s.mu.Lock()
			s.userCount++
			s.mu.Unlock()
		}
	case "UNSUBSCRIBE":
		var info struct {
			Nick   string `json:"nick"`
			System string `json:"system"`
		}
		_ = json.Unmarshal(pkt.Data, &info)
		if info.Nick != "" && !s.isSelfPresence(info.Nick, info.System) {
			// \x02l marks this for the red-glow R→L sweep effect.
			if !s.ignoredName(info.Nick) {
				s.appendNotice("\x02l" + info.Nick + " has left.")
			}
			s.mu.Lock()
			if s.userCount > 0 {
				s.userCount--
			}
			s.mu.Unlock()
		}
	}
}

// appendNotice records a system message in the transcript (no nick,
// renders as the "* foo" notice line). Caller should already hold no
// locks — append takes the mutex itself.
//
// The text may contain \x01<code> color hints that the transcript
// renderer interprets:
//
//	\x01n  default notice color (dark gray)
//	\x01w  bright white
//	\x01r  light red
//	\x01y  yellow
//	\x01c  light cyan
//	\x01m  light magenta
//	\x01g  light green
func (s *Session) appendNotice(text string) {
	msg := &Message{Nick: nil, Str: text, Time: time.Now().UnixNano() / 1000000}
	s.append(msg)
	if s.OnMessage != nil {
		s.OnMessage(msg)
	}
}

// Notice is the public alias for appendNotice. Used by the App to surface
// command results / errors into the transcript instead of a status bar.
func (s *Session) Notice(text string) {
	s.appendNotice(text)
}

// SameUser reports whether n is the user self describes. The name alone
// isn't enough: a web guest can take a BBS user's name, but web messages
// carry a name@origin mailbox and door messages never do; and a user of
// the same name on another BBS has another host.
func SameUser(self, n *Nick) bool {
	if self == nil || n == nil || !strings.EqualFold(self.Name, n.Name) || n.Mailbox != "" {
		return false
	}
	return n.Host == "" || self.Host == "" || strings.EqualFold(n.Host, self.Host)
}

// isSelf reports whether n is us (see SameUser).
func (s *Session) isSelf(n *Nick) bool { return SameUser(s.Self, n) }

// isSelfPresence reports whether a SUBSCRIBE/UNSUBSCRIBE by nick from
// system is our own door's.
func (s *Session) isSelfPresence(nick, system string) bool {
	if !s.isSelfName(nick) {
		return false
	}
	return system == "" || s.Self.Host == "" || strings.EqualFold(system, s.Self.Host)
}

func (s *Session) isSelfName(name string) bool {
	if s.Self == nil {
		return false
	}
	return strings.EqualFold(s.Self.Name, name)
}

// Clear empties the local message buffer (does not affect server history).
func (s *Session) Clear() {
	s.mu.Lock()
	s.messages = nil
	s.mu.Unlock()
}

// Who returns the current channel's subscriber list from the server. As a
// side effect, every entry's system name is captured into the host cache
// so the roster modal has BBS info even for users we haven't seen messages
// from.
func (s *Session) Who() ([]string, error) {
	if s.Client == nil {
		return nil, fmt.Errorf("session: no client")
	}
	entries, err := s.Client.Who("chat", s.locMessages(s.Channel))
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.Nick == "" {
			continue
		}
		key := strings.ToLower(e.Nick)
		s.mu.Lock()
		s.nickNames[key] = e.Nick
		if e.System != "" {
			s.hostCache[key] = e.System
		}
		s.mu.Unlock()
		out = append(out, e.Nick)
	}
	return out, nil
}

// JoinChannel switches the viewed channel. The channel we leave stays
// joined (still subscribed, its new messages counted as unread) until
// Leave; a channel already joined is just switched to. Local history is
// cleared and reloaded from the server, and Send goes to the new channel.
func (s *Session) JoinChannel(channel string, historyCount int) error {
	if channel == "" {
		return fmt.Errorf("session: empty channel name")
	}
	if strings.EqualFold(channel, s.Channel) {
		return fmt.Errorf("already in %s", s.Channel)
	}
	if s.isSelfName(channel) {
		return fmt.Errorf("%s is your private mailbox, not a channel", channel)
	}
	if known := s.joinedName(channel); known != "" {
		channel = known
	} else {
		if strings.Contains(channel, ".") {
			return fmt.Errorf("room names can't contain dots")
		}
		if s.isMailbox(channel) {
			return fmt.Errorf("%s is a user's private mailbox, not a room (use /msg)", channel)
		}
		if err := s.Client.Subscribe("chat", s.locMessages(channel)); err != nil {
			return err
		}
		s.mu.Lock()
		s.joined = append(s.joined, channel)
		s.mu.Unlock()
	}
	s.mu.Lock()
	s.Channel = channel
	s.messages = nil
	s.tuner = nil // each channel has its own TV state; history below re-sets it
	delete(s.unread, strings.ToLower(channel))
	// Most recently viewed last, so /part falls back to the previous one.
	s.joined = moveToEnd(s.joined, channel)
	s.mu.Unlock()
	if historyCount > 0 {
		var hist []*Message
		var lastTime int64
		if err := s.Client.Slice("chat", s.locHistory(channel), -historyCount, nil, LockRead, &hist); err == nil {
			for _, m := range hist {
				if m == nil {
					continue
				}
				s.rememberNick(m.Nick)
				if s.ignored(m) {
					continue
				}
				if s.maybeTuner(m, false) {
					continue
				}
				s.append(m)
				if m.Time > lastTime {
					lastTime = m.Time
				}
			}
		}
		if lastTime > 0 {
			s.appendNotice(time.Unix(0, (lastTime) * 1000000).Format("\x01mLast msg:\x01M 15:04 on 01/02/2006"))
		}
	}
	// Durable TV state wins over anything inferred from message history.
	s.loadTunerState(channel)
	s.announceRoster(channel)
	if s.OnNotice != nil {
		s.OnNotice(fmt.Sprintf("joined %s", channel))
	}
	return nil
}

// Leave unsubscribes from a joined channel that is not the one being
// viewed. Use PartCurrent for the viewed channel.
func (s *Session) Leave(channel string) error {
	name := s.joinedName(channel)
	if name == "" {
		return fmt.Errorf("not in %s", channel)
	}
	if strings.EqualFold(name, s.Channel) {
		return fmt.Errorf("%s is the channel you're in; use /part", name)
	}
	_ = s.Client.Unsubscribe("chat", s.locMessages(name))
	s.mu.Lock()
	s.joined = removeName(s.joined, name)
	delete(s.unread, strings.ToLower(name))
	s.mu.Unlock()
	return nil
}

// PartCurrent leaves the viewed channel and switches to the most recently
// viewed other joined channel, returning its name. It returns "" and
// changes nothing when the viewed channel is the only one joined: the
// caller decides what parting the last channel means (the door exits).
func (s *Session) PartCurrent(historyCount int) (string, error) {
	s.mu.Lock()
	next := ""
	for i := len(s.joined) - 1; i >= 0; i-- {
		if !strings.EqualFold(s.joined[i], s.Channel) {
			next = s.joined[i]
			break
		}
	}
	s.mu.Unlock()
	if next == "" {
		return "", nil
	}
	old := s.Channel
	if err := s.JoinChannel(next, historyCount); err != nil {
		return "", err
	}
	_ = s.Client.Unsubscribe("chat", s.locMessages(old))
	s.mu.Lock()
	s.joined = removeName(s.joined, old)
	s.mu.Unlock()
	return next, nil
}

// Joined returns the joined channels in join order (most recently viewed
// last).
func (s *Session) Joined() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.joined))
	copy(out, s.joined)
	return out
}

// Unread returns how many messages arrived on a joined channel since it
// was last viewed.
func (s *Session) Unread(channel string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.unread[strings.ToLower(channel)]
}

// UnreadTotal sums Unread over every joined channel.
func (s *Session) UnreadTotal() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, c := range s.unread {
		n += c
	}
	return n
}

// RoomInfo is one row of the /channels list.
type RoomInfo struct {
	Name    string
	Users   int // -1 when the server didn't say
	Unread  int
	Joined  bool
	Current bool
}

// roomListCap bounds the WHO round trips a /channels listing makes.
const roomListCap = 40

// isMailbox reports whether name, which isn't a registered public room, is
// a user's private mailbox: a name we've seen someone use, or a channel
// whose own namesake is subscribed to it. A member's PMs live in
// channels.<alias>, so joining it as a room would show them to anyone.
func (s *Session) isMailbox(name string) bool {
	if keys, err := s.Client.Keys("chat", "public_channels"); err == nil {
		for _, k := range keys {
			if strings.EqualFold(k, name) {
				return false
			}
		}
	}
	s.mu.Lock()
	_, seen := s.nickNames[strings.ToLower(name)]
	s.mu.Unlock()
	if seen {
		return true
	}
	entries, _ := s.Client.Who("chat", s.locMessages(name))
	for _, e := range entries {
		if strings.EqualFold(e.Nick, name) {
			return true
		}
	}
	return false
}

// Rooms lists the joined channels plus every public room the server
// registers under public_channels (the registry the web chat writes when a
// room is created), with live user counts. The MOTD channel is hidden.
// Blocking: one KEYS plus one WHO per room, bounded by roomListCap.
func (s *Session) Rooms() []RoomInfo {
	seen := map[string]bool{}
	var names []string
	add := func(n string) {
		n = strings.TrimSpace(n)
		key := strings.ToLower(n)
		if n == "" || seen[key] || strings.EqualFold(n, s.MotdChannel) || s.isSelfName(n) {
			return
		}
		seen[key] = true
		names = append(names, n)
	}
	for _, n := range s.Joined() {
		add(n)
	}
	if s.Client != nil {
		if keys, err := s.Client.Keys("chat", "public_channels"); err == nil {
			sort.Slice(keys, func(i, j int) bool { return strings.ToLower(keys[i]) < strings.ToLower(keys[j]) })
			for _, k := range keys {
				add(k)
			}
		}
	}
	out := make([]RoomInfo, 0, len(names))
	for i, n := range names {
		r := RoomInfo{Name: n, Users: -1, Unread: s.Unread(n), Joined: s.joinedName(n) != "", Current: strings.EqualFold(n, s.Channel)}
		if i < roomListCap && s.Client != nil {
			if entries, err := s.Client.Who("chat", s.locMessages(n)); err == nil {
				r.Users = countDistinctNicks(entries)
			}
		}
		out = append(out, r)
	}
	// Joined rooms first (current at the top), then the rest alphabetically.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Current != out[j].Current {
			return out[i].Current
		}
		if out[i].Joined != out[j].Joined {
			return out[i].Joined
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

// handleBackground deals with traffic on a joined channel that isn't being
// viewed: messages are counted (and handed to OnBackground), presence and
// TV markers are ignored until the user switches there.
func (s *Session) handleBackground(channel, leaf string, pkt *Packet) {
	if leaf != "messages" || !strings.EqualFold(strings.ToUpper(pkt.Oper), "WRITE") {
		return
	}
	name := s.joinedName(channel)
	if name == "" {
		return
	}
	var msg Message
	if err := json.Unmarshal(pkt.Data, &msg); err != nil {
		return
	}
	if msg.Nick == nil || msg.Nick.Name == "" || s.isSelf(msg.Nick) || IsTunerMarker(msg.Str) {
		return
	}
	s.rememberNick(msg.Nick)
	if s.ignored(&msg) {
		return
	}
	s.mu.Lock()
	s.unread[strings.ToLower(name)]++
	s.mu.Unlock()
	if s.OnBackground != nil {
		s.OnBackground(name, &msg)
	}
}

// joinedName returns the joined channel matching name case-insensitively,
// in its joined spelling, or "".
func (s *Session) joinedName(name string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, j := range s.joined {
		if strings.EqualFold(j, name) {
			return j
		}
	}
	return ""
}

func countDistinctNicks(entries []WhoEntry) int {
	seen := map[string]bool{}
	for _, e := range entries {
		if e.Nick != "" {
			seen[strings.ToLower(e.Nick)] = true
		}
	}
	return len(seen)
}

func removeName(list []string, name string) []string {
	out := list[:0]
	for _, n := range list {
		if !strings.EqualFold(n, name) {
			out = append(out, n)
		}
	}
	return out
}

func moveToEnd(list []string, name string) []string {
	return append(removeName(list, name), name)
}

// Messages returns a snapshot of the buffered messages (oldest first).
func (s *Session) Messages() []*Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Message, len(s.messages))
	copy(out, s.messages)
	return out
}

func (s *Session) append(m *Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, m)
	if len(s.messages) > s.MaxBuffer {
		drop := len(s.messages) - s.MaxBuffer
		s.messages = s.messages[drop:]
	}
}

func (s *Session) recentSelf(msg *Message) bool {
	now := time.Now().UnixNano() / 1000000
	if now-msg.Time > 5000 {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.messages) - 1; i >= 0 && i >= len(s.messages)-5; i-- {
		m := s.messages[i]
		if m.Nick != nil && m.Nick.Name == msg.Nick.Name && m.Str == msg.Str {
			return true
		}
	}
	return false
}

func (s *Session) locMessages(channel string) string { return "channels." + channel + ".messages" }
func (s *Session) locHistory(channel string) string  { return "channels." + channel + ".history" }

// locTuner is a dedicated location holding only TV tune/off events, so the
// current TV state survives no matter how much chat scrolls past in .history.
func (s *Session) locTuner(channel string) string { return "channels." + channel + ".tvtuner" }

// sanitizeText strips form-feed, CR, LF, and bell — same set as
// json-chat.js:302's Message constructor.
func sanitizeText(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '\f', '\r', '\n', '\b', 0x07, 0x14, 0x15, 0x10:
			continue
		}
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}
