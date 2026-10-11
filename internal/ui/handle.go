package ui

import (
	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
	"github.com/hmderdoc/avatar_chat_universal/internal/nickstyle"
	"github.com/hmderdoc/shadecell"
)

// handleQ maps handle-style colours to the 16-colour palette perceptually
// (Oklab, github.com/hmderdoc/shadecell), so a pastel stays a pastel instead
// of snapping by plain RGB distance. Painting runs on the UI goroutine only.
var handleQ = shadecell.New()

// cgaRGBOf is the RGB of a CGA palette index, as the web and shell show it.
func cgaRGBOf(idx ansi.Attr) ansi.RGB {
	r, g, b, _ := nickstyle.RGB(nickstyle.CGA[idx&0x0F])
	return ansi.RGB{R: r, G: g, B: b}
}

// handleWidth is how many cells paintHandle uses for name + style.
func handleWidth(name string, style *nickstyle.Style) int {
	return len(name)
}

// paintHandle draws name at (x,y) in its owner's handle style, clipped to
// maxW cells, and returns the cells used. Unstyled characters use base.
// With truecolor the stored hex is painted as is over base's background;
// otherwise each colour goes to its nearest of the 16, and a colour that
// would vanish into the background is lifted (black to dark gray, the way
// chat_style_lib.js pipeName does it). A tag in the
// style is never drawn.
func paintHandle(f *ansi.Frame, x, y int, name string, style *nickstyle.Style, base ansi.Attr, maxW int, truecolor bool) int {
	if maxW <= 0 {
		return 0
	}
	if style.Wire() == nil {
		if len(name) > maxW {
			name = name[:maxW]
		}
		f.PrintAt(x, y, []byte(name), base)
		return len(name)
	}
	bgIdx := (base >> 4) & 7
	bgRGB := cgaRGBOf(bgIdx)
	colors := style.Align(name)
	n := 0
	for i := 0; i < len(name) && n < maxW; i++ {
		paintStyledCell(f, x+n, y, name[i], colors[i], "", base, bgRGB, truecolor)
		n++
	}
	return n
}

func paintStyledCell(f *ansi.Frame, x, y int, ch byte, fgHex, bgHex string, base ansi.Attr, baseBg ansi.RGB, truecolor bool) {
	fr, fg, fb, okFg := nickstyle.RGB(fgHex)
	br, bg, bb, okBg := nickstyle.RGB(bgHex)
	if !okFg && !okBg {
		f.SetCell(x, y, ch, base)
		return
	}
	if truecolor {
		fgc := cgaRGBOf(base & 0x0F)
		if okFg {
			fgc = ansi.RGB{R: fr, G: fg, B: fb}
		}
		bgc := baseBg
		if okBg {
			bgc = ansi.RGB{R: br, G: bg, B: bb}
		}
		f.SetCellTrue(x, y, ch, fgc, bgc)
		return
	}
	attr := base
	if okBg {
		attr = ansi.BG(attr, ansi.Attr(handleQ.Nearest8(shadecell.RGB{R: br, G: bg, B: bb})))
	}
	if okFg {
		attr = ansi.FG(attr, ansi.Attr(handleQ.Nearest16(shadecell.RGB{R: fr, G: fg, B: fb})))
	}
	if attr&0x0F == (attr>>4)&7 {
		// Same colour as the background: lift so the letter still shows.
		if attr&0x0F == 0 {
			attr = ansi.FG(attr, ansi.DarkGray)
		} else {
			attr = ansi.FG(attr, (attr&0x07)|0x08)
		}
	}
	f.SetCell(x, y, ch, attr)
}

func shadecellRGB(r, g, b uint8) shadecell.RGB { return shadecell.RGB{R: r, G: g, B: b} }
