package ui

import (
	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
	"github.com/hmderdoc/avatar_chat_universal/internal/theme"
)

// Modal boxes: every panel (/who, /channels, /settings, the colour grid,
// the /img viewer, the upload dialog) is framed with a single-line box in
// the theme's modal_frame colour, with the "[X]" dismiss button on the
// top edge at the left and the title after it. A left click on the [X]
// does what Esc does there. Contents stay inside the box: rows 1..H-2,
// columns 1..W-2.

// Columns of "[X]" on a box's top edge (x1 exclusive).
const (
	closeX0 = 1
	closeX1 = 4
)

// CP437 single-line box pieces; the UTF-8 charset maps them too.
const (
	boxH  = 0xC4 // ─
	boxV  = 0xB3 // │
	boxTL = 0xDA // ┌
	boxTR = 0xBF // ┐
	boxBL = 0xC0 // └
	boxBR = 0xD9 // ┘
)

// drawBox frames all of f, the edge in modal_frame's foreground over bg's
// background, and puts the [X] at the top left.
func drawBox(f *ansi.Frame, th *theme.Theme, bg ansi.Attr) {
	edge := ansi.FG(bg, th.ModalFrame)
	w, h := f.W, f.H
	for x := 1; x < w-1; x++ {
		f.SetCell(x, 0, boxH, edge)
		f.SetCell(x, h-1, boxH, edge)
	}
	for y := 1; y < h-1; y++ {
		f.SetCell(0, y, boxV, edge)
		f.SetCell(w-1, y, boxV, edge)
	}
	f.SetCell(0, 0, boxTL, edge)
	f.SetCell(w-1, 0, boxTR, edge)
	f.SetCell(0, h-1, boxBL, edge)
	f.SetCell(w-1, h-1, boxBR, edge)
	f.PrintAt(closeX0, 0, []byte("[X]"), th.CloseButton)
}

// boxTitle writes title on the top edge after the [X], clipped so the
// corner stays.
func boxTitle(f *ansi.Frame, title string, attr ansi.Attr) {
	f.PrintAt(closeX1+1, 0, []byte(truncate(" "+title+" ", f.W-closeX1-2)), attr)
}

// closeHit reports whether k is a left press on the [X] of a box drawn
// at screen column x, row y.
func closeHit(k ansi.Key, x, y int) bool {
	return k.Type == ansi.KeyMouse && !k.Release && k.Button == ansi.MouseLeft &&
		k.Y == y && k.X >= x+closeX0 && k.X < x+closeX1
}
