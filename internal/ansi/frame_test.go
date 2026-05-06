package ansi

import (
	"bytes"
	"strings"
	"testing"
)

func TestFrameInitialRenderFullPaint(t *testing.T) {
	f := NewFrame(0, 0, 4, 2, LightGray)
	var buf bytes.Buffer
	if err := f.Render(&buf); err != nil {
		t.Fatalf("render: %v", err)
	}
	out := buf.String()
	// Expect two cursor moves (one per row) and 8 space chars + SGR + ESC[H.
	if !strings.Contains(out, "\x1b[1;1H") || !strings.Contains(out, "\x1b[2;1H") {
		t.Errorf("expected row cursor moves, got %q", out)
	}
	spaces := strings.Count(out, " ")
	if spaces != 8 {
		t.Errorf("expected 8 spaces (full paint), got %d in %q", spaces, out)
	}
}

func TestFrameSecondRenderEmitsNothingIfUnchanged(t *testing.T) {
	f := NewFrame(0, 0, 4, 2, LightGray)
	var buf bytes.Buffer
	_ = f.Render(&buf)
	buf.Reset()
	if err := f.Render(&buf); err != nil {
		t.Fatalf("render: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("expected empty diff, got %d bytes: %q", buf.Len(), buf.String())
	}
}

func TestFrameDirtyDiff(t *testing.T) {
	f := NewFrame(0, 0, 10, 3, LightGray)
	var buf bytes.Buffer
	_ = f.Render(&buf) // initial paint
	buf.Reset()

	f.SetCell(5, 1, 'X', LightRed)
	if err := f.Render(&buf); err != nil {
		t.Fatalf("render: %v", err)
	}
	out := buf.String()
	// Expect a single cursor move to (col=6, row=2) and a single 'X'.
	if !strings.Contains(out, "\x1b[2;6H") {
		t.Errorf("expected cursor move to row 2 col 6, got %q", out)
	}
	if strings.Count(out, "X") != 1 {
		t.Errorf("expected exactly one X, got %q", out)
	}
	// And no cursor moves for unchanged rows.
	if strings.Contains(out, "\x1b[1;") || strings.Contains(out, "\x1b[3;") {
		t.Errorf("unchanged rows should not be repainted: %q", out)
	}
}

func TestFrameDiffShrinksToChangedSpan(t *testing.T) {
	f := NewFrame(0, 0, 20, 1, LightGray)
	var buf bytes.Buffer
	_ = f.Render(&buf)
	buf.Reset()

	f.SetCell(3, 0, 'a', LightGray)
	f.SetCell(7, 0, 'b', LightGray)
	_ = f.Render(&buf)
	out := buf.String()
	// We paint columns 3..7 inclusive — 5 cells.
	// 4 of those are unchanged spaces; 'a' and 'b' are interleaved.
	cellsWritten := 0
	for _, c := range out {
		if c == 'a' || c == 'b' || c == ' ' {
			cellsWritten++
		}
	}
	if cellsWritten != 5 {
		t.Errorf("expected 5 painted cells (span 3..7), got %d in %q", cellsWritten, out)
	}
}

func TestPrintAtRespectsAttrAndWraps(t *testing.T) {
	f := NewFrame(0, 0, 5, 2, LightGray)
	f.PrintAt(3, 0, []byte("abcde"), LightRed)
	// "ab" goes at row 0 col 3..4, then wraps to row 1 col 0..2 with "cde"
	if f.cur[0][3].Char != 'a' || f.cur[0][3].Attr != LightRed {
		t.Errorf("row 0 col 3: got %+v", f.cur[0][3])
	}
	if f.cur[0][4].Char != 'b' {
		t.Errorf("row 0 col 4: got %+v", f.cur[0][4])
	}
	if f.cur[1][0].Char != 'c' {
		t.Errorf("row 1 col 0: got %+v", f.cur[1][0])
	}
	if f.cur[1][2].Char != 'e' {
		t.Errorf("row 1 col 2: got %+v", f.cur[1][2])
	}
}

func TestFrameOutOfBoundsSetCellNoop(t *testing.T) {
	f := NewFrame(0, 0, 3, 3, LightGray)
	f.SetCell(-1, 0, 'X', LightRed)
	f.SetCell(3, 0, 'X', LightRed)
	f.SetCell(0, 5, 'X', LightRed)
	// Should not panic and should not have written anywhere.
	for r := 0; r < f.H; r++ {
		for c := 0; c < f.W; c++ {
			if f.cur[r][c].Char != ' ' {
				t.Errorf("OOB write leaked into (%d,%d)", c, r)
			}
		}
	}
}
