package telnetvision

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
)

// framePayload mirrors telnetvision/wire.py frame_payload (mode=ramp=0).
func framePayload(cols, rows int, caption string, pixels []byte) []byte {
	p := []byte{msgFrame}
	p = binary.BigEndian.AppendUint16(p, uint16(cols))
	p = binary.BigEndian.AppendUint16(p, uint16(rows))
	p = append(p, 0, 0) // mode, ramp
	p = binary.BigEndian.AppendUint16(p, uint16(len(caption)))
	p = append(p, caption...)
	return append(p, pixels...)
}

func TestDecodeFrame(t *testing.T) {
	px := []byte{10, 11, 12, 20, 21, 22, 30, 31, 32, 40, 41, 42} // 2 rows(px) x 2 cols x 3
	fr, ok := decodeFrame(framePayload(2, 1, "hi there", px))
	if !ok {
		t.Fatal("decodeFrame failed")
	}
	if fr.Cols != 2 || fr.Rows != 1 || fr.Caption != "hi there" {
		t.Fatalf("bad header: %+v", fr)
	}
	if !bytes.Equal(fr.Pixels, px) {
		t.Fatalf("pixels mismatch")
	}
	// Truncated / non-frame payloads are rejected.
	if _, ok := decodeFrame([]byte{msgFrame, 0, 2}); ok {
		t.Fatal("short payload should fail")
	}
	if _, ok := decodeFrame([]byte{0x99, 0, 0, 0, 0, 0, 0, 0, 0}); ok {
		t.Fatal("non-frame type should fail")
	}
}

func TestReadMsgRoundTrip(t *testing.T) {
	payload := framePayload(2, 1, "", make([]byte, 12))
	var buf bytes.Buffer
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(payload)))
	buf.Write(hdr[:])
	buf.Write(payload)
	got, err := readMsg(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("readMsg payload mismatch")
	}
}

func TestRenderTo_Truecolor(t *testing.T) {
	px := []byte{10, 11, 12, 20, 21, 22, 30, 31, 32, 40, 41, 42}
	fr, _ := decodeFrame(framePayload(2, 1, "", px))
	dst := ansi.NewFrame(0, 0, 2, 1, 0)
	fr.RenderTo(dst, 0, 0, 2, 1, RenderOpts{Truecolor: true})

	c0 := dst.CellAt(0, 0)
	if !c0.True || c0.Char != halfBlock {
		t.Fatalf("cell0 not a truecolor half-block: %+v", c0)
	}
	if c0.Fg != (ansi.RGB{R: 10, G: 11, B: 12}) || c0.Bg != (ansi.RGB{R: 30, G: 31, B: 32}) {
		t.Fatalf("cell0 colors wrong: fg=%+v bg=%+v", c0.Fg, c0.Bg)
	}
	c1 := dst.CellAt(1, 0)
	if c1.Fg != (ansi.RGB{R: 20, G: 21, B: 22}) || c1.Bg != (ansi.RGB{R: 40, G: 41, B: 42}) {
		t.Fatalf("cell1 colors wrong: fg=%+v bg=%+v", c1.Fg, c1.Bg)
	}
}

func TestRenderTo_CGA(t *testing.T) {
	px := []byte{255, 0, 0, 0, 0, 255} // 1x1: top=red, bottom=blue (6 bytes)
	fr, _ := decodeFrame(framePayload(1, 1, "", px))
	dst := ansi.NewFrame(0, 0, 1, 1, 0)
	fr.RenderTo(dst, 0, 0, 1, 1, RenderOpts{Truecolor: false, Saturation: 1.0, Dither: false})
	c := dst.CellAt(0, 0)
	if c.True || c.Char != halfBlock {
		t.Fatalf("CGA cell should be non-true half-block: %+v", c)
	}
	// Pure red nearest-quantizes to CGA Red(4); pure blue to Blue(1) (bg 0-7).
	if (c.Attr & 0x0f) != ansi.Red {
		t.Errorf("fg = %d, want Red(4)", c.Attr&0x0f)
	}
	if ((c.Attr >> 4) & 0x07) != ansi.Blue {
		t.Errorf("bg = %d, want Blue(1)", (c.Attr>>4)&0x07)
	}
}

func TestRenderTo_ScalesToRegion(t *testing.T) {
	// A 1x1 source should fill a 4x2 region without panicking.
	px := []byte{1, 2, 3, 4, 5, 6}
	fr, _ := decodeFrame(framePayload(1, 1, "", px))
	dst := ansi.NewFrame(0, 0, 4, 2, 0)
	fr.RenderTo(dst, 0, 0, 4, 2, RenderOpts{Truecolor: true})
	if !dst.CellAt(3, 1).True {
		t.Fatal("region not fully painted")
	}
}
