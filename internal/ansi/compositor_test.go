package ansi

import (
	"bytes"
	"strings"
	"testing"
)

func TestCompositorTopOpaqueWins(t *testing.T) {
	bg := NewFrame(0, 0, 4, 1, LightGray)
	bg.PrintAt(0, 0, []byte("BBBB"), LightBlue)

	top := NewTransparentFrame(0, 0, 4, 1)
	top.SetCell(2, 0, 'X', LightRed) // only column 2 is opaque

	c := NewCompositor(bg, top)
	var buf bytes.Buffer
	if err := c.Render(&buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	// Expect to see B B X B (positions 0,1,2,3).
	if !strings.Contains(out, "B") || !strings.Contains(out, "X") {
		t.Errorf("missing expected glyphs: %q", out)
	}
	bCount := strings.Count(out, "B")
	if bCount < 3 {
		t.Errorf("expected 3 B's, got %d in %q", bCount, out)
	}
}

func TestCompositorAllTransparentEmitsDefault(t *testing.T) {
	a := NewTransparentFrame(0, 0, 4, 1)
	b := NewTransparentFrame(0, 0, 4, 1)
	c := NewCompositor(a, b)
	c.Default = Cell{Char: '#', Attr: LightRed}
	var buf bytes.Buffer
	if err := c.Render(&buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Count(out, "#") != 4 {
		t.Errorf("expected 4 default '#', got %q", out)
	}
}

func TestCompositorDirtyDiff(t *testing.T) {
	bg := NewFrame(0, 0, 10, 1, LightGray)
	bg.PrintAt(0, 0, []byte("..........."), LightGray)
	c := NewCompositor(bg)

	var buf bytes.Buffer
	_ = c.Render(&buf)
	buf.Reset()

	// No change → no output.
	if err := c.Render(&buf); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Errorf("unchanged composite should emit nothing, got %d bytes: %q", buf.Len(), buf.String())
	}

	// One cell change → small emit.
	bg.SetCell(5, 0, 'X', LightRed)
	if err := c.Render(&buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "X") {
		t.Errorf("expected X in diff, got %q", out)
	}
	if strings.Count(out, "X") != 1 {
		t.Errorf("expected exactly one X, got %q", out)
	}
}

func TestTransparentFrameInit(t *testing.T) {
	f := NewTransparentFrame(0, 0, 3, 2)
	for r := 0; r < 2; r++ {
		for col := 0; col < 3; col++ {
			if !f.cur[r][col].Transparent {
				t.Errorf("cell (%d,%d) should be transparent on fresh frame", col, r)
			}
		}
	}
	// SetCell creates an opaque cell.
	f.SetCell(1, 0, 'A', LightCyan)
	if f.cur[0][1].Transparent {
		t.Errorf("SetCell should produce an opaque cell")
	}
}
