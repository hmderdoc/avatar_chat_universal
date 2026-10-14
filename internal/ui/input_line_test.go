package ui

import (
	"io"
	"strings"
	"testing"
	"time"

	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
	"github.com/hmderdoc/avatar_chat_universal/internal/chat"
)

func tabLine(text string, nicks ...string) *InputLine {
	i := &InputLine{MaxLength: 500, historyIdx: -1}
	i.CompletionProvider = func() []string { return nicks }
	i.buf = []rune(text)
	i.pos = len(i.buf)
	return i
}

func TestTabCompletion(t *testing.T) {
	tab := ansi.Key{Type: ansi.KeyTab}
	for _, tc := range []struct {
		text, want string
	}{
		{"hi be", "hi Beta Tester"},
		{"hi Beta T", "hi Beta Tester"},    // a name with a space
		{"beta tester x", "beta tester x"}, // no match: unchanged
		{"hi ", "hi "},                     // nothing to complete
		{"", ""},
		{"Al", "Alice"},
		{"  al", "  Alice"},
	} {
		i := tabLine(tc.text, "Beta Tester", "Alice", "bob")
		i.HandleKey(tab)
		if got := string(i.buf); got != tc.want {
			t.Errorf("%q + Tab = %q, want %q", tc.text, got, tc.want)
		}
	}

	// Repeated Tab cycles; another key ends the cycle.
	i := tabLine("hi b", "Bob", "Beta Tester")
	i.HandleKey(tab)
	if got := string(i.buf); got != "hi Beta Tester" {
		t.Fatalf("first Tab = %q", got)
	}
	i.HandleKey(tab)
	if got := string(i.buf); got != "hi Bob" {
		t.Fatalf("second Tab = %q", got)
	}
	i.HandleKey(ansi.Key{Type: ansi.KeyChar, Rune: ' '})
	if got := string(i.buf); got != "hi Bob " {
		t.Fatalf("after space = %q", got)
	}
}

func TestCursorEditing(t *testing.T) {
	key := func(kt ansi.KeyType) ansi.Key { return ansi.Key{Type: kt} }
	ch := func(r rune) ansi.Key { return ansi.Key{Type: ansi.KeyChar, Rune: r} }
	i := &InputLine{MaxLength: 500, historyIdx: -1}
	for _, r := range "helo" {
		i.HandleKey(ch(r))
	}
	i.HandleKey(key(ansi.KeyLeft))
	i.HandleKey(ch('l')) // insert mid-line
	if got := i.Value(); got != "hello" || i.pos != 4 {
		t.Fatalf("insert: %q pos %d", got, i.pos)
	}
	i.HandleKey(key(ansi.KeyHome))
	i.HandleKey(key(ansi.KeyLeft)) // stops at 0
	i.HandleKey(key(ansi.KeyDelete))
	i.HandleKey(key(ansi.KeyBackspace)) // nothing before the cursor
	if got := i.Value(); got != "ello" || i.pos != 0 {
		t.Fatalf("home+delete: %q pos %d", got, i.pos)
	}
	i.HandleKey(key(ansi.KeyRight))
	i.HandleKey(key(ansi.KeyBackspace))
	if got := i.Value(); got != "llo" || i.pos != 0 {
		t.Fatalf("backspace: %q pos %d", got, i.pos)
	}
	i.HandleKey(key(ansi.KeyEnd))
	i.HandleKey(key(ansi.KeyRight)) // stops at the end
	i.HandleKey(key(ansi.KeyDelete))
	i.HandleKey(ch('!'))
	if got := i.Value(); got != "llo!" || i.pos != 4 {
		t.Fatalf("end: %q pos %d", got, i.pos)
	}

	// History recall and SetValue put the cursor at the end.
	i.PushHistory("first")
	i.Reset()
	i.HandleKey(key(ansi.KeyUp))
	if i.pos != len("first") {
		t.Fatalf("history pos %d", i.pos)
	}
	i.SetValue("/msg x ")
	if i.pos != len("/msg x ") {
		t.Fatalf("SetValue pos %d", i.pos)
	}
}

func TestTabMidLine(t *testing.T) {
	i := tabLine("hi b how are you", "Bob", "Beta Tester")
	i.pos = len("hi b")
	i.HandleKey(ansi.Key{Type: ansi.KeyTab})
	if got := i.Value(); got != "hi Beta Tester how are you" || i.pos != len("hi Beta Tester") {
		t.Fatalf("first Tab = %q pos %d", got, i.pos)
	}
	i.HandleKey(ansi.Key{Type: ansi.KeyTab})
	if got := i.Value(); got != "hi Bob how are you" || i.pos != len("hi Bob") {
		t.Fatalf("second Tab = %q pos %d", got, i.pos)
	}
}

func TestInputScroll(t *testing.T) {
	// Prompt "> " leaves 8 text columns.
	i := &InputLine{Frame: &ansi.Frame{W: 10}, Prompt: "> ", MaxLength: 500, historyIdx: -1}
	i.SetValue("abcdefghijklmnop") // 16 runes
	col, vis := i.view()
	if string(vis) != "jklmnop" || col != 9 {
		t.Fatalf("end: %q col %d", string(vis), col)
	}
	i.HandleKey(ansi.Key{Type: ansi.KeyHome})
	col, vis = i.view()
	if string(vis) != "abcdefgh" || col != 2 {
		t.Fatalf("home: %q col %d", string(vis), col)
	}
	for range 9 {
		i.HandleKey(ansi.Key{Type: ansi.KeyRight})
	}
	col, vis = i.view() // cursor on 'j', scrolled just enough
	if string(vis) != "cdefghij" || col != 9 {
		t.Fatalf("right x9: %q col %d", string(vis), col)
	}
	for range 3 {
		i.HandleKey(ansi.Key{Type: ansi.KeyLeft})
	}
	col, vis = i.view() // still in view: no scroll
	if string(vis) != "cdefghij" || col != 6 {
		t.Fatalf("left x3: %q col %d", string(vis), col)
	}
}

// TestApp_ArrowKeysMoveInputCursor feeds raw terminal bytes (CSI and SS3
// arrows, as different clients send them) through the real input decoder
// and App, and checks the edit and the terminal caret position.
func TestApp_ArrowKeysMoveInputCursor(t *testing.T) {
	raw := "helo\x1b[Dl\x1bOD\x1bOD\x1b[3~\x1b[C!\x1b[H>"
	in := ansi.NewInput(strings.NewReader(raw))
	sess := chat.NewSession(nil, &chat.Nick{Name: "alice"}, "main")
	app := NewApp(io.Discard, in, sess, 80, 25, ansi.CharsetCP437)
	for {
		k, ok, err := in.Next(200 * time.Millisecond)
		if err != nil || !ok {
			break
		}
		app.handleKey(k)
	}
	if got := app.inputLine.Value(); got != ">hel!o" {
		t.Fatalf("input = %q, want %q", got, ">hel!o")
	}
	col, _ := app.cursorSpot()
	want := app.inputFrame.X + len(app.inputLine.Prompt) + 1 + 1 // just after '>'
	if col != want {
		t.Fatalf("caret col = %d, want %d", col, want)
	}
}

func TestSplitRecipient(t *testing.T) {
	known := []string{"Beta Tester", "Beta", "bob", "Disk McHardy"}
	for _, tc := range []struct{ rest, name, body string }{
		{" bob hi there", "bob", "hi there"},
		{` "Beta Tester" hi`, "Beta Tester", "hi"},
		{" beta tester  hi", "beta tester", "hi"},
		{" Beta hi", "Beta", "hi"},
		{" Disk McHardy yo", "Disk McHardy", "yo"},
		{" stranger hello", "stranger", "hello"},
		{` "Two Words" x`, "Two Words", "x"},
		{" bob", "bob", ""},
		{"", "", ""},
	} {
		name, body := splitRecipient(tc.rest, known)
		if name != tc.name || body != tc.body {
			t.Errorf("splitRecipient(%q) = %q, %q; want %q, %q", tc.rest, name, body, tc.name, tc.body)
		}
	}
	if got := msgPrefix("Beta Tester"); got != `/msg "Beta Tester" ` {
		t.Errorf("msgPrefix = %q", got)
	}
	if got := msgPrefix("bob"); got != "/msg bob " {
		t.Errorf("msgPrefix = %q", got)
	}
}

func TestCtrlEditingKeys(t *testing.T) {
	ctrl := func(r rune) ansi.Key { return ansi.Key{Type: ansi.KeyChar, Rune: r, Ctrl: true} }
	i := &InputLine{MaxLength: 500, historyIdx: -1}
	i.SetValue("hello big world")
	i.HandleKey(ctrl(0x17)) // Ctrl-W
	if got := i.Value(); got != "hello big " {
		t.Fatalf("Ctrl-W = %q", got)
	}
	i.HandleKey(ctrl(0x01)) // Ctrl-A
	i.HandleKey(ansi.Key{Type: ansi.KeyRight})
	i.HandleKey(ctrl(0x0B)) // Ctrl-K
	if got := i.Value(); got != "h" || i.pos != 1 {
		t.Fatalf("Ctrl-K = %q pos %d", got, i.pos)
	}
	i.SetValue("abc")
	i.HandleKey(ctrl(0x01))
	i.HandleKey(ctrl(0x05)) // Ctrl-E
	if i.pos != 3 {
		t.Fatalf("Ctrl-E pos %d", i.pos)
	}
	i.HandleKey(ctrl(0x15)) // Ctrl-U
	if i.Value() != "" || i.pos != 0 {
		t.Fatalf("Ctrl-U = %q", i.Value())
	}
}

// Esc with a half-typed line clears it; only Esc on an empty line quits.
func TestApp_EscClearsBeforeQuitting(t *testing.T) {
	sess := chat.NewSession(nil, &chat.Nick{Name: "alice"}, "main")
	app := NewApp(io.Discard, ansi.NewInput(strings.NewReader("")), sess, 80, 25, ansi.CharsetCP437)
	app.inputLine.SetValue("half a thought")
	if exit, _ := app.handleKey(ansi.Key{Type: ansi.KeyEsc}); exit {
		t.Fatal("Esc with text quit")
	}
	if app.inputLine.Value() != "" {
		t.Fatalf("line not cleared: %q", app.inputLine.Value())
	}
	if exit, _ := app.handleKey(ansi.Key{Type: ansi.KeyEsc}); !exit {
		t.Fatal("Esc on an empty line didn't quit")
	}
}

func TestPasteIsOneLine(t *testing.T) {
	i := &InputLine{MaxLength: 20, historyIdx: -1}
	i.SetValue("ab")
	i.HandleKey(ansi.Key{Type: ansi.KeyLeft})
	if cut := i.Paste("x\r\n/quit\ty"); cut {
		t.Fatal("cut too early")
	}
	if got := i.Value(); got != "ax /quit yb" || i.pos != 10 {
		t.Fatalf("paste = %q pos %d", got, i.pos)
	}
	if cut := i.Paste("0123456789abc"); !cut || len(i.Value()) != 20 {
		t.Fatalf("over-long paste: cut=%v len=%d", cut, len(i.Value()))
	}
}

func TestWordMoves(t *testing.T) {
	i := &InputLine{MaxLength: 500, historyIdx: -1}
	i.SetValue("one two  three")
	i.HandleKey(ansi.Key{Type: ansi.KeyLeft, Ctrl: true})
	if i.pos != 9 {
		t.Fatalf("word left pos %d", i.pos)
	}
	i.HandleKey(ansi.Key{Type: ansi.KeyLeft, Ctrl: true})
	if i.pos != 4 {
		t.Fatalf("word left again pos %d", i.pos)
	}
	i.HandleKey(ansi.Key{Type: ansi.KeyRight, Ctrl: true})
	if i.pos != 7 {
		t.Fatalf("word right pos %d", i.pos)
	}
}
