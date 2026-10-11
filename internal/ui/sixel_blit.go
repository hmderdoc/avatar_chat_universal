package ui

import (
	"fmt"
	"io"
	"strings"

	"github.com/hmderdoc/avatar_chat_universal/internal/inlineimg"
)

// EnableImages turns on inline pictures for image links. Call before Run.
func (a *App) EnableImages(cfg ImageConfig) {
	if cfg.Fetcher == nil {
		return
	}
	a.images = newImageManager(cfg)
	a.transcript.Images = a.images
}

// sixelRegions is the sixel pictures that may be drawn this frame: none
// while a screensaver, the TV lounge or a modal covers the transcript (the
// pixels would fight it; the cell version shows instead).
func (a *App) sixelRegions() []sixelRegion {
	if a.images == nil || !a.images.cfg.Sixel || a.idleAnim != nil || a.loungeActive() ||
		a.modal != nil || a.imageViewer != nil {
		return nil
	}
	return a.transcript.SixelRegions
}

func sixelSignature(regions []sixelRegion) string {
	if len(regions) == 0 {
		return ""
	}
	var b strings.Builder
	for _, r := range regions {
		fmt.Fprintf(&b, "%s@%d,%d;", r.URL, r.X, r.Y)
	}
	return b.String()
}

// blitSixels writes the sixel pictures over their cells. Each one is
// written when it is new or moved (forced: the transcript was repainted
// whole), or when this frame's cell output touched its box, which erased
// its pixels. Otherwise the terminal still shows it and it is left alone,
// so a static picture costs nothing per frame.
func (a *App) blitSixels(w io.Writer, forced bool) error {
	regions := a.sixelRegions()
	sig := sixelSignature(regions)
	changed := forced || sig != a.sixelSig
	a.sixelSig = sig
	if len(regions) == 0 {
		return nil
	}
	f := a.transcriptFrame
	for _, r := range regions {
		if !changed && !a.spansTouch(r) {
			continue
		}
		if _, err := fmt.Fprintf(w, "\x1b[%d;%dH", f.Y+r.Y+1, f.X+r.X+1); err != nil {
			return err
		}
		if _, err := w.Write(r.Pic.Sixel); err != nil {
			return err
		}
	}
	return nil
}

// spansTouch reports whether the compositor rewrote any cell of r's box in
// the frame it just rendered.
func (a *App) spansTouch(r sixelRegion) bool {
	for _, s := range a.transcriptComp.Spans {
		if s.Row >= r.Y && s.Row < r.Y+r.Pic.Rows && s.Last >= r.X && s.First < r.X+r.Pic.Cols {
			return true
		}
	}
	return false
}

// rasterItems is the downloaded pictures for the /img viewer.
func (a *App) rasterItems() []*imageEntry {
	if a.images == nil {
		return nil
	}
	return a.images.ready()
}

// zoomPicture lays a picture out for the full-screen viewer.
func (a *App) zoomPicture(e *imageEntry, cols, rows int) *inlineimg.Picture {
	return a.images.layout(e, cols, rows, a.tvColorTrue(), a.images.cfg.Sixel)
}
