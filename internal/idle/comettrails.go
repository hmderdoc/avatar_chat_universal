package idle

import (
	"math"
	"math/rand"

	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
)

// CometTrails are bouncing glowing comets with fading particle trails.
// Port of canvas-animations.js:303-358.
type CometTrails struct {
	comets  []comet
	palette []ansi.Attr
	rng     *rand.Rand
}

type comet struct {
	x, y   float64
	dx, dy float64
	trail  []point
}

type point struct{ x, y float64 }

func NewCometTrails(w, h int) *CometTrails {
	count := (w + h) / 20
	if count < 3 {
		count = 3
	}
	speed := 0.8
	c := &CometTrails{
		palette: []ansi.Attr{ansi.White, ansi.LightCyan, ansi.LightBlue, ansi.LightGray},
		rng:     rngFor(),
	}
	for i := 0; i < count; i++ {
		angle := c.rng.Float64() * math.Pi * 2
		c.comets = append(c.comets, comet{
			x:  c.rng.Float64() * float64(w),
			y:  c.rng.Float64() * float64(h),
			dx: math.Cos(angle) * speed,
			dy: math.Sin(angle) * speed,
		})
	}
	return c
}

func (c *CometTrails) Name() string       { return "comet_trails" }
func (c *CometTrails) Category() Category { return Background }

func (c *CometTrails) drawPoint(f *ansi.Frame, x, y float64, ch byte, colorIdx int) {
	cx := int(math.Round(x))
	cy := int(math.Round(y))
	if cx < 0 || cy < 0 || cx >= f.W || cy >= f.H {
		return
	}
	if colorIdx >= len(c.palette) {
		colorIdx = len(c.palette) - 1
	}
	f.SetCell(cx, cy, ch, c.palette[colorIdx])
}

func (c *CometTrails) Tick(f *ansi.Frame) {
	f.Clear()
	maxX := float64(f.W - 1)
	maxY := float64(f.H - 1)
	for i := range c.comets {
		cm := &c.comets[i]
		cm.x += cm.dx
		cm.y += cm.dy
		if cm.x < 0 {
			cm.x = 0
			cm.dx = math.Abs(cm.dx)
		}
		if cm.x > maxX {
			cm.x = maxX
			cm.dx = -math.Abs(cm.dx)
		}
		if cm.y < 0 {
			cm.y = 0
			cm.dy = math.Abs(cm.dy)
		}
		if cm.y > maxY {
			cm.y = maxY
			cm.dy = -math.Abs(cm.dy)
		}
		cm.trail = append([]point{{cm.x, cm.y}}, cm.trail...)
		if len(cm.trail) > 10 {
			cm.trail = cm.trail[:10]
		}
		for t, p := range cm.trail {
			var ch byte
			switch {
			case t == 0:
				ch = '@'
			case t < 3:
				ch = 'O'
			case t < 6:
				ch = '+'
			default:
				ch = '.'
			}
			c.drawPoint(f, p.x, p.y, ch, t)
			if t <= 1 {
				flank := byte('+')
				if t == 0 {
					flank = '*'
				}
				c.drawPoint(f, p.x+0.6, p.y, flank, t+1)
				c.drawPoint(f, p.x-0.6, p.y, flank, t+1)
			}
		}
	}
	_ = rand.Intn
}
