package idle

import (
	"math"

	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
)

// Plasma renders a procedural plasma field built from layered sine waves.
// Port of canvas-animations.js:361-386.
type Plasma struct {
	t       float64
	speed   float64
	scale   float64
	palette []ansi.Attr
}

func NewPlasma(w, h int) *Plasma {
	_ = w
	_ = h
	return &Plasma{
		speed: 0.18,
		scale: 0.12,
		palette: []ansi.Attr{
			ansi.Blue, ansi.LightBlue, ansi.LightCyan, ansi.Cyan,
			ansi.LightMagenta, ansi.Magenta, ansi.LightRed, ansi.Yellow, ansi.White,
		},
	}
}

func (p *Plasma) Name() string       { return "plasma" }
func (p *Plasma) Category() Category { return Background }

func (p *Plasma) Tick(f *ansi.Frame) {
	p.t += p.speed
	for y := 0; y < f.H; y++ {
		for x := 0; x < f.W; x++ {
			nx := float64(x+1) * p.scale
			ny := float64(y+1) * p.scale
			val := math.Sin(nx+p.t) +
				math.Sin((ny+p.t*0.7)*1.3) +
				math.Sin(math.Sqrt(nx*nx+ny*ny)+p.t*0.4)
			norm := (val + 3) / 6
			if norm < 0 {
				norm = 0
			}
			if norm > 1 {
				norm = 1
			}
			pal := int(math.Floor(norm * float64(len(p.palette))))
			if pal >= len(p.palette) {
				pal = len(p.palette) - 1
			}
			var ch byte
			switch {
			case norm < 0.2:
				ch = ' '
			case norm < 0.4:
				ch = '.'
			case norm < 0.6:
				ch = '*'
			case norm < 0.8:
				ch = 'o'
			default:
				ch = '@'
			}
			f.SetCell(x, y, ch, p.palette[pal])
		}
	}
}
