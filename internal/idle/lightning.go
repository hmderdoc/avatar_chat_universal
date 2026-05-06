package idle

import (
	"math/rand"

	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
)

// Lightning paints jagged bolts down the screen with afterglow. Each cell
// has a fade value that decays each frame; bolts boost it back to peak.
// Port of canvas-animations.js:600-682.
type Lightning struct {
	w, h     int
	fade     [][]int
	chars    [][]byte
	bolts    []bolt
	cooldown int
	rng      *rand.Rand
}

type bolt struct {
	path []boltPt
	life int
}

type boltPt struct{ x, y int }

func NewLightning(w, h int) *Lightning {
	l := &Lightning{w: w, h: h, rng: rngFor()}
	l.fade = make([][]int, h)
	l.chars = make([][]byte, h)
	for y := 0; y < h; y++ {
		l.fade[y] = make([]int, w)
		l.chars[y] = make([]byte, w)
		for x := 0; x < w; x++ {
			l.chars[y][x] = ' '
		}
	}
	return l
}

func (l *Lightning) Name() string       { return "lightning" }
func (l *Lightning) Category() Category { return Background }

func (l *Lightning) spawnBolt() {
	if l.h <= 0 {
		return
	}
	x := l.rng.Intn(l.w)
	y := 0
	path := []boltPt{{x, y}}
	for y < l.h-1 {
		step := 1
		if l.rng.Float64() < 0.25 {
			step = 2
		}
		y += step
		if y >= l.h {
			y = l.h - 1
		}
		x += l.rng.Intn(3) - 1
		if x < 0 {
			x = 0
		} else if x >= l.w {
			x = l.w - 1
		}
		path = append(path, boltPt{x, y})
	}
	l.bolts = append(l.bolts, bolt{path: path, life: 6})
}

func (l *Lightning) Tick(f *ansi.Frame) {
	if l.cooldown <= 0 {
		l.spawnBolt()
		l.cooldown = 15 + l.rng.Intn(25)
	} else {
		l.cooldown--
	}
	for y := 0; y < l.h; y++ {
		for x := 0; x < l.w; x++ {
			v := l.fade[y][x] - 6
			if v < 0 {
				v = 0
				if l.chars[y][x] != ' ' {
					l.chars[y][x] = ' '
				}
			}
			l.fade[y][x] = v
		}
	}
	for b := len(l.bolts) - 1; b >= 0; b-- {
		bo := &l.bolts[b]
		bo.life--
		for i, seg := range bo.path {
			if seg.x < 0 || seg.y < 0 || seg.x >= l.w || seg.y >= l.h {
				continue
			}
			ch := byte('|')
			if i > 0 {
				dx := seg.x - bo.path[i-1].x
				switch {
				case dx > 0:
					ch = '/'
				case dx < 0:
					ch = '\\'
				}
			}
			l.chars[seg.y][seg.x] = ch
			l.fade[seg.y][seg.x] = 255
			if seg.y+1 < l.h && l.fade[seg.y+1][seg.x] < 120 {
				l.fade[seg.y+1][seg.x] = 120
			}
			if seg.x > 0 && l.fade[seg.y][seg.x-1] < 80 {
				l.fade[seg.y][seg.x-1] = 80
			}
			if seg.x+1 < l.w && l.fade[seg.y][seg.x+1] < 80 {
				l.fade[seg.y][seg.x+1] = 80
			}
		}
		if bo.life <= 0 {
			l.bolts = append(l.bolts[:b], l.bolts[b+1:]...)
		}
	}
	for y := 0; y < l.h && y < f.H; y++ {
		for x := 0; x < l.w && x < f.W; x++ {
			intensity := l.fade[y][x]
			if intensity <= 0 {
				f.SetCell(x, y, ' ', ansi.Black)
				continue
			}
			var attr ansi.Attr
			switch {
			case intensity > 180:
				attr = ansi.White
			case intensity > 120:
				attr = ansi.LightCyan
			case intensity > 60:
				attr = ansi.Cyan
			default:
				attr = ansi.LightGray
			}
			f.SetCell(x, y, l.chars[y][x], attr)
		}
	}
}
