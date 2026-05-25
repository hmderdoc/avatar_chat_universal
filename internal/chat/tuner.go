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
// to our own transcript (it isn't a chat line).
func (s *Session) broadcastMarker(marker string) error {
	msg := &Message{Nick: s.Self, Str: marker, Time: time.Now().UnixNano() / 1000000}
	if err := s.Client.Write("chat", s.locMessages(s.Channel), msg, LockWrite); err != nil {
		return err
	}
	_ = s.Client.Push("chat", s.locHistory(s.Channel), msg, LockWrite)
	return nil
}
