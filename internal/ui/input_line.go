package ui

import (
	"sort"
	"strings"

	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
)

// InputLine is a single-line edit buffer rendered into a Frame. The cursor
// is implicit — we paint a colored block one cell to the right of the last
// character in the buffer. Up/Down arrows walk a small ring of previously
// submitted lines (history), matching the JS door's input behaviour.
type InputLine struct {
	Frame *ansi.Frame

	Prompt    string
	MaxLength int

	PromptAttr ansi.Attr
	TextAttr   ansi.Attr
	CursorAttr ansi.Attr

	buf []rune

	history    []string // most-recent last
	historyMax int
	historyIdx int  // -1 = editing new line; 0..len-1 = pointing at a history slot
	scratch    []rune // saves the in-progress line when starting to scroll history

	// CompletionProvider returns candidate nicks for Tab completion. Set
	// by the App after wiring up the Session.
	CompletionProvider func() []string

	// Tab cycling state. tabMatches is non-nil while the user is rotating
	// through completions with consecutive Tab presses; any non-Tab key
	// resets it.
	tabMatches []string
	tabIdx     int
	tabAnchor  int // start position (in i.buf) of the word being completed
}

func NewInputLine(f *ansi.Frame, prompt string) *InputLine {
	return &InputLine{
		Frame:      f,
		Prompt:     prompt,
		MaxLength:  500,
		PromptAttr: ansi.Yellow,
		TextAttr:   ansi.LightGreen,
		CursorAttr: ansi.LightGray | ansi.BgLightGray,
		historyMax: 64,
		historyIdx: -1,
	}
}

// HandleKey applies a key event to the buffer. Returns submit=true when the
// user pressed ENTER and the buffer is non-empty; the caller should retrieve
// Value(), call PushHistory() if appropriate, and Reset().
func (i *InputLine) HandleKey(k ansi.Key) (submit bool, cancel bool) {
	// Tab cycling state is sticky across consecutive Tab presses but
	// resets the moment any other key arrives.
	if k.Type != ansi.KeyTab {
		i.tabMatches = nil
		i.tabIdx = 0
	}

	switch k.Type {
	case ansi.KeyEnter:
		if len(i.buf) == 0 {
			return false, false
		}
		return true, false
	case ansi.KeyEsc:
		return false, true
	case ansi.KeyBackspace:
		if len(i.buf) > 0 {
			i.buf = i.buf[:len(i.buf)-1]
		}
	case ansi.KeyTab:
		i.handleTab()
	case ansi.KeyUp:
		i.historyPrev()
	case ansi.KeyDown:
		i.historyNext()
	case ansi.KeyChar:
		if k.Ctrl {
			return false, false
		}
		if k.Rune >= 0x20 && k.Rune < 0x7F && len(i.buf) < i.MaxLength {
			i.buf = append(i.buf, k.Rune)
		}
	}
	return false, false
}

// handleTab does prefix-completion against the last word in the buffer.
// First press: find all candidates whose lower-case form starts with the
// last word; replace the word with the first match. Subsequent Tab presses
// cycle through the rest.
func (i *InputLine) handleTab() {
	if i.CompletionProvider == nil {
		return
	}
	// Already cycling — advance to next match.
	if len(i.tabMatches) > 0 {
		i.tabIdx = (i.tabIdx + 1) % len(i.tabMatches)
		i.buf = append(i.buf[:i.tabAnchor], []rune(i.tabMatches[i.tabIdx])...)
		return
	}
	// Find the last whitespace-bounded word in the buffer.
	end := len(i.buf)
	start := end
	for start > 0 && i.buf[start-1] != ' ' && i.buf[start-1] != '\t' {
		start--
	}
	if start == end {
		return // nothing to complete
	}
	prefix := strings.ToLower(string(i.buf[start:end]))
	candidates := i.CompletionProvider()
	var matches []string
	for _, c := range candidates {
		if strings.HasPrefix(strings.ToLower(c), prefix) {
			matches = append(matches, c)
		}
	}
	if len(matches) == 0 {
		return
	}
	sort.Strings(matches)
	i.tabMatches = matches
	i.tabIdx = 0
	i.tabAnchor = start
	i.buf = append(i.buf[:start], []rune(matches[0])...)
}

// historyPrev replaces buf with an earlier history entry. The first call
// (when historyIdx == -1) saves the in-progress line into scratch so the
// user can return to it via historyNext.
func (i *InputLine) historyPrev() {
	if len(i.history) == 0 {
		return
	}
	if i.historyIdx == -1 {
		i.scratch = append(i.scratch[:0], i.buf...)
		i.historyIdx = len(i.history) - 1
	} else if i.historyIdx > 0 {
		i.historyIdx--
	} else {
		return
	}
	i.buf = append(i.buf[:0], []rune(i.history[i.historyIdx])...)
}

// historyNext walks forward toward the in-progress line. At the end of the
// history (one step past newest), it restores the saved scratch buffer.
func (i *InputLine) historyNext() {
	if i.historyIdx == -1 {
		return
	}
	if i.historyIdx < len(i.history)-1 {
		i.historyIdx++
		i.buf = append(i.buf[:0], []rune(i.history[i.historyIdx])...)
		return
	}
	// past the newest entry — restore scratch and exit history mode.
	i.historyIdx = -1
	i.buf = append(i.buf[:0], i.scratch...)
}

// PushHistory records a submitted line into the history ring. Duplicates
// of the most recent entry are skipped. Call this AFTER Value()/Reset()
// so the line we're recording isn't the one the user is still editing.
func (i *InputLine) PushHistory(line string) {
	if line == "" {
		return
	}
	if n := len(i.history); n > 0 && i.history[n-1] == line {
		return
	}
	i.history = append(i.history, line)
	if i.historyMax > 0 && len(i.history) > i.historyMax {
		i.history = i.history[len(i.history)-i.historyMax:]
	}
	i.historyIdx = -1
	i.scratch = i.scratch[:0]
}

// Value returns the current buffer as a string.
func (i *InputLine) Value() string { return string(i.buf) }

// Reset empties the buffer.
func (i *InputLine) Reset() {
	i.buf = i.buf[:0]
	i.historyIdx = -1
	i.scratch = i.scratch[:0]
}

// view computes the visible slice of the buffer plus the column the
// cursor should park at, applying horizontal scrolling once the buffer
// outgrows the available text width. The trick is to reserve one column
// for the cursor when overflowing so it sits *after* the last typed char
// instead of on top of it -- otherwise the most recent character feels
// "swallowed" and typing feels boxed-in at the right edge.
func (i *InputLine) view() (cursorCol int, visible []rune) {
	promptLen := len(i.Prompt)
	maxText := i.Frame.W - promptLen
	if maxText < 1 {
		return promptLen, nil
	}
	if len(i.buf) < maxText {
		return promptLen + len(i.buf), i.buf
	}
	// Overflow: keep one column reserved for the cursor so the user can
	// see what they just typed.
	show := maxText - 1
	if show < 0 {
		show = 0
	}
	return promptLen + show, i.buf[len(i.buf)-show:]
}

// CursorCol returns the 0-based column within the frame where the next
// typed character should appear. Long buffers scroll horizontally; the
// cursor stays parked one column past the rightmost visible char.
func (i *InputLine) CursorCol() int {
	col, _ := i.view()
	if col >= i.Frame.W {
		col = i.Frame.W - 1
	}
	return col
}

// Render paints the prompt + (right-clipped) buffer to the frame. The
// actual blinking cursor is the OS terminal's caret, parked at
// CursorCol() by the App after every render — drawing our own cursor
// block here would just fight whatever the user's terminal shows for the
// real cursor.
func (i *InputLine) Render() {
	i.Frame.Clear()
	x := 0
	for _, r := range i.Prompt {
		if r > 0x7F {
			r = '?'
		}
		i.Frame.SetCell(x, 0, byte(r), i.PromptAttr)
		x++
		if x >= i.Frame.W {
			return
		}
	}
	_, visible := i.view()
	for _, r := range visible {
		if r > 0x7F {
			r = '?'
		}
		i.Frame.SetCell(x, 0, byte(r), i.TextAttr)
		x++
	}
}
