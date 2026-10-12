package ui

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
	"github.com/hmderdoc/avatar_chat_universal/internal/chat"
	"github.com/hmderdoc/avatar_chat_universal/internal/ignore"
)

// At 80x24: header, two button rows, the transcript, the input, and the
// reserved last row; no bottom bar.
func TestLayoutRows80x24(t *testing.T) {
	app := boxApp(t, 80, 24, "")
	if app.headerFrame.Y != 0 || app.headerFrame.H != 1 {
		t.Fatalf("header at %d h%d", app.headerFrame.Y, app.headerFrame.H)
	}
	if app.actionFrame.Y != 1 || app.actionFrame.H != 2 {
		t.Fatalf("buttons at %d h%d", app.actionFrame.Y, app.actionFrame.H)
	}
	tf := app.transcriptFrame
	if tf.Y != 3 || tf.Y+tf.H != app.inputFrame.Y {
		t.Fatalf("transcript rows %d..%d, input %d", tf.Y, tf.Y+tf.H-1, app.inputFrame.Y)
	}
	if app.inputFrame.Y != 22 {
		t.Fatalf("input on row %d, want 22 (23 reserved)", app.inputFrame.Y)
	}
	for _, f := range []*ansi.Frame{app.bgAnimFrame, app.fgAnimFrame} {
		if f.Y != tf.Y || f.H != tf.H {
			t.Fatal("transcript layers out of step")
		}
	}
}

func buttonNames(btns []ActionButton) string {
	var n []string
	for _, b := range btns {
		n = append(n, buttonName(b.Label))
	}
	return strings.Join(n, ",")
}

// All six fit at 80 columns, badges and all; narrower, the optional ones
// go last first and Images and Quit stay.
func TestSixButtonsFit80(t *testing.T) {
	app := boxApp(t, 80, 24, "")
	app.refreshActionBar()
	if got := buttonNames(app.actionBar.Buttons); got != "Who,Rooms,Images,Settings,Help,Quit" {
		t.Fatalf("buttons %s", got)
	}
	busy := []ActionButton{
		{Label: "Who [12]", Highlight: true},
		{Label: "Rooms [99]", Highlight: true},
		{Label: "Images [150]", Highlight: true},
		{Label: "Settings", optional: true},
		{Label: "Help", optional: true},
		{Label: "Quit Esc"},
	}
	if got := fitButtons(append([]ActionButton(nil), busy...), 80); len(got) != 6 || buttonsWidth(got) > 80 {
		t.Fatalf("busy bar at 80: %s width %d", buttonNames(got), buttonsWidth(got))
	}
	app = boxApp(t, 40, 24, "")
	app.refreshActionBar()
	if got := buttonNames(app.actionBar.Buttons); got != "Who,Rooms,Images,Quit" || buttonsWidth(app.actionBar.Buttons) > 40 {
		t.Fatalf("40 columns: %s width %d", got, buttonsWidth(app.actionBar.Buttons))
	}
	// An unread PM keeps Who (flashing, counted) even when narrow.
	app.unreadPM = 2
	app.refreshActionBar()
	if b := app.actionBar.Buttons[0]; b.Label != "Who [2]" || !b.Highlight {
		t.Fatalf("PM badge %+v", b)
	}
}

// The raised button: face on the first row, ▄ after it, ▀ under it one
// column right; pressed, the face moves right and the shadow goes.
func TestButtonShadowAndPress(t *testing.T) {
	for _, cs := range []ansi.Charset{ansi.CharsetCP437, ansi.CharsetUTF8} {
		f := ansi.NewFrame(0, 1, 30, 2, 0)
		f.Charset = cs
		bar := NewActionBar(f)
		bar.Buttons = []ActionButton{{Label: "Who"}, {Label: "Help"}}
		bar.Render()
		g := frameGrid(f)
		if string(g[0][1:6]) != " Who " || g[0][6] != halfLower || g[1][1] != ' ' || !bytes.Equal(g[1][2:7], bytes.Repeat([]byte{halfUpper}, 5)) {
			t.Fatalf("raised: %q / %q", g[0], g[1])
		}
		if c := f.CellAt(6, 0); c.Attr&0x0F != bar.ShadowAttr&0x0F || c.Attr&0x70 != bar.BarAttr&0x70 {
			t.Fatalf("shadow attr %v", c.Attr)
		}
		bar.Pressed = "Who"
		bar.Render()
		g = frameGrid(f)
		if string(g[0][1:7]) != "  Who " || string(g[1][1:7]) != "      " {
			t.Fatalf("pressed: %q / %q", g[0], g[1])
		}
		// Both rows of the span take the click.
		if l, ok := bar.LabelAt(6); !ok || l != "Who" {
			t.Fatal("shadow column not clickable")
		}
		var out bytes.Buffer
		_ = f.Render(&out)
		if cs == ansi.CharsetUTF8 && !strings.Contains(out.String(), "▀") {
			t.Fatalf("UTF-8 shadow not mapped: %q", out.String())
		}
	}
}

// buttonX is a column inside the named button.
func buttonX(t *testing.T, app *App, name string) int {
	t.Helper()
	app.draw()
	for _, p := range app.actionBar.pills {
		if buttonName(p.label) == name {
			return p.x0 + 2
		}
	}
	t.Fatalf("no button %q", name)
	return 0
}

// A click draws the button pushed in; its action runs only once that
// frame has been up for pressHold.
func TestButtonClickPressesThenRuns(t *testing.T) {
	checks := map[string]func(app *App, exit bool) bool{
		"Who":      func(app *App, _ bool) bool { return strings.Contains(lastNotice(app), "/who") },
		"Rooms":    func(app *App, _ bool) bool { _, ok := app.modal.(*ChannelsModal); return ok },
		"Images":   func(app *App, _ bool) bool { return app.imageViewer != nil },
		"Settings": func(app *App, _ bool) bool { return strings.Contains(app.Conn.(*bytes.Buffer).String(), "Settings") },
		"Help":     func(app *App, _ bool) bool { _, ok := app.modal.(*HelpModal); return ok },
		"Quit":     func(_ *App, exit bool) bool { return exit },
	}
	for name, ran := range checks {
		app := boxApp(t, 80, 24, "")
		x := buttonX(t, app, name)
		normal := frameGrid(app.actionFrame)
		for _, y := range []int{1, 2} { // either row; the second click is dropped
			if exit, _ := app.handleKey(click(x, y)); exit {
				t.Fatalf("%s: ran on the click", name)
			}
		}
		app.Conn.(*bytes.Buffer).Reset()
		app.draw()
		pressed := frameGrid(app.actionFrame)
		if rowText(pressed, 0) == rowText(normal, 0) || rowText(pressed, 1) == rowText(normal, 1) {
			t.Fatalf("%s: pressed frame looks the same", name)
		}
		if app.pressTick(time.Now()) || ran(app, false) {
			t.Fatalf("%s: ran before the hold", name)
		}
		exit := app.pressTick(time.Now().Add(2 * pressHold))
		if !ran(app, exit) {
			t.Fatalf("%s: didn't run after the hold", name)
		}
		if app.pressName != "" {
			t.Fatalf("%s: still pressed", name)
		}
	}
}

// Quit does what Esc does: with a video loading it stops that instead.
func TestQuitButtonIsEscape(t *testing.T) {
	app := boxApp(t, 80, 24, "")
	app.videos = &videoState{}
	app.videos.loading = "https://x.org/v.mp4"
	app.handleKey(click(buttonX(t, app, "Quit"), 1))
	app.draw()
	if app.pressTick(time.Now().Add(2 * pressHold)) {
		t.Fatal("Quit exited over a busy video")
	}
	if exit, _ := app.handleKey(ansi.Key{Type: ansi.KeyEsc}); !exit {
		t.Fatal("Esc no longer exits")
	}
}

// /help opens a panel instead of printing notices, and scrolls when it
// doesn't fit.
func TestHelpModal(t *testing.T) {
	app := boxApp(t, 80, 14, "")
	app.UserThemes = true
	before := len(app.Session.Messages())
	app.handleSlashCommand("/help")
	h, ok := app.modal.(*HelpModal)
	if !ok {
		t.Fatal("/help didn't open the panel")
	}
	if len(app.Session.Messages()) != before {
		t.Fatal("/help printed notices")
	}
	app.draw()
	g := frameGrid(app.modalFrame)
	if !strings.Contains(rowText(g, 0), "Help") || !strings.Contains(rowText(g, 2), "Commands") {
		t.Fatalf("panel top %q / %q", rowText(g, 0), rowText(g, 2))
	}
	if h.total <= h.rows {
		t.Fatalf("expected more text than fits: %d rows of %d", h.total, h.rows)
	}
	first := rowText(g, 2)
	app.handleKey(ansi.Key{Type: ansi.KeyDown})
	app.draw()
	if h.top != 1 || rowText(frameGrid(app.modalFrame), 2) == first {
		t.Fatalf("Down didn't scroll (top %d)", h.top)
	}
	app.handleKey(ansi.Key{Type: ansi.KeyEnd})
	app.draw()
	if h.top != h.total-h.rows {
		t.Fatalf("End: top %d", h.top)
	}
	app.handleKey(ansi.Key{Type: ansi.KeyMouse, Button: ansi.MouseWheelUp})
	app.handleKey(ansi.Key{Type: ansi.KeyPgUp})
	app.draw()
	if h.top >= h.total-h.rows-1 {
		t.Fatalf("wheel/PgUp: top %d", h.top)
	}
	// The conditional lines follow the system: user themes on here.
	all := ""
	for _, s := range h.sections {
		all += strings.Join(s.lines, "\n")
	}
	if !strings.Contains(all, "/theme lists") || strings.Contains(all, "/upload") {
		t.Fatal("conditional lines wrong")
	}
	app.handleKey(ansi.Key{Type: ansi.KeyEsc})
	if app.modal != nil {
		t.Fatal("Esc didn't close help")
	}
}

// Rooms: joined first (current marked), then the rest; J joins by name.
func TestRoomsJoinedFirstAndJoinByName(t *testing.T) {
	f := ansi.NewFrame(0, 0, 80, 24, 0)
	m := NewChannelsModal(f, []chat.RoomInfo{
		{Name: "art"}, {Name: "main", Joined: true, Current: true}, {Name: "tv"}, {Name: "dev", Joined: true, Unread: 3},
	})
	var order []string
	for _, r := range m.rooms {
		order = append(order, r.Name)
	}
	if strings.Join(order, ",") != "main,dev,art,tv" {
		t.Fatalf("order %v", order)
	}
	m.Render()
	if row := rowText(frameGrid(f), m.list.y0); !strings.Contains(row, "#main") || !strings.Contains(row, "here") {
		t.Fatalf("first row %q", row)
	}
	m.HandleKey(charKey('j'))
	for _, r := range "#New-room q" { // q is text here; the space is dropped
		if m.HandleKey(charKey(r)) {
			t.Fatal("prompt closed on typing")
		}
	}
	m.HandleKey(ansi.Key{Type: ansi.KeyBackspace})
	m.Render()
	if _, _, ok := findText(f, "Join room: #New-room"); !ok {
		t.Fatal("prompt not shown")
	}
	if !m.HandleKey(enterKey) || m.Join != "New-room" {
		t.Fatalf("join %q", m.Join)
	}
	// Esc puts the prompt away without closing the panel.
	m = NewChannelsModal(f, nil)
	m.HandleKey(charKey('J'))
	if m.HandleKey(escKey) || m.prompting {
		t.Fatal("Esc on the prompt")
	}
}

// Who: M puts "/msg <name> " in the input; I toggles the ignore.
func TestWhoMessageAndIgnore(t *testing.T) {
	app := boxApp(t, 80, 24, "")
	var stored string
	app.Ignores = &ignore.List{
		Load: func() string { return stored },
		Save: func(v string) error { stored = v; return nil },
	}
	app.openRoster([]string{"alice", "Bob_Smith"})
	app.handleKey(ansi.Key{Type: ansi.KeyDown}) // alice, Bob_Smith (sorted)
	app.draw()
	if _, _, ok := findText(app.modalFrame, "Ignored: no"); !ok {
		t.Fatal("ignored state not shown")
	}
	app.handleKey(charKey('i'))
	app.draw()
	if !app.Ignores.Hidden("bob_smith", "") {
		t.Fatal("I didn't ignore")
	}
	if _, _, ok := findText(app.modalFrame, "Ignored: yes"); !ok {
		t.Fatal("state not updated")
	}
	if _, _, ok := findText(app.modalFrame, "I unignore"); !ok {
		t.Fatal("key line didn't switch to unignore")
	}
	app.handleKey(charKey('I'))
	if app.Ignores.Hidden("bob_smith", "") {
		t.Fatal("I didn't unignore")
	}
	app.handleKey(charKey('m'))
	if app.modal != nil || app.inputLine.Value() != "/msg Bob_Smith " {
		t.Fatalf("M: modal %v input %q", app.modal != nil, app.inputLine.Value())
	}
	// Ourselves: M and I do nothing.
	app.openRoster([]string{"alice"})
	app.handleKey(charKey('i'))
	app.handleKey(charKey('m'))
	if app.modal == nil || app.Ignores.Hidden("alice", "") {
		t.Fatal("M/I acted on ourselves")
	}
}

// A click on a key-line action does its key.
func TestHintClicks(t *testing.T) {
	app := boxApp(t, 80, 24, "")
	app.openRoster([]string{"alice", "bob"})
	app.handleKey(ansi.Key{Type: ansi.KeyDown})
	app.draw()
	x, y, ok := findText(app.modalFrame, "M message")
	if !ok {
		t.Fatal("no M message hint")
	}
	app.handleKey(click(x+3, y))
	if app.modal != nil || app.inputLine.Value() != "/msg bob " {
		t.Fatalf("M click: input %q", app.inputLine.Value())
	}

	f := ansi.NewFrame(0, 0, 80, 24, 0)
	m := NewChannelsModal(f, []chat.RoomInfo{{Name: "main", Joined: true}})
	m.Render()
	x, y, _ = findText(f, "J join by name")
	if m.HandleMouse(click(x, y)) || !m.prompting {
		t.Fatal("J click didn't open the prompt")
	}
	m.Render()
	x, y, _ = findText(f, "Esc cancel")
	if m.HandleMouse(click(x+1, y)) || m.prompting {
		t.Fatal("Esc cancel click")
	}
	m.Render()
	x, y, _ = findText(f, "Esc close")
	if !m.HandleMouse(click(x, y)) {
		t.Fatal("Esc close click")
	}
	// A click beside the hints does nothing.
	m.Render()
	if m.HandleMouse(click(1, f.H-2)) {
		t.Fatal("click off the hints acted")
	}

	app.handleSlashCommand("/help")
	app.draw()
	x, y, _ = findText(app.modalFrame, "Esc close")
	app.handleKey(click(x+4, y))
	if app.modal != nil {
		t.Fatal("help Esc close click")
	}

	app.openImageViewer()
	var out bytes.Buffer
	_ = app.imageViewer.Render(&out)
	s := app.imageViewer.hints
	if len(s) != 1 {
		t.Fatalf("viewer hints %v", s)
	}
	app.handleKey(click(s[0].x0, s[0].y))
	if app.imageViewer != nil {
		t.Fatal("viewer Esc click")
	}
}

// The panels' synchronous key lines: /settings "Esc cancel", the colour
// grid "Esc cancel", the upload dialog "[Enter] start upload".
func TestSyncPanelHintClicks(t *testing.T) {
	g := newColourGrid(false, "")
	f := g.draw(boxApp(t, 80, 24, "").theme(), ansi.CharsetCP437, 80, 24)
	x, y, _ := findText(f, "Enter use")
	if done, ok := g.handle(click(f.X+x, f.Y+y)); !done || !ok {
		t.Fatal("grid Enter use click")
	}
	x, y, _ = findText(f, "Esc cancel")
	if done, ok := g.handle(click(f.X+x, f.Y+y)); !done || ok {
		t.Fatal("grid Esc cancel click")
	}

	// The upload dialog is 60x13 at (10,5); its key line is row 10.
	app := boxApp(t, 80, 24, sgrClick(12+1, 5+10))
	if !app.confirmUpload() {
		t.Fatal("[Enter] start upload click")
	}

	// /settings (centred; found from a first draw) closes on Esc cancel.
	app = boxApp(t, 80, 24, "")
	p := newSettingsPanel("alice", nil)
	app.drawSettings(p, nil)
	sx, sy, _ := findText(p.frame, "Esc cancel")
	app = boxApp(t, 80, 24, sgrClick(p.frame.X+sx, p.frame.Y+sy)+"s")
	if r := app.runSettings(); r != settingsClosed {
		t.Fatalf("settings Esc cancel click: %v", r)
	}
	if strings.Contains(lastNotice(app), "saved") {
		t.Fatal("the click went on to save")
	}
}

// The lounge keeps its caption bar, now on the transcript area's last row,
// with the video one row shorter and popups above it.
func TestLoungeCaptionRow(t *testing.T) {
	app := loungeApp(t)
	app.addPopup(&chat.Message{Nick: &chat.Nick{Name: "bob"}, Str: "hello tv"})
	app.draw()
	fg := app.fgAnimFrame
	last := fg.H - 1
	x, y, ok := findText(fg, "tuned to cam")
	if !ok || y != last {
		t.Fatalf("caption at row %d (ok %v), want %d", y, ok, last)
	}
	if fg.CellAt(x, y).Attr != app.theme().TVCaption || fg.CellAt(0, last).Attr != app.theme().TVCaption {
		t.Fatal("caption not in tv_caption")
	}
	if _, py, ok := findText(fg, "hello tv"); !ok || py != last-1 {
		t.Fatalf("popup at row %d, want %d", py, last-1)
	}
	// The buttons and the input are still there.
	if got := buttonNames(app.actionBar.Buttons); !strings.HasPrefix(got, "Who") {
		t.Fatalf("buttons %s", got)
	}
	// The history overlay ends above the caption.
	app.showLoungeHistory()
	app.draw()
	if app.transcript.Reserve != 1 {
		t.Fatalf("reserve %d", app.transcript.Reserve)
	}
	app.hideLoungeHistory()
	app.tvOptedOut = true
	app.draw()
	if app.transcript.Reserve != 0 {
		t.Fatal("reserve kept outside the lounge")
	}
}
