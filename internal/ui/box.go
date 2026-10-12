package ui

import (
	"strings"

	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
	"github.com/hmderdoc/avatar_chat_universal/internal/theme"
)

// Modal boxes: every panel (/who, /channels, /help, /settings, the colour
// grid, the /img viewer, the upload dialog) is framed with a single-line
// box in the theme's modal_frame colour, with the "[X]" dismiss button on
// the top edge at the left and the title after it. A left click on the
// [X] does what Esc does there. Contents stay inside the box: rows
// 1..H-2, columns 1..W-2.

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

// Key lines: the action names on a panel's key line ("M message", "Esc
// close") are clickable too. drawHints prints the line and records where
// each named action landed, like the bar's buttons; a click on one does its
// key.

// hint is one action on a key line: its text as printed and its key.
type hint struct {
	label string
	key   ansi.Key
}

// hintSpan is where a hint landed: frame columns x0..x1-1 of row y.
type hintSpan struct {
	x0, x1, y int
	key       ansi.Key
}

type hintLine []hintSpan

func charKey(r rune) ansi.Key { return ansi.Key{Type: ansi.KeyChar, Rune: r} }

var (
	escKey   = ansi.Key{Type: ansi.KeyEsc}
	enterKey = ansi.Key{Type: ansi.KeyEnter}
)

// drawHints prints text (already clipped) at (x, y) of f in attr and
// returns the spans of the hints found in it; a hint cut off by the clip
// isn't clickable.
func drawHints(f *ansi.Frame, x, y int, text string, attr ansi.Attr, hints ...hint) hintLine {
	putStr(f, x, y, text, attr)
	var out hintLine
	for _, h := range hints {
		if i := strings.Index(text, h.label); i >= 0 {
			out = append(out, hintSpan{x0: x + i, x1: x + i + len(h.label), y: y, key: h.key})
		}
	}
	return out
}

// hit maps a left press on a frame drawn at screen column fx, row fy to
// the key of the hint under it.
func (l hintLine) hit(k ansi.Key, fx, fy int) (ansi.Key, bool) {
	if k.Type != ansi.KeyMouse || k.Release || k.Button != ansi.MouseLeft {
		return ansi.Key{}, false
	}
	for _, s := range l {
		if k.Y == fy+s.y && k.X >= fx+s.x0 && k.X < fx+s.x1 {
			return s.key, true
		}
	}
	return ansi.Key{}, false
}
