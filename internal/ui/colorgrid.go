package ui

import (
	"math"
	"time"

	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
	"github.com/hmderdoc/avatar_chat_universal/internal/nickstyle"
	"github.com/hmderdoc/avatar_chat_universal/internal/theme"
)

// The "Pick a colour" grid the /settings handle editor opens on Enter or a
// click on a letter: the sixteen CGA colours in two rows of eight, and on
// a truecolor terminal a hue x lightness block and a grey ramp below them.
// Arrows move the [ ] cursor, Enter or a click uses the colour for the
// letter, Esc or the [X] cancels. Like the panel it is synchronous.

const (
	gridHues  = 12 // columns of the truecolor block
	gridCellW = 4  // "[██]" per swatch
	gridX0    = 2  // column of the first swatch
	gridHint  = "Arrows move  Enter use  Esc cancel"
)

// gridShades are the truecolor block's rows, dark to light, as HSV
// saturation and value.
var gridShades = [][2]float64{{1, 0.5}, {1, 0.75}, {0.85, 1}, {0.55, 1}, {0.3, 1}}

type colourGrid struct {
	rows     [][]string // a hex per swatch
	row, col int
	frame    *ansi.Frame
	hints    hintLine // the key line's actions, for clicks
}

// newColourGrid lays out the swatches and starts the cursor on current,
// or on its nearest CGA colour when it isn't one of them.
func newColourGrid(truecolor bool, current string) *colourGrid {
	g := &colourGrid{rows: [][]string{nickstyle.CGA[:8], nickstyle.CGA[8:]}}
	if truecolor {
		for _, sv := range gridShades {
			row := make([]string, gridHues)
			for i := range row {
				row[i] = hsvToHex(float64(i)*360/gridHues, sv[0], sv[1])
			}
			g.rows = append(g.rows, row)
		}
		grey := make([]string, gridHues)
		for i := range grey {
			v := 255 * float64(i+1) / gridHues
			grey[i] = nickstyle.ToHex(v, v, v)
		}
		g.rows = append(g.rows, grey)
	}
	h := nickstyle.Hex(current)
	if h == "" {
		return g
	}
	for r, row := range g.rows {
		for c, hex := range row {
			if hex == h {
				g.row, g.col = r, c
				return g
			}
		}
	}
	idx := cgaIndex(cycleColor(h, 0))
	g.row, g.col = idx/8, idx%8
	return g
}

// cgaIndex is hex's place in the CGA palette, or -1.
func cgaIndex(hex string) int {
	for i, c := range nickstyle.CGA {
		if c == hex {
			return i
		}
	}
	return -1
}

func (g *colourGrid) current() string { return g.rows[g.row][g.col] }

// name is what the line under the grid says: the CGA name, else "custom".
func (g *colourGrid) name() string {
	if i := cgaIndex(g.current()); i >= 0 {
		return cgaNames[i]
	}
	return "custom"
}

// move steps the cursor, keeping it on a swatch.
func (g *colourGrid) move(dr, dc int) {
	g.row += dr
	if g.row < 0 {
		g.row = 0
	}
	if g.row >= len(g.rows) {
		g.row = len(g.rows) - 1
	}
	g.col += dc
	if n := len(g.rows[g.row]); g.col >= n {
		g.col = n - 1
	}
	if g.col < 0 {
		g.col = 0
	}
}

// rowY is the frame row of swatch row r: the two CGA rows, a gap, then
// the truecolor rows.
func rowY(r int) int {
	if r < 2 {
		return 2 + r
	}
	return 3 + r
}

func (g *colourGrid) size() (w, h int) {
	cols := 0
	for _, row := range g.rows {
		if len(row) > cols {
			cols = len(row)
		}
	}
	w = gridX0*2 + gridCellW*cols
	if hw := len(gridHint) + 2*gridX0; hw > w {
		w = hw
	}
	return w, rowY(len(g.rows)-1) + 5
}

// swatchAt maps a frame-relative cell to a swatch.
func (g *colourGrid) swatchAt(x, y int) (r, c int, ok bool) {
	for r := range g.rows {
		if y != rowY(r) || x < gridX0 {
			continue
		}
		c := (x - gridX0) / gridCellW
		if c < len(g.rows[r]) {
			return r, c, true
		}
	}
	return 0, 0, false
}

// handle acts on one key or mouse report: done with ok means use
// current(), done without it means cancel.
func (g *colourGrid) handle(k ansi.Key) (done, ok bool) {
	switch k.Type {
	case ansi.KeyEsc:
		return true, false
	case ansi.KeyEnter:
		return true, true
	case ansi.KeyUp:
		g.move(-1, 0)
	case ansi.KeyDown:
		g.move(1, 0)
	case ansi.KeyLeft:
		g.move(0, -1)
	case ansi.KeyRight:
		g.move(0, 1)
	case ansi.KeyMouse:
		f := g.frame
		if f == nil || k.Release || k.Button != ansi.MouseLeft {
			return false, false
		}
		if closeHit(k, f.X, f.Y) {
			return true, false
		}
		if hk, ok := g.hints.hit(k, f.X, f.Y); ok {
			return g.handle(hk)
		}
		if r, c, hit := g.swatchAt(k.X-f.X, k.Y-f.Y); hit {
			g.row, g.col = r, c
			return true, true
		}
	}
	return false, false
}

// draw paints the grid centred on a cols x rows screen.
func (g *colourGrid) draw(th *theme.Theme, cs ansi.Charset, cols, rows int) *ansi.Frame {
	w, h := g.size()
	x, y := (cols-w)/2, (rows-h)/2
	if y < 0 {
		y = 0
	}
	bg := th.ModalBg
	f := g.frame
	if f == nil || f.X != x || f.Y != y {
		f = ansi.NewFrame(x, y, w, h, bg)
		f.Charset = cs
		g.frame = f
	}
	f.SetFill(bg)
	f.Clear()
	drawBox(f, th, bg)
	boxTitle(f, "Pick a colour", th.ModalTitle)
	bgIdx := (bg >> 4) & 7
	for r, row := range g.rows {
		for c, hex := range row {
			sx := gridX0 + c*gridCellW
			sy := rowY(r)
			if i := cgaIndex(hex); i >= 0 {
				ch, attr := byte(0xDB), ansi.FG(bg, ansi.Attr(i))
				if ansi.Attr(i) == bgIdx {
					// Black on black: a shade so the swatch is there.
					ch, attr = 0xB0, ansi.FG(bg, ansi.DarkGray)
				}
				f.SetCell(sx+1, sy, ch, attr)
				f.SetCell(sx+2, sy, ch, attr)
			} else {
				cr, cg, cb, _ := nickstyle.RGB(hex)
				fg := ansi.RGB{R: cr, G: cg, B: cb}
				f.SetCellTrue(sx+1, sy, 0xDB, fg, cgaRGBOf(bgIdx))
				f.SetCellTrue(sx+2, sy, 0xDB, fg, cgaRGBOf(bgIdx))
			}
			if r == g.row && c == g.col {
				f.SetCell(sx, sy, '[', th.ModalText)
				f.SetCell(sx+3, sy, ']', th.ModalText)
			}
		}
	}
	f.PrintAt(gridX0, h-3, []byte(truncate(g.name(), w-2*gridX0)), th.ModalText)
	g.hints = drawHints(f, gridX0, h-2, truncate(gridHint, w-2*gridX0), th.ModalHint, hint{"Enter use", enterKey}, hint{"Esc cancel", escKey})
	return f
}

// pickColour runs the grid for the panel's current letter and stores
// the pick; the panel is repainted whole afterwards.
func (a *App) pickColour(p *settingsPanel) {
	if len(p.colors) == 0 {
		return
	}
	th := p.theme
	if th == nil {
		th = a.theme()
	}
	g := newColourGrid(a.tvColorTrue(), p.colors[p.cursor])
	defer func() {
		if p.frame != nil {
			p.frame.ForceRedraw()
		}
	}()
	for {
		_ = g.draw(th, a.Charset, a.Width, a.Height).Render(a.Conn)
		_ = a.parkCursor(a.Conn)
		var k ansi.Key
		for {
			key, ok, err := a.Input.Next(200 * time.Millisecond)
			if err != nil {
				return
			}
			if ok {
				k = key
				break
			}
		}
		if done, ok := g.handle(k); done {
			if ok {
				p.colors[p.cursor] = g.current()
				p.changed = true
			}
			return
		}
	}
}

// hsvToHex is h in degrees, s and v 0..1, as '#rrggbb'.
func hsvToHex(h, s, v float64) string {
	c := v * s
	x := c * (1 - math.Abs(math.Mod(h/60, 2)-1))
	m := v - c
	var r, g, b float64
	switch {
	case h < 60:
		r, g = c, x
	case h < 120:
		r, g = x, c
	case h < 180:
		g, b = c, x
	case h < 240:
		g, b = x, c
	case h < 300:
		r, b = x, c
	default:
		r, b = c, x
	}
	return nickstyle.ToHex((r+m)*255, (g+m)*255, (b+m)*255)
}
