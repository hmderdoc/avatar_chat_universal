package idle

import (
	"math/rand"

	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
)

// TvStatic paints a sparse field of random CP437 chars in random colors
// each tick, simulating analog TV snow. Port of canvas-animations.js:29-47.
type TvStatic struct {
	chars []byte
	attrs []ansi.Attr
	rng   *rand.Rand
}

func NewTvStatic(w, h int) *TvStatic {
	_ = w
	_ = h
	return &TvStatic{
		chars: []byte{0xB0, 0xB1, 0xB2, 0xDB, ' ', '.', ':', ';', '!', '+', '*', '=', '?', '%', '#', '@'},
		attrs: []ansi.Attr{ansi.White, ansi.LightGray, ansi.LightCyan, ansi.Cyan, ansi.LightGreen, ansi.LightMagenta, ansi.LightBlue, ansi.LightRed, ansi.Yellow},
		rng:   rngFor(),
	}
}

func (t *TvStatic) Name() string       { return "tv_static" }
func (t *TvStatic) Category() Category { return Background }

func (t *TvStatic) Tick(f *ansi.Frame) {
	w := f.W
	h := f.H
	count := w * h / 4
	for i := 0; i < count; i++ {
		x := t.rng.Intn(w)
		y := t.rng.Intn(h)
		ch := t.chars[t.rng.Intn(len(t.chars))]
		attr := t.attrs[t.rng.Intn(len(t.attrs))]
		f.SetCell(x, y, ch, attr)
	}
}
