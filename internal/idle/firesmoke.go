package idle

import (
	"math"
	"math/rand"

	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
)

// FireSmoke is a doom-fire-style flame propagation with periodic smoke
// puffs floating up. Port of canvas-animations.js:1245-1331.
type FireSmoke struct {
	w, h   int
	buffer [][]int
	smoke  []smokePuff
	decay  int
	rng    *rand.Rand
}

type smokePuff struct {
	x, y float64
	life int
}

type fireGradient struct {
	threshold int
	ch        byte
	attr      ansi.Attr
}

func NewFireSmoke(w, h int) *FireSmoke {
	fs := &FireSmoke{w: w, h: h, decay: 1, rng: rngFor()}
	fs.buffer = make([][]int, h+2)
	for i := range fs.buffer {
		fs.buffer[i] = make([]int, w)
	}
	return fs
}

func (fs *FireSmoke) Name() string       { return "fire_smoke" }
func (fs *FireSmoke) Category() Category { return Background }

func (fs *FireSmoke) sample(row, x int) int {
	if x < 0 {
		x = 0
	} else if x >= fs.w {
		x = fs.w - 1
	}
	if row < 0 {
		row = 0
	} else if row >= len(fs.buffer) {
		row = len(fs.buffer) - 1
	}
	return fs.buffer[row][x]
}

var fireGradients = []fireGradient{
	{20, ' ', ansi.Black | ansi.BgBlack},
	{70, '.', ansi.DarkGray | ansi.BgBlack},
	{120, '`', ansi.Yellow | ansi.BgRed},
	{170, '^', ansi.White | ansi.BgRed},
	{255, '#', ansi.White | ansi.BgRed},
}

func findGradient(v int) fireGradient {
	for _, g := range fireGradients {
		if v <= g.threshold {
			return g
		}
	}
	return fireGradients[len(fireGradients)-1]
}

func (fs *FireSmoke) Tick(f *ansi.Frame) {
	w, h := fs.w, fs.h
	if h <= 0 || w <= 0 {
		return
	}
	bottom := h + 1
	for x := 0; x < w; x++ {
		if fs.rng.Float64() < 0.22 {
			fs.buffer[bottom][x] = 255
		} else {
			fs.buffer[bottom][x] = 0
		}
	}
	for y := h; y > 0; y-- {
		dest := fs.buffer[y-1]
		for x := 0; x < w; x++ {
			val := (fs.sample(y, x) +
				fs.sample(y, x-1) +
				fs.sample(y, x+1) +
				fs.sample(y+1, x)) / 4
			val -= fs.decay + fs.rng.Intn(3)
			if val < 0 {
				val = 0
			}
			dest[x] = val
			if val > 200 && y < h-4 && fs.rng.Float64() < 0.035 {
				fs.smoke = append(fs.smoke, smokePuff{
					x:    float64(x) + fs.rng.Float64()*0.6 - 0.3,
					y:    float64(y - 1),
					life: 18,
				})
			}
		}
	}
	for s := len(fs.smoke) - 1; s >= 0; s-- {
		p := &fs.smoke[s]
		p.y -= 0.25 + fs.rng.Float64()*0.15
		p.x += fs.rng.Float64()*0.5 - 0.25
		p.life--
		if p.y < 0 || p.x < -1 || p.x > float64(w) || p.life <= 0 {
			fs.smoke = append(fs.smoke[:s], fs.smoke[s+1:]...)
		}
	}
	for y := 0; y < h && y < f.H; y++ {
		for x := 0; x < w && x < f.W; x++ {
			g := findGradient(fs.buffer[y][x])
			f.SetCell(x, y, g.ch, g.attr)
		}
	}
	for _, s := range fs.smoke {
		sx := int(math.Round(s.x))
		sy := int(math.Round(s.y))
		if sx < 0 || sy < 0 || sx >= f.W || sy >= f.H {
			continue
		}
		var ch byte
		switch {
		case s.life > 12:
			ch = '~'
		case s.life > 6:
			ch = '.'
		default:
			ch = ' '
		}
		attr := ansi.LightGray
		if s.life <= 6 {
			attr = ansi.DarkGray
		}
		f.SetCell(sx, sy, ch, attr)
	}
	_ = rand.Intn
}
