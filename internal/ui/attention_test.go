package ui

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
	"github.com/hmderdoc/avatar_chat_universal/internal/chat"
	"github.com/hmderdoc/avatar_chat_universal/internal/theme"
)

func TestTranscript_MentionRowsHighlighted(t *testing.T) {
	f := ansi.NewTransparentFrame(0, 0, 80, 20)
	tr := NewTranscript(f, "alice")
	now := time.Now().UnixNano() / 1000000
	bob := &chat.Nick{Name: "bob"}
	tr.Render([]*chat.Message{
		{Nick: bob, Str: "first line, nobody named", Time: now},
		{Nick: bob, Str: "hey @Alice look", Time: now},
		{Nick: &chat.Nick{Name: "alice"}, Str: "alice talking about alice", Time: now},
	})
	th := theme.Default()
	bubbleX := avatarW + avatarMargin
	var plain, mention, edge bool
	for y := 0; y < f.H; y++ {
		c := f.CellAt(bubbleX+1, y)
		switch {
		case c.Char == 'f' && c.Attr == th.BubbleLeft:
			plain = true
		case c.Char == 'h' && c.Attr == th.BubbleMention:
			mention = true
			e := f.CellAt(bubbleX, y)
			edge = e.Char == mentionEdge && e.Attr&0x0F == th.MentionEdge&0x0F && e.Attr&^0x0F == th.BubbleMention&^0x0F
		}
	}
	if !plain || !mention || !edge {
		t.Fatalf("plain row=%v mention row=%v edge=%v", plain, mention, edge)
	}
	// Our own message saying our name is not a mention.
	for y := 0; y < f.H; y++ {
		for x := 0; x < f.W; x++ {
			if c := f.CellAt(x, y); c.Char == 'a' && c.Attr == th.BubbleMention {
				t.Fatalf("own message highlighted at %d,%d", x, y)
			}
		}
	}
}

func TestApp_PMRingsOnceAndCountsUnread(t *testing.T) {
	sess := chat.NewSession(nil, &chat.Nick{Name: "alice"}, "main")
	app := NewApp(io.Discard, ansi.NewInput(strings.NewReader("")), sess, 80, 25, ansi.CharsetCP437)
	app.Bell = true

	pm := &chat.Message{Nick: &chat.Nick{Name: "bob"}, Str: "psst", Private: true}
	sess.OnMessage(pm)
	sess.OnMessage(pm)
	if app.unreadPM != 2 {
		t.Fatalf("unreadPM = %d, want 2", app.unreadPM)
	}
	var buf bytes.Buffer
	if err := app.composeInto(&buf); err != nil {
		t.Fatal(err)
	}
	if bytes.Count(buf.Bytes(), []byte{0x07}) != 1 || buf.Bytes()[0] != 0x07 {
		t.Fatalf("want exactly one BEL at the start of the frame")
	}
	buf.Reset()
	_ = app.composeInto(&buf)
	if bytes.IndexByte(buf.Bytes(), 0x07) >= 0 {
		t.Fatal("BEL repeated on the next frame")
	}

	// Our own echoed PM and a plain public line don't count or ring.
	app.lastBell = time.Time{}
	sess.OnMessage(&chat.Message{Nick: &chat.Nick{Name: "alice"}, Str: "->bob: hi", Private: true})
	sess.OnMessage(&chat.Message{Nick: &chat.Nick{Name: "bob"}, Str: "hello room"})
	if app.unreadPM != 2 || app.bellPending {
		t.Fatalf("unreadPM=%d bellPending=%v after non-attention messages", app.unreadPM, app.bellPending)
	}
	// A mention rings; bell=off silences it.
	sess.OnMessage(&chat.Message{Nick: &chat.Nick{Name: "bob"}, Str: "alice: you around?"})
	if !app.bellPending {
		t.Fatal("mention did not ring")
	}
	app.bellPending = false
	app.lastBell = time.Time{}
	app.Bell = false
	sess.OnMessage(pm)
	if app.bellPending {
		t.Fatal("bell = off still rang")
	}
}
