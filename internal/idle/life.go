package idle

import (
	"math/rand"

	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
)

// Life is Conway's Game of Life with vertical half-block compression
// (each terminal row holds two cell rows via 0xDB/0xDF/0xDC). Reseeds when
// the grid stagnates or after a generation cap. Port of canvas-animations.js:88-167.
type Life struct {
	w, h         int // h is doubled for vertical half-block resolution
	grid, next   [][]byte
	tickCount    int
	maxTicks     int
	stagnant     int
	maxStagnant  int
	lastHash     uint32
	colorIndex   int
	density      float64
	palette      []ansi.Attr
	rng          *rand.Rand
}

func NewLife(w, h int) *Life {
	l := &Life{
		w:           w,
		h:           h * 2,
		maxTicks:    400,
		maxStagnant: 60,
		density:     0.28,
		palette: []ansi.Attr{
			ansi.LightGreen, ansi.Green, ansi.LightCyan, ansi.Cyan,
			ansi.LightMagenta, ansi.Magenta, ansi.Yellow, ansi.White,
		},
		rng: rngFor(),
	}
	l.alloc()
	return l
}

func (l *Life) Name() string       { return "life" }
func (l *Life) Category() Category { return Background }

func (l *Life) alloc() {
	l.grid = make([][]byte, l.h)
	l.next = make([][]byte, l.h)
	for y := 0; y < l.h; y++ {
		l.grid[y] = make([]byte, l.w)
		l.next[y] = make([]byte, l.w)
		for x := 0; x < l.w; x++ {
			if l.rng.Float64() < l.density {
				l.grid[y][x] = 1
			}
		}
	}
}

func (l *Life) step() {
	w, h := l.w, l.h
	for y := 0; y < h; y++ {
		yp := (y + 1) % h
		ym := (y - 1 + h) % h
		for x := 0; x < w; x++ {
			xp := (x + 1) % w
			xm := (x - 1 + w) % w
			alive := l.grid[y][x] != 0
			n := int(l.grid[ym][xm] + l.grid[ym][x] + l.grid[ym][xp] +
				l.grid[y][xm] + l.grid[y][xp] +
				l.grid[yp][xm] + l.grid[yp][x] + l.grid[yp][xp])
			next := byte(0)
			if alive && (n == 2 || n == 3) {
				next = 1
			} else if !alive && n == 3 {
				next = 1
			}
			l.next[y][x] = next
		}
	}
	l.grid, l.next = l.next, l.grid
}

func (l *Life) hash() uint32 {
	var acc uint32
	for y := 0; y < l.h; y += 4 {
		for x := 0; x < l.w; x += 4 {
			acc = (acc*131 + uint32(l.grid[y][x])) & 0x7fffffff
		}
	}
	return acc
}

func (l *Life) Tick(f *ansi.Frame) {
	l.step()
	l.tickCount++
	if l.tickCount%15 == 0 {
		l.colorIndex++
	}
	h := l.hash()
	if h == l.lastHash {
		l.stagnant++
	} else {
		l.stagnant = 0
	}
	l.lastHash = h
	if l.stagnant > l.maxStagnant || l.tickCount > l.maxTicks {
		l.alloc()
		l.tickCount = 0
		l.stagnant = 0
		l.colorIndex = 0
	}

	attr := l.palette[l.colorIndex%len(l.palette)]
	f.Clear()
	visH := l.h / 2
	for vy := 0; vy < visH && vy < f.H; vy++ {
		yTop := vy * 2
		yBot := yTop + 1
		for x := 0; x < l.w && x < f.W; x++ {
			top := l.grid[yTop][x] != 0
			bot := l.grid[yBot][x] != 0
			var ch byte
			switch {
			case top && bot:
				ch = 0xDB
			case top:
				ch = 0xDF
			case bot:
				ch = 0xDC
			default:
				ch = ' '
			}
			f.SetCell(x, vy, ch, attr)
		}
	}
	_ = rand.Intn // keep import
}
