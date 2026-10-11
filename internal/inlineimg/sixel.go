package inlineimg

import (
	"bytes"
	"image"
	"image/color/palette"
	"image/draw"
	"strconv"
)

// EncodeSixel writes img as a DEC sixel image (DCS q ... ST). Colours are
// reduced to the 256-colour Plan 9 palette with Floyd-Steinberg error
// diffusion, which is plenty for chat pictures and needs no per-image
// palette search. Every pixel is painted, so the result is opaque.
//
// Pure Go on purpose: the shell needs img2sixel on the host, the door does
// not, which matters to sysops on Windows and on minimal Mystic boxes.
func EncodeSixel(img image.Image) []byte {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return nil
	}
	pal := image.NewPaletted(image.Rect(0, 0, w, h), palette.Plan9)
	draw.FloydSteinberg.Draw(pal, pal.Bounds(), img, b.Min)

	var out bytes.Buffer
	out.Grow(w * h / 3)
	// P2=1: pixels we don't set stay as they were (none are left unset).
	// Raster attributes: 1:1 aspect, w x h.
	out.WriteString("\x1bP0;1;0q\"1;1;")
	out.WriteString(strconv.Itoa(w))
	out.WriteByte(';')
	out.WriteString(strconv.Itoa(h))

	used := make([]bool, len(pal.Palette))
	for _, ix := range pal.Pix {
		used[ix] = true
	}
	for i, c := range pal.Palette {
		if !used[i] {
			continue
		}
		r, g, bb, _ := c.RGBA()
		// Sixel colour components are percentages.
		out.WriteByte('#')
		out.WriteString(strconv.Itoa(i))
		out.WriteString(";2;")
		out.WriteString(strconv.Itoa(int(r * 100 / 0xffff)))
		out.WriteByte(';')
		out.WriteString(strconv.Itoa(int(g * 100 / 0xffff)))
		out.WriteByte(';')
		out.WriteString(strconv.Itoa(int(bb * 100 / 0xffff)))
	}

	row := make([]byte, w)
	present := make([]bool, len(pal.Palette))
	for y0 := 0; y0 < h; y0 += 6 {
		for i := range present {
			present[i] = false
		}
		for y := y0; y < y0+6 && y < h; y++ {
			for _, ix := range pal.Pix[y*pal.Stride : y*pal.Stride+w] {
				present[ix] = true
			}
		}
		first := true
		for ci := range present {
			if !present[ci] {
				continue
			}
			for x := 0; x < w; x++ {
				var bits byte
				for k := 0; k < 6 && y0+k < h; k++ {
					if pal.Pix[(y0+k)*pal.Stride+x] == uint8(ci) {
						bits |= 1 << k
					}
				}
				row[x] = '?' + bits
			}
			if !first {
				out.WriteByte('$') // back to the start of this band
			}
			first = false
			out.WriteByte('#')
			out.WriteString(strconv.Itoa(ci))
			writeRLE(&out, row)
		}
		if y0+6 < h {
			out.WriteByte('-') // next band
		}
	}
	out.WriteString("\x1b\\")
	return out.Bytes()
}

// writeRLE writes sixel characters with !count repeats for runs over 3.
// Trailing empty sixels ('?') are dropped: they paint nothing.
func writeRLE(out *bytes.Buffer, row []byte) {
	end := len(row)
	for end > 0 && row[end-1] == '?' {
		end--
	}
	for i := 0; i < end; {
		j := i + 1
		for j < end && row[j] == row[i] {
			j++
		}
		n := j - i
		if n > 3 {
			out.WriteByte('!')
			out.WriteString(strconv.Itoa(n))
			out.WriteByte(row[i])
		} else {
			for k := 0; k < n; k++ {
				out.WriteByte(row[i])
			}
		}
		i = j
	}
}
