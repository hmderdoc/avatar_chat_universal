package ui

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
	"github.com/hmderdoc/avatar_chat_universal/internal/bitmap"
	"github.com/hmderdoc/avatar_chat_universal/internal/chat"
	"github.com/hmderdoc/avatar_chat_universal/internal/nickstyle"
)

// vtGrid replays what the door wrote (CP437 charset) onto a w x h grid of
// bytes: cursor moves, clears and text; colours are ignored.
func vtGrid(out []byte, w, h int) [][]byte {
	g := make([][]byte, h)
	for y := range g {
		g[y] = bytes.Repeat([]byte{' '}, w)
	}
	x, y := 0, 0
	for i := 0; i < len(out); i++ {
		c := out[i]
		if c == 0x1b && i+1 < len(out) && out[i+1] == '[' {
			j := i + 2
			for j < len(out) && (out[j] < 0x40 || out[j] > 0x7e) {
				j++
			}
			if j >= len(out) {
				break
			}
			params := string(out[i+2 : j])
			switch out[j] {
			case 'H':
				y, x = 0, 0
				if p := strings.Split(params, ";"); len(p) == 2 {
					r, _ := strconv.Atoi(p[0])
					col, _ := strconv.Atoi(p[1])
					y, x = r-1, col-1
				}
			case 'J':
				if params == "2" {
					for y := range g {
						g[y] = bytes.Repeat([]byte{' '}, w)
					}
				}
			}
			i = j
			continue
		}
		if c < 0x20 && c != 0x00 {
			continue
		}
		if y >= 0 && y < h && x >= 0 && x < w {
			g[y][x] = c
		}
		x++
	}
	return g
}

func frameGrid(f *ansi.Frame) [][]byte {
	g := make([][]byte, f.H)
	for y := range g {
		g[y] = make([]byte, f.W)
		for x := range g[y] {
			g[y][x] = f.CellAt(x, y).Char
		}
	}
	return g
}

// checkBox: g's box at (x,y) w x h is whole (corners, [X] at the top left,
// both sides) and, unless bottomText, its bottom edge is untouched.
func checkBox(t *testing.T, name string, g [][]byte, x, y, w, h int, bottomText bool) {
	t.Helper()
	at := func(cx, cy int) byte { return g[cy][cx] }
	if at(x, y) != boxTL || at(x+w-1, y) != boxTR || at(x, y+h-1) != boxBL || at(x+w-1, y+h-1) != boxBR {
		t.Errorf("%s: corners %q %q %q %q", name, at(x, y), at(x+w-1, y), at(x, y+h-1), at(x+w-1, y+h-1))
	}
	if got := string(g[y][x+closeX0 : x+closeX1]); got != "[X]" {
		t.Errorf("%s: top left %q, want [X]", name, got)
	}
	if at(x+closeX1, y) != boxH {
		t.Errorf("%s: no edge after the [X]", name)
	}
	for r := y + 1; r < y+h-1; r++ {
		if at(x, r) != boxV || at(x+w-1, r) != boxV {
			t.Errorf("%s: side overdrawn on row %d: %q", name, r-y, g[r][x:x+w])
		}
	}
	for c := x + 1; c < x+w-1; c++ {
		if bottomText && c > x+1 {
			break
		}
		if at(c, y+h-1) != boxH {
			t.Errorf("%s: bottom edge overdrawn: %q", name, g[y+h-1][x:x+w])
			break
		}
	}
}

func rowText(g [][]byte, y int) string { return string(g[y]) }

func boxApp(t *testing.T, w, h int, keys string) *App {
	t.Helper()
	sess := chat.NewSession(nil, &chat.Nick{Name: "alice"}, "main")
	in := ansi.NewInput(strings.NewReader(keys))
	in.SetMouse(true)
	return NewApp(&bytes.Buffer{}, in, sess, w, h, ansi.CharsetCP437)
}

func manyRooms(n int) []chat.RoomInfo {
	var rooms []chat.RoomInfo
	for i := 0; i < n; i++ {
		rooms = append(rooms, chat.RoomInfo{Name: fmt.Sprintf("room%02d", i), Users: i})
	}
	return rooms
}

func testBitmap(w, h int) *chat.BitmapEntry {
	img := &bitmap.Image{Width: w, Height: h}
	img.Cells = make([][]bitmap.Cell, h)
	for y := range img.Cells {
		img.Cells[y] = make([]bitmap.Cell, w)
		for x := range img.Cells[y] {
			img.Cells[y][x] = bitmap.Cell{Char: 'o', FG: 15, BG: 1}
		}
	}
	return &chat.BitmapEntry{Image: img, Sender: "bob"}
}

// Every modal is boxed, the [X] is at its top left and nothing inside
// draws over the box, at 80x24 and larger.
func TestModalBoxes(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {132, 50}} {
		W, H := size[0], size[1]
		tag := fmt.Sprintf("%dx%d", W, H)
		usable := H - 1

		app := boxApp(t, W, H, "")
		app.openChannels()
		cm := app.modal.(*ChannelsModal)
		cm.rooms = manyRooms(60)
		cm.selected = 59
		cm.Render()
		g := frameGrid(app.modalFrame)
		checkBox(t, tag+" channels", g, 0, 0, W, usable, false)
		if !strings.Contains(rowText(g, usable-2), "Esc close") || !strings.Contains(rowText(g, 0), "Channels") {
			t.Errorf("%s channels: help %q title %q", tag, rowText(g, usable-2), rowText(g, 0))
		}

		var names []string
		for i := 0; i < 60; i++ {
			names = append(names, fmt.Sprintf("user%02d", i))
		}
		app.openRoster(names)
		rm := app.modal.(*RosterModal)
		rm.selected = 59
		rm.Render()
		g = frameGrid(app.modalFrame)
		checkBox(t, tag+" roster", g, 0, 0, W, usable, false)
		if !strings.Contains(rowText(g, usable-2), "Esc close") || !strings.Contains(rowText(g, 0), "Who's Here") {
			t.Errorf("%s roster: help %q", tag, rowText(g, usable-2))
		}
		app.closeModal()

		p := newSettingsPanel("alice", nil)
		p.theme = app.theme()
		p.status = strings.Repeat("s", 200)
		app.drawSettings(p, nil)
		f := p.frame
		g = frameGrid(f)
		checkBox(t, tag+" settings", g, 0, 0, f.W, f.H, false)
		if !strings.Contains(rowText(g, f.H-2), "S save") || !strings.Contains(rowText(g, 0), "Settings") {
			t.Errorf("%s settings: keys row %q", tag, rowText(g, f.H-2))
		}
		if f.X+f.W > W || f.Y+f.H > H {
			t.Errorf("%s settings off screen: %d,%d %dx%d", tag, f.X, f.Y, f.W, f.H)
		}

		for _, tc := range []bool{true, false} {
			gr := newColourGrid(tc, "")
			gf := gr.draw(app.theme(), ansi.CharsetCP437, W, H)
			g = frameGrid(gf)
			name := fmt.Sprintf("%s grid truecolor=%v", tag, tc)
			checkBox(t, name, g, 0, 0, gf.W, gf.H, false)
			if !strings.Contains(rowText(g, gf.H-2), gridHint) || !strings.Contains(rowText(g, gf.H-3), "black") {
				t.Errorf("%s: rows %q / %q", name, rowText(g, gf.H-3), rowText(g, gf.H-2))
			}
			if gf.X+gf.W > W || gf.Y+gf.H > H {
				t.Errorf("%s off screen", name)
			}
		}

		// /img: a bitmap bigger than the screen, and the empty viewer.
		v := NewImageViewer([]*chat.BitmapEntry{testBitmap(200, 100)}, W, usable)
		var out bytes.Buffer
		if err := v.Render(&out); err != nil {
			t.Fatal(err)
		}
		g = vtGrid(out.Bytes(), W, usable)
		checkBox(t, tag+" viewer", g, 0, 0, W, usable, true)
		if g[1][1] != 'o' || g[usable-3][W-2] != 'o' {
			t.Errorf("%s viewer: picture not inside the box: %q", tag, rowText(g, 1))
		}
		if !strings.Contains(rowText(g, 0), "Image 1/1") || !strings.Contains(rowText(g, usable-1), "Esc close") {
			t.Errorf("%s viewer: header %q footer %q", tag, rowText(g, 0), rowText(g, usable-1))
		}
		out.Reset()
		_ = NewImageViewer(nil, W, usable).Render(&out)
		g = vtGrid(out.Bytes(), W, usable)
		checkBox(t, tag+" empty viewer", g, 0, 0, W, usable, false)

		// The upload dialog over the chat.
		app = boxApp(t, W, H, "\x1b")
		var conn bytes.Buffer
		app.Conn = &conn
		app.confirmUpload()
		g = vtGrid(conn.Bytes(), W, H)
		dw, dh := 60, 13
		checkBox(t, tag+" upload dialog", g, (W-dw)/2, (H-dh)/2, dw, dh, false)
	}
}

// sgrClick is a left press at 0-based (x, y) as an SGR mouse report.
func sgrClick(x, y int) string { return fmt.Sprintf("\x1b[<0;%d;%dM", x+1, y+1) }

// A click on the [X] closes each modal the way Esc does.
func TestCloseButtonClicks(t *testing.T) {
	app := boxApp(t, 80, 24, "")
	app.openChannels()
	app.draw()
	app.handleKey(click(closeX0+1, 0))
	if app.modal != nil {
		t.Fatal("[X] didn't close /channels")
	}
	app.openRoster([]string{"bob"})
	app.draw()
	app.handleKey(click(closeX0, 0))
	if app.modal != nil {
		t.Fatal("[X] didn't close /who")
	}
	app.openChannels()
	app.handleKey(click(closeX1, 0)) // just past it
	if app.modal == nil {
		t.Fatal("a click beside the [X] closed /channels")
	}
	app.closeModal()
	app.openImageViewer()
	app.handleKey(click(closeX0+2, 0))
	if app.imageViewer != nil {
		t.Fatal("[X] didn't close /img")
	}

	// Settings: the [X] is Esc, so an unsaved edit is dropped.
	// The panel at 80x24 is 72x21 at (4,1).
	app = boxApp(t, 80, 24, "f"+sgrClick(4+closeX0, 1)+"s")
	saved := false
	app.OnStyleSave = func(*nickstyle.Style) error { saved = true; return nil }
	app.openSettings()
	if saved || app.pendingStyle != nil {
		t.Fatal("[X] on /settings kept the edit")
	}

	// The grid's [X] cancels the grid only (52x15 at (14,4)); S then has
	// nothing to save.
	app = boxApp(t, 80, 24, "\r"+sgrClick(14+closeX0, 4)+"s")
	app.OnStyleSave = func(*nickstyle.Style) error { saved = true; return nil }
	app.openSettings()
	if saved {
		t.Fatal("cancelled grid changed the style")
	}

	// The upload dialog (60x13 at (10,5)).
	app = boxApp(t, 80, 24, sgrClick(10+closeX0, 5)+"\r")
	if app.confirmUpload() {
		t.Fatal("[X] on the upload dialog went ahead")
	}
}

// The chat screen's [X] sits left of the top bar's pills and is Esc exit.
func TestChatCloseButton(t *testing.T) {
	app := mouseApp(t, &bytes.Buffer{})
	f := app.actionFrame
	if got := string([]byte{f.CellAt(1, 0).Char, f.CellAt(2, 0).Char, f.CellAt(3, 0).Char}); got != "[X]" {
		t.Fatalf("top bar starts %q", got)
	}
	if f.CellAt(2, 0).Attr != app.theme().CloseButton {
		t.Fatal("[X] not in close_button")
	}
	labels := ""
	for _, p := range app.actionBar.pills {
		labels += p.label + ","
	}
	if !strings.HasPrefix(labels, "[X],/who,") || !strings.Contains(labels, "Esc exit") {
		t.Fatalf("pills %s", labels)
	}
	if buttonsWidth(app.actionBar.Buttons)+closeW > app.Width {
		t.Fatal("pills overflow the bar")
	}
	if exit, _ := app.handleKey(click(2, f.Y)); !exit {
		t.Fatal("[X] didn't exit")
	}
	// With a video playing, Esc (and so the [X]) stops it first.
	app.videos = &videoState{}
	app.videos.loading = "https://x.org/v.mp4"
	if exit, _ := app.handleKey(click(2, f.Y)); exit {
		t.Fatal("[X] exited over a busy video")
	}
}

func TestColourGridNavigation(t *testing.T) {
	// Truecolor: two rows down is the first hue row.
	app := boxApp(t, 80, 24, "\r\x1b[B\x1b[B\x1b[C\rs")
	var st *nickstyle.Style
	app.OnStyleSave = func(s *nickstyle.Style) error { st = s; return nil }
	app.openSettings()
	if want := hsvToHex(30, 1, 0.5); st == nil || st.Colors[0] != want {
		t.Fatalf("truecolor pick = %+v, want %s", st, want)
	}

	// 16 colours: only the CGA rows, so Down stops on the second.
	app = boxApp(t, 80, 24, "\r\x1b[B\x1b[B\x1b[B\x1b[B\x1b[B\x1b[C\rs")
	app.TVColor = "16"
	st = nil
	app.OnStyleSave = func(s *nickstyle.Style) error { st = s; return nil }
	app.openSettings()
	if st == nil || st.Colors[0] != nickstyle.CGA[9] {
		t.Fatalf("16-colour pick = %+v, want light blue", st)
	}
	if g := newColourGrid(false, ""); len(g.rows) != 2 {
		t.Fatalf("16-colour grid has %d rows", len(g.rows))
	}
	if g := newColourGrid(true, ""); len(g.rows) != 2+len(gridShades)+1 {
		t.Fatalf("truecolor grid has %d rows", len(g.rows))
	}

	// A click on the second letter opens the grid for it (letters start
	// at panel column 3, row 9; the panel is at (4,1)).
	app = boxApp(t, 80, 24, sgrClick(4+lettersX0+lettersStep, 1+lettersY)+"\x1b[C\rs")
	st = nil
	app.OnStyleSave = func(s *nickstyle.Style) error { st = s; return nil }
	app.openSettings()
	if st == nil || len(st.Colors) < 2 || st.Colors[0] != "" || st.Colors[1] != nickstyle.CGA[1] {
		t.Fatalf("letter click pick = %+v", st)
	}

	// The cursor starts on the letter's colour, or its nearest CGA one.
	if g := newColourGrid(false, "#5555ff"); g.row != 1 || g.col != 1 {
		t.Fatalf("start %d,%d", g.row, g.col)
	}
	if g := newColourGrid(true, hsvToHex(60, 0.55, 1)); g.row != 5 || g.col != 2 {
		t.Fatalf("start on a hue swatch %d,%d", g.row, g.col)
	}
	g := newColourGrid(false, "#fe5050")
	if g.current() != "#ff5555" || g.name() != "light red" {
		t.Fatalf("nearest start %s %s", g.current(), g.name())
	}
	if newColourGrid(true, hsvToHex(0, 1, 0.5)).name() != "custom" {
		t.Fatal("off-palette swatch should be named custom")
	}

	// A click on a swatch uses it.
	g = newColourGrid(true, "")
	f := g.draw(boxApp(t, 80, 24, "").theme(), ansi.CharsetCP437, 80, 24)
	if done, ok := g.handle(click(f.X+gridX0+3*gridCellW+1, f.Y+rowY(1))); !done || !ok || g.current() != nickstyle.CGA[11] {
		t.Fatalf("swatch click: %v %v %s", done, ok, g.current())
	}
	if done, ok := g.handle(click(f.X+closeX0, f.Y)); !done || ok {
		t.Fatal("grid [X] should cancel")
	}
}

// A truecolor gradient from the web shows names, never hex codes.
func TestSettingsShowsNoHex(t *testing.T) {
	app := boxApp(t, 80, 24, "")
	var out bytes.Buffer
	app.Conn = &out
	style := &nickstyle.Style{Colors: nickstyle.Preset("gradient", 5, "#a3f2c1", "#123456")}
	p := newSettingsPanel("alice", style)
	p.theme = app.theme()
	for i := range p.colors {
		p.cursor = i
		app.drawSettings(p, nil)
		for y := 0; y < p.frame.H; y++ {
			if row := rowText(frameGrid(p.frame), y); strings.Contains(row, "#") {
				t.Fatalf("letter %d: row %d shows %q", i+1, y, row)
			}
		}
	}
	if bytes.IndexByte(out.Bytes(), '#') >= 0 {
		t.Fatal("panel output carries a '#'")
	}
	if got := colorLabel("#a3f2c1"); !strings.HasPrefix(got, "~") || strings.Contains(got, "#") {
		t.Fatalf("label %q", got)
	}
	if !strings.Contains(rowText(frameGrid(p.frame), 11), "Enter pick") {
		t.Fatal("info line doesn't mention Enter")
	}
}

// The bottom bar leads with /theme when users may pick one; the old
// history pill is gone (PgUp still scrolls).
func TestBottomBarTheme(t *testing.T) {
	app := mouseApp(t, &bytes.Buffer{})
	app.refreshActionBar()
	for _, b := range app.bottomActionBar.Buttons {
		if b.Label == "/theme" || strings.Contains(b.Label, "PgUp") {
			t.Fatalf("bottom bar has %q without user themes", b.Label)
		}
	}
	app.handleKey(ansi.Key{Type: ansi.KeyPgUp})
	if app.transcript.Scroll == 0 {
		t.Fatal("PgUp no longer scrolls")
	}

	app, _ = themeApp(t, "")
	app.refreshActionBar()
	if app.bottomActionBar.Buttons[0].Label != "/theme" {
		t.Fatalf("first pill %q", app.bottomActionBar.Buttons[0].Label)
	}
	app.bottomActionBar.Render()
	x0 := app.bottomActionBar.pills[0].x0
	app.handleKey(click(x0+1, app.bottomActionFrame.Y))
	found := false
	for _, m := range app.Session.Messages() {
		if strings.Contains(m.Str, "themes: ") {
			found = true
		}
	}
	if !found {
		t.Fatal("/theme pill didn't list the themes")
	}
}

// The /img box follows the door's charset with or without pictures.
func TestImageViewerBoxCharset(t *testing.T) {
	sess := chat.NewSession(nil, &chat.Nick{Name: "alice"}, "main")
	app := NewApp(&bytes.Buffer{}, ansi.NewInput(strings.NewReader("")), sess, 80, 24, ansi.CharsetUTF8)
	app.openImageViewer()
	var out bytes.Buffer
	_ = app.imageViewer.Render(&out)
	if !strings.Contains(out.String(), "┌") || bytes.IndexByte(out.Bytes(), boxTL) >= 0 {
		t.Fatalf("empty /img box not in UTF-8: %q", out.String()[:40])
	}
}

// While /settings covers the bar, the bar's [X] is blank and unclickable,
// and the pills don't move.
func TestActionBarCloseHidden(t *testing.T) {
	f := ansi.NewFrame(0, 0, 40, 1, ansi.White|ansi.BgBlack)
	bar := NewActionBar(f)
	bar.Close = true
	bar.Buttons = []ActionButton{{Label: "/who"}}
	bar.Render()
	shown, _ := bar.LabelAt(2)
	at := -1
	for x := 0; x < 40; x++ {
		if l, ok := bar.LabelAt(x); ok && l == "/who" {
			at = x
			break
		}
	}
	bar.CloseHidden = true
	bar.Render()
	if _, ok := bar.LabelAt(2); shown != "[X]" || ok {
		t.Fatalf("[X] shown=%q, still clickable when hidden=%v", shown, ok)
	}
	if l, ok := bar.LabelAt(at); !ok || l != "/who" {
		t.Fatalf("pills moved when the [X] was hidden")
	}
}
