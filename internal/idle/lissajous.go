package idle

import (
	"math"

	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
)

// Lissajous draws three lissajous-curve trails painted on top of each other.
// Each tick adds a new point per curve at the head; old points fade and
// expire. Port of canvas-animations.js:554-597.
type Lissajous struct {
	time   float64
	speed  float64
	curves []lissajousCurve
	trail  []trailPoint
}

type lissajousCurve struct {
	a, b  float64
	phase float64
	color ansi.Attr
}

type trailPoint struct {
	x, y  float64
	color ansi.Attr
	life  float64
}

func NewLissajous(w, h int) *Lissajous {
	_ = w
	_ = h
	return &Lissajous{
		speed: 0.12,
		curves: []lissajousCurve{
			{a: 3, b: 2, phase: 0, color: ansi.LightMagenta},
			{a: 4, b: 5, phase: math.Pi / 2, color: ansi.LightCyan},
			{a: 5, b: 4, phase: math.Pi / 3, color: ansi.Yellow},
		},
	}
}

func (l *Lissajous) Name() string       { return "lissajous" }
func (l *Lissajous) Category() Category { return Background }

func (l *Lissajous) Tick(f *ansi.Frame) {
	l.time += l.speed
	midX := float64(f.W-1) / 2
	midY := float64(f.H-1) / 2
	ampX := float64(f.W)/2 - 2
	ampY := float64(f.H)/2 - 2
	if ampX < 1 {
		ampX = 1
	}
	if ampY < 1 {
		ampY = 1
	}
	for _, c := range l.curves {
		x := midX + math.Sin(l.time*c.a+c.phase)*ampX
		y := midY + math.Cos(l.time*c.b+c.phase)*ampY
		l.trail = append(l.trail, trailPoint{x: x, y: y, color: c.color, life: 28})
	}
	f.Clear()
	for t := len(l.trail) - 1; t >= 0; t-- {
		p := &l.trail[t]
		p.life -= 1.2
		if p.life <= 0 {
			l.trail = append(l.trail[:t], l.trail[t+1:]...)
			continue
		}
		sx := int(math.Round(p.x))
		sy := int(math.Round(p.y))
		if sx < 0 || sy < 0 || sx >= f.W || sy >= f.H {
			continue
		}
		norm := p.life / 28
		var ch byte
		switch {
		case norm > 0.7:
			ch = '@'
		case norm > 0.45:
			ch = 'o'
		case norm > 0.2:
			ch = '+'
		default:
			ch = '.'
		}
		attr := p.color
		if norm < 0.25 && p.color == ansi.Yellow {
			attr = ansi.LightGray
		}
		f.SetCell(sx, sy, ch, attr)
	}
}
