// Package canvas is the door's ANSI art editor: a grid of CP437 cells in
// the 16-colour CGA palette (16 foregrounds, 8 backgrounds, no iCE), the
// edits a drawing program needs, and conversion to the chat's [BITMAP|...]
// message so the art travels to the web, the shell, Moebius and the bridges.
package canvas

import (
	"github.com/hmderdoc/avatar_chat_universal/internal/bitmap"
)

// Glyphs a half-block pixel edit produces.
const (
	Space     = 0x20
	FullBlock = 0xDB
	HalfUpper = 0xDF // foreground paints the top half
	HalfLower = 0xDC // foreground paints the bottom half
)

// Limits. Width is the terminal's; height keeps the message well under the
// web chat's 32000-character BITMAP cap (CHAT_MAX_BITMAP_LEN) even for art
// that compresses badly.
const (
	MaxWidth  = 80
	MaxHeight = 40
	// MaxMessage is the web chat's cap on a BITMAP message.
	MaxMessage = 32000
)

// Cell is one character cell: a CP437 glyph and a CGA attribute
// (fg | bg<<4, fg 0-15, bg 0-7).
type Cell struct {
	Ch   byte
	Attr byte
}

// Fg and Bg split the attribute.
func (c Cell) Fg() byte { return c.Attr & 0x0F }
func (c Cell) Bg() byte { return (c.Attr >> 4) & 0x07 }

// Attr builds an attribute, clamping the background to the 8 colours a
// terminal without iCE can show.
func Attr(fg, bg byte) byte { return fg&0x0F | (bg&0x07)<<4 }

// Canvas is a W x H grid, row-major.
type Canvas struct {
	W, H  int
	Cells []Cell
}

// New returns a blank canvas (spaces, light grey on black), clamped to the
// limits.
func New(w, h int) *Canvas {
	if w < 1 {
		w = 1
	}
	if w > MaxWidth {
		w = MaxWidth
	}
	if h < 1 {
		h = 1
	}
	if h > MaxHeight {
		h = MaxHeight
	}
	c := &Canvas{W: w, H: h, Cells: make([]Cell, w*h)}
	c.Clear()
	return c
}

// Clone is a deep copy.
func (c *Canvas) Clone() *Canvas {
	out := &Canvas{W: c.W, H: c.H, Cells: make([]Cell, len(c.Cells))}
	copy(out.Cells, c.Cells)
	return out
}

// In reports whether (x, y) is on the canvas.
func (c *Canvas) In(x, y int) bool { return x >= 0 && y >= 0 && x < c.W && y < c.H }

// At returns the cell at (x, y); off-canvas reads as a blank cell.
func (c *Canvas) At(x, y int) Cell {
	if !c.In(x, y) {
		return Cell{Ch: Space, Attr: Attr(7, 0)}
	}
	return c.Cells[y*c.W+x]
}

// Set writes a cell; off-canvas writes are ignored.
func (c *Canvas) Set(x, y int, cell Cell) {
	if c.In(x, y) {
		cell.Attr &= 0x7F // no blink / iCE
		c.Cells[y*c.W+x] = cell
	}
}

// Clear blanks every cell.
func (c *Canvas) Clear() {
	for i := range c.Cells {
		c.Cells[i] = Cell{Ch: Space, Attr: Attr(7, 0)}
	}
}

// Blank reports whether nothing has been drawn: every cell is a space on
// black (the foreground of a space does not show).
func (c *Canvas) Blank() bool {
	for _, cell := range c.Cells {
		if (cell.Ch != Space && cell.Ch != 0) || cell.Bg() != 0 {
			return false
		}
	}
	return true
}

// Fill flood-fills (4-way) the region of cells identical to the one at
// (x, y) with cell. It returns how many cells changed.
func (c *Canvas) Fill(x, y int, cell Cell) int {
	if !c.In(x, y) {
		return 0
	}
	cell.Attr &= 0x7F
	target := c.At(x, y)
	if target == cell {
		return 0
	}
	n := 0
	stack := [][2]int{{x, y}}
	for len(stack) > 0 {
		p := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		px, py := p[0], p[1]
		if !c.In(px, py) || c.At(px, py) != target {
			continue
		}
		c.Cells[py*c.W+px] = cell
		n++
		stack = append(stack, [2]int{px + 1, py}, [2]int{px - 1, py}, [2]int{px, py + 1}, [2]int{px, py - 1})
	}
	return n
}

// halves reads a cell as two stacked pixels (top, bottom colour), the way a
// half-block editor sees it. Glyphs other than blocks read as their
// background, so painting over text replaces it.
func halves(cell Cell) (top, bottom byte) {
	switch cell.Ch {
	case HalfUpper:
		return cell.Fg(), cell.Bg()
	case HalfLower:
		return cell.Bg(), cell.Fg()
	case FullBlock:
		return cell.Fg(), cell.Fg()
	default:
		return cell.Bg(), cell.Bg()
	}
}

// fromHalves builds the cell that shows top over bottom. A background can
// only be colour 0-7, so the half that is dark goes in the background; when
// both halves are bright the bottom one is shown at its dim shade.
func fromHalves(top, bottom byte) Cell {
	switch {
	case top == bottom:
		if top == 0 {
			return Cell{Ch: Space, Attr: Attr(7, 0)}
		}
		return Cell{Ch: FullBlock, Attr: Attr(top, 0)}
	case bottom < 8:
		return Cell{Ch: HalfUpper, Attr: Attr(top, bottom)}
	case top < 8:
		return Cell{Ch: HalfLower, Attr: Attr(bottom, top)}
	default:
		return Cell{Ch: HalfUpper, Attr: Attr(top, bottom-8)}
	}
}

// Pixel returns the colour of half-pixel (x, py), py counting half rows
// (0..2H-1).
func (c *Canvas) Pixel(x, py int) byte {
	top, bottom := halves(c.At(x, py/2))
	if py%2 == 0 {
		return top
	}
	return bottom
}

// SetPixel paints half-pixel (x, py) with colour.
func (c *Canvas) SetPixel(x, py int, colour byte) {
	y := py / 2
	if !c.In(x, y) || py < 0 {
		return
	}
	top, bottom := halves(c.At(x, y))
	if py%2 == 0 {
		top = colour & 0x0F
	} else {
		bottom = colour & 0x0F
	}
	c.Set(x, y, fromHalves(top, bottom))
}

// Bounds returns the smallest rectangle holding everything drawn, so a
// mostly empty canvas sends a small message. ok is false for a blank canvas.
func (c *Canvas) Bounds() (x0, y0, x1, y1 int, ok bool) {
	x0, y0, x1, y1 = c.W, c.H, -1, -1
	for y := 0; y < c.H; y++ {
		for x := 0; x < c.W; x++ {
			cell := c.At(x, y)
			if (cell.Ch == Space || cell.Ch == 0) && cell.Bg() == 0 {
				continue
			}
			if x < x0 {
				x0 = x
			}
			if x > x1 {
				x1 = x
			}
			if y < y0 {
				y0 = y
			}
			if y > y1 {
				y1 = y
			}
		}
	}
	return x0, y0, x1, y1, x1 >= 0
}

// Bitmap encodes the drawn part of the canvas as a [BITMAP|...] message
// from sender. Rows below the art and columns right of it are trimmed;
// leading blank space to the left and above is kept so placement on the
// page survives. ok is false for a blank canvas.
func (c *Canvas) Bitmap(from string) (msg string, ok bool) {
	_, _, x1, y1, ok := c.Bounds()
	if !ok {
		return "", false
	}
	w, h := x1+1, y1+1
	cells := make([]bitmap.Cell, 0, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			cell := c.At(x, y)
			cells = append(cells, bitmap.Cell{
				Char: cell.Ch,
				FG:   bitmap.WireColor(cell.Fg()),
				BG:   bitmap.WireColor(cell.Bg()),
			})
		}
	}
	return bitmap.Encode(w, h, from, cells), true
}

// History is an undo stack of whole-canvas snapshots. A canvas is at most
// 3200 cells, so a snapshot is small and undo is exact for every edit.
type History struct {
	max   int
	stack []*Canvas
}

// NewHistory keeps up to max snapshots.
func NewHistory(max int) *History { return &History{max: max} }

// Push records c's state before an edit.
func (h *History) Push(c *Canvas) {
	h.stack = append(h.stack, c.Clone())
	if len(h.stack) > h.max {
		h.stack = h.stack[len(h.stack)-h.max:]
	}
}

// Pop returns the last snapshot, or nil when there is nothing to undo.
func (h *History) Pop() *Canvas {
	if len(h.stack) == 0 {
		return nil
	}
	c := h.stack[len(h.stack)-1]
	h.stack = h.stack[:len(h.stack)-1]
	return c
}

// Len is how many undo steps are available.
func (h *History) Len() int { return len(h.stack) }
