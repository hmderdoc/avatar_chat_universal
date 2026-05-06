package idle

import (
	"math"
	"math/rand"

	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
)

// Fireflies are wandering glowing dots with random color shifts.
// Port of canvas-animations.js:215-264.
type Fireflies struct {
	bugs      []firefly
	tickCount int
	palette   []ansi.Attr
	rng       *rand.Rand
}

type firefly struct {
	x, y     float64
	dx, dy   float64
	colorIdx int
}

func NewFireflies(w, h int) *Fireflies {
	count := (w + h) / 3
	if count > 14 {
		count = 14
	}
	if count < 4 {
		count = 4
	}
	f := &Fireflies{
		palette: []ansi.Attr{ansi.LightGreen, ansi.LightCyan, ansi.LightMagenta, ansi.Yellow},
		rng:     rngFor(),
	}
	for i := 0; i < count; i++ {
		f.bugs = append(f.bugs, firefly{
			x:        f.rng.Float64() * float64(w),
			y:        f.rng.Float64() * float64(h),
			dx:       f.rng.Float64()*0.8 - 0.4,
			dy:       f.rng.Float64()*0.6 - 0.3,
			colorIdx: f.rng.Intn(len(f.palette)),
		})
	}
	return f
}

func (f *Fireflies) Name() string       { return "fireflies" }
func (f *Fireflies) Category() Category { return Background }

func (f *Fireflies) Tick(fr *ansi.Frame) {
	fr.Clear()
	f.tickCount++
	w := float64(fr.W - 1)
	h := float64(fr.H - 1)
	for i := range f.bugs {
		b := &f.bugs[i]
		if f.rng.Float64() < 0.2 {
			b.dx += f.rng.Float64()*0.2 - 0.1
			b.dy += f.rng.Float64()*0.2 - 0.1
			b.dx = clamp(b.dx, -0.7, 0.7)
			b.dy = clamp(b.dy, -0.5, 0.5)
		}
		b.x += b.dx
		b.y += b.dy
		if b.x < 0 {
			b.x = 0
			b.dx = math.Abs(b.dx)
		}
		if b.x > w {
			b.x = w
			b.dx = -math.Abs(b.dx)
		}
		if b.y < 0 {
			b.y = 0
			b.dy = math.Abs(b.dy)
		}
		if b.y > h {
			b.y = h
			b.dy = -math.Abs(b.dy)
		}
		if f.tickCount%18 == 0 && f.rng.Float64() < 0.6 {
			b.colorIdx = (b.colorIdx + 1) % len(f.palette)
		}
		fr.SetCell(int(math.Round(b.x)), int(math.Round(b.y)), '*', f.palette[b.colorIdx])
	}
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
