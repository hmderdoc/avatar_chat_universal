package idle

import (
	"math/rand"
	"strings"

	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
)

// FigletMessage is a foreground animation that renders a chunky bitmap
// version of one or more text messages and bounces them around the screen,
// optionally cycling colors. Port of canvas-animations.js:776+, but using
// a hand-coded 5-row block font instead of TDF/FIGlet font files (TDF
// support is on the roadmap; embedding font data would balloon the binary
// by 50-200KB and we wanted to keep the first cut compact).
type FigletMessage struct {
	messages   []string
	current    string
	colors     bool
	move       bool
	palette    []ansi.Attr
	colorIndex int
	colorTick  int

	x, y          float64
	dx, dy        float64
	tickCount     int
	refreshTicks  int
	rng           *rand.Rand
}

// NewFigletMessage builds the animation. messages is the list of strings to
// rotate through (separated by '|' in config); empty list defaults to
// "Avatar Chat". colors enables the rainbow cycle, move enables the bouncing.
func NewFigletMessage(w, h int, messages []string, colors, move bool) *FigletMessage {
	if len(messages) == 0 {
		messages = []string{"Avatar Chat"}
	}
	fm := &FigletMessage{
		messages:     messages,
		colors:       colors,
		move:         move,
		palette:      []ansi.Attr{ansi.LightCyan, ansi.LightMagenta, ansi.Yellow, ansi.LightGreen, ansi.LightRed, ansi.White, ansi.LightBlue},
		refreshTicks: 180,
		rng:          rngFor(),
	}
	fm.pickMessage(w, h)
	if move {
		fm.dx = 0.45
		fm.dy = 0.25
		// Random initial direction.
		if fm.rng.Float64() < 0.5 {
			fm.dx = -fm.dx
		}
		if fm.rng.Float64() < 0.5 {
			fm.dy = -fm.dy
		}
	}
	return fm
}

func (fm *FigletMessage) Name() string       { return "figlet_message" }
func (fm *FigletMessage) Category() Category { return Foreground }

// pickMessage selects a new message and resets position. Fits the message
// to the frame width by truncating if necessary (no wrap to multiple
// rows; figlet is naturally 5 rows tall).
func (fm *FigletMessage) pickMessage(w, h int) {
	msg := fm.messages[fm.rng.Intn(len(fm.messages))]
	msg = strings.ToUpper(msg)
	// Truncate so the rendered width fits the frame.
	for {
		bw, _ := figletMeasure(msg)
		if bw <= w-2 || len(msg) <= 1 {
			break
		}
		msg = msg[:len(msg)-1]
	}
	fm.current = msg
	bw, bh := figletMeasure(msg)
	fm.x = float64((w - bw) / 2)
	fm.y = float64((h - bh) / 2)
	if fm.x < 0 {
		fm.x = 0
	}
	if fm.y < 0 {
		fm.y = 0
	}
	fm.tickCount = 0
}

func (fm *FigletMessage) Tick(f *ansi.Frame) {
	f.Clear() // transparent fill since this is a FG layer
	fm.tickCount++
	if fm.tickCount >= fm.refreshTicks {
		fm.pickMessage(f.W, f.H)
	}

	bw, bh := figletMeasure(fm.current)
	if bw == 0 || bh == 0 {
		return
	}

	if fm.move {
		fm.x += fm.dx
		fm.y += fm.dy
		maxX := float64(f.W - bw)
		maxY := float64(f.H - bh)
		if maxX < 0 {
			maxX = 0
		}
		if maxY < 0 {
			maxY = 0
		}
		if fm.x < 0 {
			fm.x = 0
			fm.dx = -fm.dx
		}
		if fm.x > maxX {
			fm.x = maxX
			fm.dx = -fm.dx
		}
		if fm.y < 0 {
			fm.y = 0
			fm.dy = -fm.dy
		}
		if fm.y > maxY {
			fm.y = maxY
			fm.dy = -fm.dy
		}
	}

	if fm.colors {
		fm.colorTick++
		if fm.colorTick >= 6 {
			fm.colorTick = 0
			fm.colorIndex = (fm.colorIndex + 1) % len(fm.palette)
		}
	}

	baseAttr := ansi.White
	if fm.colors {
		baseAttr = fm.palette[fm.colorIndex]
	}
	originX := int(fm.x)
	originY := int(fm.y)

	for i := 0; i < len(fm.current); i++ {
		ch := fm.current[i]
		glyph := figletGlyph(ch)
		gx := originX + i*(figletGlyphWidth+figletGlyphPad)
		// Per-char rainbow stagger when colors enabled.
		attr := baseAttr
		if fm.colors {
			attr = fm.palette[(fm.colorIndex+i)%len(fm.palette)]
		}
		for row := 0; row < figletGlyphHeight; row++ {
			line := glyph[row]
			for col := 0; col < figletGlyphWidth && col < len(line); col++ {
				if line[col] != '#' {
					continue
				}
				cx := gx + col
				cy := originY + row
				if cx < 0 || cy < 0 || cx >= f.W || cy >= f.H {
					continue
				}
				f.SetCell(cx, cy, 0xDB, attr)
			}
		}
	}
}
