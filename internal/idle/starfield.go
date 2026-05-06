package idle

import (
	"math"
	"math/rand"

	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
)

// Starfield is a parallax scrolling-stars animation, ported from the JS
// chat door's canvas-animations.js:170-212. Stars move left at varying
// speeds; brightness derives from a per-star "depth" value. When a star
// scrolls off the left edge it respawns on the right.
type Starfield struct {
	stars     []star
	speedBase float64
	rng       *rand.Rand
}

type star struct {
	x, y       float64
	speed      float64
	depth      float64 // 0..1, controls brightness tier
}

// NewStarfield builds a starfield sized for a (w, h) frame. Default density
// is one star per ~10 cells; minimum 12.
func NewStarfield(w, h int) *Starfield {
	s := &Starfield{
		speedBase: 0.6,
		rng:       rngFor(),
	}
	count := (w * h) / 10
	if count < 12 {
		count = 12
	}
	s.stars = make([]star, count)
	for i := range s.stars {
		s.stars[i] = s.spawn(w, h, true)
	}
	return s
}

func (s *Starfield) Name() string       { return "starfield" }
func (s *Starfield) Category() Category { return Background }

// spawn returns a fresh star. If anywhere==true, position is anywhere in
// the frame (initial population); otherwise the star starts at the right
// edge (respawn after wrapping).
func (s *Starfield) spawn(w, h int, anywhere bool) star {
	x := float64(w)
	if anywhere {
		x = 1 + s.rng.Float64()*float64(w-1)
	}
	return star{
		x:     x,
		y:     1 + s.rng.Float64()*float64(h-1),
		speed: s.speedBase + s.rng.Float64()*s.speedBase,
		depth: s.rng.Float64(),
	}
}

func (s *Starfield) Tick(f *ansi.Frame) {
	f.Clear()
	chars := []byte{'.', ':', '*'}
	palette := []ansi.Attr{ansi.LightGray, ansi.LightCyan, ansi.White}
	for i := range s.stars {
		st := &s.stars[i]
		st.x -= st.speed
		if st.x < 1 {
			*st = s.spawn(f.W, f.H, false)
		}
		cx := int(math.Round(st.x)) - 1
		cy := int(math.Round(st.y)) - 1
		if cx < 0 || cy < 0 || cx >= f.W || cy >= f.H {
			continue
		}
		brightness := 0
		switch {
		case st.depth >= 0.66:
			brightness = 2
		case st.depth >= 0.33:
			brightness = 1
		}
		f.SetCell(cx, cy, chars[brightness], palette[brightness])
	}
}
