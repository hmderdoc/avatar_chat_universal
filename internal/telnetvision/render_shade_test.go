package telnetvision

import (
	"testing"

	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
)

// A grey gradient rendered in 16-color shade mode must use the shade glyphs,
// not only half blocks, and must stay on grey fg/bg pairs.
func TestRenderToShade(t *testing.T) {
	const cols, rows = 32, 4
	fr := &Frame{Cols: cols, Rows: rows, Pixels: make([]byte, 2*rows*cols*3)}
	for y := 0; y < 2*rows; y++ {
		for x := 0; x < cols; x++ {
			v := byte(x * 255 / (cols - 1))
			i := (y*cols + x) * 3
			fr.Pixels[i], fr.Pixels[i+1], fr.Pixels[i+2] = v, v, v
		}
	}
	dst := ansi.NewFrame(0, 0, cols, rows, 0)
	fr.RenderTo(dst, 0, 0, cols, rows, RenderOpts{Truecolor: false, Shade: true, Saturation: 1.0})
	shades, halves := 0, 0
	for x := 0; x < cols; x++ {
		c := dst.CellAt(x, 0)
		switch c.Char {
		case 0xB0, 0xB1, 0xB2:
			shades++
		case 0xDF, 0xDC:
			halves++
		}
		fg, bg := byte(c.Attr)&15, byte(c.Attr)>>4
		isGrey := func(i byte) bool { return i == 0 || i == 7 || i == 8 || i == 15 }
		if !isGrey(fg) || !isGrey(bg) {
			t.Fatalf("x=%d: fg %d bg %d not grey", x, fg, bg)
		}
	}
	if shades == 0 {
		t.Fatal("no shade glyphs on a grey ramp")
	}
	if halves != 0 {
		t.Fatalf("%d half blocks on uniform cells", halves)
	}
}
