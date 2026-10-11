package canvas

import (
	"fmt"
	"io"
	"time"

	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
)

// FKeySets are the F1-F10 glyph sets, PabloDraw's defaults as Moebius ships
// them (moebius_chat/app/prefs.js), so artists' muscle memory carries over.
// Set 6 (index 5) is Moebius's default: shades, blocks and half blocks.
var FKeySets = [][10]byte{
	{218, 191, 192, 217, 196, 179, 195, 180, 193, 194},
	{201, 187, 200, 188, 205, 186, 204, 185, 202, 203},
	{213, 184, 212, 190, 205, 179, 198, 181, 207, 209},
	{214, 183, 211, 189, 196, 186, 199, 182, 208, 210},
	{197, 206, 216, 215, 232, 232, 155, 156, 153, 239},
	{176, 177, 178, 219, 223, 220, 221, 222, 254, 250},
	{1, 2, 3, 4, 5, 6, 240, 14, 15, 32},
	{24, 25, 30, 31, 16, 17, 18, 29, 20, 21},
	{174, 175, 242, 243, 169, 170, 253, 246, 171, 172},
	{227, 241, 244, 245, 234, 157, 228, 248, 251, 252},
	{224, 225, 226, 229, 230, 231, 235, 236, 237, 238},
	{128, 135, 165, 164, 152, 159, 247, 249, 173, 168},
	{131, 132, 133, 160, 166, 134, 142, 143, 145, 146},
	{136, 137, 138, 130, 144, 140, 139, 141, 161, 158},
	{147, 148, 149, 162, 167, 150, 129, 151, 163, 154},
	{47, 92, 40, 41, 123, 125, 91, 93, 96, 39},
}

// defaultSet is Moebius's default_fkeys.
const defaultSet = 5

// undoLevels is how many edits can be undone.
const undoLevels = 64

// Control keys. Ctrl-H, -I, -J and -M arrive as Backspace, Tab and Enter.
const (
	ctrlB = 0x02 // brush
	ctrlE = 0x05 // pick colours from the cell
	ctrlF = 0x06 // next foreground
	ctrlG = 0x07 // next background
	ctrlK = 0x0B // fill
	ctrlL = 0x0C // redraw
	ctrlN = 0x0E // next glyph set
	ctrlO = 0x0F // half-block mode
	ctrlP = 0x10 // previous glyph set
	ctrlU = 0x15 // undo
	ctrlX = 0x18 // clear
	ctrlY = 0x19 // send
	ctrlZ = 0x1A // undo
)

// Result is how the editor closed.
type Result struct {
	Sent    bool
	Message string // the [BITMAP|...] message when Sent
	W, H    int    // the size actually sent, after trimming
}

// Editor is the full-screen art editor. It owns the screen and the input
// while Run is active.
type Editor struct {
	conn    io.Writer
	input   *ansi.Input
	screenW int
	screenH int
	Charset ansi.Charset
	// From is the sender name put in the message.
	From string

	c    *Canvas
	hist *History

	cx, cy int // cursor cell
	py     int // cursor half-row in half-block mode (0..2H-1)
	half   bool
	brush  bool
	fg, bg byte
	set    int
	glyph  byte // what the brush and fill stamp in character mode

	vx, vy int // viewport origin on the canvas

	dirty      bool // drawn since the last send
	confirming bool // "discard?" prompt is up
	status     string
	statusAt   time.Time

	blinkOn   bool
	lastBlink time.Time
	frame     *ansi.Frame
}

// NewEditor returns an editor for a w x h canvas on a screenW x screenH
// terminal.
func NewEditor(conn io.Writer, in *ansi.Input, screenW, screenH, w, h int) *Editor {
	return &Editor{
		conn: conn, input: in, screenW: screenW, screenH: screenH,
		c: New(w, h), hist: NewHistory(undoLevels),
		fg: 15, bg: 0, set: defaultSet, glyph: FullBlock,
	}
}

// Canvas exposes the canvas, for tests and for seeding.
func (e *Editor) Canvas() *Canvas { return e.c }

// Run takes over the screen until the art is sent or abandoned.
func (e *Editor) Run() (Result, error) {
	e.frame = ansi.NewFrame(0, 0, e.screenW, e.screenH, ansi.LightGray|ansi.BgBlack)
	e.frame.Charset = e.Charset
	_ = ansi.HideCursor(e.conn)
	defer ansi.ShowCursor(e.conn)
	if err := ansi.ClearScreen(e.conn); err != nil {
		return Result{}, err
	}
	for {
		e.draw()
		if err := e.frame.Render(e.conn); err != nil {
			return Result{}, err
		}
		key, ok, err := e.input.Next(150 * time.Millisecond)
		if err != nil {
			return Result{}, err
		}
		if time.Since(e.lastBlink) > 350*time.Millisecond {
			e.blinkOn = !e.blinkOn
			e.lastBlink = time.Now()
		}
		if !ok {
			continue
		}
		if done, res := e.HandleKey(key); done {
			return res, nil
		}
	}
}

// viewH is the number of canvas rows on screen: everything but the status
// line on top and the key line at the bottom.
func (e *Editor) viewH() int {
	if h := e.screenH - 2; h > 0 {
		return h
	}
	return 1
}

func (e *Editor) say(format string, args ...interface{}) {
	e.status = fmt.Sprintf(format, args...)
	e.statusAt = time.Now()
}

func (e *Editor) edit() {
	e.hist.Push(e.c)
	e.dirty = true
}

func (e *Editor) brushCell() Cell { return Cell{Ch: e.glyph, Attr: Attr(e.fg, e.bg)} }

// stamp writes glyph at the cursor with the current colours.
func (e *Editor) stamp(ch byte) {
	e.edit()
	e.glyph = ch
	e.c.Set(e.cx, e.cy, Cell{Ch: ch, Attr: Attr(e.fg, e.bg)})
}

func (e *Editor) paint() {
	e.edit()
	if e.half {
		e.c.SetPixel(e.cx, e.py, e.fg)
	} else {
		e.c.Set(e.cx, e.cy, e.brushCell())
	}
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// move shifts the cursor; in half-block mode dy counts half rows.
func (e *Editor) move(dx, dy int) {
	e.cx = clamp(e.cx+dx, 0, e.c.W-1)
	if e.half {
		e.py = clamp(e.py+dy, 0, 2*e.c.H-1)
		e.cy = e.py / 2
	} else {
		e.cy = clamp(e.cy+dy, 0, e.c.H-1)
		e.py = e.cy * 2
	}
	if e.brush && (dx != 0 || dy != 0) {
		e.paint()
	}
}

// HandleKey applies one key. done is true when the editor should close.
func (e *Editor) HandleKey(k ansi.Key) (done bool, res Result) {
	if e.confirming {
		e.confirming = false
		if k.Type == ansi.KeyChar && (k.Rune == 'y' || k.Rune == 'Y') {
			return true, Result{}
		}
		e.say("kept your drawing")
		return false, Result{}
	}
	switch k.Type {
	case ansi.KeyEsc:
		if e.dirty {
			e.confirming = true
			return false, Result{}
		}
		return true, Result{}
	case ansi.KeyUp:
		e.move(0, -1)
	case ansi.KeyDown:
		e.move(0, 1)
	case ansi.KeyLeft:
		e.move(-1, 0)
	case ansi.KeyRight:
		e.move(1, 0)
	case ansi.KeyHome:
		e.cx = 0
	case ansi.KeyEnd:
		e.cx = e.c.W - 1
	case ansi.KeyPgUp:
		e.move(0, -e.pageStep())
	case ansi.KeyPgDn:
		e.move(0, e.pageStep())
	case ansi.KeyEnter:
		if !e.half {
			e.cx = 0
			e.move(0, 1)
		}
	case ansi.KeyBackspace:
		if e.cx > 0 {
			e.cx--
		}
		e.edit()
		e.c.Set(e.cx, e.cy, Cell{Ch: Space, Attr: Attr(7, e.bg)})
	case ansi.KeyDelete:
		e.edit()
		e.c.Set(e.cx, e.cy, Cell{Ch: Space, Attr: Attr(7, e.bg)})
	case ansi.KeyTab:
		e.fg, e.bg = e.bg, e.fg&0x07
	case ansi.KeyF1, ansi.KeyF2, ansi.KeyF3, ansi.KeyF4, ansi.KeyF5,
		ansi.KeyF6, ansi.KeyF7, ansi.KeyF8, ansi.KeyF9, ansi.KeyF10:
		e.stamp(FKeySets[e.set][int(k.Type-ansi.KeyF1)])
		if !e.half {
			e.move(1, 0)
		}
	case ansi.KeyChar:
		return e.handleChar(k)
	}
	return false, Result{}
}

func (e *Editor) handleChar(k ansi.Key) (bool, Result) {
	if !k.Ctrl && k.Rune >= 0x20 && k.Rune < 0x7F {
		if e.half {
			if k.Rune == ' ' {
				e.paint()
			}
			return false, Result{}
		}
		e.stamp(byte(k.Rune))
		e.move(1, 0)
		return false, Result{}
	}
	switch k.Rune {
	case ctrlY:
		return e.send()
	case ctrlZ, ctrlU:
		if prev := e.hist.Pop(); prev != nil {
			e.c = prev
			e.dirty = true
			e.say("undone (%d more)", e.hist.Len())
		} else {
			e.say("nothing to undo")
		}
	case ctrlO:
		e.half = !e.half
		e.py = e.cy * 2
		e.say("%s mode", e.modeName())
	case ctrlB:
		e.brush = !e.brush
		if e.brush {
			e.say("brush on: moving paints")
		} else {
			e.say("brush off")
		}
	case ctrlF:
		e.fg = (e.fg + 1) & 0x0F
	case ctrlG:
		e.bg = (e.bg + 1) & 0x07
	case ctrlE:
		cell := e.c.At(e.cx, e.cy)
		if e.half {
			e.fg = e.c.Pixel(e.cx, e.py)
		} else {
			e.fg, e.bg, e.glyph = cell.Fg(), cell.Bg(), cell.Ch
		}
		e.say("picked colours from the cursor")
	case ctrlN:
		e.set = (e.set + 1) % len(FKeySets)
	case ctrlP:
		e.set = (e.set + len(FKeySets) - 1) % len(FKeySets)
	case ctrlK:
		e.edit()
		fill := e.brushCell()
		if e.half {
			fill = fromHalves(e.fg, e.fg)
		}
		if n := e.c.Fill(e.cx, e.cy, fill); n == 0 {
			e.hist.Pop()
		}
	case ctrlX:
		e.edit()
		e.c.Clear()
		e.say("cleared (^Z undoes)")
	case ctrlL:
		if e.frame != nil {
			e.frame.ForceRedraw()
		}
	}
	return false, Result{}
}

func (e *Editor) send() (bool, Result) {
	msg, ok := e.c.Bitmap(e.From)
	if !ok {
		e.say("nothing drawn yet")
		return false, Result{}
	}
	if len(msg) > MaxMessage {
		e.say("too detailed to send (%d chars, max %d): simplify or ^X", len(msg), MaxMessage)
		return false, Result{}
	}
	_, _, x1, y1, _ := e.c.Bounds()
	e.dirty = false
	return true, Result{Sent: true, Message: msg, W: x1 + 1, H: y1 + 1}
}

// pageStep is a screenful of rows in the cursor's units.
func (e *Editor) pageStep() int {
	if e.half {
		return 2 * e.viewH()
	}
	return e.viewH()
}

func (e *Editor) modeName() string {
	if e.half {
		return "HALF"
	}
	return "CHAR"
}

// follow scrolls the viewport so the cursor stays on screen.
func (e *Editor) follow() {
	vh := e.viewH()
	if e.cy < e.vy {
		e.vy = e.cy
	}
	if e.cy >= e.vy+vh {
		e.vy = e.cy - vh + 1
	}
	if e.cx < e.vx {
		e.vx = e.cx
	}
	if e.cx >= e.vx+e.screenW {
		e.vx = e.cx - e.screenW + 1
	}
}

func (e *Editor) draw() {
	f := e.frame
	f.Clear()
	e.follow()
	vh := e.viewH()
	off := ansi.DarkGray | ansi.BgBlack
	for r := 0; r < vh; r++ {
		y := e.vy + r
		for s := 0; s < e.screenW; s++ {
			x := e.vx + s
			if !e.c.In(x, y) {
				f.SetCell(s, r+1, 0xFA, off) // middle dot: off the canvas
				continue
			}
			cell := e.c.At(x, y)
			ch, attr := cell.Ch, ansi.Attr(cell.Attr)
			if x == e.cx && y == e.cy {
				ch, attr = e.cursorCell(cell)
			}
			f.SetCell(s, r+1, ch, attr)
		}
	}
	e.drawStatus()
	e.drawKeys()
	if e.confirming {
		msg := " Discard this drawing? y = discard, any other key = keep "
		x := (e.screenW - len(msg)) / 2
		if x < 0 {
			x = 0
		}
		f.PrintAt(x, e.screenH/2, []byte(msg), ansi.White|ansi.BgRed)
	}
}

// cursorCell shows the cursor: a blinking marker in character mode; in
// half-block mode the half under the cursor blinks between the paint colour
// and the cell.
func (e *Editor) cursorCell(cell Cell) (byte, ansi.Attr) {
	if !e.blinkOn {
		return cell.Ch, ansi.Attr(cell.Attr)
	}
	if e.half {
		top, bottom := halves(cell)
		mark := byte(15)
		if e.fg != 15 {
			mark = e.fg
		}
		if e.py%2 == 0 {
			top = mark
		} else {
			bottom = mark
		}
		c := fromHalves(top, bottom)
		return c.Ch, ansi.Attr(c.Attr)
	}
	return '+', ansi.Yellow | ansi.BgRed
}

func (e *Editor) drawStatus() {
	f := e.frame
	bar := ansi.White | ansi.BgBlue
	for x := 0; x < e.screenW; x++ {
		f.SetCell(x, 0, ' ', bar)
	}
	brush := ""
	if e.brush {
		brush = " BRUSH"
	}
	head := fmt.Sprintf(" Draw %dx%d  %d,%d  %s%s  ", e.c.W, e.c.H, e.cx+1, e.cy+1, e.modeName(), brush)
	f.PrintAt(0, 0, []byte(head), bar)
	x := len(head)
	// Colour swatch: the current fg on the current bg.
	f.SetCell(x, 0, FullBlock, ansi.Attr(Attr(e.fg, e.bg)))
	f.SetCell(x+1, 0, 'A', ansi.Attr(Attr(e.fg, e.bg)))
	f.SetCell(x+2, 0, ' ', ansi.Attr(Attr(e.bg, e.bg)))
	x += 4
	set := fmt.Sprintf("F-set %d:", e.set+1)
	f.PrintAt(x, 0, []byte(set), bar)
	x += len(set)
	for i, g := range FKeySets[e.set] {
		if x+i >= e.screenW {
			break
		}
		f.SetCell(x+i, 0, g, ansi.Yellow|ansi.BgBlue)
	}
	x += 11
	if e.status != "" && time.Since(e.statusAt) < 4*time.Second && x < e.screenW {
		s := e.status
		if x+len(s) > e.screenW {
			s = s[:e.screenW-x]
		}
		f.PrintAt(x, 0, []byte(s), ansi.Yellow|ansi.BgBlue)
	}
}

const keyLine = "^Y send ^Z undo ^O half ^B brush ^K fill ^E pick ^F^G fg/bg ^N^P F-set ^X clr"

func (e *Editor) drawKeys() {
	f := e.frame
	y := e.screenH - 1
	attr := ansi.LightGray | ansi.BgBlack
	s := keyLine
	if len(s) > e.screenW {
		s = s[:e.screenW]
	}
	f.PrintAt(0, y, []byte(s), attr)
}
