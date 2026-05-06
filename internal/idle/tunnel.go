package idle

import (
	"math"

	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
)

// Tunnel is the recursive-tunnel illusion: concentric pulsing rings
// with a rotating twist. Port of canvas-animations.js:685-721.
type Tunnel struct {
	time    float64
	speed   float64
	scale   float64
	palette []ansi.Attr
}

func NewTunnel(w, h int) *Tunnel {
	_ = w
	_ = h
	return &Tunnel{
		speed:   0.22,
		scale:   0.17,
		palette: []ansi.Attr{ansi.LightBlue, ansi.Cyan, ansi.LightMagenta, ansi.LightGray, ansi.White},
	}
}

func (t *Tunnel) Name() string       { return "tunnel" }
func (t *Tunnel) Category() Category { return Background }

func (t *Tunnel) Tick(f *ansi.Frame) {
	t.time += t.speed
	cx := float64(f.W-1) / 2
	cy := float64(f.H-1) / 2
	for y := 0; y < f.H; y++ {
		for x := 0; x < f.W; x++ {
			dx := float64(x) - cx
			dy := float64(y) - cy
			dist := math.Sqrt(dx*dx+dy*dy) * t.scale
			angle := math.Atan2(dy, dx)
			band := math.Sin(dist-t.time)*0.5 + 0.5
			twist := math.Sin(angle*3+t.time*0.7)*0.5 + 0.5
			mix := (band + twist) / 2
			if mix < 0 {
				mix = 0
			}
			if mix > 1 {
				mix = 1
			}
			idx := int(math.Floor(mix * float64(len(t.palette))))
			if idx >= len(t.palette) {
				idx = len(t.palette) - 1
			}
			var ch byte
			switch {
			case mix > 0.8:
				ch = '#'
			case mix > 0.6:
				ch = '='
			case mix > 0.4:
				ch = '-'
			case mix > 0.25:
				ch = '+'
			case mix > 0.1:
				ch = '.'
			default:
				ch = ' '
			}
			f.SetCell(x, y, ch, t.palette[idx])
		}
	}
}
