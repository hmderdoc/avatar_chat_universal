package ui

import (
	"time"

	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
)

// Header paints the green title bar at the top of the screen. It alternates
// between two faces every few seconds (mirroring avatar_chat.js:4045-4109):
//   - StatsText: "Avatar Chat | <channel> | users N | joined 1 | host:port"
//     rendered black-on-green, shown by default.
//   - MotdText:  "MOTD | <text>" rendered white-on-green, shown when set.
type Header struct {
	Frame *ansi.Frame

	StatsText string
	MotdText  string

	StatsAttr ansi.Attr // black on green
	MotdAttr  ansi.Attr // white on green

	// Cycle controls how often we swap faces. 4.5s stats / 3.5s motd
	// matches the JS rhythm reasonably without doing the JS's char-by-char
	// scroll (which we may add later).
	StatsDuration time.Duration
	MotdDuration  time.Duration
}

// time package import added below

func NewHeader(f *ansi.Frame) *Header {
	return &Header{
		Frame:         f,
		StatsAttr:     ansi.Black | ansi.BgGreen,
		MotdAttr:      ansi.White | ansi.BgGreen,
		StatsDuration: 4500 * time.Millisecond,
		MotdDuration:  3500 * time.Millisecond,
	}
}

func (h *Header) Render() {
	text := " " + h.StatsText
	attr := h.StatsAttr
	if h.MotdText != "" {
		total := h.StatsDuration + h.MotdDuration
		if total <= 0 {
			total = 8 * time.Second
		}
		now := time.Now().UnixNano()
		phase := time.Duration(now % int64(total))
		if phase >= h.StatsDuration {
			text = " MOTD | " + h.MotdText
			attr = h.MotdAttr
		}
	}
	h.Frame.Clear()
	for x := 0; x < h.Frame.W; x++ {
		h.Frame.SetCell(x, 0, ' ', attr)
	}
	if len(text) > h.Frame.W-1 {
		text = text[:h.Frame.W-1]
	}
	h.Frame.PrintAt(0, 0, []byte(text), attr)
}

// Status is a thin wrapper for a one-line status bar at the bottom.
type Status struct {
	Frame *ansi.Frame
	Text  string
	Attr  ansi.Attr
}

func NewStatus(f *ansi.Frame) *Status {
	return &Status{Frame: f, Attr: ansi.Black | ansi.BgLightGray}
}

func (s *Status) Render() {
	s.Frame.Clear()
	for x := 0; x < s.Frame.W; x++ {
		s.Frame.SetCell(x, 0, ' ', s.Attr)
	}
	s.Frame.PrintAt(1, 0, []byte(s.Text), s.Attr)
}

// ActionButton is one labelled hotkey on an ActionBar, rendered as a
// "pill" (label with surrounding spaces) in a different background from
// the bar so the hotkey region pops visually.
type ActionButton struct {
	Label     string
	Highlight bool // true for new-image / new-PM / etc. attention indicators
	Hidden    bool // suppress this frame; the App toggles this each tick to flash
}

// ActionBar is a horizontal toolbar of ActionButtons. The bar is filled
// with BarAttr; each button renders with ButtonAttr or HighlightAttr.
// Buttons are separated by a margin column of bar background so the
// pills stand out.
type ActionBar struct {
	Frame *ansi.Frame

	Buttons []ActionButton
	Text    string // legacy fallback when Buttons is empty

	BarAttr       ansi.Attr // bar background between pills
	ButtonAttr    ansi.Attr // normal pill
	HighlightAttr ansi.Attr // attention pill (flashing /img count etc.)

	// Attr is the legacy single-color setter retained so existing callers
	// keep working; setting it updates BarAttr.
	Attr ansi.Attr
}

func NewActionBar(f *ansi.Frame) *ActionBar {
	bar := ansi.White | ansi.BgBlue
	return &ActionBar{
		Frame:         f,
		BarAttr:       bar,
		ButtonAttr:    ansi.Black | ansi.BgLightGray,
		HighlightAttr: ansi.Black | ansi.BgBrown,
		Attr:          bar,
	}
}

func (a *ActionBar) Render() {
	if a.Attr != 0 && a.BarAttr == 0 {
		a.BarAttr = a.Attr
	}
	a.Frame.Clear()
	for x := 0; x < a.Frame.W; x++ {
		a.Frame.SetCell(x, 0, ' ', a.BarAttr)
	}
	if len(a.Buttons) == 0 {
		// Legacy plain-text mode.
		a.Frame.PrintAt(1, 0, []byte(a.Text), a.BarAttr)
		return
	}
	x := 1
	for i, btn := range a.Buttons {
		if i > 0 {
			x += 2 // gap between pills, rendered in bar bg
		}
		// Reserve label width even when hidden so the bar layout is stable.
		pill := " " + btn.Label + " "
		if btn.Hidden {
			x += len(pill)
			continue
		}
		attr := a.ButtonAttr
		if btn.Highlight {
			attr = a.HighlightAttr
		}
		for j := 0; j < len(pill); j++ {
			col := x + j
			if col >= a.Frame.W {
				break
			}
			a.Frame.SetCell(col, 0, pill[j], attr)
		}
		x += len(pill)
	}
}
