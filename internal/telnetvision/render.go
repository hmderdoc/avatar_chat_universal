package telnetvision

import "github.com/hmderdoc/avatar_chat_universal/internal/ansi"

// halfBlock is the upper-half-block glyph (CP437 0xDF / U+2580): the cell's
// foreground paints the top pixel, the background paints the bottom pixel, so
// one character row carries two pixel rows.
const halfBlock = 0xDF

// RenderOpts controls how a frame is painted into a target region.
type RenderOpts struct {
	Truecolor  bool    // true: 24-bit cells; false: CGA-16 (quantized + dithered)
	Saturation float64 // CGA only: saturation boost before quantizing (e.g. 1.8)
	Dither     bool    // CGA only: ordered (Bayer) dithering
}

// RenderTo paints the frame into dst's region [ox,ox+w) x [oy,oy+h), scaling
// the source to fit with nearest-neighbor sampling. Each target cell becomes a
// half-block (top pixel = fg, bottom = bg). Truecolor sets RGB cells; CGA mode
// quantizes to the 16-color palette (fg 0-15, bg restricted to 0-7, as BBS
// half-blocks require).
func (fr *Frame) RenderTo(dst *ansi.Frame, ox, oy, w, h int, opts RenderOpts) {
	if fr == nil || dst == nil || w <= 0 || h <= 0 || fr.Cols <= 0 || fr.Rows <= 0 {
		return
	}
	sat := opts.Saturation
	if sat == 0 {
		sat = 1.0
	}
	for ty := 0; ty < h; ty++ {
		sy := ty * fr.Rows / h
		for tx := 0; tx < w; tx++ {
			sx := tx * fr.Cols / w
			tp := ((2*sy)*fr.Cols + sx) * 3
			bp := ((2*sy+1)*fr.Cols + sx) * 3
			if bp+2 >= len(fr.Pixels) {
				continue
			}
			tr, tg, tb := int(fr.Pixels[tp]), int(fr.Pixels[tp+1]), int(fr.Pixels[tp+2])
			br, bg, bb := int(fr.Pixels[bp]), int(fr.Pixels[bp+1]), int(fr.Pixels[bp+2])
			if opts.Truecolor {
				dst.SetCellTrue(ox+tx, oy+ty, halfBlock,
					ansi.RGB{R: uint8(tr), G: uint8(tg), B: uint8(tb)},
					ansi.RGB{R: uint8(br), G: uint8(bg), B: uint8(bb)})
			} else {
				top := quantCGA(tr, tg, tb, tx, 2*ty, sat, opts.Dither)
				bot := quantCGA(br, bg, bb, tx, 2*ty+1, sat, opts.Dither)
				// fg = top (0-15), bg = bottom limited to 0-7 (no iCE bg).
				attr := ansi.Attr(top&0x0f) | (ansi.Attr(bot&0x07) << 4)
				dst.SetCell(ox+tx, oy+ty, halfBlock, attr)
			}
		}
	}
}

// --- CGA quantization (ported from telnetvision/door/main.go) ----------------

// cgaRGB is the 16-color CGA/VGA palette in IBM index order (1=blue, 4=red).
var cgaRGB = [16][3]int{
	{0, 0, 0}, {0, 0, 170}, {0, 170, 0}, {0, 170, 170},
	{170, 0, 0}, {170, 0, 170}, {170, 85, 0}, {170, 170, 170},
	{85, 85, 85}, {85, 85, 255}, {85, 255, 85}, {85, 255, 255},
	{255, 85, 85}, {255, 85, 255}, {255, 255, 85}, {255, 255, 255},
}

// bayer4 is a 4x4 ordered-dither matrix; stable frame-to-frame (no shimmer).
var bayer4 = [4][4]float64{{0, 8, 2, 10}, {12, 4, 14, 6}, {3, 11, 1, 9}, {15, 7, 13, 5}}

func clampB(v float64) int {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return int(v)
}

// saturate boosts saturation while preserving luma (cheap, no HSV round-trip).
func saturate(r, g, b int, f float64) (int, int, int) {
	l := 0.299*float64(r) + 0.587*float64(g) + 0.114*float64(b)
	return clampB(l + (float64(r)-l)*f), clampB(l + (float64(g)-l)*f), clampB(l + (float64(b)-l)*f)
}

func nearestCGA(r, g, b int) int {
	best, bi := 1<<30, 0
	for i := 0; i < 16; i++ {
		dr, dg, db := r-cgaRGB[i][0], g-cgaRGB[i][1], b-cgaRGB[i][2]
		if d := dr*dr + dg*dg + db*db; d < best {
			best, bi = d, i
		}
	}
	return bi
}

func quantCGA(r, g, b, px, py int, sat float64, dither bool) int {
	if sat != 1.0 {
		r, g, b = saturate(r, g, b, sat)
	}
	if dither {
		off := ((bayer4[py&3][px&3]+0.5)/16.0 - 0.5) * 96.0
		r, g, b = clampB(float64(r)+off), clampB(float64(g)+off), clampB(float64(b)+off)
	}
	return nearestCGA(r, g, b)
}
