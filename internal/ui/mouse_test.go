package ui

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
	"github.com/hmderdoc/avatar_chat_universal/internal/chat"
	"github.com/hmderdoc/avatar_chat_universal/internal/inlineimg"
)

func mouseApp(t *testing.T, out io.Writer) *App {
	t.Helper()
	sess := chat.NewSession(nil, &chat.Nick{Name: "alice"}, "main")
	app := NewApp(out, ansi.NewInput(strings.NewReader("")), sess, 80, 25, ansi.CharsetCP437)
	app.draw()
	var msgs []*chat.Message
	for i := 0; i < 3; i++ {
		msgs = append(msgs, &chat.Message{Nick: &chat.Nick{Name: "bob"}, Str: "line from bob"},
			&chat.Message{Nick: &chat.Nick{Name: "alice"}, Str: "line from alice"})
	}
	app.transcript.Render(msgs)
	return app
}

func click(x, y int) ansi.Key {
	return ansi.Key{Type: ansi.KeyMouse, Button: ansi.MouseLeft, X: x, Y: y}
}

func TestClickSpeakerStartsMsg(t *testing.T) {
	app := mouseApp(t, io.Discard)
	var h hit
	found := false
	for _, x := range app.transcript.Hits {
		if x.Kind == hitSpeaker && x.Ref == "bob" {
			h, found = x, true
		}
	}
	if !found {
		t.Fatal("no speaker hit for bob")
	}
	app.handleKey(click(h.X+app.transcriptFrame.X, h.Y+app.transcriptFrame.Y))
	if got := app.inputLine.Value(); got != "/msg bob " {
		t.Fatalf("input = %q", got)
	}
	// Our own name does nothing.
	app.inputLine.Reset()
	for _, x := range app.transcript.Hits {
		if x.Kind == hitSpeaker && x.Ref == "alice" {
			app.handleKey(click(x.X, x.Y+app.transcriptFrame.Y))
		}
	}
	if app.inputLine.Value() != "" {
		t.Fatalf("clicking own name typed %q", app.inputLine.Value())
	}
}

func TestWheelScrollsTranscript(t *testing.T) {
	app := mouseApp(t, io.Discard)
	up := ansi.Key{Type: ansi.KeyMouse, Button: ansi.MouseWheelUp, X: 40, Y: 10}
	app.handleKey(up)
	app.handleKey(up)
	if app.transcript.Scroll != 2*wheelRows {
		t.Fatalf("scroll = %d", app.transcript.Scroll)
	}
	app.handleKey(ansi.Key{Type: ansi.KeyMouse, Button: ansi.MouseWheelDown, X: 40, Y: 10})
	if app.transcript.Scroll != wheelRows {
		t.Fatalf("scroll = %d", app.transcript.Scroll)
	}
	// Releases never act.
	app.handleKey(ansi.Key{Type: ansi.KeyMouse, Button: ansi.MouseWheelUp, Release: true})
	if app.transcript.Scroll != wheelRows {
		t.Fatal("release scrolled")
	}
}

func TestClickPills(t *testing.T) {
	app := mouseApp(t, io.Discard)
	pillX := func(bar *ActionBar, label string) int {
		for _, p := range bar.pills {
			if p.label == label {
				return p.x0 + 1
			}
		}
		t.Fatalf("no pill %q", label)
		return 0
	}
	app.handleKey(click(pillX(app.bottomActionBar, "/msg"), app.bottomActionFrame.Y))
	if app.inputLine.Value() != "/msg " {
		t.Fatalf("input = %q", app.inputLine.Value())
	}
	if exit, _ := app.handleKey(click(pillX(app.bottomActionBar, "/quit"), app.bottomActionFrame.Y)); !exit {
		t.Fatal("/quit pill didn't exit")
	}
	if exit, _ := app.handleKey(click(pillX(app.actionBar, "Esc exit"), app.actionFrame.Y)); !exit {
		t.Fatal("Esc pill didn't exit")
	}
	app.handleKey(click(pillX(app.actionBar, "/channels"), app.actionFrame.Y))
	if _, ok := app.modal.(*ChannelsModal); !ok {
		t.Fatal("/channels pill didn't open the list")
	}
}

func TestChannelsClickSelectsThenJoins(t *testing.T) {
	f := ansi.NewFrame(0, 0, 80, 24, 0)
	m := NewChannelsModal(f, []chat.RoomInfo{{Name: "main", Current: true}, {Name: "art"}, {Name: "tv"}})
	m.Render()
	y := m.list.y0 + 2 // third row
	if m.HandleMouse(click(5, y)) || m.selected != 2 {
		t.Fatalf("first click: selected %d", m.selected)
	}
	if !m.HandleMouse(click(5, y)) || m.Join != "tv" {
		t.Fatalf("second click: join %q", m.Join)
	}
	m2 := NewChannelsModal(f, []chat.RoomInfo{{Name: "a"}, {Name: "b"}})
	m2.Render()
	m2.HandleMouse(ansi.Key{Type: ansi.KeyMouse, Button: ansi.MouseWheelDown})
	if m2.selected != 1 {
		t.Fatal("wheel didn't move the selection")
	}
	if m2.HandleMouse(click(5, m2.list.y0+5)) {
		t.Fatal("click below the list acted")
	}
}

func TestResizeKeepsInputAndScroll(t *testing.T) {
	app := mouseApp(t, io.Discard)
	app.ResizeEvery = time.Second
	app.inputLine.SetValue("half typed")
	app.transcript.Scroll = 4
	app.openChannels()
	app.handleKey(ansi.Key{Type: ansi.KeyResize, Cols: 100, Rows: 40})
	if app.Width != 100 || app.Height != 40 || app.transcriptFrame.W != 100 || app.inputFrame.W != 100 {
		t.Fatalf("size %dx%d frames %d/%d", app.Width, app.Height, app.transcriptFrame.W, app.inputFrame.W)
	}
	if app.inputLine.Value() != "half typed" || app.transcript.Scroll != 4 || app.transcript.Frame != app.transcriptFrame {
		t.Fatalf("lost state: %q scroll %d", app.inputLine.Value(), app.transcript.Scroll)
	}
	if app.modal != nil {
		t.Fatal("modal kept across a resize")
	}
	app.draw() // renders at the new size without panicking
	// A Shift-F3 (ESC[1;2R) and a probe answer with the same size do nothing.
	app.handleKey(ansi.Key{Type: ansi.KeyResize, Cols: 2, Rows: 1})
	if app.Width != 100 {
		t.Fatal("tiny report resized")
	}
	app.ResizeEvery = 0
	app.handleKey(ansi.Key{Type: ansi.KeyResize, Cols: 90, Rows: 30})
	if app.Width != 100 {
		t.Fatal("resized with probing off")
	}
}

func TestProbeSizeWaitsForFrame(t *testing.T) {
	var out bytes.Buffer
	app := mouseApp(t, &out)
	app.ResizeEvery = time.Second
	now := time.Now()
	app.lastSizeProbe = now
	app.probeSize(now.Add(500 * time.Millisecond))
	if out.Len() != 0 {
		t.Fatal("probed early")
	}
	app.pending, app.pendingOff = []byte("frame"), 0
	app.probeSize(now.Add(2 * time.Second))
	if out.Len() != 0 {
		t.Fatal("probed mid-frame")
	}
	app.pendingOff = len(app.pending)
	app.probeSize(now.Add(2 * time.Second))
	if out.String() != ansi.SizeProbe {
		t.Fatalf("wrote %q", out.String())
	}
}

func TestMouseCommand(t *testing.T) {
	var out bytes.Buffer
	app := mouseApp(t, &out)
	app.MouseAllowed = true
	app.setMouse(true)
	var saved []bool
	app.OnMouseSave = func(on bool) error { saved = append(saved, on); return nil }
	out.Reset()
	app.handleMouseCmd(nil)
	if app.mouseOn || !strings.Contains(out.String(), ansi.MouseOff) || len(saved) != 1 || saved[0] {
		t.Fatalf("toggle off: on=%v out=%q saved=%v", app.mouseOn, out.String(), saved)
	}
	app.handleMouseCmd([]string{"on"})
	if !app.mouseOn || !saved[1] {
		t.Fatal("on")
	}
	restore := app.pauseMouse()
	if app.mouseOn {
		t.Fatal("pause left it on")
	}
	restore()
	if !app.mouseOn {
		t.Fatal("pause didn't restore")
	}
	app.MouseAllowed = false
	app.handleMouseCmd([]string{"off"})
	if !app.mouseOn || len(saved) != 2 {
		t.Fatal("sysop off still toggled")
	}
}

func TestMediaLineIsClickable(t *testing.T) {
	f := ansi.NewTransparentFrame(0, 0, 80, 20)
	tr := NewTranscript(f, "alice")
	a := &App{transcript: tr}
	a.EnableAudioLinks(AudioLinkConfig{Fetcher: inlineimg.NewFetcher("")})
	tr.Render([]*chat.Message{{Nick: &chat.Nick{Name: "bob"}, Str: "listen https://x.org/Song.mp3", Time: time.Now().UnixMilli()}})
	x, y, ok := findText(f, "\x0e Song.mp3  /play")
	if !ok {
		t.Fatal("no /play line")
	}
	h, ok := tr.HitAt(x+3, y)
	if !ok || h.Kind != hitMedia || h.Ref != "https://x.org/Song.mp3" {
		t.Fatalf("hit at the /play line = %+v %v", h, ok)
	}
}
