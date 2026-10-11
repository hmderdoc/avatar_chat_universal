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

// Render writes the entire modal to w. Cells are emitted as ANSI
// 256-color SGR sequences so the original bitmap colors come through on
// any terminal that supports them (SyncTERM does, modern xterms do).
func (v *ImageViewer) Render(w io.Writer) error {
	if v.index >= len(v.images) && v.index < v.count() {
		return v.renderRaster(w, v.rasters[v.index-len(v.images)])
	}
	var buf bytes.Buffer
	buf.WriteString("\x1b[2J\x1b[H")
	if v.count() == 0 {
		buf.WriteString("\x1b[1;1H")
		buf.WriteString("\x1b[1;33;44m No images yet \x1b[0m")
		buf.WriteString("\x1b[3;1HType /img after someone shares a bitmap.")
		buf.WriteString("\x1b[5;1HEsc to close.")
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

	// Header.
	header := fmt.Sprintf(" Image %d/%d | %s | %dx%d | row %d-%d ",
		v.index+1, v.count(), entry.Sender, img.Width, img.Height,
		v.scrollY+1, minInt(v.scrollY+visibleH, imgH))
	buf.WriteString("\x1b[1;1H\x1b[1;37;44m")
	buf.WriteString(padOrTrim(header, v.width))
	buf.WriteString("\x1b[0m")

	// Image body.
	visibleW := v.width
	if visibleW > img.Width {
		visibleW = img.Width
	}
	for y := 0; y < visibleH; y++ {
		srcY := y + v.scrollY
		if srcY >= imgH {
			break
		}
		fmt.Fprintf(&buf, "\x1b[%d;1H", 2+y)
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

	// Footer.
	footer := " Esc close   <-/-> prev/next   Up/Down scroll   Home/End first/last "
	fmt.Fprintf(&buf, "\x1b[%d;1H\x1b[1;37;44m", v.height)
	buf.WriteString(padOrTrim(footer, v.width))
	buf.WriteString("\x1b[0m")

	_, err := w.Write(buf.Bytes())
	return err
}

// renderRaster paints one downloaded picture full screen.
func (v *ImageViewer) renderRaster(w io.Writer, e *imageEntry) error {
	th := orDefault(v.Theme)
	f := ansi.NewFrame(0, 0, v.width, v.height, th.ModalBg)
	f.Charset = v.Charset
	bodyH := v.height - 2
	if bodyH < 1 {
		bodyH = 1
	}
	pic := v.zoom(e, v.width, bodyH)
	b := e.img.Bounds()
	header := fmt.Sprintf(" Image %d/%d | %s | %dx%d | %s ", v.index+1, v.count(), e.from, b.Dx(), b.Dy(), e.url)
	f.PrintAt(0, 0, []byte(padOrTrim(header, v.width)), th.ViewerBar)
	x0 := 0
	if pic != nil {
		x0 = (v.width - pic.Cols) / 2
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
	footer := " Esc close   <-/-> prev/next   Home/End first/last "
	f.PrintAt(0, v.height-1, []byte(padOrTrim(footer, v.width)), th.ViewerBar)
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

func padOrTrim(s string, w int) string {
	if len(s) >= w {
		return s[:w]
	}
	pad := w - len(s)
	out := make([]byte, len(s)+pad)
	copy(out, s)
	for i := len(s); i < len(out); i++ {
		out[i] = ' '
	}
	return string(out)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
