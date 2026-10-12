package ui

import (
	"strings"
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

	// Close puts the "[X]" dismiss button at the left end, in CloseAttr,
	// before the text; CloseHit reports a click on it.
	Close     bool
	CloseAttr ansi.Attr
}

// headerCloseX0 and headerCloseX1 are the columns of the header's [X]
// (x1 exclusive).
const (
	headerCloseX0 = 1
	headerCloseX1 = 4
)

// CloseHit reports whether column x is on the [X].
func (h *Header) CloseHit(x int) bool {
	return h.Close && x >= headerCloseX0 && x < headerCloseX1
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
	x := 0
	if h.Close {
		h.Frame.PrintAt(headerCloseX0, 0, []byte("[X]"), h.CloseAttr)
		x = headerCloseX1
	}
	if len(text) > h.Frame.W-1-x {
		text = text[:max(0, h.Frame.W-1-x)]
	}
	h.Frame.PrintAt(x, 0, []byte(text), attr)
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

// ActionButton is one button on the ActionBar: a coloured face with the
// label, raised over a half-block shadow.
type ActionButton struct {
	Label     string
	Highlight bool // true for new-image / new-PM / etc. attention indicators
	Hidden    bool // suppress this frame; the App toggles this each tick to flash
	// optional buttons are the first to go when the bar is too narrow for
	// everything (see fitButtons); attention buttons are never optional.
	optional bool
}

// buttonGap is the bar columns between one button's shadow and the next
// button's face.
const buttonGap = 1

// CP437 half blocks for the shadows; the UTF-8 charset maps them too.
const (
	halfLower = 0xDC // ▄
	halfUpper = 0xDF // ▀
)

// buttonWidth is the columns one button takes: its face (the label with
// a space either side) and the shadow column after it.
func buttonWidth(b ActionButton) int { return len(b.Label) + 3 }

// buttonsWidth is the columns ActionBar.Render uses for btns: one leading
// space, each button with its shadow, buttonGap between them.
func buttonsWidth(btns []ActionButton) int {
	if len(btns) == 0 {
		return 0
	}
	w := 1 + buttonGap*(len(btns)-1)
	for _, b := range btns {
		w += buttonWidth(b)
	}
	return w
}

// fitButtons drops optional buttons, last first, until the bar fits in
// width columns, so a flashing badge never pushes Quit off an 80-column
// screen.
func fitButtons(btns []ActionButton, width int) []ActionButton {
	for buttonsWidth(btns) > width {
		drop := -1
		for i := len(btns) - 1; i >= 0; i-- {
			if btns[i].optional {
				drop = i
				break
			}
		}
		if drop < 0 {
			break
		}
		btns = append(btns[:drop:drop], btns[drop+1:]...)
	}
	return btns
}

// buttonName is a label's first word, which names the button whatever
// its badge says: "Images [3]" -> "Images", "Quit Esc" -> "Quit".
func buttonName(label string) string {
	if i := strings.IndexByte(label, ' '); i > 0 {
		return label[:i]
	}
	return label
}

// ActionBar is the two rows of raised buttons under the header. Each
// button's face sits on the first row with a shadow in ShadowAttr (a
// foreground, drawn on BarAttr's background): a lower half block at the
// face's right end and upper half blocks under it, one column right. The
// button named Pressed is drawn pushed in instead: the face one column
// right, over where the shadow was, and no shadow.
type ActionBar struct {
	Frame *ansi.Frame // two rows

	Buttons []ActionButton
	Pressed string // buttonName of the button drawn pushed in, or ""

	BarAttr       ansi.Attr // bar background between buttons
	ButtonAttr    ansi.Attr // normal face
	HighlightAttr ansi.Attr // attention face (flashing Images count etc.)
	ShadowAttr    ansi.Attr // shadow foreground

	// pills is where Render put each button (both rows), for mouse clicks.
	pills []pillSpan
}

type pillSpan struct {
	x0, x1 int // columns, x1 exclusive
	label  string
}

// LabelAt returns the label of the button drawn at column x, if any.
func (a *ActionBar) LabelAt(x int) (string, bool) {
	for _, p := range a.pills {
		if x >= p.x0 && x < p.x1 {
			return p.label, true
		}
	}
	return "", false
}

func NewActionBar(f *ansi.Frame) *ActionBar {
	return &ActionBar{
		Frame:         f,
		BarAttr:       ansi.White | ansi.BgBlue,
		ButtonAttr:    ansi.Black | ansi.BgLightGray,
		HighlightAttr: ansi.Black | ansi.BgBrown,
		ShadowAttr:    ansi.Black,
	}
}

func (a *ActionBar) Render() {
	f := a.Frame
	f.Clear()
	a.pills = a.pills[:0]
	for y := 0; y < f.H; y++ {
		for x := 0; x < f.W; x++ {
			f.SetCell(x, y, ' ', a.BarAttr)
		}
	}
	shadow := ansi.FG(a.BarAttr, a.ShadowAttr)
	x := 1
	for i, btn := range a.Buttons {
		if i > 0 {
			x += buttonGap
		}
		face := " " + btn.Label + " "
		// The span covers the shadow column too, and stays while the
		// button flashes off, so a click always lands.
		a.pills = append(a.pills, pillSpan{x0: x, x1: x + len(face) + 1, label: btn.Label})
		if !btn.Hidden {
			attr := a.ButtonAttr
			if btn.Highlight {
				attr = a.HighlightAttr
			}
			if a.Pressed != "" && buttonName(btn.Label) == a.Pressed {
				putStr(f, x+1, 0, face, attr)
			} else {
				putStr(f, x, 0, face, attr)
				f.SetCell(x+len(face), 0, halfLower, shadow)
				for j := 1; j <= len(face); j++ {
					f.SetCell(x+j, 1, halfUpper, shadow)
				}
			}
		}
		x += len(face) + 1
	}
}
