// Package theme owns the color palette and screensaver profile for the
// avatar_chat_universal door. The default theme ("futurewave") matches the
// out-of-the-box look; sysops can ship custom themes by placing a
// themes/<name>.ini file alongside the door binary and pointing the main
// config's `theme = <name>` key at it.
//
// Theme files are flat key=value INIs (same syntax as avatar_chat.ini).
// Every key is optional -- unset keys inherit from Default(). Color values
// are pipe-separated combinations of CGA names: "lightcyan|bgmagenta",
// "white", "yellow|bgblue", etc. See ParseColor for the full list.
package theme

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
)

// Theme groups every color slot the chat UI knows about plus optional
// screensaver overrides. A theme can leave the idle fields nil/empty to
// inherit the main config's values; setting them lets a theme ship a
// curated screensaver profile.
type Theme struct {
	Name string

	// Header bar (MOTD strip across the top).
	HeaderStats ansi.Attr // "MOTD :" label
	HeaderMotd  ansi.Attr // MOTD body text

	// Action bars (the rows of /command pills).
	TopActionBar          ansi.Attr // background fill of the top row
	TopActionPill         ansi.Attr // pill text (e.g. "/who")
	TopActionHighlight    ansi.Attr // unread-image flash, etc.
	BottomActionBar       ansi.Attr
	BottomActionPill      ansi.Attr
	BottomActionHighlight ansi.Attr

	// Input line (the row where the user types).
	InputPrompt ansi.Attr
	InputText   ansi.Attr
	InputCursor ansi.Attr

	// Message bubbles.
	BubbleLeft    ansi.Attr // others' messages
	BubbleSelf    ansi.Attr // own messages
	BubblePrivate ansi.Attr // private (PM) messages, regardless of side

	// Bubble header line (speaker, timestamp, BBS host).
	SpeakerLeft ansi.Attr
	SpeakerSelf ansi.Attr
	Timestamp   ansi.Attr
	Host        ansi.Attr

	// Notice rendering (non-bubble status lines like "* Last msg ..." and
	// "* Users in main: ...").
	NoticeDefault ansi.Attr

	// Modal frames (roster, image viewer, avatar selector chrome).
	ModalBg    ansi.Attr
	ModalTitle ansi.Attr

	// Screensaver / idle profile. nil/empty means "inherit from main config".
	IdleRandom   *bool
	IdleSequence []string
	IdleDisable  []string
}

// Default returns the built-in "futurewave" theme: the look the door has
// shipped with from day one. All other themes load on top of this, so any
// key the sysop omits stays consistent.
func Default() *Theme {
	return &Theme{
		Name: "futurewave",

		HeaderStats: ansi.Black | ansi.BgGreen,
		HeaderMotd:  ansi.White | ansi.BgGreen,

		TopActionBar:          ansi.White | ansi.BgBlue,
		TopActionPill:         ansi.LightCyan | ansi.BgMagenta,
		TopActionHighlight:    ansi.White | ansi.BgRed,
		BottomActionBar:       ansi.White | ansi.BgMagenta,
		BottomActionPill:      ansi.Black | ansi.BgCyan,
		BottomActionHighlight: ansi.White | ansi.BgRed,

		InputPrompt: ansi.Yellow,
		InputText:   ansi.LightGreen,
		InputCursor: ansi.LightGray | ansi.BgLightGray,

		BubbleLeft:    ansi.Black | ansi.BgCyan,
		BubbleSelf:    ansi.White | ansi.BgBlue,
		BubblePrivate: ansi.White | ansi.BgRed,

		SpeakerLeft: ansi.LightMagenta,
		SpeakerSelf: ansi.LightCyan,
		Timestamp:   ansi.LightBlue,
		Host:        ansi.Magenta,

		NoticeDefault: ansi.DarkGray,

		ModalBg:    ansi.LightGray | ansi.BgBlack,
		ModalTitle: ansi.Yellow | ansi.BgBlue,
	}
}

// Load reads the theme INI at path, applying overrides on top of Default().
// A missing file returns Default() with no error so sysops can drop a theme
// in later without restarting; only parse errors are returned.
func Load(path string) (*Theme, error) {
	t := Default()
	if path == "" {
		return t, nil
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return t, nil
		}
		return nil, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "[") {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(line[:eq]))
		val := strings.TrimSpace(line[eq+1:])
		applyKey(t, key, val)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("theme: %s: %v", path, err)
	}
	return t, nil
}

func applyKey(t *Theme, key, val string) {
	// Color slots.
	if attrPtr := colorSlot(t, key); attrPtr != nil {
		if a, ok := ParseColor(val); ok {
			*attrPtr = a
		}
		return
	}
	// Idle / screensaver overrides.
	switch key {
	case "name":
		t.Name = val
	case "idle_random":
		b := parseBool(val)
		t.IdleRandom = &b
	case "idle_sequence":
		t.IdleSequence = splitCSV(val)
	case "idle_disable":
		t.IdleDisable = splitCSV(val)
	}
}

// colorSlot returns a pointer to the named ansi.Attr field on t, or nil if
// the key isn't a known color slot. Centralizing this here keeps Load and
// any future "show defaults" command in sync.
func colorSlot(t *Theme, key string) *ansi.Attr {
	switch key {
	case "header_stats":
		return &t.HeaderStats
	case "header_motd":
		return &t.HeaderMotd
	case "top_action_bar":
		return &t.TopActionBar
	case "top_action_pill":
		return &t.TopActionPill
	case "top_action_highlight":
		return &t.TopActionHighlight
	case "bottom_action_bar":
		return &t.BottomActionBar
	case "bottom_action_pill":
		return &t.BottomActionPill
	case "bottom_action_highlight":
		return &t.BottomActionHighlight
	case "input_prompt":
		return &t.InputPrompt
	case "input_text":
		return &t.InputText
	case "input_cursor":
		return &t.InputCursor
	case "bubble_left":
		return &t.BubbleLeft
	case "bubble_self":
		return &t.BubbleSelf
	case "bubble_private":
		return &t.BubblePrivate
	case "speaker_left":
		return &t.SpeakerLeft
	case "speaker_self":
		return &t.SpeakerSelf
	case "timestamp":
		return &t.Timestamp
	case "host":
		return &t.Host
	case "notice_default":
		return &t.NoticeDefault
	case "modal_bg":
		return &t.ModalBg
	case "modal_title":
		return &t.ModalTitle
	}
	return nil
}

// ParseColor turns "lightcyan|bgmagenta" into the matching ansi.Attr.
// Pipe-separated tokens are ORed together; whitespace is ignored. Case
// insensitive. Returns false on any unknown token so callers can fall back
// to a default rather than render with a junk color.
func ParseColor(s string) (ansi.Attr, bool) {
	if strings.TrimSpace(s) == "" {
		return 0, false
	}
	var out ansi.Attr
	for _, raw := range strings.Split(s, "|") {
		tok := strings.ToLower(strings.TrimSpace(raw))
		if tok == "" {
			continue
		}
		v, ok := colorTokens[tok]
		if !ok {
			return 0, false
		}
		out |= v
	}
	return out, true
}

var colorTokens = map[string]ansi.Attr{
	"black":        ansi.Black,
	"blue":         ansi.Blue,
	"green":        ansi.Green,
	"cyan":         ansi.Cyan,
	"red":          ansi.Red,
	"magenta":      ansi.Magenta,
	"brown":        ansi.Brown,
	"lightgray":    ansi.LightGray,
	"darkgray":     ansi.DarkGray,
	"lightblue":    ansi.LightBlue,
	"lightgreen":   ansi.LightGreen,
	"lightcyan":    ansi.LightCyan,
	"lightred":     ansi.LightRed,
	"lightmagenta": ansi.LightMagenta,
	"yellow":       ansi.Yellow,
	"white":        ansi.White,
	"bgblack":      ansi.BgBlack,
	"bgblue":       ansi.BgBlue,
	"bggreen":      ansi.BgGreen,
	"bgcyan":       ansi.BgCyan,
	"bgred":        ansi.BgRed,
	"bgmagenta":    ansi.BgMagenta,
	"bgbrown":      ansi.BgBrown,
	"bglightgray":  ansi.BgLightGray,
}

func parseBool(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "yes", "on", "1":
		return true
	}
	return false
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		t := strings.ToLower(strings.TrimSpace(p))
		if t != "" {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
