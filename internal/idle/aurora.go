package idle

import (
	"math"
	"math/rand"

	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
)

// Aurora renders flowing vertical light bands with shifting thickness.
// Port of canvas-animations.js:455-498.
type Aurora struct {
	time    float64
	speed   float64
	wave    float64
	columns []float64
	palette []ansi.Attr
	rng     *rand.Rand
}

func NewAurora(w, h int) *Aurora {
	_ = h
	a := &Aurora{
		speed: 0.12,
		wave:  0.35,
		palette: []ansi.Attr{
			ansi.LightCyan, ansi.Cyan, ansi.LightGreen,
			ansi.LightMagenta, ansi.Yellow, ansi.White,
		},
		rng: rngFor(),
	}
	a.columns = make([]float64, w)
	for i := range a.columns {
		a.columns[i] = a.rng.Float64() * math.Pi * 2
	}
	return a
}

func (a *Aurora) Name() string       { return "aurora" }
func (a *Aurora) Category() Category { return Background }

func (a *Aurora) Tick(f *ansi.Frame) {
	a.time += a.speed
	if len(a.columns) != f.W {
		// frame got resized — re-initialize column phases
		a.columns = make([]float64, f.W)
		for i := range a.columns {
			a.columns[i] = a.rng.Float64() * math.Pi * 2
		}
	}
	f.Clear()
	for x := 0; x < f.W; x++ {
		phase := a.columns[x] + a.time
		center := math.Sin(phase)*float64(f.H)/4 + float64(f.H)/2
		thickness := (math.Cos(phase*0.7)+1)*float64(f.H)/6 + 2
		for y := 0; y < f.H; y++ {
			dist := math.Abs(float64(y) - center)
			if dist > thickness {
				continue
			}
			norm := 1 - dist/(thickness+0.01)
			idx := int(math.Floor(norm * float64(len(a.palette))))
			if idx >= len(a.palette) {
				idx = len(a.palette) - 1
			}
			if idx < 0 {
				idx = 0
			}
			f.SetCell(x, y, '|', a.palette[idx])
		}
	}
}
