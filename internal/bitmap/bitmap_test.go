package bitmap

import (
	"bytes"
	"compress/zlib"
	"encoding/hex"
	"strings"
	"testing"
)

// buildPayload synthesizes a BITMAP message for a given grid for round-
// trip testing. Cells are encoded in the same row-major fg/bg/char layout
// the JS writer produces.
func buildPayload(t *testing.T, width, height int, cells [][]Cell, from string) string {
	t.Helper()
	total := width * height
	raw := make([]byte, 1+total*3)
	raw[0] = byte(height)
	for i := 0; i < total; i++ {
		y := i / width
		x := i % width
		c := cells[y][x]
		raw[1+i] = c.FG
		raw[1+total+i] = c.BG
		raw[1+total*2+i] = c.Char
	}
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	if _, err := zw.Write(raw); err != nil {
		t.Fatal(err)
	}
	zw.Close()
	return "[BITMAP|" + itoa(width) + "|" + itoa(height) + "|" + from + "|" + hex.EncodeToString(buf.Bytes()) + "]"
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

func TestParseRoundTrip(t *testing.T) {
	cells := [][]Cell{
		{{Char: 'A', FG: 7, BG: 0}, {Char: 'B', FG: 12, BG: 4}, {Char: 'C', FG: 14, BG: 1}},
		{{Char: '1', FG: 10, BG: 0}, {Char: '2', FG: 11, BG: 0}, {Char: '3', FG: 9, BG: 0}},
	}
	msg := buildPayload(t, 3, 2, cells, "alice")
	img, err := Parse(msg)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if img.Width != 3 || img.Height != 2 {
		t.Errorf("dims = %dx%d, want 3x2", img.Width, img.Height)
	}
	if img.From != "alice" {
		t.Errorf("from = %q, want alice", img.From)
	}
	for y := 0; y < 2; y++ {
		for x := 0; x < 3; x++ {
			got := img.Cells[y][x]
			want := cells[y][x]
			if got != want {
				t.Errorf("cell (%d,%d) = %+v, want %+v", x, y, got, want)
			}
		}
	}
}

func TestIsBitmap(t *testing.T) {
	if !IsBitmap("[BITMAP|1|1|alice|aabbcc]") {
		t.Error("should recognize valid envelope")
	}
	for _, s := range []string{"", "BITMAP|1|1|alice|aabbcc", "[BITMAP|", "[NOT-BITMAP]", "[BITMAP|]"} {
		if IsBitmap(s) {
			t.Errorf("should reject %q", s)
		}
	}
}

func TestParseRejectsBadInput(t *testing.T) {
	cases := []string{
		"",
		"hello world",
		"[BITMAP|0|1|alice|aabb]",     // zero width
		"[BITMAP|1|1|alice|]",         // empty hex
		"[BITMAP|1|1|alice|GG]",       // non-hex
		"[BITMAP|1|1|alice|aabbc]",    // odd hex length
	}
	for _, s := range cases {
		if _, err := Parse(s); err == nil {
			t.Errorf("expected parse failure for %q", s)
		}
	}
}

func TestParseShortEnvelope(t *testing.T) {
	if _, err := Parse("[BITMAP|" + strings.Repeat("a", 0) + "]"); err == nil {
		t.Error("empty envelope should fail")
	}
}
