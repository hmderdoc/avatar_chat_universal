package ansi

import (
	"bytes"
	"strconv"
)

// Wire encoding shared by Frame.Render and Compositor.Render.
//
// Every Cell.SGR() starts with a reset, so the terminal's attribute state
// after any cell is fully determined by that cell. The encoder tracks that
// state across one Render call and sends only what has to change. Every
// saving is pixel-identical to writing Cell.SGR() + glyph for each cell:
//
//   - Partial SGR: only the parts that differ (`ESC[37m` instead of
//     `ESC[0;37;40m`; an RGB cell that only changes bg skips the fg triple).
//     Turning bold or blink OFF, or returning bg to the default, still sends
//     the cell's full reset sequence.
//   - Glyphs that draw no foreground — a space, and for truecolor cells a
//     full block or a half block whose two colours are equal — only need the
//     background (and blink) to match, and go out as a space.
//   - A truecolor half block may go out as its mirror (▀ fg A / bg B looks
//     the same as ▄ fg B / bg A) when that reuses the current colours.
//   - Unchanged cells inside a changed span are hopped with CUF (`ESC[nC`),
//     or resent when that is shorter and needs no SGR.
//
// The state starts unknown on every Render: other writers (sixel, raw
// clears, audio APCs) touch the terminal between renders, so the first cell
// always carries its full SGR.

const (
	glyphSpace     = 0x20
	glyphFullBlock = 0xDB
	glyphUpperHalf = 0xDF
	glyphLowerHalf = 0xDC
)

// colorKind: what a colour slot holds after an SGR.
const (
	colorDefault = iota // after reset (FgOnly cells leave bg here)
	colorCGA            // value = ANSI code 30..37 / 40..47
	colorRGB            // value = 0xRRGGBB
)

type wireColor struct {
	kind uint8
	v    uint32
}

type wireState struct {
	known       bool
	bold, blink bool
	fg, bg      wireColor
}

func rgbColor(c RGB) wireColor {
	return wireColor{kind: colorRGB, v: uint32(c.R)<<16 | uint32(c.G)<<8 | uint32(c.B)}
}

// wantState is the attribute state Cell.SGR() leaves the terminal in.
func wantState(c Cell) wireState {
	if c.True {
		if c.FgOnly {
			return wireState{known: true, fg: rgbColor(c.Fg), bg: wireColor{kind: colorDefault}}
		}
		return wireState{known: true, fg: rgbColor(c.Fg), bg: rgbColor(c.Bg)}
	}
	a := c.Attr
	return wireState{
		known: true,
		bold:  a&8 != 0,
		blink: a&Blink != 0,
		fg:    wireColor{kind: colorCGA, v: uint32(cgaFgToANSI[a&7])},
		bg:    wireColor{kind: colorCGA, v: uint32(cgaBgToANSI[(a>>4)&7])},
	}
}

func appendColor(dst []byte, slot int, c wireColor) []byte {
	if c.kind == colorCGA {
		return strconv.AppendUint(dst, uint64(c.v), 10)
	}
	dst = strconv.AppendInt(dst, int64(slot), 10)
	dst = append(dst, ";2;"...)
	dst = strconv.AppendUint(dst, uint64(c.v>>16&255), 10)
	dst = append(dst, ';')
	dst = strconv.AppendUint(dst, uint64(c.v>>8&255), 10)
	dst = append(dst, ';')
	return strconv.AppendUint(dst, uint64(c.v&255), 10)
}

// candidate is one way to put a cell on screen.
type candidate struct {
	glyph   byte
	anyFg   bool // glyph draws no foreground: fg and bold do not matter
	want    wireState
	fullSGR string // the cell's own full sequence, used when a partial cannot reach `want`
}

// partialSGR appends the sequence that moves `from` to `c.want`, and returns
// the resulting state. ok=false when only a reset can get there.
func partialSGR(dst []byte, from wireState, c candidate) ([]byte, wireState, bool) {
	w := c.want
	next := from
	// Turning an attribute off, or returning bg to default, needs the reset.
	if from.blink && !w.blink {
		return dst, from, false
	}
	if !c.anyFg && from.bold && !w.bold {
		return dst, from, false
	}
	if w.bg.kind == colorDefault && from.bg.kind != colorDefault {
		return dst, from, false
	}
	if !c.anyFg && w.fg.kind == colorDefault && from.fg.kind != colorDefault {
		return dst, from, false
	}
	start := len(dst)
	sep := func() {
		if len(dst) == start {
			dst = append(dst, "\x1b["...)
		} else {
			dst = append(dst, ';')
		}
	}
	if !c.anyFg && w.bold && !from.bold {
		sep()
		dst = append(dst, '1')
		next.bold = true
	}
	if w.blink && !from.blink {
		sep()
		dst = append(dst, '5')
		next.blink = true
	}
	if !c.anyFg && w.fg != from.fg {
		sep()
		dst = appendColor(dst, 38, w.fg)
		next.fg = w.fg
	}
	if w.bg != from.bg {
		sep()
		dst = appendColor(dst, 48, w.bg)
		next.bg = w.bg
	}
	if len(dst) != start {
		dst = append(dst, 'm')
	}
	return dst, next, true
}

// encodeCell appends the SGR needed for `cell` given state `st` to dst and
// returns the glyph to send and the state after it.
func encodeCell(dst []byte, cell Cell, st wireState) ([]byte, byte, wireState) {
	want := wantState(cell)
	if !st.known {
		return append(dst, cell.SGR()...), cell.Char, want
	}
	var cands [2]candidate
	n := 0
	ch := cell.Char
	switch {
	case ch == glyphSpace:
		cands[0] = candidate{glyph: glyphSpace, anyFg: true, want: want}
		n = 1
	case cell.True && !cell.FgOnly && ch == glyphFullBlock:
		w := want
		w.bg = w.fg
		cands[0] = candidate{glyph: glyphSpace, anyFg: true, want: w}
		n = 1
	case cell.True && !cell.FgOnly && (ch == glyphUpperHalf || ch == glyphLowerHalf):
		if cell.Fg == cell.Bg {
			cands[0] = candidate{glyph: glyphSpace, anyFg: true, want: want}
			n = 1
		} else {
			mirror := byte(glyphLowerHalf)
			if ch == glyphLowerHalf {
				mirror = glyphUpperHalf
			}
			m := want
			m.fg, m.bg = want.bg, want.fg
			cands[0] = candidate{glyph: ch, want: want}
			cands[1] = candidate{glyph: mirror, want: m}
			n = 2
		}
	default:
		cands[0] = candidate{glyph: ch, want: want}
		n = 1
	}
	// Longest partial: ESC[1;5;38;2;255;255;255;48;2;255;255;255m (42 bytes).
	var bufs [2][64]byte
	best := -1
	var bestOut []byte
	var bestGlyph byte
	var bestState wireState
	for i := 0; i < n; i++ {
		out, next, ok := partialSGR(bufs[i][:0], st, cands[i])
		if !ok {
			continue
		}
		if best < 0 || len(out) < len(bestOut) {
			best, bestOut, bestGlyph, bestState = i, out, cands[i].glyph, next
		}
	}
	if best < 0 {
		return append(dst, cell.SGR()...), cell.Char, want
	}
	return append(dst, bestOut...), bestGlyph, bestState
}

// appendCUP appends an absolute cursor move; row/col are 1-based.
func appendCUP(dst []byte, row, col int) []byte {
	dst = append(dst, "\x1b["...)
	dst = strconv.AppendInt(dst, int64(row), 10)
	if col != 1 {
		dst = append(dst, ';')
		dst = strconv.AppendInt(dst, int64(col), 10)
	}
	return append(dst, 'H')
}

func appendCUF(dst []byte, n int) []byte {
	dst = append(dst, "\x1b["...)
	if n != 1 {
		dst = strconv.AppendInt(dst, int64(n), 10)
	}
	return append(dst, 'C')
}

// maxResendGap bounds how many unchanged cells are considered for resending
// instead of a CUF hop (a hop is at most 4-5 bytes).
const maxResendGap = 4

// wireEncoder writes changed cells of one Render call.
type wireEncoder struct {
	buf     *bytes.Buffer
	cs      Charset
	st      wireState
	scratch []byte
	tmp     []byte
}

func newWireEncoder(buf *bytes.Buffer, cs Charset) *wireEncoder {
	return &wireEncoder{buf: buf, cs: cs, scratch: make([]byte, 0, 64), tmp: make([]byte, 0, 32)}
}

// span writes the changed cells of row `cells` between first and last
// (inclusive). row1/col1 are the 1-based terminal coordinates of cells[0].
func (e *wireEncoder) span(row1, col1 int, cells []Cell, changed func(i int) bool, first, last int) {
	cur := -1 // index the cursor sits at, -1 = not on this row yet
	var glyphBuf [4]byte
	for i := first; i <= last; i++ {
		if !changed(i) {
			continue
		}
		if cur != i {
			moved := false
			if cur >= 0 && i > cur {
				gap := i - cur
				if gap <= maxResendGap {
					st := e.st
					tmp := e.tmp[:0]
					ok := true
					for g := cur; g < i; g++ {
						sgr, glyph, next := encodeCell(e.scratch[:0], cells[g], st)
						if len(sgr) != 0 {
							ok = false
							break
						}
						tmp = append(tmp, encodeCellBytes(glyph, e.cs, glyphBuf[:0])...)
						st = next
					}
					e.tmp = tmp
					if ok && len(tmp) < len(appendCUF(e.scratch[:0], gap)) {
						e.buf.Write(tmp)
						e.st = st
						moved = true
					}
				}
				if !moved {
					e.buf.Write(appendCUF(e.scratch[:0], gap))
					moved = true
				}
			}
			if !moved {
				e.buf.Write(appendCUP(e.scratch[:0], row1, col1+i))
			}
		}
		sgr, glyph, next := encodeCell(e.scratch[:0], cells[i], e.st)
		e.scratch = sgr[:0]
		e.buf.Write(sgr)
		e.buf.Write(encodeCellBytes(glyph, e.cs, glyphBuf[:0]))
		e.st = next
		cur = i + 1
	}
}
