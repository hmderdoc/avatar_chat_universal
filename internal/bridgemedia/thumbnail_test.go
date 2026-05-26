package bridgemedia

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func TestThumbnailJPEG_ScalesDownAndEncodes(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 500, 400))
	for y := 0; y < 400; y++ {
		for x := 0; x < 500; x++ {
			src.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, src); err != nil {
		t.Fatal(err)
	}
	out, ok := ThumbnailJPEG(buf.Bytes(), 320, 200*1024)
	if !ok {
		t.Fatal("expected a thumbnail")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if format != "jpeg" {
		t.Errorf("format = %s, want jpeg", format)
	}
	if cfg.Width > 320 || cfg.Height > 320 {
		t.Errorf("thumbnail %dx%d exceeds 320 on the long edge", cfg.Width, cfg.Height)
	}
	if len(out) > 200*1024 {
		t.Errorf("thumbnail %d bytes exceeds cap", len(out))
	}
}

func TestThumbnailJPEG_RejectsNonImage(t *testing.T) {
	if _, ok := ThumbnailJPEG([]byte("not an image at all"), 320, 200*1024); ok {
		t.Error("expected failure on non-image bytes")
	}
}
