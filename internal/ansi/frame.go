package ansi

import (
	"bytes"
	"fmt"
	"io"
)

// Cell is one screen position: a single CP437 byte and its attribute. The
// Transparent flag lets a cell "vote pass" during compositing so a lower
// layer's opaque cell at the same coordinate shows through. Frames built
// with NewTransparentFrame initialize every cell as transparent; SetCell /
// Print* always emit opaque cells.
type Cell struct {
	Char        byte
	Attr        Attr
	Transparent bool
}

// Frame is a fixed-position 2D grid of cells with dirty tracking. Frames are
// always painted at absolute terminal coordinates; there is no nesting. To
// build complex layouts, instantiate multiple frames at non-overlapping
// regions and call Render on each.
//
// Cell coordinates are 0-based and frame-relative. Terminal coordinates are
// 1-based and absolute (computed at render time as F.X + col + 1, F.Y + row + 1).
type Frame struct {
	X, Y, W, H int

	Charset Charset // how to emit cell bytes; defaults to CP437

	// Transparent makes Clear() fill with transparent cells (no character,
	// no attribute, just "no opinion"). Used for overlay layers in a
	// Compositor pipeline where a higher layer should let a lower layer
	// show through wherever the higher layer hasn't painted.
	Transparent bool

	cur  [][]Cell // current state (modified by Set/Print)
	last [][]Cell // last rendered state (for diffing)

	cursorX, cursorY int
	fillAttr         Attr

	first bool // true until first Render — forces full repaint
}

// NewFrame creates an opaque frame at (x,y) with size (w,h). The frame is
// initialized to spaces with the given fill attribute. (x,y) are 0-based;
// the frame paints to terminal positions (x+1..x+w, y+1..y+h) in 1-based
// absolute coords.
func NewFrame(x, y, w, h int, fill Attr) *Frame {
	f := &Frame{X: x, Y: y, W: w, H: h, fillAttr: fill, first: true}
	f.cur = make([][]Cell, h)
	f.last = make([][]Cell, h)
	for i := 0; i < h; i++ {
		f.cur[i] = make([]Cell, w)
		f.last[i] = make([]Cell, w)
	}
	f.Clear()
	return f
}

// NewTransparentFrame creates a transparent frame at (x,y) with size (w,h).
// Every cell defaults to Transparent=true; SetCell / Print* still write
// opaque cells. Use these as upper layers in a Compositor pipeline so the
// untouched cells let the lower layer show through.
func NewTransparentFrame(x, y, w, h int) *Frame {
	f := &Frame{X: x, Y: y, W: w, H: h, Transparent: true, first: true}
	f.cur = make([][]Cell, h)
	f.last = make([][]Cell, h)
	for i := 0; i < h; i++ {
		f.cur[i] = make([]Cell, w)
		f.last[i] = make([]Cell, w)
	}
	f.Clear()
	return f
}

// Clear resets the frame. Opaque frames fill with spaces in the fill
// attribute; transparent frames fill with Transparent=true cells.
func (f *Frame) Clear() {
	var cell Cell
	if f.Transparent {
		cell = Cell{Transparent: true}
	} else {
		cell = Cell{Char: ' ', Attr: f.fillAttr}
	}
	for r := 0; r < f.H; r++ {
		for c := 0; c < f.W; c++ {
			f.cur[r][c] = cell
		}
	}
	f.cursorX, f.cursorY = 0, 0
}

// GotoXY sets the internal cursor (0-based, frame-relative).
func (f *Frame) GotoXY(x, y int) {
	f.cursorX, f.cursorY = x, y
}

// SetCell writes one cell at (x,y) with attribute attr.
func (f *Frame) SetCell(x, y int, ch byte, attr Attr) {
	if x < 0 || x >= f.W || y < 0 || y >= f.H {
		return
	}
	f.cur[y][x] = Cell{Char: ch, Attr: attr}
}

// Print writes b at the current cursor with the fill attribute, advancing
// the cursor and wrapping on the right edge. Bytes 0x00-0x1F are treated as
// CP437 graphic chars (no special handling), so newlines etc. are rendered
// literally — use PrintLn or explicit GotoXY for newlines.
func (f *Frame) Print(b []byte) {
	f.PrintAttr(b, f.fillAttr)
}

// PrintAttr is like Print but with an explicit attribute.
func (f *Frame) PrintAttr(b []byte, attr Attr) {
	for _, ch := range b {
		if f.cursorY >= f.H {
			return
		}
		if f.cursorX >= f.W {
			f.cursorX = 0
			f.cursorY++
			if f.cursorY >= f.H {
				return
			}
		}
		f.cur[f.cursorY][f.cursorX] = Cell{Char: ch, Attr: attr}
		f.cursorX++
	}
}

// PrintAt is shorthand for GotoXY+Print.
func (f *Frame) PrintAt(x, y int, b []byte, attr Attr) {
	f.GotoXY(x, y)
	f.PrintAttr(b, attr)
}

// ForceRedraw causes the next Render to repaint every cell.
func (f *Frame) ForceRedraw() { f.first = true }

// SetFill changes the attribute used by Clear() and by Print() when no
// explicit attr is given. Call ForceRedraw afterward if you want the new
// fill to land on screen without waiting for the next Clear.
func (f *Frame) SetFill(attr Attr) { f.fillAttr = attr }

// ClearCell resets the cell at (x, y) to a transparent cell (composited
// frames see through it) on a transparent frame, or to a fill-attribute
// space on an opaque frame. Useful when an overlay (ticker, notice) needs
// to vacate a region without disturbing other cells.
func (f *Frame) ClearCell(x, y int) {
	if x < 0 || y < 0 || x >= f.W || y >= f.H {
		return
	}
	if f.Transparent {
		f.cur[y][x] = Cell{Transparent: true}
		return
	}
	f.cur[y][x] = Cell{Char: ' ', Attr: f.fillAttr}
}

// CellAt returns the cell at (x, y) in the frame's local coordinate space,
// or a zero Cell when out of bounds. Useful for cross-package animations
// (e.g. the idle ANSI gallery) that need to read the contents of a Frame
// loaded from disk.
func (f *Frame) CellAt(x, y int) Cell {
	if x < 0 || y < 0 || x >= f.W || y >= f.H {
		return Cell{}
	}
	return f.cur[y][x]
}

// Snapshot returns a flat row-major copy of every cell in the frame.
// Useful as a baseline for animation effects that mutate f.cur each tick
// (splash strobe, palette cycling, etc.) without losing the original.
func (f *Frame) Snapshot() []Cell {
	out := make([]Cell, f.W*f.H)
	for y := 0; y < f.H; y++ {
		for x := 0; x < f.W; x++ {
			out[y*f.W+x] = f.cur[y][x]
		}
	}
	return out
}

// CycleColors rewrites every cell's foreground (and background, when
// non-zero) to a single CGA index drawn from a R-G-B-cycling palette
// indexed by `tick`. The intensity (bright bit) and blink bit on each
// cell are preserved from `orig`. Black cells stay black so the
// artwork's silhouette is preserved as colors strobe through it.
//
// Call this on each animation tick of a splash or other effect; pair
// with Render() to see the new attrs land. `orig` should be the result
// of Snapshot() taken once before the cycle started.
func (f *Frame) CycleColors(orig []Cell, tick int) {
	target := rgbCyclePalette[((tick%len(rgbCyclePalette))+len(rgbCyclePalette))%len(rgbCyclePalette)]
	for y := 0; y < f.H; y++ {
		for x := 0; x < f.W; x++ {
			o := orig[y*f.W+x]
			if o.Transparent {
				f.cur[y][x] = o
				continue
			}
			attr := o.Attr
			fgBase := byte(attr & 0x07)
			bgBase := byte((attr >> 4) & 0x07)
			if fgBase != 0 {
				attr = (attr &^ 0x07) | Attr(target)
			}
			if bgBase != 0 {
				attr = (attr &^ 0x70) | (Attr(target) << 4)
			}
			f.cur[y][x] = Cell{Char: o.Char, Attr: attr}
		}
	}
}

// rgbCyclePalette walks red, green, blue (CGA indices) in order. Each
// tick of CycleColors maps every colored cell to one of these.
var rgbCyclePalette = [3]byte{4, 2, 1}

// Render writes the diff (or full repaint, on first call) to w. After Render
// returns, last == cur and subsequent renders only emit changed regions.
func (f *Frame) Render(w io.Writer) error {
	var buf bytes.Buffer
	scratch := make([]byte, 0, 4)
	for r := 0; r < f.H; r++ {
		firstC, lastC := -1, -1
		if f.first {
			firstC, lastC = 0, f.W-1
		} else {
			for c := 0; c < f.W; c++ {
				if f.cur[r][c] != f.last[r][c] {
					if firstC < 0 {
						firstC = c
					}
					lastC = c
				}
			}
		}
		if firstC < 0 {
			continue
		}
		fmt.Fprintf(&buf, "\x1b[%d;%dH", f.Y+r+1, f.X+firstC+1)
		var prevAttr Attr
		var primed bool
		for c := firstC; c <= lastC; c++ {
			cell := f.cur[r][c]
			if !primed || cell.Attr != prevAttr {
				buf.WriteString(cell.Attr.SGR())
				prevAttr = cell.Attr
				primed = true
			}
			buf.Write(encodeCellBytes(cell.Char, f.Charset, scratch))
		}
	}
	if buf.Len() == 0 {
		return nil
	}
	_, err := w.Write(buf.Bytes())
	if err != nil {
		return err
	}
	for r := 0; r < f.H; r++ {
		copy(f.last[r], f.cur[r])
	}
	f.first = false
	return nil
}
