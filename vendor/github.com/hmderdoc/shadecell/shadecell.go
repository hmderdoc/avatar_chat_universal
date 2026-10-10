// Package shadecell turns 24-bit pixel pairs into the best 16-color text cell
// a classic ANSI terminal can show: a space, a full block, one of the three
// CP437 shade glyphs (░ ▒ ▓) or an upper/lower half block, with a foreground
// of 16 and a background of 8. It is a Go port of the per-cell matcher in
// hmderdoc/shadeans, as used by the futureland shell's compositor.
//
// Plain nearest-colour snapping gives 16 flat colours and no shading. The
// shade glyphs blend a foreground and a background at 25, 50 and 75 percent
// coverage, so the same palette yields a few hundred usable tones.
//
// Every candidate is scored in Oklab (perceptual) while shade mixes are
// formed in linear light (physical):
//
//   - uniform candidates (space, full block, three shades): the eye fuses the
//     cell into one colour M; cost = sum over both samples of |sample - M|^2
//     plus a texture cost lambda * n * a(1-a) * loudness(fg, bg), so shades
//     only win between close-valued colours, the way ramps are drawn by hand;
//   - structural candidates (upper / lower half block): real geometry; each
//     half gets its own nearest colour, and the orientation is free so a
//     bright half can always be the foreground (backgrounds stop at 8).
//
// shadeans' whole-image coherence pass is skipped: cells are decided on
// their own, so a video frame or an animation costs O(1) per cell and the
// decision is stable frame to frame. Results are memoised on quantised
// colour keys and fill in on demand. A Quantizer is not safe for concurrent
// use; give each rendering goroutine its own.
package shadecell

import "math"

// RGB is a 24-bit colour.
type RGB struct{ R, G, B uint8 }

// Cell is one text cell: a CP437 glyph and a CGA attribute (fg | bg<<4).
type Cell struct {
	Ch   byte
	Attr byte
}

// Fg is the foreground palette index, 0-15 in IBM order (1=blue, 4=red).
func (c Cell) Fg() byte { return c.Attr & 15 }

// Bg is the background palette index, 0-7 in IBM order.
func (c Cell) Bg() byte { return c.Attr >> 4 }

// CP437 glyph codes a Cell can carry.
const (
	Space       = 0x20
	ShadeLight  = 0xB0 // ░ 25 % coverage
	ShadeMedium = 0xB1 // ▒ 50 %
	ShadeDark   = 0xB2 // ▓ 75 %
	FullBlock   = 0xDB // █
	HalfLower   = 0xDC // ▄ foreground paints the bottom
	HalfUpper   = 0xDF // ▀ foreground paints the top
)

// UTF8 returns the Unicode spelling of a block/shade code for terminals that
// take UTF-8 instead of CP437. Any other byte comes back as itself.
func UTF8(ch byte) string {
	switch ch {
	case ShadeLight:
		return "░"
	case ShadeMedium:
		return "▒"
	case ShadeDark:
		return "▓"
	case FullBlock:
		return "█"
	case HalfLower:
		return "▄"
	case HalfUpper:
		return "▀"
	}
	return string(rune(ch))
}

// DefaultLambda is shadeans' default texture weight: how much of a dither
// pattern the eye still sees.
const DefaultLambda = 0.1

// ChromaWeight weights hue and chroma differences above lightness when
// measuring how loud a fg/bg pattern is. Plain Oklab distance lets a lone
// cell reach a neutral tone by checkering two complementary colours (cyan on
// brown reads as grey only from across the room); weighting chroma keeps
// blends on same-hue ramps and colour-with-grey, the pairs an ANSI artist
// shades with.
const ChromaWeight = 3

const samples = 2 // samples per cell: the upper and lower pixel

// Palette is the 16-colour IBM VGA palette, index order 0=black 1=blue
// 2=green 3=cyan 4=red 5=magenta 6=brown 7=light grey 8-15 bright.
var Palette = [16]RGB{
	{0, 0, 0}, {0, 0, 170}, {0, 170, 0}, {0, 170, 170},
	{170, 0, 0}, {170, 0, 170}, {170, 85, 0}, {170, 170, 170},
	{85, 85, 85}, {85, 85, 255}, {85, 255, 85}, {85, 255, 255},
	{255, 85, 85}, {255, 85, 255}, {255, 255, 85}, {255, 255, 255},
}

type v3 [3]float32

var shades = [3]struct {
	ch       byte
	coverage float32
}{{ShadeLight, 0.25}, {ShadeMedium, 0.5}, {ShadeDark, 0.75}}

func srgbToLinear(v uint8) float32 {
	c := float64(v) / 255
	if c <= 0.04045 {
		return float32(c / 12.92)
	}
	return float32(math.Pow((c+0.055)/1.055, 2.4))
}

func cbrt(v float32) float32 {
	if v <= 0 {
		return 0
	}
	return float32(math.Cbrt(float64(v)))
}

func linearToOklab(r, g, b float32) v3 {
	l := cbrt(0.41222147*r + 0.53633255*g + 0.051445995*b)
	m := cbrt(0.2119035*r + 0.6806995*g + 0.10739696*b)
	s := cbrt(0.08830246*r + 0.28171885*g + 0.6299787*b)
	return v3{
		0.21045426*l + 0.7936178*m - 0.004072047*s,
		1.9779985*l - 2.4285922*m + 0.4505937*s,
		0.025904037*l + 0.78277177*m - 0.80867577*s,
	}
}

// Oklab converts an sRGB colour to Oklab.
func Oklab(c RGB) [3]float32 {
	return linearToOklab(srgbToLinear(c.R), srgbToLinear(c.G), srgbToLinear(c.B))
}

func dist2(a, b v3) float32 {
	d0, d1, d2 := a[0]-b[0], a[1]-b[1], a[2]-b[2]
	return d0*d0 + d1*d1 + d2*d2
}

func loudness(a, b v3) float32 {
	dl, da, db := a[0]-b[0], a[1]-b[1], a[2]-b[2]
	return dl*dl + ChromaWeight*(da*da+db*db)
}

type palette struct {
	lab        [16]v3
	pair       [16][16]float32 // loudness of each fg/bg pattern
	mix        [3][16][16]v3   // [level][fg][bg] fused colour of a shade cell
	patternVar [3]float32      // a(1-a) per level
}

var pal = buildPalette()

func buildPalette() *palette {
	p := &palette{}
	var lin [16]v3
	for i, c := range Palette {
		lin[i] = v3{srgbToLinear(c.R), srgbToLinear(c.G), srgbToLinear(c.B)}
		p.lab[i] = linearToOklab(lin[i][0], lin[i][1], lin[i][2])
	}
	for f := 0; f < 16; f++ {
		for b := 0; b < 16; b++ {
			p.pair[f][b] = loudness(p.lab[f], p.lab[b])
		}
	}
	for s, sh := range shades {
		a := sh.coverage
		for f := 0; f < 16; f++ {
			for b := 0; b < 16; b++ {
				lf, lb := lin[f], lin[b]
				p.mix[s][f][b] = linearToOklab(
					lf[0]*a+lb[0]*(1-a),
					lf[1]*a+lb[1]*(1-a),
					lf[2]*a+lb[2]*(1-a))
			}
		}
		p.patternVar[s] = a * (1 - a)
	}
	return p
}

// sampleInfo is what one 24-bit sample resolves to, memoised on a 15-bit
// sRGB key (5 bits per channel).
type sampleInfo struct {
	lab     v3
	n16, n8 byte    // nearest of 16 (a foreground) / of the 8 dark (a background)
	d16, d8 float32 // their Oklab distances
	ok      bool
}

type uniformBest struct {
	ch, attr byte
	cost     float32 // against the bucket's centre colour, texture included
	ok       bool
}

const (
	lSteps  = 128
	abSteps = 64
	abRange = 0.32
)

// Quantizer maps pixel pairs to cells. Zero value is not usable; call New.
type Quantizer struct {
	lambda  float32
	samples []sampleInfo  // 1<<15
	uniform []uniformBest // lSteps * abSteps * abSteps
}

// New returns a quantizer with the default texture weight.
func New() *Quantizer { return NewLambda(DefaultLambda) }

// NewLambda returns a quantizer with a custom texture weight: higher values
// make shade glyphs rarer (flatter, more poster-like), lower values let the
// eye-mixed tones win more often.
func NewLambda(lambda float64) *Quantizer {
	return &Quantizer{
		lambda:  float32(lambda),
		samples: make([]sampleInfo, 1<<15),
		uniform: make([]uniformBest, lSteps<<12),
	}
}

// Pair returns the best cell for an upper pixel over a lower pixel.
func (q *Quantizer) Pair(top, bottom RGB) Cell {
	t := q.sample(top)
	b := q.sample(bottom)
	mean := v3{(t.lab[0] + b.lab[0]) / 2, (t.lab[1] + b.lab[1]) / 2, (t.lab[2] + b.lab[2]) / 2}
	u := q.uniformBest(mean)
	// Parallelogram identity: the sum of |s - M|^2 over both samples equals
	// 2|mean - M|^2 + (|t - mean|^2 + |b - mean|^2); the second term is the
	// same for every uniform candidate, so the memoised cost just adds it.
	within := dist2(t.lab, mean) + dist2(b.lab, mean)
	bestCh, bestAttr, bestCost := u.ch, u.attr, u.cost+within
	if t != b {
		// Upper half block: top is the foreground.
		if c := t.d16 + b.d8; c < bestCost {
			bestCost, bestCh, bestAttr = c, HalfUpper, t.n16|b.n8<<4
		}
		// Lower half block: bottom is the foreground (lets a bright lower half win).
		if c := b.d16 + t.d8; c < bestCost {
			bestCost, bestCh, bestAttr = c, HalfLower, b.n16|t.n8<<4
		}
	}
	// A half block whose halves agree is a solid: emit the canonical form.
	if (bestCh == HalfUpper || bestCh == HalfLower) && bestAttr&15 == bestAttr>>4 {
		return Solid(bestAttr & 15)
	}
	return Cell{bestCh, bestAttr}
}

// Solid is the canonical cell for one flat palette colour: a space on a dark
// background, or a full block in a bright foreground.
func Solid(colour byte) Cell {
	if colour < 8 {
		return Cell{Space, colour << 4}
	}
	return Cell{FullBlock, colour}
}

// Nearest16 is the perceptually nearest palette index (0-15) for a colour,
// for text foregrounds.
func (q *Quantizer) Nearest16(c RGB) byte { return q.sample(c).n16 }

// Nearest8 is the perceptually nearest dark palette index (0-7), for
// backgrounds on terminals without bright backgrounds.
func (q *Quantizer) Nearest8(c RGB) byte { return q.sample(c).n8 }

// TextAttr is the attribute for a text glyph: nearest fg of 16, bg of 8.
func (q *Quantizer) TextAttr(fg, bg RGB) byte {
	return q.sample(fg).n16 | q.sample(bg).n8<<4
}

func (q *Quantizer) sample(c RGB) *sampleInfo {
	key := int(c.R>>3)<<10 | int(c.G>>3)<<5 | int(c.B>>3)
	s := &q.samples[key]
	if s.ok {
		return s
	}
	lab := Oklab(c)
	var n16, n8 byte
	d16, d8 := float32(math.MaxFloat32), float32(math.MaxFloat32)
	for i := 0; i < 16; i++ {
		d := dist2(lab, pal.lab[i])
		if d < d16 {
			d16, n16 = d, byte(i)
		}
		if i < 8 && d < d8 {
			d8, n8 = d, byte(i)
		}
	}
	*s = sampleInfo{lab: lab, n16: n16, n8: n8, d16: d16, d8: d8, ok: true}
	return s
}

func bucket(v, lo, hi float32, steps int) int {
	k := int((v - lo) / (hi - lo) * float32(steps))
	if k < 0 {
		return 0
	}
	if k >= steps {
		return steps - 1
	}
	return k
}

// uniformBest is the best single-colour cell for a fused colour, memoised on
// its Oklab bucket.
func (q *Quantizer) uniformBest(lab v3) *uniformBest {
	lq := bucket(lab[0], 0, 1, lSteps)
	aq := bucket(lab[1], -abRange, abRange, abSteps)
	bq := bucket(lab[2], -abRange, abRange, abSteps)
	u := &q.uniform[lq<<12|aq<<6|bq]
	if u.ok {
		return u
	}
	centre := v3{
		(float32(lq) + 0.5) / lSteps,
		-abRange + (float32(aq)+0.5)*(2*abRange/abSteps),
		-abRange + (float32(bq)+0.5)*(2*abRange/abSteps),
	}
	best := uniformBest{ch: Space, attr: 0, cost: math.MaxFloat32}
	// Solids: a space on a dark background, or a full block in any colour.
	for k := 0; k < 16; k++ {
		if c := samples * dist2(centre, pal.lab[k]); c < best.cost {
			s := Solid(byte(k))
			best = uniformBest{ch: s.Ch, attr: s.Attr, cost: c}
		}
	}
	for level, sh := range shades {
		texture := q.lambda * samples * pal.patternVar[level]
		for f := 0; f < 16; f++ {
			for b := 0; b < 8; b++ {
				if f == b {
					continue
				}
				c := samples*dist2(centre, pal.mix[level][f][b]) + texture*pal.pair[f][b]
				if c < best.cost {
					best = uniformBest{ch: sh.ch, attr: byte(f) | byte(b)<<4, cost: c}
				}
			}
		}
	}
	best.ok = true
	*u = best
	return u
}

// ansiDigit maps an IBM palette index (1=blue, 4=red) to the ANSI SGR colour
// digit (1=red, 4=blue).
var ansiDigit = [8]byte{0, 4, 2, 6, 1, 5, 3, 7}

// SGR renders an attribute as classic ANSI parameters for ESC [ ... m:
// "0;3x;4y" for a dark foreground, "1;3x;4y" for a bright one. It begins
// with a reset so bold never leaks between cells.
func SGR(attr byte) string {
	fg, bg := attr&15, attr>>4&7
	var b [8]byte
	b[0], b[1] = '0', ';'
	if fg >= 8 {
		b[0] = '1'
	}
	b[2], b[3], b[4] = '3', '0'+ansiDigit[fg&7], ';'
	b[5], b[6] = '4', '0'+ansiDigit[bg]
	return string(b[:7])
}
