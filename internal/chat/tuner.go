package chat

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// TV-lounge "tuner" control marker. A user points the channel at a
// telnetvision feed with /tvtuner; we broadcast a marker into the channel so
// every client in the room can pick it up and enter TV lounge mode. Modeled on
// the [BITMAP|...] convention: a normal chat message whose body is a marker,
// detected and suppressed from the visible transcript.
//
//	[TVTUNER|<host>|<port>|<channel>]   tune the room to host:port / channel
//	[TVTUNER|off]                       turn the TV off

const tunerPrefix = "[TVTUNER|"

// Tuner identifies a telnetvision feed the channel is pointed at.
type Tuner struct {
	Host    string
	Port    int
	Channel string
	By      string // nick that tuned the room (for the notice)
}

// IsTunerMarker reports whether s is a TVTUNER control marker.
func IsTunerMarker(s string) bool {
	return strings.HasPrefix(s, tunerPrefix) && strings.HasSuffix(s, "]") && len(s) > len(tunerPrefix)+1
}

// ParseTunerMarker decodes a TVTUNER marker. off is true for [TVTUNER|off].
func ParseTunerMarker(s string) (t *Tuner, off bool, ok bool) {
	if !IsTunerMarker(s) {
		return nil, false, false
	}
	parts := strings.Split(s[1:len(s)-1], "|") // strip [ ]
	if len(parts) < 2 || parts[0] != "TVTUNER" {
		return nil, false, false
	}
	if strings.EqualFold(strings.TrimSpace(parts[1]), "off") {
		return nil, true, true
	}
	if len(parts) != 4 {
		return nil, false, false
	}
	host := strings.TrimSpace(parts[1])
	port, err := strconv.Atoi(strings.TrimSpace(parts[2]))
	channel := strings.TrimSpace(parts[3])
	if host == "" || channel == "" || err != nil || port <= 0 || port > 65535 {
		return nil, false, false
	}
	return &Tuner{Host: host, Port: port, Channel: channel}, false, true
}

// FormatTuner builds a tune marker; FormatTunerOff builds the off marker.
func FormatTuner(host string, port int, channel string) string {
	return fmt.Sprintf("%s%s|%d|%s]", tunerPrefix, host, port, channel)
}
func FormatTunerOff() string { return tunerPrefix + "off]" }

// Tuner returns a copy of the channel's active tuner, or nil if none.
func (s *Session) Tuner() *Tuner {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tuner == nil {
		return nil
	}
	t := *s.tuner
	return &t
}

// maybeTuner applies msg if it's a tuner marker and returns true so the caller
// skips appending the raw marker to the transcript. announce=false stays quiet
// (used when replaying history so old markers don't spam notices).
func (s *Session) maybeTuner(msg *Message, announce bool) bool {
	if msg == nil || !IsTunerMarker(msg.Str) {
		return false
	}
	t, off, ok := ParseTunerMarker(msg.Str)
	if !ok {
		return false
	}
	by := ""
	if msg.Nick != nil {
		by = msg.Nick.Name
	}
	s.applyTuner(t, off, by, announce)
	return true
}

func (s *Session) applyTuner(t *Tuner, off bool, by string, announce bool) {
	s.mu.Lock()
	if off {
		s.tuner = nil
	} else if t != nil {
		t.By = by
		s.tuner = t
	}
	s.mu.Unlock()
	if !announce {
		return
	}
	who := by
	if who == "" {
		who = "Someone"
	}
	if off {
		s.appendNotice("\x01y" + who + " turned off the TV.")
	} else if t != nil {
		s.appendNotice(fmt.Sprintf("\x01c%s tuned the room to %s:%d/%s\x01n  (/tvoff to leave, /tvon to rejoin)", who, t.Host, t.Port, t.Channel))
	}
}

// SetTuner tunes the channel: broadcasts the marker to everyone and applies it
// locally. We apply directly because our own echoed marker is suppressed by
// recentSelf, so we can't rely on it coming back to us.
func (s *Session) SetTuner(host string, port int, channel string) error {
	if err := s.broadcastMarker(FormatTuner(host, port, channel)); err != nil {
		return err
	}
	by := ""
	if s.Self != nil {
		by = s.Self.Name
	}
	s.applyTuner(&Tuner{Host: host, Port: port, Channel: channel}, false, by, true)
	return nil
}

// ClearTuner turns the channel's TV off for everyone.
func (s *Session) ClearTuner() error {
	if err := s.broadcastMarker(FormatTunerOff()); err != nil {
		return err
	}
	by := ""
	if s.Self != nil {
		by = s.Self.Name
	}
	s.applyTuner(nil, true, by, true)
	return nil
}

// broadcastMarker writes a control marker to the channel without appending it
// to our own transcript (it isn't a chat line). It goes three places:
//   - .messages: live, so everyone in the room reacts immediately
//   - .tvtuner:  durable state — a dedicated location only tune/off events use,
//     so the current TV state survives any amount of chat scrolling past
func (s *Session) broadcastMarker(marker string) error {
	msg := &Message{Nick: s.Self, Str: marker, Time: time.Now().UnixNano() / 1000000}
	if err := s.Client.Write("chat", s.locMessages(s.Channel), msg, LockWrite); err != nil {
		return err
	}
	// Durable state, written so SLICE can read it back on join. Two server
	// behaviors to satisfy: the Go chatserver only slices PUSH-built arrays
	// (WRITE goes to a scalar map), while Synchronet json-db rejects a PUSH to
	// a missing record ("Record not found"). The portable idiom (same as the
	// JS door's ensureHistoryArray) is: WRITE [] to create/reset the record,
	// then PUSH the latest event. Net result on both: the record holds exactly
	// the most recent tune/off.
	loc := s.locTuner(s.Channel)
	_ = s.Client.Write("chat", loc, []*Message{}, LockWrite)
	_ = s.Client.Push("chat", loc, msg, LockWrite)
	return nil
}

// loadTunerState reads the channel's durable TV state from the dedicated
// .tvtuner location (the last tune/off event) and applies it. Used on connect
// and channel switch so joining a tuned room enters the lounge regardless of
// how busy the chat has been since it was tuned. Quiet (no notice replay).
func (s *Session) loadTunerState(channel string) {
	var last []*Message
	if err := s.Client.Slice("chat", s.locTuner(channel), -1, nil, LockRead, &last); err != nil {
		return
	}
	for _, m := range last {
		if m != nil {
			s.maybeTuner(m, false)
		}
	}
}
