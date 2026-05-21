package media

import (
	"bytes"
	_ "embed"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"sync"

	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
	"github.com/hmderdoc/avatar_chat_universal/internal/avatar"
	"github.com/hmderdoc/avatar_chat_universal/internal/bitmap"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

const (
	DefaultCellWidth  = 8
	DefaultCellHeight = 16
)

// graphic.js is the canonical Synchronet char/attr model. This sheet is the
// matching web CP437 atlas used to turn those CP437 cells into pixels.
//
//go:embed assets/cp437-ibm-vga8.png
var defaultGlyphSheetPNG []byte

var (
	defaultGlyphSheetOnce sync.Once
	defaultGlyphSheetImg  image.Image
)

type RenderOptions struct {
	CellWidth  int
	CellHeight int

	// GlyphSheet is optional. When present, it should be a 64x4 CP437 sprite
	// sheet ordered by byte value, matching spa_web5's cp437-ibm-vga8.png.
	GlyphSheet image.Image
}

func (o RenderOptions) normalized() RenderOptions {
	if o.CellWidth <= 0 {
		o.CellWidth = DefaultCellWidth
	}
	if o.CellHeight <= 0 {
		o.CellHeight = DefaultCellHeight
	}
	if o.GlyphSheet == nil && o.CellWidth == DefaultCellWidth && o.CellHeight == DefaultCellHeight {
		o.GlyphSheet = defaultGlyphSheet()
	}
	return o
}

func defaultGlyphSheet() image.Image {
	defaultGlyphSheetOnce.Do(func() {
		img, err := png.Decode(bytes.NewReader(defaultGlyphSheetPNG))
		if err == nil {
			defaultGlyphSheetImg = img
		}
	})
	return defaultGlyphSheetImg
}

func AvatarPNG(base64Avatar string, opts RenderOptions) ([]byte, error) {
	a, err := avatar.FromBase64(base64Avatar)
	if err != nil {
		return nil, err
	}
	f := ansi.NewFrame(0, 0, avatar.Width, avatar.Height, ansi.LightGray|ansi.BgBlack)
	a.RenderTo(f, 0, 0)
	return FramePNG(f, opts)
}

func BitmapPNG(img *bitmap.Image, opts RenderOptions) ([]byte, error) {
	if img == nil {
		return nil, fmt.Errorf("media: nil bitmap image")
	}
	opts = opts.normalized()
	dst := image.NewRGBA(image.Rect(0, 0, img.Width*opts.CellWidth, img.Height*opts.CellHeight))
	for y := 0; y < img.Height; y++ {
		for x := 0; x < img.Width; x++ {
			cell := img.Cells[y][x]
			drawCellRGB(dst, opts, x, y, cell.Char, palette256[cell.FG], palette256[cell.BG])
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, dst); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func FramePNG(f *ansi.Frame, opts RenderOptions) ([]byte, error) {
	if f == nil {
		return nil, fmt.Errorf("media: nil frame")
	}
	opts = opts.normalized()
	dst := image.NewRGBA(image.Rect(0, 0, f.W*opts.CellWidth, f.H*opts.CellHeight))
	for y := 0; y < f.H; y++ {
		for x := 0; x < f.W; x++ {
			cell := f.CellAt(x, y)
			if cell.Transparent {
				continue
			}
			drawCell(dst, opts, x, y, cell.Char, cell.Attr)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, dst); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func drawCell(dst *image.RGBA, opts RenderOptions, x, y int, ch byte, attr ansi.Attr) {
	drawCellRGB(dst, opts, x, y, ch, palette16[attr&0x0f], palette16[(attr>>4)&7])
}

func drawCellRGB(dst *image.RGBA, opts RenderOptions, x, y int, ch byte, fg, bg color.RGBA) {
	cellRect := image.Rect(
		x*opts.CellWidth,
		y*opts.CellHeight,
		(x+1)*opts.CellWidth,
		(y+1)*opts.CellHeight,
	)
	draw.Draw(dst, cellRect, image.NewUniform(bg), image.Point{}, draw.Src)

	if ch == 0 || ch == ' ' {
		return
	}
	if opts.GlyphSheet != nil {
		drawGlyph(dst, opts, cellRect.Min.X, cellRect.Min.Y, ch, fg)
		return
	}
	drawFallbackGlyph(dst, cellRect, ch, fg)
}

func drawGlyph(dst *image.RGBA, opts RenderOptions, px, py int, ch byte, fg color.RGBA) {
	sheetCols := 64
	srcX := int(ch%byte(sheetCols)) * opts.CellWidth
	srcY := int(ch/byte(sheetCols)) * opts.CellHeight
	srcRect := image.Rect(srcX, srcY, srcX+opts.CellWidth, srcY+opts.CellHeight)
	mask := image.NewAlpha(image.Rect(0, 0, opts.CellWidth, opts.CellHeight))
	for y := 0; y < opts.CellHeight; y++ {
		for x := 0; x < opts.CellWidth; x++ {
			r, g, b, a := opts.GlyphSheet.At(srcRect.Min.X+x, srcRect.Min.Y+y).RGBA()
			if a == 0 {
				continue
			}
			luma := (r + g + b) / 3
			if luma > 0x7fff {
				mask.SetAlpha(x, y, color.Alpha{A: 255})
			}
		}
	}
	draw.DrawMask(dst, image.Rect(px, py, px+opts.CellWidth, py+opts.CellHeight), image.NewUniform(fg), image.Point{}, mask, image.Point{}, draw.Over)
}

func drawFallbackGlyph(dst *image.RGBA, rect image.Rectangle, ch byte, fg color.RGBA) {
	if ch >= 0x20 && ch <= 0x7e {
		drawASCIIGlyph(dst, rect, ch, fg)
		return
	}
	switch ch {
	case 0xdb:
		draw.Draw(dst, rect, image.NewUniform(fg), image.Point{}, draw.Src)
	case 0xdf:
		r := image.Rect(rect.Min.X, rect.Min.Y, rect.Max.X, rect.Min.Y+rect.Dy()/2)
		draw.Draw(dst, r, image.NewUniform(fg), image.Point{}, draw.Src)
	case 0xdc:
		r := image.Rect(rect.Min.X, rect.Min.Y+rect.Dy()/2, rect.Max.X, rect.Max.Y)
		draw.Draw(dst, r, image.NewUniform(fg), image.Point{}, draw.Src)
	case 0xdd:
		r := image.Rect(rect.Min.X+rect.Dx()/2, rect.Min.Y, rect.Max.X, rect.Max.Y)
		draw.Draw(dst, r, image.NewUniform(fg), image.Point{}, draw.Src)
	case 0xde:
		r := image.Rect(rect.Min.X, rect.Min.Y, rect.Min.X+rect.Dx()/2, rect.Max.Y)
		draw.Draw(dst, r, image.NewUniform(fg), image.Point{}, draw.Src)
	case 0xb0, 0xb1, 0xb2:
		drawShade(dst, rect, ch, fg)
	case 0xb3, 0xba:
		drawVLine(dst, rect, fg)
	case 0xc4, 0xcd:
		drawHLine(dst, rect, fg)
	case 0xda, 0xc9:
		drawHLineTo(dst, rect, fg, "right")
		drawVLineTo(dst, rect, fg, "down")
	case 0xbf, 0xbb:
		drawHLineTo(dst, rect, fg, "left")
		drawVLineTo(dst, rect, fg, "down")
	case 0xc0, 0xc8:
		drawHLineTo(dst, rect, fg, "right")
		drawVLineTo(dst, rect, fg, "up")
	case 0xd9, 0xbc:
		drawHLineTo(dst, rect, fg, "left")
		drawVLineTo(dst, rect, fg, "up")
	case 0xc3, 0xcc:
		drawHLineTo(dst, rect, fg, "right")
		drawVLine(dst, rect, fg)
	case 0xb4, 0xb9:
		drawHLineTo(dst, rect, fg, "left")
		drawVLine(dst, rect, fg)
	case 0xc2, 0xcb:
		drawHLine(dst, rect, fg)
		drawVLineTo(dst, rect, fg, "down")
	case 0xc1, 0xca:
		drawHLine(dst, rect, fg)
		drawVLineTo(dst, rect, fg, "up")
	case 0xc5, 0xce:
		drawHLine(dst, rect, fg)
		drawVLine(dst, rect, fg)
	default:
		drawASCIIGlyph(dst, rect, '?', fg)
	}
}

func drawASCIIGlyph(dst *image.RGBA, rect image.Rectangle, ch byte, fg color.RGBA) {
	face := basicfont.Face7x13
	d := &font.Drawer{
		Dst:  dst,
		Src:  image.NewUniform(fg),
		Face: face,
	}
	w := d.MeasureString(string([]byte{ch})).Round()
	x := rect.Min.X + (rect.Dx()-w)/2
	y := rect.Min.Y + (rect.Dy()-face.Metrics().Height.Round())/2 + face.Metrics().Ascent.Round()
	d.Dot = fixed.P(x, y)
	d.DrawString(string([]byte{ch}))
}

func drawShade(dst *image.RGBA, rect image.Rectangle, ch byte, fg color.RGBA) {
	step := 4
	switch ch {
	case 0xb1:
		step = 3
	case 0xb2:
		step = 2
	}
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		for x := rect.Min.X; x < rect.Max.X; x++ {
			if (x+y)%step == 0 {
				dst.SetRGBA(x, y, fg)
			}
		}
	}
}

func drawHLine(dst *image.RGBA, rect image.Rectangle, fg color.RGBA) {
	y := rect.Min.Y + rect.Dy()/2
	draw.Draw(dst, image.Rect(rect.Min.X, y, rect.Max.X, y+max(1, rect.Dy()/8)), image.NewUniform(fg), image.Point{}, draw.Src)
}

func drawVLine(dst *image.RGBA, rect image.Rectangle, fg color.RGBA) {
	x := rect.Min.X + rect.Dx()/2
	draw.Draw(dst, image.Rect(x, rect.Min.Y, x+max(1, rect.Dx()/8), rect.Max.Y), image.NewUniform(fg), image.Point{}, draw.Src)
}

func drawHLineTo(dst *image.RGBA, rect image.Rectangle, fg color.RGBA, dir string) {
	y := rect.Min.Y + rect.Dy()/2
	x := rect.Min.X + rect.Dx()/2
	r := image.Rect(x, y, rect.Max.X, y+max(1, rect.Dy()/8))
	if dir == "left" {
		r = image.Rect(rect.Min.X, y, x+max(1, rect.Dx()/8), y+max(1, rect.Dy()/8))
	}
	draw.Draw(dst, r, image.NewUniform(fg), image.Point{}, draw.Src)
}

func drawVLineTo(dst *image.RGBA, rect image.Rectangle, fg color.RGBA, dir string) {
	x := rect.Min.X + rect.Dx()/2
	y := rect.Min.Y + rect.Dy()/2
	r := image.Rect(x, y, x+max(1, rect.Dx()/8), rect.Max.Y)
	if dir == "up" {
		r = image.Rect(x, rect.Min.Y, x+max(1, rect.Dx()/8), y+max(1, rect.Dy()/8))
	}
	draw.Draw(dst, r, image.NewUniform(fg), image.Point{}, draw.Src)
}

var palette16 = [16]color.RGBA{
	{0x00, 0x00, 0x00, 0xff},
	{0x00, 0x00, 0xa8, 0xff},
	{0x00, 0xa8, 0x00, 0xff},
	{0x00, 0xa8, 0xa8, 0xff},
	{0xa8, 0x00, 0x00, 0xff},
	{0xa8, 0x00, 0xa8, 0xff},
	{0xa8, 0x54, 0x00, 0xff},
	{0xa8, 0xa8, 0xa8, 0xff},
	{0x54, 0x54, 0x54, 0xff},
	{0x54, 0x54, 0xfc, 0xff},
	{0x54, 0xfc, 0x54, 0xff},
	{0x54, 0xfc, 0xfc, 0xff},
	{0xfc, 0x54, 0x54, 0xff},
	{0xfc, 0x54, 0xfc, 0xff},
	{0xfc, 0xfc, 0x54, 0xff},
	{0xff, 0xff, 0xff, 0xff},
}

var palette256 = buildPalette256()

func buildPalette256() [256]color.RGBA {
	var p [256]color.RGBA
	ansi16 := [16]color.RGBA{
		{0x00, 0x00, 0x00, 0xff},
		{0x80, 0x00, 0x00, 0xff},
		{0x00, 0x80, 0x00, 0xff},
		{0x80, 0x80, 0x00, 0xff},
		{0x00, 0x00, 0x80, 0xff},
		{0x80, 0x00, 0x80, 0xff},
		{0x00, 0x80, 0x80, 0xff},
		{0xc0, 0xc0, 0xc0, 0xff},
		{0x80, 0x80, 0x80, 0xff},
		{0xff, 0x00, 0x00, 0xff},
		{0x00, 0xff, 0x00, 0xff},
		{0xff, 0xff, 0x00, 0xff},
		{0x00, 0x00, 0xff, 0xff},
		{0xff, 0x00, 0xff, 0xff},
		{0x00, 0xff, 0xff, 0xff},
		{0xff, 0xff, 0xff, 0xff},
	}
	copy(p[:16], ansi16[:])
	levels := [6]uint8{0x00, 0x5f, 0x87, 0xaf, 0xd7, 0xff}
	for i := 16; i <= 231; i++ {
		n := i - 16
		p[i] = color.RGBA{
			R: levels[n/36],
			G: levels[(n/6)%6],
			B: levels[n%6],
			A: 0xff,
		}
	}
	for i := 232; i <= 255; i++ {
		v := uint8(8 + (i-232)*10)
		p[i] = color.RGBA{R: v, G: v, B: v, A: 0xff}
	}
	return p
}
