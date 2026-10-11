package inlineimg

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	_ "image/gif" // first frame
	_ "image/jpeg"
	_ "image/png"

	"github.com/hmderdoc/shadecell"
	_ "golang.org/x/image/bmp"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"

	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
)

// maxPixels refuses images whose header promises more pixels than this
// (40 megapixels), before any of them are decoded: a 5 MB PNG can claim to
// be gigapixels.
const maxPixels = 40_000_000

// Decode decodes a PNG, JPEG, GIF (first frame), WebP or BMP.
func Decode(b []byte) (image.Image, error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("inlineimg: not an image we can read: %v", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > maxPixels {
		return nil, fmt.Errorf("inlineimg: %s is %dx%d, too large", format, cfg.Width, cfg.Height)
	}
	img, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("inlineimg: decode %s: %v", format, err)
	}
	return img, nil
}

// Picture is an image laid out for a box of text cells. Cells is always
// filled (row-major, Cols x Rows) with the half-block version — 24-bit
// when Truecolor, otherwise 16-colour shade cells — so the picture is
// ordinary frame content. Sixel, when set, is a DEC sixel image covering
// exactly the same cells, for the caller to write over them.
type Picture struct {
	Cols, Rows int
	Cells      []ansi.Cell
	Truecolor  bool
	Sixel      []byte
}

// Options says how to render.
type Options struct {
	MaxCols, MaxRows int
	// Truecolor picks 24-bit half blocks for the cells; otherwise each cell
	// is shadecell's best 16-colour glyph for its two pixels.
	Truecolor bool
	// Sixel also encodes a DEC sixel image for the same box, at CellW x
	// CellH pixels per cell (0 = assume 8x16).
	Sixel        bool
	CellW, CellH int
}

// Render scales img to fit opts' box, keeping its aspect ratio. A text
// cell is taken to be twice as tall as it is wide, so each cell shows two
// square pixels stacked (the half-block convention).
func Render(img image.Image, q *shadecell.Quantizer, opts Options) *Picture {
	b := img.Bounds()
	if b.Dx() <= 0 || b.Dy() <= 0 || opts.MaxCols <= 0 || opts.MaxRows <= 0 {
		return nil
	}
	cols, rows := fitCells(b.Dx(), b.Dy(), opts.MaxCols, opts.MaxRows)
	px := scale(img, cols, rows*2)
	pic := &Picture{Cols: cols, Rows: rows, Cells: make([]ansi.Cell, cols*rows), Truecolor: opts.Truecolor}
	for y := 0; y < rows; y++ {
		for x := 0; x < cols; x++ {
			top := px.NRGBAAt(x, 2*y)
			bot := px.NRGBAAt(x, 2*y+1)
			c := &pic.Cells[y*cols+x]
			if opts.Truecolor {
				*c = ansi.Cell{Char: 0xDF, True: true,
					Fg: ansi.RGB{R: top.R, G: top.G, B: top.B},
					Bg: ansi.RGB{R: bot.R, G: bot.G, B: bot.B}}
				continue
			}
			sc := q.Pair(shadecell.RGB{R: top.R, G: top.G, B: top.B}, shadecell.RGB{R: bot.R, G: bot.G, B: bot.B})
			*c = ansi.Cell{Char: sc.Ch, Attr: ansi.Attr(sc.Attr)}
		}
	}
	if opts.Sixel {
		cw, ch := opts.CellW, opts.CellH
		if cw <= 0 || ch <= 0 {
			cw, ch = 8, 16
		}
		// Sixel paints in bands of 6 pixel rows; round the height DOWN to
		// a whole band so the image never spills into the row below.
		w := cols * cw
		h := rows * ch / 6 * 6
		if w > 0 && h > 0 {
			pic.Sixel = EncodeSixel(scale(img, w, h))
		}
	}
	return pic
}

// fitCells picks the largest cols x rows box inside maxCols x maxRows with
// the image's aspect ratio (each cell = 1 x 2 square pixels).
// Small images are not blown up past one image pixel per cell pixel.
func fitCells(w, h, maxCols, maxRows int) (int, int) {
	if maxCols > w {
		maxCols = w
	}
	if limit := (h + 1) / 2; maxRows > limit {
		maxRows = limit
	}
	cols := maxCols
	rows := (h*cols + w) / (2 * w) // round
	if rows > maxRows {
		rows = maxRows
		cols = (2*w*rows + h/2) / h
	}
	if cols < 1 {
		cols = 1
	}
	if cols > maxCols {
		cols = maxCols
	}
	if rows < 1 {
		rows = 1
	}
	return cols, rows
}

// scale resamples img to w x h over black (transparent areas show as the
// terminal's black, not as garbage colour).
func scale(img image.Image, w, h int) *image.NRGBA {
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Bounds(), &image.Uniform{C: color.Black}, image.Point{}, draw.Src)
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, img.Bounds(), draw.Over, nil)
	return dst
}
