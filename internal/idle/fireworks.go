package idle

import (
	"math"
	"math/rand"

	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
)

// Fireworks spawns periodic bursts of particles with gravity and fade.
// Port of canvas-animations.js:389-452.
type Fireworks struct {
	bursts     []burst
	maxBursts  int
	gravity    float64
	spawnDelay int
	palette    []ansi.Attr
	rng        *rand.Rand
}

type burst struct {
	particles []particle
}

type particle struct {
	x, y     float64
	dx, dy   float64
	life     int
	colorIdx int
}

func NewFireworks(w, h int) *Fireworks {
	_ = w
	_ = h
	return &Fireworks{
		maxBursts: 4,
		gravity:   0.08,
		palette: []ansi.Attr{
			ansi.Yellow, ansi.White, ansi.LightRed,
			ansi.LightMagenta, ansi.LightCyan, ansi.LightGreen,
		},
		rng: rngFor(),
	}
}

func (fw *Fireworks) Name() string       { return "fireworks" }
func (fw *Fireworks) Category() Category { return Background }

func (fw *Fireworks) spawnBurst(f *ansi.Frame) {
	count := 24 + fw.rng.Intn(22)
	parts := make([]particle, 0, count)
	for i := 0; i < count; i++ {
		angle := math.Pi * 2 * float64(i) / float64(count)
		speed := 0.7 + fw.rng.Float64()*1.3
		parts = append(parts, particle{
			x:        fw.rng.Float64() * float64(f.W),
			y:        fw.rng.Float64() * float64(f.H/2),
			dx:       math.Cos(angle) * speed,
			dy:       math.Sin(angle) * speed,
			life:     25 + fw.rng.Intn(18),
			colorIdx: fw.rng.Intn(len(fw.palette)),
		})
	}
	fw.bursts = append(fw.bursts, burst{particles: parts})
}

func (fw *Fireworks) Tick(f *ansi.Frame) {
	fw.spawnDelay--
	if fw.spawnDelay <= 0 && len(fw.bursts) < fw.maxBursts {
		fw.spawnBurst(f)
		fw.spawnDelay = 18 + fw.rng.Intn(18)
	}
	f.Clear()
	for b := len(fw.bursts) - 1; b >= 0; b-- {
		bb := &fw.bursts[b]
		for p := len(bb.particles) - 1; p >= 0; p-- {
			pt := &bb.particles[p]
			pt.x += pt.dx
			pt.y += pt.dy
			pt.dy += fw.gravity
			pt.life--
			if pt.life <= 0 || pt.y > float64(f.H) {
				bb.particles = append(bb.particles[:p], bb.particles[p+1:]...)
				continue
			}
			intensity := pt.colorIdx + pt.life/10
			if intensity >= len(fw.palette) {
				intensity = len(fw.palette) - 1
			}
			if intensity < 0 {
				intensity = 0
			}
			var ch byte
			switch {
			case pt.life > 20:
				ch = '*'
			case pt.life > 10:
				ch = '+'
			default:
				ch = '.'
			}
			cx := int(math.Round(pt.x))
			cy := int(math.Round(pt.y))
			if cx < 0 || cy < 0 || cx >= f.W || cy >= f.H {
				continue
			}
			f.SetCell(cx, cy, ch, fw.palette[intensity])
		}
		if len(bb.particles) == 0 {
			fw.bursts = append(fw.bursts[:b], fw.bursts[b+1:]...)
		}
	}
	_ = rand.Intn
}
