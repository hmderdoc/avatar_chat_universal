package idle

import (
	"math"

	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
)

// SineWave sweeps a horizontal sine wave across the frame with cycling
// colors. Port of canvas-animations.js:267-300.
type SineWave struct {
	phase   float64
	freq    float64
	amp     float64
	speed   float64
	char    byte
	palette []ansi.Attr
}

func NewSineWave(w, h int) *SineWave {
	_ = w
	maxAmp := h / 2
	if maxAmp < 1 {
		maxAmp = 1
	}
	amp := maxAmp - 1
	if amp < 1 {
		amp = 1
	}
	return &SineWave{
		freq:    1.2,
		amp:     float64(amp),
		speed:   0.3,
		char:    '~',
		palette: []ansi.Attr{ansi.LightBlue, ansi.Cyan, ansi.LightCyan, ansi.White},
	}
}

func (s *SineWave) Name() string       { return "sine_wave" }
func (s *SineWave) Category() Category { return Background }

func (s *SineWave) Tick(f *ansi.Frame) {
	s.phase += s.speed
	f.Clear()
	mid := f.H / 2
	if mid < 1 {
		mid = 1
	}
	width := f.W
	if width < 1 {
		width = 1
	}
	for x := 0; x < f.W; x++ {
		angle := float64(x)/float64(width)*math.Pi*2*s.freq + s.phase
		y := int(math.Round(float64(mid) + math.Sin(angle)*s.amp))
		if y < 0 {
			y = 0
		}
		if y >= f.H {
			y = f.H - 1
		}
		idx := ((x + int(math.Floor(s.phase))) % len(s.palette))
		if idx < 0 {
			idx += len(s.palette)
		}
		f.SetCell(x, y, s.char, s.palette[idx])
	}
}
