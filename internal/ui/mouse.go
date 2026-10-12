package ui

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
)

// Mouse and live resize.
//
// Mouse: with Mouse set, Run asks the terminal for button reports (xterm
// 1000, SGR 1006) and the App acts on them: the wheel scrolls the chat or
// moves a list's selection, the buttons press and run, a panel's key
// line actions run, a speaker's name starts a /msg, a picture opens in /img, a
// "► /play" line plays, and a link card shows its full URL (the terminal
// can't open it, and with reports on, its own select-to-copy may need
// Shift). /mouse turns it off for the user.
//
// Resize: with ResizeEvery set, the loop sends ansi.SizeProbe between
// frames; the reply comes back as KeyResize and Resize re-lays out the
// screen, keeping what's typed and where the chat is scrolled.

// wheelRows is how far one wheel notch scrolls the chat.
const wheelRows = 3

// Minimum screen the layout copes with; smaller reports are ignored (a
// Shift-F3 is ESC[1;2R, which reads as a 2x1 "size").
const (
	minCols = 40
	minRows = 12
)

// listGeom is where a list modal drew its rows, for mapping a click.
type listGeom struct {
	top  int // index of the first visible entry
	y0   int // frame row of the first visible entry
	rows int // visible rows
	x1   int // clicks at x >= x1 aren't on the list
	n    int // entries
}

// pick maps a left press to an entry index.
func (g listGeom) pick(k ansi.Key) (int, bool) {
	if k.Release || k.Button != ansi.MouseLeft || k.X >= g.x1 {
		return 0, false
	}
	r := k.Y - g.y0
	if r < 0 || r >= g.rows || g.top+r >= g.n {
		return 0, false
	}
	return g.top + r, true
}

// wheelKey turns a wheel notch into Up/Down, anything else into a no-op.
func wheelKey(k ansi.Key) ansi.Key {
	if k.Release {
		return ansi.Key{Type: ansi.KeyChar}
	}
	switch k.Button {
	case ansi.MouseWheelUp:
		return ansi.Key{Type: ansi.KeyUp}
	case ansi.MouseWheelDown:
		return ansi.Key{Type: ansi.KeyDown}
	}
	return ansi.Key{Type: ansi.KeyChar}
}

// setMouse switches reporting on the terminal and in the decoder.
func (a *App) setMouse(on bool) {
	a.finishFrame()
	if on {
		_, _ = a.Conn.Write([]byte(ansi.MouseOn))
	} else {
		_, _ = a.Conn.Write([]byte(ansi.MouseOff))
	}
	a.Input.SetMouse(on)
	a.mouseOn = on
}

// handleMouseCmd is /mouse [on|off].
func (a *App) handleMouseCmd(args []string) {
	if !a.MouseAllowed {
		a.Session.Notice("/mouse: mouse support is off on this system")
		return
	}
	want := !a.mouseOn
	if len(args) > 0 {
		switch strings.ToLower(args[0]) {
		case "on", "yes", "1":
			want = true
		case "off", "no", "0":
			want = false
		}
	}
	a.setMouse(want)
	if a.OnMouseSave != nil {
		if err := a.OnMouseSave(want); err != nil {
			a.Session.Notice(fmt.Sprintf("/mouse: %v", err))
		}
	}
	if want {
		a.Session.Notice("mouse on: wheel scrolls, click names, pictures, /play lines and the buttons (/mouse off)")
	} else {
		a.Session.Notice("mouse off (/mouse on brings it back)")
	}
}

// handleMouse acts on one report. Returns exit like handleKey.
func (a *App) handleMouse(k ansi.Key) (exit bool) {
	if a.imageViewer != nil {
		if closeHit(k, 0, 0) {
			a.closeImageViewer()
			return false
		}
		if hk, ok := a.imageViewer.hints.hit(k, 0, 0); ok {
			if a.imageViewer.HandleKey(hk) {
				a.closeImageViewer()
			}
			a.dirty = true
			return false
		}
		wk := wheelKey(k)
		if wk.Type == ansi.KeyUp {
			wk.Type = ansi.KeyLeft
		} else if wk.Type == ansi.KeyDown {
			wk.Type = ansi.KeyRight
		}
		if wk.Type != ansi.KeyChar {
			a.imageViewer.HandleKey(wk)
			a.Session.MarkBitmapViewed(a.imageViewer.CurrentIndex())
			a.dirty = true
		}
		return false
	}
	if a.modal != nil {
		m := a.modal
		closed := m.HandleMouse(k)
		a.dirty = true
		if closed {
			return a.afterModal(m)
		}
		return false
	}
	if k.Release {
		return false
	}
	// Frame rows: the header is row 0, the buttons rows 1-2, the
	// transcript starts at row 3; the input sits at its end.
	switch k.Button {
	case ansi.MouseWheelUp, ansi.MouseWheelDown:
		if a.loungeActive() {
			return false
		}
		if k.Button == ansi.MouseWheelUp {
			a.transcript.Scroll += wheelRows
		} else if a.transcript.Scroll -= wheelRows; a.transcript.Scroll < 0 {
			a.transcript.Scroll = 0
		}
		a.markActive()
		a.dirty = true
		return false
	case ansi.MouseLeft:
	default:
		return false
	}
	a.markActive()
	switch {
	case k.Y == a.headerFrame.Y && a.header.CloseHit(k.X):
		return a.clickPill("[X]")
	case k.Y >= a.actionFrame.Y && k.Y < a.actionFrame.Y+a.actionFrame.H:
		if label, ok := a.actionBar.LabelAt(k.X); ok {
			a.pressButton(label)
		}
	case k.Y >= a.transcriptFrame.Y && k.Y < a.transcriptFrame.Y+a.transcriptFrame.H && !a.loungeActive():
		if h, ok := a.transcript.HitAt(k.X-a.transcriptFrame.X, k.Y-a.transcriptFrame.Y); ok {
			a.clickHit(h)
		}
	}
	return false
}

// pressHold is how long a clicked button shows pushed in before its
// action runs.
const pressHold = 100 * time.Millisecond

// pressButton starts a click on a button: the next frame draws it pushed
// in, and pressTick runs it once that frame has been up for pressHold. A
// click while another press is under way is dropped.
func (a *App) pressButton(label string) {
	if a.pressName != "" {
		return
	}
	a.pressName, a.pressLabel, a.pressDrawn = buttonName(label), label, false
	a.dirty = true
}

// pressTick runs the pressed button's action when it's due. Returns exit
// like handleKey.
func (a *App) pressTick(now time.Time) (exit bool) {
	if a.pressName == "" || !a.pressDrawn || now.Before(a.pressUntil) {
		return false
	}
	label := a.pressLabel
	a.pressName, a.pressLabel, a.pressDrawn = "", "", false
	a.dirty = true // the face pops back out
	return a.clickPill(label)
}

// buttonCommands maps a button's name to the command it runs.
var buttonCommands = map[string]string{
	"Who":      "/who",
	"Rooms":    "/channels",
	"Images":   "/img",
	"Settings": "/settings",
	"Help":     "/help",
}

// clickPill runs a button's action (or the header's [X]).
func (a *App) clickPill(label string) (exit bool) {
	name := buttonName(label) // "Images [3]" -> "Images", "Quit Esc" -> "Quit"
	switch name {
	case "Quit", "[X]":
		return a.escape()
	}
	cmd, ok := buttonCommands[name]
	if !ok {
		return false
	}
	exit, _ = a.handleSlashCommand(cmd)
	a.dirty = true
	return exit
}

// escape is what the Esc key does outside a modal.
func (a *App) escape() bool {
	if a.videoBusy() {
		a.stopVideo()
		a.Session.Notice("video stopped")
		return false
	}
	return true
}

func (a *App) clickHit(h hit) {
	switch h.Kind {
	case hitSpeaker:
		if !strings.EqualFold(h.Ref, a.selfName()) {
			a.inputLine.SetValue("/msg " + h.Ref + " ")
		}
	case hitPicture:
		a.openImageViewer()
		if a.imageViewer != nil {
			a.imageViewer.ShowRaster(h.Ref)
		}
	case hitMedia:
		a.playLink(h.Ref)
	case hitLink:
		a.Session.Notice("link: " + h.Ref)
	}
	a.dirty = true
}

// Resize re-lays out the screen at cols x rows, keeping the typed line,
// the scroll position and the chat state; open lists and the image viewer
// close, since they were laid out for the old size.
func (a *App) Resize(cols, rows int) {
	if cols < minCols || rows < minRows || (cols == a.Width && rows == a.Height) {
		return
	}
	if a.imageViewer != nil {
		a.imageViewer = nil
	}
	a.modal, a.modalFrame = nil, nil
	in, tr := a.inputLine, a.transcript
	a.Width, a.Height = cols, rows
	a.layout()
	in.Frame, tr.Frame = a.inputLine.Frame, a.transcript.Frame
	a.inputLine, a.transcript = in, tr
	a.ApplyTheme(a.theme()) // rebinds colours and force-redraws
	if a.idleAnim != nil {
		a.startAnimation(nil) // sized for the old frame
	}
	a.dirty = true
}

// pauseMouse turns reporting off for a full-screen sub-program (editors,
// pickers, Zmodem) that reads keys itself, and returns the undo.
func (a *App) pauseMouse() func() {
	if !a.mouseOn {
		return func() {}
	}
	a.setMouse(false)
	a.mousePaused = true
	return func() {
		a.mousePaused = false
		a.setMouse(true)
	}
}

// finishFrame writes the rest of an in-flight frame (blocking), so a
// control sequence written straight to the conn can't splice into it.
func (a *App) finishFrame() {
	if a.frameInFlight() {
		_, _ = a.Conn.Write(a.pending[a.pendingOff:])
		a.pendingOff = len(a.pending)
	}
}

// probeSize sends a size probe when it's due and nothing else is on the
// wire; the reply arrives through the input as KeyResize.
func (a *App) probeSize(now time.Time) {
	if a.ResizeEvery <= 0 || now.Sub(a.lastSizeProbe) < a.ResizeEvery || a.frameInFlight() {
		return
	}
	a.lastSizeProbe = now
	// A terminal without CSI u leaves the cursor in the far corner, so put
	// it back here. Not by a redraw: the /img viewer re-sends its whole
	// picture on each frame, which flickered every probe.
	var b bytes.Buffer
	b.WriteString(ansi.SizeProbe)
	_ = a.restCursor(&b)
	_, _ = a.Conn.Write(b.Bytes())
}
