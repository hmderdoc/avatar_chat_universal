package ansi

import (
	"bytes"
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"testing"
)

// lookVT replays CUP / CUF / SGR / glyphs and records what each cell LOOKS
// like, so two byte streams that draw the same picture compare equal.
type lookVT struct {
	w, h        int
	x, y        int
	bold, blink bool
	fg, bg      string
	cells       [][]string
}

func newLookVT(w, h int) *lookVT {
	v := &lookVT{w: w, h: h, fg: "d", bg: "d"}
	v.cells = make([][]string, h)
	for y := range v.cells {
		v.cells[y] = make([]string, w)
		for x := range v.cells[y] {
			v.cells[y][x] = "?"
		}
	}
	return v
}

func (v *lookVT) sgr(params string) {
	ps := []int{0}
	if params != "" {
		ps = ps[:0]
		for _, p := range strings.Split(params, ";") {
			n, _ := strconv.Atoi(p)
			ps = append(ps, n)
		}
	}
	for k := 0; k < len(ps); k++ {
		switch n := ps[k]; {
		case n == 0:
			v.bold, v.blink, v.fg, v.bg = false, false, "d", "d"
		case n == 1:
			v.bold = true
		case n == 5:
			v.blink = true
		case n >= 30 && n <= 37:
			v.fg = "c" + strconv.Itoa(n-30)
		case n >= 40 && n <= 47:
			v.bg = "c" + strconv.Itoa(n-40)
		case (n == 38 || n == 48) && k+4 < len(ps) && ps[k+1] == 2:
			c := fmt.Sprintf("r%d,%d,%d", ps[k+2], ps[k+3], ps[k+4])
			if n == 38 {
				v.fg = c
			} else {
				v.bg = c
			}
			k += 4
		}
	}
}

func (v *lookVT) look(g byte) string {
	fg := v.fg
	if v.bold {
		fg += "+"
	}
	bl := ""
	if v.blink {
		bl = "*"
	}
	solid := func(c string) string { return "S" + c + bl }
	switch g {
	case ' ':
		return solid(v.bg)
	case 0xDB:
		return solid(fg)
	case 0xDF:
		if fg == v.bg {
			return solid(fg)
		}
		return "H" + fg + "/" + v.bg + bl
	case 0xDC:
		if fg == v.bg {
			return solid(fg)
		}
		return "H" + v.bg + "/" + fg + bl
	}
	return "G" + string(g) + fg + "/" + v.bg + bl
}

func (v *lookVT) write(b []byte) {
	for i := 0; i < len(b); i++ {
		if b[i] == 0x1b && i+1 < len(b) && b[i+1] == '[' {
			j := i + 2
			for j < len(b) && (b[j] < 0x40 || b[j] > 0x7e) {
				j++
			}
			params, final := string(b[i+2:j]), b[j]
			i = j
			p := strings.Split(params, ";")
			num := func(k int) int {
				if k < len(p) && p[k] != "" {
					n, _ := strconv.Atoi(p[k])
					return n
				}
				return 1
			}
			switch final {
			case 'H':
				v.y, v.x = num(0)-1, num(1)-1
			case 'C':
				v.x += num(0)
				if v.x > v.w-1 {
					v.x = v.w - 1
				}
			case 'm':
				v.sgr(params)
			default:
				panic("unhandled CSI " + params + string(final))
			}
			continue
		}
		if v.y >= 0 && v.y < v.h && v.x >= 0 && v.x < v.w {
			v.cells[v.y][v.x] = v.look(b[i])
		}
		v.x++
	}
}

func (v *lookVT) String() string {
	var s strings.Builder
	for _, r := range v.cells {
		s.WriteString(strings.Join(r, "|"))
		s.WriteByte('\n')
	}
	return s.String()
}

// plainRender is the reference: full Cell.SGR() + glyph for every changed
// cell, which is what the renderers sent before the wire encoder.
func plainRender(prev, cur [][]Cell, x0, y0 int) []byte {
	var b bytes.Buffer
	for r := range cur {
		for c := range cur[r] {
			if prev != nil && prev[r][c] == cur[r][c] {
				continue
			}
			fmt.Fprintf(&b, "\x1b[%d;%dH%s", y0+r+1, x0+c+1, cur[r][c].SGR())
			b.WriteByte(cur[r][c].Char)
		}
	}
	return b.Bytes()
}

func randomCell(r *rand.Rand) Cell {
	glyphs := []byte{' ', 'A', 'z', 0xDF, 0xDC, 0xDB, 0xB0, '#'}
	g := glyphs[r.Intn(len(glyphs))]
	pal := []RGB{{0, 0, 0}, {255, 0, 0}, {10, 200, 30}, {255, 255, 255}, {7, 7, 200}}
	switch r.Intn(4) {
	case 0:
		return Cell{Char: g, True: true, Fg: pal[r.Intn(len(pal))], Bg: pal[r.Intn(len(pal))]}
	case 1:
		return Cell{Char: g, True: true, FgOnly: true, Fg: pal[r.Intn(len(pal))]}
	default:
		return Cell{Char: g, Attr: Attr(r.Intn(256))}
	}
}

func TestWireCompositorPixelIdentical(t *testing.T) {
	for seed := int64(1); seed <= 60; seed++ {
		r := rand.New(rand.NewSource(seed))
		W, H := 13+r.Intn(60), 3+r.Intn(10)
		back := NewFrame(0, 0, W, H, LightGray)
		mid := NewTransparentFrame(0, 0, W, H)
		front := NewTransparentFrame(0, 0, W, H)
		comp := NewCompositor(back, mid, front)
		vNew, vRef := newLookVT(W, H), newLookVT(W, H)
		var prev [][]Cell
		var nNew, nRef int
		for step := 0; step < 40; step++ {
			n := 1 + r.Intn(W*H/3+1)
			for k := 0; k < n; k++ {
				x, y := r.Intn(W), r.Intn(H)
				c := randomCell(r)
				switch r.Intn(4) {
				case 0:
					mid.cur[y][x] = c
				case 1:
					mid.ClearCell(x, y)
				case 2:
					front.cur[y][x] = c
				default:
					back.cur[y][x] = c
				}
			}
			if r.Intn(8) == 0 {
				front.Clear()
			}
			var out bytes.Buffer
			if err := comp.Render(&out); err != nil {
				t.Fatal(err)
			}
			cur := make([][]Cell, H)
			for y := range cur {
				cur[y] = make([]Cell, W)
				for x := range cur[y] {
					cur[y][x] = comp.composeAt(x, y)
				}
			}
			ref := plainRender(prev, cur, 0, 0)
			prev = cur
			nNew += out.Len()
			nRef += len(ref)
			vNew.write(out.Bytes())
			vRef.write(ref)
			if a, b := vNew.String(), vRef.String(); a != b {
				t.Fatalf("seed %d step %d: screens differ\nnew:\n%s\nref:\n%s\nbytes %q", seed, step, a, b, out.String())
			}
		}
	}
}

func TestWireFramePixelIdentical(t *testing.T) {
	for seed := int64(100); seed <= 160; seed++ {
		r := rand.New(rand.NewSource(seed))
		W, H := 5+r.Intn(70), 2+r.Intn(8)
		f := NewFrame(2, 1, W, H, LightGray)
		vNew, vRef := newLookVT(W+2, H+1), newLookVT(W+2, H+1)
		var prev [][]Cell
		for step := 0; step < 30; step++ {
			n := 1 + r.Intn(W*H/4+1)
			for k := 0; k < n; k++ {
				f.cur[r.Intn(H)][r.Intn(W)] = randomCell(r)
			}
			var out bytes.Buffer
			if err := f.Render(&out); err != nil {
				t.Fatal(err)
			}
			cur := make([][]Cell, H)
			for y := range cur {
				cur[y] = append([]Cell(nil), f.cur[y]...)
			}
			ref := plainRender(prev, cur, 2, 1)
			prev = cur
			vNew.write(out.Bytes())
			vRef.write(ref)
			if a, b := vNew.String(), vRef.String(); a != b {
				t.Fatalf("seed %d step %d: screens differ\nnew:\n%s\nref:\n%s\nbytes %q", seed, step, a, b, out.String())
			}
		}
	}
}

func renderRow(t *testing.T, cells ...Cell) string {
	t.Helper()
	f := NewFrame(0, 0, len(cells), 1, LightGray)
	copy(f.cur[0], cells)
	var out bytes.Buffer
	if err := f.Render(&out); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func TestWireShortcuts(t *testing.T) {
	a, b := RGB{200, 0, 0}, RGB{0, 0, 200}
	// Mirror: second ▀ (fg b / bg a) reuses the current colours as ▄.
	if got := renderRow(t, Cell{Char: 0xDF, True: true, Fg: a, Bg: b}, Cell{Char: 0xDF, True: true, Fg: b, Bg: a}); !strings.HasSuffix(got, "\xdf\xdc") {
		t.Errorf("mirror: %q", got)
	}
	// Uniform half block and full block go out as a space, bg only.
	if got := renderRow(t, Cell{Char: 'x', True: true, Fg: a, Bg: b}, Cell{Char: 0xDB, True: true, Fg: a, Bg: b}); !strings.HasSuffix(got, "x\x1b[48;2;200;0;0m ") {
		t.Errorf("full block: %q", got)
	}
	if got := renderRow(t, Cell{Char: 'x', True: true, Fg: a, Bg: b}, Cell{Char: 0xDC, True: true, Fg: b, Bg: b}); !strings.HasSuffix(got, "x ") {
		t.Errorf("uniform half: %q", got)
	}
	// CGA: fg-only change is partial; bold off resets.
	if got := renderRow(t, Cell{Char: 'a', Attr: LightGray}, Cell{Char: 'b', Attr: Red}); !strings.HasSuffix(got, "a\x1b[31mb") {
		t.Errorf("partial cga: %q", got)
	}
	if got := renderRow(t, Cell{Char: 'a', Attr: White}, Cell{Char: 'b', Attr: LightGray}); !strings.HasSuffix(got, "a\x1b[0;37;40mb") {
		t.Errorf("bold off: %q", got)
	}
}

func TestWireCUFHop(t *testing.T) {
	f := NewFrame(0, 0, 20, 1, LightGray)
	var out bytes.Buffer
	_ = f.Render(&out)
	f.cur[0][1] = Cell{Char: 'a', Attr: LightGray}
	f.cur[0][15] = Cell{Char: 'b', Attr: LightGray}
	out.Reset()
	_ = f.Render(&out)
	if got := out.String(); !strings.Contains(got, "a\x1b[13Cb") {
		t.Errorf("expected a CUF hop over the unchanged run: %q", got)
	}
	// A short gap is resent instead (cheaper than the hop).
	f.cur[0][1] = Cell{Char: 'c', Attr: LightGray}
	f.cur[0][3] = Cell{Char: 'd', Attr: LightGray}
	out.Reset()
	_ = f.Render(&out)
	if got := out.String(); !strings.HasSuffix(got, "c d") {
		t.Errorf("expected the 1-cell gap resent: %q", got)
	}
}

// Dense truecolor half blocks with a tiny palette: exercises mirrors and
// solid-cell spaces constantly.
func TestWireDenseHalfBlocksPixelIdentical(t *testing.T) {
	for seed := int64(300); seed <= 340; seed++ {
		r := rand.New(rand.NewSource(seed))
		W, H := 20+r.Intn(40), 4+r.Intn(6)
		f := NewFrame(0, 0, W, H, LightGray)
		vNew, vRef := newLookVT(W, H), newLookVT(W, H)
		pal := []RGB{{0, 0, 0}, {255, 0, 0}, {0, 0, 255}}
		glyphs := []byte{0xDF, 0xDC, 0xDB, ' '}
		var prev [][]Cell
		for step := 0; step < 20; step++ {
			for y := 0; y < H; y++ {
				for x := 0; x < W; x++ {
					if r.Intn(3) == 0 {
						f.cur[y][x] = Cell{Char: glyphs[r.Intn(len(glyphs))], True: true, Fg: pal[r.Intn(3)], Bg: pal[r.Intn(3)]}
					}
				}
			}
			var out bytes.Buffer
			_ = f.Render(&out)
			cur := make([][]Cell, H)
			for y := range cur {
				cur[y] = append([]Cell(nil), f.cur[y]...)
			}
			ref := plainRender(prev, cur, 0, 0)
			prev = cur
			vNew.write(out.Bytes())
			vRef.write(ref)
			if a, b := vNew.String(), vRef.String(); a != b {
				t.Fatalf("seed %d step %d: screens differ", seed, step)
			}
		}
	}
}
