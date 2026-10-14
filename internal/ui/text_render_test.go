package ui

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
	"github.com/hmderdoc/avatar_chat_universal/internal/chat"
)

// Remote text reaches the cells as CP437 with no control bytes: accents map,
// smart quotes become ASCII, and an escape sequence can't get through.
func TestTranscript_RemoteTextIsCP437WithoutControls(t *testing.T) {
	f := ansi.NewTransparentFrame(0, 0, 80, 20)
	tr := NewTranscript(f, "alice")
	now := time.Now().UnixNano() / 1000000
	tr.Render([]*chat.Message{
		{Nick: &chat.Nick{Name: "José", Host: "web"}, Str: "café don’t \x1b[2J\x07boom", Time: now},
		{Str: "\x1b]0;pwned\x07 notice", Time: now},
	})
	var all []byte
	for y := 0; y < f.H; y++ {
		for x := 0; x < f.W; x++ {
			all = append(all, f.CellAt(x, y).Char)
		}
	}
	for _, c := range all {
		if c != 0 && c < 0x20 || c == 0x7F {
			t.Fatalf("control byte %#x reached a cell", c)
		}
	}
	for _, want := range []string{"caf\x82 don't [2Jboom", "Jos\x82", "]0;pwned notice"} {
		if !bytes.Contains(all, []byte(want)) {
			t.Errorf("cells lack %q", want)
		}
	}
	if strings.Contains(string(all), "\xE2\x80\x99") {
		t.Error("raw UTF-8 bytes reached the cells")
	}
}

// Scrolled back, the view stays on the same lines as new ones arrive,
// counts them, and marks itself on the bottom row.
func TestTranscript_ScrolledBackHoldsPlace(t *testing.T) {
	f := ansi.NewTransparentFrame(0, 0, 80, 20)
	tr := NewTranscript(f, "alice")
	now := time.Now().UnixNano() / 1000000
	var msgs []*chat.Message
	for i := 0; i < 40; i++ {
		who := "bob"
		if i%2 == 1 {
			who = "carol"
		}
		msgs = append(msgs, &chat.Message{Nick: &chat.Nick{Name: who}, Str: fmt.Sprintf("line %02d", i), Time: now})
	}
	tr.Render(msgs)
	tr.Scroll = 15
	tr.Render(msgs)
	row := func() string {
		var b []byte
		for x := 0; x < f.W; x++ {
			b = append(b, f.CellAt(x, 5).Char)
		}
		return string(b)
	}
	before := row()
	msgs = append(msgs,
		&chat.Message{Nick: &chat.Nick{Name: "bob"}, Str: "new one", Time: now},
		&chat.Message{Nick: &chat.Nick{Name: "carol"}, Str: "new two", Time: now})
	tr.Render(msgs)
	if got := row(); got != before {
		t.Fatalf("view moved:\n%q\n%q", before, got)
	}
	if tr.NewBelow != 2 {
		t.Fatalf("NewBelow = %d", tr.NewBelow)
	}
	var bottom []byte
	for x := 0; x < f.W; x++ {
		bottom = append(bottom, f.CellAt(x, f.H-1).Char)
	}
	if !strings.Contains(string(bottom), "2 new | End: latest") {
		t.Fatalf("bottom row %q", bottom)
	}
	tr.Scroll = 0
	tr.Render(msgs)
	if tr.NewBelow != 0 {
		t.Fatal("NewBelow not cleared at the latest")
	}
}
