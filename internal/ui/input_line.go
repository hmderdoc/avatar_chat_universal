package ui

import (
	"sort"
	"strings"

	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
)

// InputLine is a single-line edit buffer rendered into a Frame. Left/Right,
// Home/End move the edit cursor; typing inserts at it, Backspace and Delete
// remove either side of it. Up/Down arrows walk a small ring of previously
// submitted lines (history), matching the JS door's input behaviour.
type InputLine struct {
	Frame *ansi.Frame

	Prompt    string
	MaxLength int

	PromptAttr ansi.Attr
	TextAttr   ansi.Attr
	CursorAttr ansi.Attr

	buf    []rune
	pos    int // edit cursor: index in buf the next typed rune goes to
	scroll int // index in buf of the first visible rune once the line overflows

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
	tabAnchor  int    // start position (in i.buf) of the word being completed
	tabTail    []rune // text after the cursor, kept behind each completion
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
	i.pos = max(0, min(i.pos, len(i.buf)))

	switch k.Type {
	case ansi.KeyEnter:
		if len(i.buf) == 0 {
			return false, false
		}
		return true, false
	case ansi.KeyEsc:
		return false, true
	case ansi.KeyBackspace:
		if i.pos > 0 {
			i.buf = append(i.buf[:i.pos-1], i.buf[i.pos:]...)
			i.pos--
		}
	case ansi.KeyDelete:
		if i.pos < len(i.buf) {
			i.buf = append(i.buf[:i.pos], i.buf[i.pos+1:]...)
		}
	case ansi.KeyLeft:
		if k.Ctrl {
			i.pos = i.wordStart()
		} else if i.pos > 0 {
			i.pos--
		}
	case ansi.KeyRight:
		if k.Ctrl {
			for i.pos < len(i.buf) && i.buf[i.pos] == ' ' {
				i.pos++
			}
			for i.pos < len(i.buf) && i.buf[i.pos] != ' ' {
				i.pos++
			}
		} else if i.pos < len(i.buf) {
			i.pos++
		}
	case ansi.KeyHome:
		i.pos = 0
	case ansi.KeyEnd:
		i.pos = len(i.buf)
	case ansi.KeyTab:
		i.handleTab()
	case ansi.KeyUp:
		i.historyPrev()
	case ansi.KeyDown:
		i.historyNext()
	case ansi.KeyChar:
		if k.Ctrl {
			i.ctrlKey(k.Rune)
			return false, false
		}
		if k.Rune >= 0x20 && k.Rune < 0x7F && len(i.buf) < i.MaxLength {
			i.buf = append(i.buf, 0)
			copy(i.buf[i.pos+1:], i.buf[i.pos:])
			i.buf[i.pos] = k.Rune
			i.pos++
		}
	}
	return false, false
}

// ctrlKey applies the usual line-editing control keys: Ctrl-A/Ctrl-E to the
// start/end, Ctrl-U clears the line, Ctrl-K deletes to the end, Ctrl-W
// deletes the word before the cursor.
func (i *InputLine) ctrlKey(r rune) {
	switch r {
	case 0x01: // Ctrl-A
		i.pos = 0
	case 0x05: // Ctrl-E
		i.pos = len(i.buf)
	case 0x15: // Ctrl-U
		i.buf, i.pos = i.buf[:0], 0
	case 0x0B: // Ctrl-K
		i.buf = i.buf[:i.pos]
	case 0x17: // Ctrl-W
		j := i.wordStart()
		i.buf = append(i.buf[:j], i.buf[i.pos:]...)
		i.pos = j
	}
}

// wordStart is where the word before the cursor begins.
func (i *InputLine) wordStart() int {
	j := i.pos
	for j > 0 && i.buf[j-1] == ' ' {
		j--
	}
	for j > 0 && i.buf[j-1] != ' ' {
		j--
	}
	return j
}

// tabWords is how many trailing words handleTab tries as one name.
const tabWords = 4

// handleTab does prefix-completion against the word before the cursor.
// First press: find all candidates whose lower-case form starts with the
// last word (or the last few, for names with spaces); replace them with the
// first match. Subsequent Tab presses
// cycle through the rest.
func (i *InputLine) handleTab() {
	if i.CompletionProvider == nil {
		return
	}
	// Already cycling — advance to next match.
	if len(i.tabMatches) > 0 {
		i.tabIdx = (i.tabIdx + 1) % len(i.tabMatches)
		i.complete(i.tabMatches[i.tabIdx])
		return
	}
	// Starts of the last few words, nearest first. Names can hold spaces
	// ("Beta Tester"), so the trailing words are tried as one prefix too,
	// longest first.
	space := func(r rune) bool { return r == ' ' || r == '\t' }
	end := i.pos
	var starts []int
	for s := end; len(starts) < tabWords; {
		for s > 0 && !space(i.buf[s-1]) {
			s--
		}
		starts = append(starts, s)
		for s > 0 && space(i.buf[s-1]) {
			s--
		}
		if s == 0 {
			break
		}
	}
	if starts[0] == end {
		return // nothing to complete
	}
	candidates := i.CompletionProvider()
	var matches []string
	start := end
	for w := len(starts) - 1; w >= 0 && len(matches) == 0; w-- {
		start = starts[w]
		prefix := strings.ToLower(string(i.buf[start:end]))
		for _, c := range candidates {
			if strings.HasPrefix(strings.ToLower(c), prefix) {
				matches = append(matches, c)
			}
		}
	}
	if len(matches) == 0 {
		return
	}
	sort.Strings(matches)
	i.tabMatches = matches
	i.tabIdx = 0
	i.tabAnchor = start
	i.tabTail = append([]rune(nil), i.buf[end:]...)
	i.complete(matches[0])
}

// complete puts name in place of the word being completed, keeping the
// text after it, and leaves the cursor just past the name.
func (i *InputLine) complete(name string) {
	i.buf = append(append(i.buf[:i.tabAnchor], []rune(name)...), i.tabTail...)
	i.pos = len(i.buf) - len(i.tabTail)
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
	i.pos = len(i.buf)
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
		i.pos = len(i.buf)
		return
	}
	// past the newest entry — restore scratch and exit history mode.
	i.historyIdx = -1
	i.buf = append(i.buf[:0], i.scratch...)
	i.pos = len(i.buf)
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

// Paste inserts pasted text at the cursor as one line: line breaks and
// tabs become spaces, and what can't be typed is left out, as when typing.
// It reports whether the text had to be cut at MaxLength.
func (i *InputLine) Paste(text string) (cut bool) {
	text = strings.ReplaceAll(text, "\r\n", " ")
	var add []rune
	for _, r := range text {
		switch {
		case r == '\r' || r == '\n' || r == '\t':
			add = append(add, ' ')
		case r >= 0x20 && r < 0x7F:
			add = append(add, r)
		}
	}
	if room := i.MaxLength - len(i.buf); len(add) > room {
		add, cut = add[:max(0, room)], true
	}
	i.pos = max(0, min(i.pos, len(i.buf)))
	tail := append([]rune(nil), i.buf[i.pos:]...)
	i.buf = append(append(i.buf[:i.pos], add...), tail...)
	i.pos += len(add)
	i.tabMatches = nil
	return cut
}

// AtEnd reports whether the cursor is past the last character.
func (i *InputLine) AtEnd() bool { return i.pos >= len(i.buf) }

// Value returns the current buffer as a string.
func (i *InputLine) Value() string { return string(i.buf) }

// SetValue replaces the buffer, leaving the cursor at its end.
func (i *InputLine) SetValue(s string) {
	i.buf = append(i.buf[:0], []rune(s)...)
	i.pos = len(i.buf)
	i.historyIdx = -1
	i.scratch = i.scratch[:0]
}

// Reset empties the buffer.
func (i *InputLine) Reset() {
	i.buf = i.buf[:0]
	i.pos, i.scroll = 0, 0
	i.historyIdx = -1
	i.scratch = i.scratch[:0]
}

// view computes the visible slice of the buffer plus the column the
// cursor should park at. Once the buffer outgrows the text width the line
// scrolls horizontally just enough to keep the cursor in view; at the end
// of the line one column stays free for the cursor so it sits *after* the
// last typed char instead of on top of it.
func (i *InputLine) view() (cursorCol int, visible []rune) {
	promptLen := len(i.Prompt)
	maxText := i.Frame.W - promptLen
	if maxText < 1 {
		return promptLen, nil
	}
	pos := max(0, min(i.pos, len(i.buf)))
	if len(i.buf) < maxText {
		i.scroll = 0
		return promptLen + pos, i.buf
	}
	if pos < i.scroll {
		i.scroll = pos
	}
	if pos > i.scroll+maxText-1 {
		i.scroll = pos - (maxText - 1)
	}
	// Don't leave blank columns at the right once the line overflows.
	i.scroll = max(0, min(i.scroll, len(i.buf)-(maxText-1)))
	end := min(len(i.buf), i.scroll+maxText)
	return promptLen + pos - i.scroll, i.buf[i.scroll:end]
}

// CursorCol returns the 0-based column within the frame where the next
// typed character should appear. Long buffers scroll horizontally to keep
// the cursor in view.
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
