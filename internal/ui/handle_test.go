package ui

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"

	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
	"github.com/hmderdoc/avatar_chat_universal/internal/chat"
	"github.com/hmderdoc/avatar_chat_universal/internal/nickstyle"
)

func TestPaintHandle16Color(t *testing.T) {
	f := ansi.NewFrame(0, 0, 20, 1, ansi.LightGray|ansi.BgBlack)
	st := &nickstyle.Style{
		Colors: []string{"#ff5555", "", "#000000", "#ffb6c1"},
		Tag:    nickstyle.Tag{Text: "op", Fg: "#000000", Bg: "#00aa00"},
	}
	n := paintHandle(f, 0, 0, "Abcd", st, ansi.Yellow|ansi.BgBlack, 20, false)
	if n != len("Abcd") {
		t.Fatalf("width = %d, want the name only (tags aren't drawn)", n)
	}
	cell := func(x int) ansi.Cell { return f.CellAt(x, 0) }
	if c := cell(0); c.True || c.Attr != ansi.LightRed|ansi.BgBlack {
		t.Errorf("A = %+v, want light red", c)
	}
	if c := cell(1); c.Attr != ansi.Yellow|ansi.BgBlack {
		t.Errorf("unstyled b = %+v, want base", c)
	}
	if c := cell(2); c.Attr != ansi.DarkGray|ansi.BgBlack {
		t.Errorf("black on black = %+v, want lifted to dark gray", c)
	}
	if c := cell(3); c.Attr&0x0F == ansi.Red {
		t.Errorf("pastel pink snapped to dark red: %+v", c)
	}
	if c := cell(5); c.Char == 'o' {
		t.Errorf("tag drawn: %+v", c)
	}
}

func TestPaintHandleTruecolor(t *testing.T) {
	f := ansi.NewFrame(0, 0, 10, 1, ansi.LightGray|ansi.BgBlue)
	st := &nickstyle.Style{Colors: []string{"#123456"}}
	paintHandle(f, 0, 0, "Ab", st, ansi.White|ansi.BgBlue, 10, true)
	c := f.CellAt(0, 0)
	if !c.True || c.Fg != (ansi.RGB{R: 0x12, G: 0x34, B: 0x56}) || c.Bg != (ansi.RGB{R: 0, G: 0, B: 0xaa}) {
		t.Errorf("truecolor cell = %+v", c)
	}
	if f.CellAt(1, 0).True {
		t.Error("unstyled letter should stay CGA")
	}
}

func TestPaintHandleClips(t *testing.T) {
	f := ansi.NewFrame(0, 0, 20, 1, 0)
	st := &nickstyle.Style{Colors: []string{"#ffffff"}, Tag: nickstyle.Tag{Text: "longtag"}}
	if n := paintHandle(f, 0, 0, "Name", st, ansi.White, 5, false); n != 4 {
		t.Errorf("clipped width = %d, want 4 (tag dropped)", n)
	}
}

func TestCycleColor(t *testing.T) {
	if got := cycleColor("", 1); got != "#000000" {
		t.Errorf("default+1 = %s", got)
	}
	if got := cycleColor("", -1); got != "#ffffff" {
		t.Errorf("default-1 = %s", got)
	}
	if got := cycleColor("#ffffff", 1); got != "" {
		t.Errorf("white+1 = %q, want default", got)
	}
	// Off-palette colours start from their nearest CGA entry.
	if got := cycleColor("#fe5050", 0); got != "#ff5555" {
		t.Errorf("nearest = %s", got)
	}
}

func TestSettingsPanelSavesStyle(t *testing.T) {
	sess := chat.NewSession(nil, &chat.Nick{Name: "Alex"}, "main")
	// f: letter 1 -> black; Right, f f: letter 2 -> blue; t (no longer a
	// key) then s.
	keys := "f\x1b[Cfft" + "s"
	in := ansi.NewInput(bytes.NewReader([]byte(keys)))
	app := NewApp(io.Discard, in, sess, 80, 24, ansi.CharsetCP437)
	var saved *nickstyle.Style
	app.OnStyleSave = func(st *nickstyle.Style) error { saved = st; return nil }
	app.BoardWide = true
	// Tags aren't edited or sent (main keeps a stored one via KeepTag).
	sess.Self.Style = &nickstyle.Style{Tag: nickstyle.Tag{Text: "op", Fg: "#000000"}}
	app.openSettings()
	if saved == nil {
		t.Fatal("style not saved")
	}
	want := []string{"#000000", "#0000aa"}
	if len(saved.Colors) != 2 || saved.Colors[0] != want[0] || saved.Colors[1] != want[1] || saved.Tag.Text != "" {
		t.Fatalf("saved = %+v, want the colours only", saved)
	}
	if sess.Self.Style == nil || sess.Self.Style.Tag.Text != "" || len(sess.Self.Style.Colors) != 2 {
		t.Fatalf("Self.Style = %+v, want colours only", sess.Self.Style)
	}
}

func TestStyleTravelsOnTheWire(t *testing.T) {
	addr := startUITestServer(t)
	ctx := context.Background()
	st := &nickstyle.Style{Colors: []string{"#ff0000", "", "#00ff00"}, Tag: nickstyle.Tag{Text: "sysop", Fg: "#ffff55"}}
	alice := chat.NewSession(chat.NewClient(addr), &chat.Nick{Name: "alice", Style: st}, "room")
	bob := chat.NewSession(chat.NewClient(addr), &chat.Nick{Name: "bob"}, "room")
	for _, s := range []*chat.Session{alice, bob} {
		if err := s.Connect(ctx, 10); err != nil {
			t.Fatal(err)
		}
		defer s.Close()
	}
	if err := alice.Send("hello"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		bob.Cycle()
		if got := bob.StyleFor("alice"); got != nil {
			if got.Tag.Text != "sysop" || len(got.Colors) != 3 || got.Colors[2] != "#00ff00" {
				t.Fatalf("received style = %+v", got)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("bob never saw alice's style")
}
