package media

import (
	"bytes"
	"image/png"
	"testing"

	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
)

func TestFramePNGDimensions(t *testing.T) {
	f := ansi.NewFrame(0, 0, 2, 3, ansi.LightGray|ansi.BgBlack)
	f.SetCell(0, 0, 0xdb, ansi.White|ansi.BgBlue)
	data, err := FramePNG(f, RenderOptions{CellWidth: 4, CellHeight: 5})
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 8 || img.Bounds().Dy() != 15 {
		t.Fatalf("bounds = %v", img.Bounds())
	}
}
