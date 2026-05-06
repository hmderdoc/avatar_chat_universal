package avatar

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
)

// Editor is an in-door drawing UI for 10x6 CP437 avatars. It scales the
// canvas to a 10x12 "pixel" grid using CP437 half-block (0xDF) cells:
// each underlying avatar cell becomes two stacked pixels, with the cell's
// FG attribute coloring the top pixel and the BG attribute coloring the
// bottom. This lets the user edit at half-block resolution while still
// producing a valid 120-byte avatar.
//
// Modes:
//   PIXEL  arrows move one half-pixel; space paints current FG (top) /
//          BG (bottom) into the cursor cell, replacing the underlying
//          char with 0xDF.
//   CHAR   arrows move whole cells; opens a CP437 glyph picker; selecting
//          a glyph stamps it at the cursor cell with current FG/BG.
//   BRUSH  like PIXEL but every arrow paints the destination as it moves.
//
// Keys: see helpLines.
type Editor struct {
	conn  io.Writer
	input *ansi.Input
	frame *ansi.Frame

	width, height int
	Charset       ansi.Charset

	// canvas is the 120-byte avatar being edited.
	canvas Avatar

	// Cursor in pixel coordinates: pixelX 0..9, pixelY 0..11.
	pixelX, pixelY int

	// Current paint colors.
	fg uint8 // 0..15
	bg uint8 // 0..7

	mode editMode

	// Char-mode picker state.
	charPickerOpen bool
	charPickerIdx  int

	// Undo: keep last 64 cell-mutations (oldChar, oldAttr, cellX, cellY).
	undo []editOp

	// Collections for "load existing" (P2).
	Collections []*Collection
	loadOpen    bool
	loadCollIdx int
	loadAvIdx   int

	// Last-tick time for cursor blink.
	lastBlink time.Time
	blinkOn   bool
}

type editMode int

const (
	modePixel editMode = iota
	modeChar
	modeBrush
)

func (m editMode) String() string {
	switch m {
	case modeChar:
		return "CHAR"
	case modeBrush:
		return "BRUSH"
	default:
		return "PIXEL"
	}
}

type editOp struct {
	cellX, cellY int
	oldChar      byte
	oldAttr      byte
}

// EditorAction tells the caller why the editor closed.
type EditorAction int

const (
	EditorCancelled EditorAction = iota
	EditorSaved
)

// NewEditor builds an editor with the given baseline (or a black canvas
// if baseline is nil/empty). Pass collections to enable the "load
// existing" feature.
func NewEditor(conn io.Writer, in *ansi.Input, width, height int, baseline Avatar, collections []*Collection) *Editor {
	e := &Editor{
		conn:        conn,
		input:       in,
		width:       width,
		height:      height,
		fg:          15, // white
		bg:          1,  // blue
		mode:        modePixel,
		Collections: collections,
	}
	if len(baseline) == Bytes {
		e.canvas = baseline.Clone()
	} else {
		e.canvas = make(Avatar, Bytes)
		// Initialize all cells to black (FG=0, BG=0) using 0xDF half-block
		// so any subsequent painting touches a half-pixel cleanly.
		for i := 0; i < Bytes; i += 2 {
			e.canvas[i] = 0xDF
			e.canvas[i+1] = 0
		}
	}
	return e
}

// Run takes over the screen, runs the editor's input loop, and returns
// (action, finalCanvas, error). If action is EditorCancelled, the caller
// should discard finalCanvas.
func (e *Editor) Run() (EditorAction, Avatar, error) {
	// Editor occupies a centered modal. Sized to fit 80x24 minimum.
	// Canvas is 20 cols (10 logical pixels x pixelDispW) + 2 borders =
	// 22; side panel needs ~28 cols for the help block; +4 padding.
	w := 60
	if w > e.width-2 {
		w = e.width - 2
	}
	if w < 54 {
		w = 54
	}
	h := 18
	if h > e.height-1 {
		h = e.height - 1
	}
	if h < 17 {
		h = 17
	}
	x := (e.width - w) / 2
	y := (e.height - h) / 2
	if y < 0 {
		y = 0
	}
	e.frame = ansi.NewFrame(x, y, w, h, ansi.LightGray|ansi.BgBlack)
	e.frame.Charset = e.Charset

	_ = ansi.HideCursor(e.conn)
	defer ansi.ShowCursor(e.conn)
	if err := ansi.ClearScreen(e.conn); err != nil {
		return EditorCancelled, nil, err
	}

	for {
		e.draw()
		if err := e.frame.Render(e.conn); err != nil {
			return EditorCancelled, nil, err
		}
		key, ok, err := e.input.Next(150 * time.Millisecond)
		if err != nil {
			return EditorCancelled, nil, err
		}
		// Toggle blink on every poll so the cursor visibly pulses even
		// when the user isn't pressing keys.
		if time.Since(e.lastBlink) > 350*time.Millisecond {
			e.blinkOn = !e.blinkOn
			e.lastBlink = time.Now()
		}
		if !ok {
			continue
		}
		exit, action := e.handleKey(key)
		if exit {
			return action, e.canvas.Clone(), nil
		}
	}
}

// ----------------------------------------------------------------------------
// Drawing
// ----------------------------------------------------------------------------

const (
	canvasOffsetX = 3
	canvasOffsetY = 3
	// canvasW / canvasH are the logical pixel grid: 10 columns of
	// vertical half-blocks (one per avatar cell column) by 12 rows
	// (two halves per avatar cell row). Each logical pixel is rendered
	// pixelDispW chars wide so that on a typical terminal -- where a
	// char is roughly 2x taller than it is wide -- the displayed pixel
	// is square, matching how pixel-art editors look.
	canvasW     = 10
	canvasH     = 12
	pixelDispW  = 2 // screen columns per logical pixel
	pixelDispH  = 1 // screen rows per logical pixel
)

// helpRows is a list of help lines, each composed of alternating
// (key, description) segments. Keys render in WHITE, descriptions in
// LIGHTGRAY so the hotkey letters pop without losing the context text.
// Lines are sized to fit the editor's right-hand panel (~30 cols).
var helpRows = [][]string{
	{"arrows", " move    ", "space", " paint"},
	{"f/F", " cycle FG   ", "g/G", " cycle BG"},
	{"tab", " swap fg/bg"},
	{"c", " char  ", "b", " brush  ", "k", " fill"},
	{"u", " undo  ", "x", " clear  ", "l", " load"},
	{"h", " flip-x  ", "v", " flip-y"},
	{"s", " save  ", "esc", " cancel"},
}

func (e *Editor) draw() {
	e.frame.Clear()

	title := fmt.Sprintf(" Avatar Editor BETA (%s) ", e.mode)
	e.frame.PrintAt(2, 0, []byte(title), ansi.LightCyan|ansi.BgMagenta)

	// Honesty: this editor is rough. Most folks will have a better time
	// drawing in Moebius / Pablo Draw and using /avatar -> Upload.
	tip := "tip: Moebius/Pablo Draw + Upload may be smoother"
	if len(tip) < e.frame.W-4 {
		e.frame.PrintAt(2, 1, []byte(tip), ansi.Yellow|ansi.BgBlack)
	}

	// Canvas border (single-line box) around the 10x12 pixel area.
	e.drawCanvasBorder()
	e.drawCanvas()

	// Side panel to the right of the canvas: swatches, cursor, then help.
	infoX := canvasOffsetX + canvasScreenW() + 4
	if infoX > e.frame.W-20 {
		infoX = e.frame.W - 20
	}
	e.drawInfo(infoX, canvasOffsetY)
	helpY := canvasOffsetY + 6
	for i, row := range helpRows {
		ry := helpY + i
		if ry >= e.frame.H-1 {
			break
		}
		x := infoX
		maxX := e.frame.W - 1
		for j, seg := range row {
			if x >= maxX {
				break
			}
			attr := ansi.LightGray | ansi.BgBlack
			if j%2 == 0 {
				attr = ansi.White | ansi.BgBlack // hotkey
			}
			// Truncate the segment if it would overrun the panel.
			if x+len(seg) > maxX {
				seg = seg[:maxX-x]
			}
			e.frame.PrintAt(x, ry, []byte(seg), attr)
			x += len(seg)
		}
	}

	// Char picker overlay (P3) -- drawn last so it sits on top.
	if e.charPickerOpen {
		e.drawCharPicker()
	}
	if e.loadOpen {
		e.drawLoadPicker()
	}
}

// canvasScreenW / canvasScreenH return the visible canvas extent in screen
// cells (one logical pixel = pixelDispW x pixelDispH chars).
func canvasScreenW() int { return canvasW * pixelDispW }
func canvasScreenH() int { return canvasH * pixelDispH }

func (e *Editor) drawCanvasBorder() {
	x0 := canvasOffsetX - 1
	y0 := canvasOffsetY - 1
	x1 := canvasOffsetX + canvasScreenW()
	y1 := canvasOffsetY + canvasScreenH()
	border := ansi.LightGray | ansi.BgBlack
	e.frame.SetCell(x0, y0, 0xC9, border)
	e.frame.SetCell(x1, y0, 0xBB, border)
	e.frame.SetCell(x0, y1, 0xC8, border)
	e.frame.SetCell(x1, y1, 0xBC, border)
	for x := x0 + 1; x < x1; x++ {
		e.frame.SetCell(x, y0, 0xCD, border)
		e.frame.SetCell(x, y1, 0xCD, border)
	}
	for y := y0 + 1; y < y1; y++ {
		e.frame.SetCell(x0, y, 0xBA, border)
		e.frame.SetCell(x1, y, 0xBA, border)
	}
}

// drawCanvas paints the logical 10x12 pixel grid into a screen area
// pixelDispW * 10 wide by pixelDispH * 12 tall, so each logical pixel
// occupies pixelDispW x pixelDispH terminal cells. For 0xDF cells the
// top pixel = FG, bottom pixel = BG; for other glyphs the underlying
// char tiles across the entire pixel-rect so the symbol stays visible.
func (e *Editor) drawCanvas() {
	for py := 0; py < canvasH; py++ {
		for px := 0; px < canvasW; px++ {
			ch, attr := e.pixelDisplayCell(px, py)
			isCursor := px == e.pixelX && py == e.pixelY
			if isCursor {
				if e.blinkOn {
					attr = ansi.Yellow | ansi.BgRed
					ch = '+'
				} else {
					attr = (attr & 0x0F) | ansi.BgRed
				}
			}
			// Tile this logical pixel across pixelDispW x pixelDispH
			// screen cells so the canvas reads as proper pixel art.
			for dy := 0; dy < pixelDispH; dy++ {
				for dx := 0; dx < pixelDispW; dx++ {
					e.frame.SetCell(
						canvasOffsetX+px*pixelDispW+dx,
						canvasOffsetY+py*pixelDispH+dy,
						ch, attr,
					)
				}
			}
		}
	}
}

// pixelDisplayCell returns the (char, attr) the renderer should paint at
// the given pixel position, given the underlying canvas state.
func (e *Editor) pixelDisplayCell(px, py int) (byte, ansi.Attr) {
	cellX := px
	cellY := py / 2
	half := py % 2 // 0 = top, 1 = bottom
	off := (cellY*Width + cellX) * 2
	if off+1 >= len(e.canvas) {
		return ' ', 0
	}
	ch := e.canvas[off]
	attr := ansi.Attr(e.canvas[off+1])

	if ch == 0xDF {
		// Half-block cell: paint each pixel as a full block colored by
		// FG (top) or BG (bottom) of the underlying attr.
		fg := byte(attr & 0x0F)
		bg := byte((attr >> 4) & 0x07)
		var color byte
		if half == 0 {
			color = fg
		} else {
			color = bg
		}
		return 0xDB, ansi.Attr(color) // full block, FG = pixel color, BG = black
	}
	// Char cell: render the glyph at full size in both pixel rows.
	return ch, attr
}

func (e *Editor) drawInfo(x, y int) {
	row := y
	// FG swatch.
	e.frame.PrintAt(x, row, []byte("FG: "), ansi.LightGray|ansi.BgBlack)
	e.frame.SetCell(x+4, row, 0xDB, ansi.Attr(e.fg))
	e.frame.SetCell(x+5, row, 0xDB, ansi.Attr(e.fg))
	e.frame.PrintAt(x+7, row, []byte(fmt.Sprintf("%2d %s", e.fg, colorName(e.fg, true))), ansi.LightGray|ansi.BgBlack)
	row++
	// BG swatch.
	e.frame.PrintAt(x, row, []byte("BG: "), ansi.LightGray|ansi.BgBlack)
	e.frame.SetCell(x+4, row, 0xDB, ansi.Attr(e.bg))
	e.frame.SetCell(x+5, row, 0xDB, ansi.Attr(e.bg))
	e.frame.PrintAt(x+7, row, []byte(fmt.Sprintf("%2d %s", e.bg, colorName(uint8(e.bg), false))), ansi.LightGray|ansi.BgBlack)
	row += 2
	e.frame.PrintAt(x, row, []byte(fmt.Sprintf("Pos: (%d,%d)", e.pixelX, e.pixelY)), ansi.LightGray|ansi.BgBlack)
	row++
	e.frame.PrintAt(x, row, []byte(fmt.Sprintf("Undo: %d", len(e.undo))), ansi.LightGray|ansi.BgBlack)
}

// drawCharPicker paints the CP437 glyph chooser as an overlay over the
// canvas area. 16-wide grid, scrollable. Forbidden bytes (per
// avatar_lib.js) are dimmed.
func (e *Editor) drawCharPicker() {
	// Use the right-side panel area; resize the picker to fit the modal.
	gridW := 16
	gridH := 14
	x := 2
	y := 1
	// Background fill.
	for ry := y; ry < y+gridH+2; ry++ {
		for rx := x; rx < x+gridW+4; rx++ {
			e.frame.SetCell(rx, ry, ' ', ansi.LightGray|ansi.BgBlue)
		}
	}
	e.frame.PrintAt(x+1, y, []byte(" CP437 -- Enter selects, Esc cancels "), ansi.LightCyan|ansi.BgMagenta)
	// 224 visible glyphs (0x20..0xFF).
	for i := 0; i < gridW*gridH; i++ {
		b := byte(0x20 + i)
		gx := x + 1 + (i % gridW)
		gy := y + 2 + (i/gridW)
		attr := ansi.LightGray | ansi.BgBlue
		if charForbidden(b) != "" {
			attr = ansi.DarkGray | ansi.BgBlue
		}
		if i == e.charPickerIdx {
			attr = ansi.Yellow | ansi.BgRed
		}
		e.frame.SetCell(gx, gy, b, attr)
	}
}

func (e *Editor) drawLoadPicker() {
	if len(e.Collections) == 0 {
		return
	}
	if e.loadCollIdx >= len(e.Collections) {
		e.loadCollIdx = 0
	}
	coll := e.Collections[e.loadCollIdx]
	if e.loadAvIdx >= len(coll.Avatars) {
		e.loadAvIdx = 0
	}
	x := 1
	y := 1
	w := e.frame.W - 2
	h := e.frame.H - 2
	for ry := y; ry < y+h; ry++ {
		for rx := x; rx < x+w; rx++ {
			e.frame.SetCell(rx, ry, ' ', ansi.LightGray|ansi.BgBlue)
		}
	}
	e.frame.PrintAt(x+1, y, []byte(" Load avatar -- Enter picks, Tab cycle, Esc cancel "), ansi.LightCyan|ansi.BgMagenta)
	e.frame.PrintAt(x+1, y+1, []byte(fmt.Sprintf("Collection: %s (%d/%d)", coll.Name, e.loadCollIdx+1, len(e.Collections))), ansi.LightGray|ansi.BgBlue)
	// Render the currently-highlighted avatar at native size below.
	if len(coll.Avatars) > 0 {
		coll.Avatars[e.loadAvIdx].RenderTo(e.frame, x+2, y+3)
		e.frame.PrintAt(x+1, y+10, []byte(fmt.Sprintf("Avatar %d/%d -- arrows browse", e.loadAvIdx+1, len(coll.Avatars))), ansi.LightGray|ansi.BgBlue)
	}
}

// ----------------------------------------------------------------------------
// Input
// ----------------------------------------------------------------------------

func (e *Editor) handleKey(k ansi.Key) (exit bool, action EditorAction) {
	if e.charPickerOpen {
		return e.handleCharPickerKey(k)
	}
	if e.loadOpen {
		return e.handleLoadPickerKey(k)
	}
	switch k.Type {
	case ansi.KeyEsc:
		return true, EditorCancelled
	case ansi.KeyUp:
		e.moveCursor(0, -1)
	case ansi.KeyDown:
		e.moveCursor(0, 1)
	case ansi.KeyLeft:
		e.moveCursor(-1, 0)
	case ansi.KeyRight:
		e.moveCursor(1, 0)
	case ansi.KeyTab:
		e.fg, e.bg = e.bg&0x0F, e.fg&0x07
	case ansi.KeyChar:
		switch k.Rune {
		case ' ':
			e.paintAtCursor()
		case 'f':
			e.fg = (e.fg + 1) & 0x0F
		case 'F':
			e.fg = (e.fg + 15) & 0x0F
		case 'g':
			e.bg = (e.bg + 1) & 0x07
		case 'G':
			e.bg = (e.bg + 7) & 0x07
		case 'c':
			if e.mode == modeChar {
				e.mode = modePixel
			} else {
				e.mode = modeChar
				e.charPickerOpen = true
				e.charPickerIdx = 0
			}
		case 'b':
			if e.mode == modeBrush {
				e.mode = modePixel
			} else {
				e.mode = modeBrush
			}
		case 'k':
			e.fillBucket()
		case 'u':
			e.popUndo()
		case 'x':
			e.clearCanvas()
		case 'l':
			if len(e.Collections) > 0 {
				e.loadOpen = true
			}
		case 'h':
			e.flipX()
		case 'v':
			e.flipY()
		case 's':
			if err := e.canvas.Validate(); err != nil {
				// Keep the editor open; user might want to fix it. We
				// leave the cancel path for an explicit Esc.
				return false, EditorCancelled
			}
			return true, EditorSaved
		}
	}
	return false, EditorCancelled
}

func (e *Editor) handleCharPickerKey(k ansi.Key) (bool, EditorAction) {
	const gridW = 16
	const gridH = 14
	switch k.Type {
	case ansi.KeyEsc:
		e.charPickerOpen = false
	case ansi.KeyEnter:
		b := byte(0x20 + e.charPickerIdx)
		if charForbidden(b) == "" {
			e.stampGlyph(b)
		}
		e.charPickerOpen = false
	case ansi.KeyLeft:
		if e.charPickerIdx%gridW > 0 {
			e.charPickerIdx--
		}
	case ansi.KeyRight:
		if e.charPickerIdx%gridW < gridW-1 && e.charPickerIdx+1 < gridW*gridH {
			e.charPickerIdx++
		}
	case ansi.KeyUp:
		if e.charPickerIdx >= gridW {
			e.charPickerIdx -= gridW
		}
	case ansi.KeyDown:
		if e.charPickerIdx+gridW < gridW*gridH {
			e.charPickerIdx += gridW
		}
	}
	return false, EditorCancelled
}

func (e *Editor) handleLoadPickerKey(k ansi.Key) (bool, EditorAction) {
	switch k.Type {
	case ansi.KeyEsc:
		e.loadOpen = false
	case ansi.KeyEnter:
		coll := e.Collections[e.loadCollIdx]
		if len(coll.Avatars) > 0 {
			e.canvas = coll.Avatars[e.loadAvIdx].Clone()
			e.undo = nil
		}
		e.loadOpen = false
	case ansi.KeyTab:
		e.loadCollIdx = (e.loadCollIdx + 1) % len(e.Collections)
		e.loadAvIdx = 0
	case ansi.KeyLeft:
		if e.loadAvIdx > 0 {
			e.loadAvIdx--
		}
	case ansi.KeyRight:
		coll := e.Collections[e.loadCollIdx]
		if e.loadAvIdx+1 < len(coll.Avatars) {
			e.loadAvIdx++
		}
	}
	return false, EditorCancelled
}

// ----------------------------------------------------------------------------
// Editing primitives
// ----------------------------------------------------------------------------

func (e *Editor) moveCursor(dx, dy int) {
	// Char mode steps a whole cell vertically (= 2 pixel rows). X is
	// always 1-cell at a time since cells are 1 col wide.
	if e.mode == modeChar && dy != 0 {
		dy *= 2
	}
	nx := clamp(e.pixelX+dx, 0, canvasW-1)
	ny := clamp(e.pixelY+dy, 0, canvasH-1)
	e.pixelX = nx
	e.pixelY = ny
	if e.mode == modeBrush {
		e.paintAtCursor()
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

// paintAtCursor sets the half-pixel under the cursor to the current FG
// (top half) or BG (bottom half), forcing the cell's char to 0xDF. In
// CHAR mode it stamps the most-recently picked glyph instead.
func (e *Editor) paintAtCursor() {
	cellX := e.pixelX
	cellY := e.pixelY / 2
	half := e.pixelY % 2
	off := (cellY*Width + cellX) * 2

	e.pushUndo(cellX, cellY)

	old := e.canvas[off]
	attr := byte(e.canvas[off+1])

	// In CHAR mode, paint isn't half-pixel -- use stampGlyph instead.
	if e.mode == modeChar {
		// User intent in char-mode + space = repaint cell with current
		// FG/BG keeping its glyph (or 0xDF if none).
		if old == 0 {
			old = 0xDF
		}
		attr = byte(e.fg) | (byte(e.bg&0x07) << 4)
		e.canvas[off] = old
		e.canvas[off+1] = attr
		return
	}

	// Half-block painting: ensure the cell uses 0xDF so each half is
	// independently colored. If we're transitioning from a glyph cell,
	// preserve the BG underneath when painting the top half (and vice
	// versa) so the user doesn't lose half their previous color.
	if old != 0xDF {
		e.canvas[off] = 0xDF
		// reset the unrelated half to BG=black so the user starts clean
	}
	if half == 0 {
		// Top -- replace FG (low 4 bits).
		attr = (attr &^ 0x0F) | byte(e.fg&0x0F)
	} else {
		// Bottom -- replace BG (bits 4..6, never set bit 7 / blink).
		attr = (attr &^ 0x70) | (byte(e.bg&0x07) << 4)
	}
	e.canvas[off+1] = attr
}

// stampGlyph writes the chosen CP437 byte into the cell at the cursor
// with current FG/BG. Used by char-mode after the picker closes.
func (e *Editor) stampGlyph(ch byte) {
	cellX := e.pixelX
	cellY := e.pixelY / 2
	off := (cellY*Width + cellX) * 2
	e.pushUndo(cellX, cellY)
	e.canvas[off] = ch
	e.canvas[off+1] = byte(e.fg) | (byte(e.bg&0x07) << 4)
}

func (e *Editor) clearCanvas() {
	// Single undo entry for a full clear is overkill (60 entries); push
	// a sentinel that pop restores by zeroing.
	prev := e.canvas.Clone()
	for i := 0; i < Bytes; i += 2 {
		e.canvas[i] = 0xDF
		e.canvas[i+1] = 0
	}
	// Stash via a special op: cellX = -1 means "full snapshot in a
	// dedicated channel". To keep the undo stack uniform, just push 60
	// individual ops from prev.
	for cy := 0; cy < Height; cy++ {
		for cx := 0; cx < Width; cx++ {
			off := (cy*Width + cx) * 2
			if prev[off] != e.canvas[off] || prev[off+1] != e.canvas[off+1] {
				e.undo = append(e.undo, editOp{
					cellX: cx, cellY: cy,
					oldChar: prev[off],
					oldAttr: prev[off+1],
				})
			}
		}
	}
	e.trimUndo()
}

func (e *Editor) flipX() {
	for cy := 0; cy < Height; cy++ {
		for cx := 0; cx < Width/2; cx++ {
			lo := (cy*Width + cx) * 2
			hi := (cy*Width + (Width - 1 - cx)) * 2
			e.pushUndo(cx, cy)
			e.pushUndo(Width-1-cx, cy)
			e.canvas[lo], e.canvas[hi] = e.canvas[hi], e.canvas[lo]
			e.canvas[lo+1], e.canvas[hi+1] = e.canvas[hi+1], e.canvas[lo+1]
		}
	}
}

func (e *Editor) flipY() {
	for cy := 0; cy < Height/2; cy++ {
		for cx := 0; cx < Width; cx++ {
			lo := (cy*Width + cx) * 2
			hi := ((Height-1-cy)*Width + cx) * 2
			e.pushUndo(cx, cy)
			e.pushUndo(cx, Height-1-cy)
			e.canvas[lo], e.canvas[hi] = e.canvas[hi], e.canvas[lo]
			e.canvas[lo+1], e.canvas[hi+1] = e.canvas[hi+1], e.canvas[lo+1]
		}
	}
}

// fillBucket flood-fills connected pixels of the same color starting from
// the cursor. Top-half / bottom-half pixels are independent: a fill of a
// top-half FG only replaces FGs of matching cells, never BGs.
func (e *Editor) fillBucket() {
	startCellX := e.pixelX
	startCellY := e.pixelY / 2
	startHalf := e.pixelY % 2
	startOff := (startCellY*Width + startCellX) * 2
	if e.canvas[startOff] != 0xDF {
		// Won't fill across glyph cells; user can convert with paint first.
		return
	}
	startColor := pixelColorAt(e.canvas, startCellX, startCellY, startHalf)
	var newColor uint8
	if startHalf == 0 {
		newColor = e.fg
	} else {
		newColor = e.bg
	}
	if startColor == newColor {
		return
	}

	type pos struct{ x, y, h int }
	visited := map[pos]bool{}
	stack := []pos{{startCellX, startCellY, startHalf}}
	for len(stack) > 0 {
		p := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if visited[p] {
			continue
		}
		visited[p] = true
		if p.x < 0 || p.x >= Width || p.y < 0 || p.y >= Height {
			continue
		}
		off := (p.y*Width + p.x) * 2
		if e.canvas[off] != 0xDF {
			continue
		}
		if pixelColorAt(e.canvas, p.x, p.y, p.h) != startColor {
			continue
		}
		e.pushUndo(p.x, p.y)
		attr := byte(e.canvas[off+1])
		if p.h == 0 {
			attr = (attr &^ 0x0F) | byte(newColor&0x0F)
		} else {
			attr = (attr &^ 0x70) | (byte(newColor&0x07) << 4)
		}
		e.canvas[off+1] = attr
		stack = append(stack,
			pos{p.x - 1, p.y, p.h},
			pos{p.x + 1, p.y, p.h},
			pos{p.x, p.y, p.h ^ 1}, // jump to other half of same cell
		)
		// Vertical neighbour: top-half -> previous cell's bottom-half;
		// bottom-half -> next cell's top-half.
		if p.h == 0 {
			stack = append(stack, pos{p.x, p.y - 1, 1})
		} else {
			stack = append(stack, pos{p.x, p.y + 1, 0})
		}
	}
}

// pixelColorAt returns the color (0..15 for top, 0..7 for bottom) of the
// half-pixel at (cellX, cellY, half) in canvas. Caller must ensure the
// cell uses 0xDF.
func pixelColorAt(canvas Avatar, cellX, cellY, half int) uint8 {
	attr := byte(canvas[(cellY*Width+cellX)*2+1])
	if half == 0 {
		return attr & 0x0F
	}
	return (attr >> 4) & 0x07
}

// ----------------------------------------------------------------------------
// Undo
// ----------------------------------------------------------------------------

const undoCap = 64

func (e *Editor) pushUndo(cellX, cellY int) {
	off := (cellY*Width + cellX) * 2
	e.undo = append(e.undo, editOp{
		cellX:   cellX,
		cellY:   cellY,
		oldChar: e.canvas[off],
		oldAttr: e.canvas[off+1],
	})
	e.trimUndo()
}

func (e *Editor) trimUndo() {
	if len(e.undo) > undoCap {
		e.undo = e.undo[len(e.undo)-undoCap:]
	}
}

func (e *Editor) popUndo() {
	if len(e.undo) == 0 {
		return
	}
	op := e.undo[len(e.undo)-1]
	e.undo = e.undo[:len(e.undo)-1]
	off := (op.cellY*Width + op.cellX) * 2
	e.canvas[off] = op.oldChar
	e.canvas[off+1] = op.oldAttr
}

// ----------------------------------------------------------------------------
// Color names (for the swatch label)
// ----------------------------------------------------------------------------

var fgNames = []string{
	"black", "blue", "green", "cyan", "red", "magenta", "brown", "lightgray",
	"darkgray", "ltblue", "ltgreen", "ltcyan", "ltred", "ltmagenta", "yellow", "white",
}
var bgNames = []string{
	"black", "blue", "green", "cyan", "red", "magenta", "brown", "ltgray",
}

func colorName(c uint8, fg bool) string {
	if fg {
		if int(c) < len(fgNames) {
			return fgNames[c]
		}
	} else {
		if int(c) < len(bgNames) {
			return bgNames[c]
		}
	}
	return strings.TrimSpace(fmt.Sprintf("%d", c))
}
