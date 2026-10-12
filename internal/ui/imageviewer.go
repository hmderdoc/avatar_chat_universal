package ui

import (
	"bytes"
	"fmt"
	"io"

	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
	"github.com/hmderdoc/avatar_chat_universal/internal/chat"
	"github.com/hmderdoc/avatar_chat_universal/internal/inlineimg"
	"github.com/hmderdoc/avatar_chat_universal/internal/theme"
)

// ImageViewer is a full-screen modal for viewing received bitmap messages.
// Unlike the roster modal it bypasses the Frame system and writes directly
// to conn, because image cells use 256-color (bytes 0-255) values that
// don't fit ansi.Attr's CGA palette.
//
// After the bitmaps it also pages through the pictures downloaded from
// image links (rasters), each laid out to fill the screen: sixel when the
// terminal draws it, otherwise 24-bit or 16-colour shade cells.
type ImageViewer struct {
	images  []*chat.BitmapEntry
	rasters []*imageEntry
	// zoom lays a raster out for a cols x rows box.
	zoom    func(e *imageEntry, cols, rows int) *inlineimg.Picture
	Charset ansi.Charset
	Theme   *theme.Theme
	index   int
	scrollY int
	width   int
	height  int

	closed bool
	hints  hintLine // "Esc close" on the footer, for clicks
}

// NewImageViewer builds a viewer for the supplied list of images. The
// initial selection is the most recent (last in the list); navigation
// keys cycle.
func NewImageViewer(images []*chat.BitmapEntry, width, height int) *ImageViewer {
	v := &ImageViewer{
		images: images,
		width:  width,
		height: height,
	}
	if len(images) > 0 {
		v.index = len(images) - 1
	}
	return v
}

// WithRasters adds downloaded pictures after the bitmaps and opens on the
// newest item.
func (v *ImageViewer) WithRasters(rasters []*imageEntry, zoom func(e *imageEntry, cols, rows int) *inlineimg.Picture) *ImageViewer {
	v.rasters = rasters
	v.zoom = zoom
	if n := v.count(); n > 0 {
		v.index = n - 1
	}
	return v
}

func (v *ImageViewer) count() int { return len(v.images) + len(v.rasters) }

// HandleKey routes navigation. Returns true when the modal should close.
func (v *ImageViewer) HandleKey(k ansi.Key) bool {
	switch k.Type {
	case ansi.KeyEsc:
		v.closed = true
		return true
	case ansi.KeyLeft, ansi.KeyPgUp:
		if v.index > 0 {
			v.index--
			v.scrollY = 0
		}
	case ansi.KeyRight, ansi.KeyPgDn:
		if v.index < v.count()-1 {
			v.index++
			v.scrollY = 0
		}
	case ansi.KeyHome:
		v.index = 0
		v.scrollY = 0
	case ansi.KeyEnd:
		v.index = v.count() - 1
		v.scrollY = 0
	case ansi.KeyUp:
		if v.scrollY > 0 {
			v.scrollY--
		}
	case ansi.KeyDown:
		v.scrollY++
		// Clamp at render time.
	case ansi.KeyChar:
		switch k.Rune {
		case 'q', 'Q':
			v.closed = true
			return true
		case 'n', 'N', ' ':
			if v.index < v.count()-1 {
				v.index++
				v.scrollY = 0
			}
		case 'p', 'P':
			if v.index > 0 {
				v.index--
				v.scrollY = 0
			}
		}
	}
	return false
}

// Closed reports whether the user dismissed the viewer.
func (v *ImageViewer) Closed() bool { return v.closed }

// CurrentIndex returns the currently-displayed image's queue index, used
// by the App to mark it viewed in the session.
func (v *ImageViewer) CurrentIndex() int { return v.index }

// ShowRaster jumps to the downloaded picture from url, if it's listed.
func (v *ImageViewer) ShowRaster(url string) {
	for i, e := range v.rasters {
		if e.url == url {
			v.index = len(v.images) + i
			v.scrollY = 0
			return
		}
	}
}

// Render writes the entire modal to w. Cells are emitted as ANSI
// 256-color SGR sequences so the original bitmap colors come through on
// any terminal that supports them (SyncTERM does, modern xterms do).
func (v *ImageViewer) Render(w io.Writer) error {
	if v.index >= len(v.images) && v.index < v.count() {
		return v.renderRaster(w, v.rasters[v.index-len(v.images)])
	}
	var buf bytes.Buffer
	buf.WriteString("\x1b[2J\x1b[H")
	th := orDefault(v.Theme)
	if v.count() == 0 {
		f := v.boxed("No images yet", "")
		f.PrintAt(2, 2, []byte(truncate("Type /img after someone shares a bitmap.", v.width-4)), th.ModalBg)
		v.hints = drawHints(f, 2, 4, "Esc to close.", th.ModalBg, hint{"Esc to close", escKey})
		if err := f.Render(&buf); err != nil {
			return err
		}
		_, err := w.Write(buf.Bytes())
		return err
	}

	entry := v.images[v.index]
	img := entry.Image
	imgH := img.Height

	// Clamp scroll to a valid range based on visible window.
	visibleH := v.height - 3 // 1 row header, 1 row footer, 1 row reserve
	if visibleH < 1 {
		visibleH = 1
	}
	maxScroll := imgH - visibleH
	if maxScroll < 0 {
		maxScroll = 0
	}
	if v.scrollY > maxScroll {
		v.scrollY = maxScroll
	}
	if v.scrollY < 0 {
		v.scrollY = 0
	}

	// Image body, inside the box.
	visibleW := v.width - 2
	if visibleW > img.Width {
		visibleW = img.Width
	}
	for y := 0; y < visibleH; y++ {
		srcY := y + v.scrollY
		if srcY >= imgH {
			break
		}
		fmt.Fprintf(&buf, "\x1b[%d;2H", 2+y)
		var prevFg, prevBg int = -1, -1
		row := img.Cells[srcY]
		for x := 0; x < visibleW; x++ {
			cell := row[x]
			if int(cell.FG) != prevFg {
				fmt.Fprintf(&buf, "\x1b[38;5;%dm", cell.FG)
				prevFg = int(cell.FG)
			}
			if int(cell.BG) != prevBg {
				fmt.Fprintf(&buf, "\x1b[48;5;%dm", cell.BG)
				prevBg = int(cell.BG)
			}
			ch := cell.Char
			if ch == 0 {
				ch = ' '
			}
			buf.WriteByte(ch)
		}
		buf.WriteString("\x1b[0m")
	}

	// The box, its header and footer: only the edges, since the picture's
	// 256-colour cells aren't Frame cells.
	header := fmt.Sprintf("Image %d/%d | %s | %dx%d | row %d-%d",
		v.index+1, v.count(), entry.Sender, img.Width, img.Height,
		v.scrollY+1, minInt(v.scrollY+visibleH, imgH))
	footer := " Esc close   <-/-> prev/next   Up/Down scroll   Home/End first/last "
	if err := renderEdges(&buf, v.boxed(header, footer)); err != nil {
		return err
	}

	_, err := w.Write(buf.Bytes())
	return err
}

// boxed is a full-screen frame with the box drawn, header on its top
// edge after the [X] and footer on its bottom edge.
func (v *ImageViewer) boxed(header, footer string) *ansi.Frame {
	th := orDefault(v.Theme)
	f := ansi.NewFrame(0, 0, v.width, v.height, th.ModalBg)
	f.Charset = v.Charset
	drawBox(f, th, th.ModalBg)
	boxTitle(f, header, th.ViewerBar)
	v.hints = nil
	if footer != "" {
		v.hints = drawHints(f, 2, v.height-1, truncate(footer, v.width-4), th.ViewerBar, hint{"Esc close", escKey})
	}
	return f
}

// renderEdges writes only f's outer rows and columns to buf.
func renderEdges(buf *bytes.Buffer, f *ansi.Frame) error {
	strips := []*ansi.Frame{
		ansi.NewFrame(f.X, f.Y, f.W, 1, 0),
		ansi.NewFrame(f.X, f.Y+f.H-1, f.W, 1, 0),
		ansi.NewFrame(f.X, f.Y+1, 1, f.H-2, 0),
		ansi.NewFrame(f.X+f.W-1, f.Y+1, 1, f.H-2, 0),
	}
	for _, s := range strips {
		s.Charset = f.Charset
		for y := 0; y < s.H; y++ {
			for x := 0; x < s.W; x++ {
				c := f.CellAt(s.X-f.X+x, s.Y-f.Y+y)
				s.SetCell(x, y, c.Char, c.Attr)
			}
		}
		if err := s.Render(buf); err != nil {
			return err
		}
	}
	return nil
}

// renderRaster paints one downloaded picture full screen.
func (v *ImageViewer) renderRaster(w io.Writer, e *imageEntry) error {
	bodyH := v.height - 2
	if bodyH < 1 {
		bodyH = 1
	}
	pic := v.zoom(e, v.width-2, bodyH)
	b := e.img.Bounds()
	header := fmt.Sprintf("Image %d/%d | %s | %dx%d | %s", v.index+1, v.count(), e.from, b.Dx(), b.Dy(), e.url)
	f := v.boxed(header, " Esc close   <-/-> prev/next   Home/End first/last ")
	x0 := 1
	if pic != nil {
		x0 = 1 + (v.width-2-pic.Cols)/2
		for r := 0; r < pic.Rows; r++ {
			for c := 0; c < pic.Cols; c++ {
				cell := pic.Cells[r*pic.Cols+c]
				if cell.True {
					f.SetCellTrue(x0+c, 1+r, cell.Char, cell.Fg, cell.Bg)
				} else {
					f.SetCell(x0+c, 1+r, cell.Char, cell.Attr)
				}
			}
		}
	}
	var buf bytes.Buffer
	buf.WriteString("\x1b[2J")
	if err := f.Render(&buf); err != nil {
		return err
	}
	if pic != nil && pic.Sixel != nil {
		fmt.Fprintf(&buf, "\x1b[2;%dH", x0+1)
		buf.Write(pic.Sixel)
	}
	_, err := w.Write(buf.Bytes())
	return err
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
