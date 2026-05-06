package idle

import (
	"math/rand"

	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
)

// MatrixRain is the falling-green-glyphs animation, ported from the JS
// chat door's canvas-animations.js:50-85. About 60% of the columns are
// active; each has a falling head with a 5-cell trail (white at the head,
// fading to dark green at the tail). The `sparse` knob throttles updates
// to stagger column motion across ticks for a smoother cascade.
type MatrixRain struct {
	columns []rainCol
	glyphs  []byte
	sparse  int
	phase   int
	rng     *rand.Rand
}

type rainCol struct {
	x     int
	y     int
	speed int
}

// NewMatrixRain builds a matrix-rain animation sized for (w, h). sparse=1
// means every column updates every tick; sparse=2 staggers across two
// ticks; etc.
func NewMatrixRain(w, h int) *MatrixRain {
	m := &MatrixRain{
		glyphs: []byte("0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ#$%&*@"),
		sparse: 1,
		rng:    rngFor(),
	}
	for x := 0; x < w; x++ {
		if m.rng.Float64() < 0.6 {
			m.columns = append(m.columns, rainCol{
				x:     x,
				y:     m.rng.Intn(h),
				speed: 1 + m.rng.Intn(2),
			})
		}
	}
	return m
}

func (m *MatrixRain) Name() string       { return "matrix_rain" }
func (m *MatrixRain) Category() Category { return Background }

func (m *MatrixRain) Tick(f *ansi.Frame) {
	f.Clear()
	phaseMod := m.phase % m.sparse
	for i := range m.columns {
		if i%m.sparse != phaseMod {
			continue
		}
		c := &m.columns[i]
		c.y += c.speed
		if c.y > f.H+5 {
			c.y = 0
		}
		for t := 0; t < 5; t++ {
			yy := c.y - t
			if yy < 0 || yy >= f.H {
				continue
			}
			ch := m.glyphs[m.rng.Intn(len(m.glyphs))]
			var attr ansi.Attr
			switch {
			case t == 0:
				attr = ansi.White
			case t < 3:
				attr = ansi.LightGreen
			default:
				attr = ansi.Green
			}
			f.SetCell(c.x, yy, ch, attr)
		}
	}
	m.phase++
}
