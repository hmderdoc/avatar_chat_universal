package idle

import (
	"io/ioutil"
	"math/rand"

	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
)

// AnsiGallery is a background animation that scrolls through SAUCE-tagged
// ANSI / BIN artwork loaded from a sysop-managed directory. Each picked
// file is parsed via ansi.LoadFile (so SGR colors and cursor positioning
// are honored) and scrolled vertically through the transcript area.
//
// Sizing rules:
//   - If the art is wider than the display, only the leftmost dispW
//     columns are shown -- alignment of the rest of the artwork is
//     preserved (no centering with negative offset, no reflow).
//   - If the art fits horizontally, it's centered.
//   - Vertical scrolling: art starts off-bottom, rises into view, exits
//     off-top, then a new file is loaded.
//
// Files outside the configured directory or that fail to parse are
// silently skipped. An empty file list renders as a black background
// (the user just sees nothing rather than getting an error).
type AnsiGallery struct {
	w, h          int
	files         []string
	rng           *rand.Rand
	art           *ansi.Frame
	scrollY       float64
	scrollPerTick float64

	// oneShot signals "play one file then stop." When set, Done() returns
	// true after the first file has scrolled fully off the top, and the
	// App's idle loop is expected to rotate to the next animation. Used
	// when the gallery is injected as a transition between regular idle
	// animations (idle_interleave_ansi).
	oneShot   bool
	plays     int
	done      bool
}

// NewAnsiGallery constructs a multi-file gallery: when one piece of art
// scrolls off the top, the next is loaded. Used when "ansi_gallery" is
// placed directly in idle_sequence.
func NewAnsiGallery(w, h int, files []string) *AnsiGallery {
	return newAnsiGallery(w, h, files, false)
}

// NewAnsiGalleryOneShot constructs a single-piece gallery: after the
// chosen file scrolls off the top, Done() returns true. Used by the
// idle-interleave path that inserts a single piece of art between every
// procedural animation.
func NewAnsiGalleryOneShot(w, h int, files []string) *AnsiGallery {
	return newAnsiGallery(w, h, files, true)
}

func newAnsiGallery(w, h int, files []string, oneShot bool) *AnsiGallery {
	g := &AnsiGallery{
		w:             w,
		h:             h,
		files:         files,
		rng:           rngFor(),
		scrollPerTick: 0.25, // ~3 rows/sec at 12 fps -- comfortable reading speed
		oneShot:       oneShot,
	}
	g.loadNext()
	return g
}

// Done reports whether the animation should yield to the next idle anim
// (only meaningful in one-shot mode). Procedural animations don't end on
// their own, so the App rotates them on the IdleSwitch timer; one-shot
// galleries finish naturally and Done() lets the App rotate early.
func (g *AnsiGallery) Done() bool { return g.done }

func (g *AnsiGallery) Name() string         { return "ansi_gallery" }
func (g *AnsiGallery) Category() Category   { return Background }
func (g *AnsiGallery) PreferredFPS() int    { return 12 }

func (g *AnsiGallery) loadNext() {
	if g.oneShot && g.plays > 0 {
		// Already played our one piece; mark done so the App rotates to
		// the next animation on its next idle tick.
		g.art = nil
		g.done = true
		return
	}
	g.art = nil
	if len(g.files) == 0 {
		return
	}
	idx := g.rng.Intn(len(g.files))
	data, err := ioutil.ReadFile(g.files[idx])
	if err != nil {
		return
	}
	f, err := ansi.LoadFile(g.files[idx], data)
	if err != nil || f == nil {
		return
	}
	g.art = f
	g.scrollY = -float64(g.h) // start fully below the visible window
	g.plays++
}

func (g *AnsiGallery) Tick(f *ansi.Frame) {
	f.Clear()
	if g.art == nil {
		// Try again on the next tick in case the file list changed (no-op
		// if it's still empty).
		g.loadNext()
		return
	}

	// Horizontal placement: center if the art fits, otherwise clip the
	// right edge so the leftmost dispW columns render at the left margin
	// of the display (alignment of the rest of the art is preserved).
	var srcXStart, dstXStart, copyW int
	if g.art.W <= f.W {
		dstXStart = (f.W - g.art.W) / 2
		copyW = g.art.W
	} else {
		dstXStart = 0
		copyW = f.W
	}
	if copyW > f.W-dstXStart {
		copyW = f.W - dstXStart
	}

	// Vertical scroll position: each display row maps to source row
	// (dispRow + yOff). Rows that fall outside the art are simply skipped.
	yOff := int(g.scrollY)
	for dispRow := 0; dispRow < f.H; dispRow++ {
		srcRow := dispRow + yOff
		if srcRow < 0 || srcRow >= g.art.H {
			continue
		}
		for col := 0; col < copyW; col++ {
			cell := g.art.CellAt(srcXStart+col, srcRow)
			if cell.Char == 0 {
				continue
			}
			f.SetCell(dstXStart+col, dispRow, cell.Char, cell.Attr)
		}
	}

	// Advance scroll. When the art has fully exited the top, swap files.
	g.scrollY += g.scrollPerTick
	if int(g.scrollY) > g.art.H {
		g.loadNext()
	}
}
