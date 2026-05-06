package idle

import (
	"math"
	"math/rand"

	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
)

// OceanRipple renders expanding circular waves with interference between
// multiple sources. Port of canvas-animations.js:501-551.
type OceanRipple struct {
	ripples    []ripple
	tickCount  int
	maxRipples int
	palette    []ansi.Attr
	rng        *rand.Rand
}

type ripple struct {
	x, y   float64
	radius float64
	speed  float64
	max    float64
}

func NewOceanRipple(w, h int) *OceanRipple {
	o := &OceanRipple{
		maxRipples: 4,
		palette:    []ansi.Attr{ansi.LightBlue, ansi.Cyan, ansi.LightCyan, ansi.White},
		rng:        rngFor(),
	}
	for i := 0; i < o.maxRipples; i++ {
		o.spawn(w, h)
	}
	return o
}

func (o *OceanRipple) Name() string       { return "ocean_ripple" }
func (o *OceanRipple) Category() Category { return Background }

func (o *OceanRipple) spawn(w, h int) {
	max := float64(w)
	if h > w {
		max = float64(h)
	}
	o.ripples = append(o.ripples, ripple{
		x:     o.rng.Float64() * float64(w),
		y:     o.rng.Float64() * float64(h),
		speed: 0.6 + o.rng.Float64()*0.5,
		max:   max + 10,
	})
}

func (o *OceanRipple) Tick(f *ansi.Frame) {
	o.tickCount++
	if len(o.ripples) < o.maxRipples && o.tickCount%45 == 0 {
		o.spawn(f.W, f.H)
	}
	for r := len(o.ripples) - 1; r >= 0; r-- {
		o.ripples[r].radius += o.ripples[r].speed
		if o.ripples[r].radius > o.ripples[r].max {
			o.ripples = append(o.ripples[:r], o.ripples[r+1:]...)
		}
	}
	for y := 0; y < f.H; y++ {
		for x := 0; x < f.W; x++ {
			value := 0.0
			for _, rp := range o.ripples {
				dx := float64(x) - rp.x
				dy := float64(y) - rp.y
				dist := math.Sqrt(dx*dx+dy*dy) + 0.01
				wave := math.Sin(dist*0.35 - rp.radius*0.5)
				value += wave / (1 + dist*0.15)
			}
			norm := (value + 1.2) / 2.4
			if norm < 0 {
				norm = 0
			}
			if norm > 1 {
				norm = 1
			}
			idx := int(math.Floor(norm * float64(len(o.palette))))
			if idx >= len(o.palette) {
				idx = len(o.palette) - 1
			}
			var ch byte
			switch {
			case norm < 0.2:
				ch = ' '
			case norm < 0.4:
				ch = '.'
			case norm < 0.6:
				ch = '~'
			case norm < 0.8:
				ch = '-'
			default:
				ch = '='
			}
			f.SetCell(x, y, ch, o.palette[idx])
		}
	}
	_ = rand.Intn
}
