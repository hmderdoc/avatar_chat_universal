package ansi

import (
	"bytes"
	"strings"
	"testing"
)

func TestCellSGR_Truecolor(t *testing.T) {
	c := Cell{Char: 0xDF, True: true, Fg: RGB{10, 20, 30}, Bg: RGB{200, 100, 50}}
	got := c.SGR()
	want := "\x1b[0;38;2;10;20;30;48;2;200;100;50m"
	if got != want {
		t.Fatalf("truecolor SGR = %q, want %q", got, want)
	}
}

func TestCellSGR_CGAUnchanged(t *testing.T) {
	// A non-true cell must emit exactly the CGA attribute SGR (no regression).
	c := Cell{Char: 'A', Attr: LightCyan | BgBlue}
	if c.SGR() != (LightCyan | BgBlue).SGR() {
		t.Fatalf("CGA cell SGR diverged from Attr.SGR()")
	}
}

func TestFrameRender_TruecolorAndCGACoexist(t *testing.T) {
	f := NewFrame(0, 0, 2, 1, 0)
	f.SetCell(0, 0, 'A', Red)               // CGA
	f.SetCellTrue(1, 0, 0xDF, RGB{1, 2, 3}, RGB{4, 5, 6}) // truecolor half-block
	var buf bytes.Buffer
	if err := f.Render(&buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "38;2;1;2;3;48;2;4;5;6") {
		t.Errorf("missing truecolor SGR in output: %q", out)
	}
	if !strings.Contains(out, Red.SGR()) {
		t.Errorf("missing CGA SGR in output: %q", out)
	}
	// 0xDF should be present (CP437 default charset emits the raw byte).
	if !bytes.Contains(buf.Bytes(), []byte{0xDF}) {
		t.Errorf("missing half-block byte 0xDF in output")
	}
}

func TestFrameRender_TruecolorDeltaRedraw(t *testing.T) {
	f := NewFrame(0, 0, 1, 1, 0)
	f.SetCellTrue(0, 0, 0xDF, RGB{10, 10, 10}, RGB{20, 20, 20})
	var buf bytes.Buffer
	f.Render(&buf) // first paint
	buf.Reset()
	f.Render(&buf) // no change -> nothing emitted
	if buf.Len() != 0 {
		t.Errorf("expected no output on unchanged truecolor cell, got %q", buf.String())
	}
	f.SetCellTrue(0, 0, 0xDF, RGB{11, 10, 10}, RGB{20, 20, 20}) // change fg.R
	buf.Reset()
	f.Render(&buf)
	if !strings.Contains(buf.String(), "38;2;11;10;10") {
		t.Errorf("changed truecolor cell not redrawn: %q", buf.String())
	}
}
