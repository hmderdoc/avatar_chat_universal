package bitmap

import "testing"

func TestEncodeRoundTrip(t *testing.T) {
	w, h := 5, 3
	cells := make([]Cell, w*h)
	for i := range cells {
		cells[i] = Cell{Char: byte(0xB0 + i%3), FG: uint8(i % 16), BG: uint8(i % 8)}
	}
	cells[4].Char = 0 // NUL is sent as a space
	s := Encode(w, h, "Hm Derdoc!", cells)
	img, err := Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	if img.Width != w || img.Height != h || img.From != "HmDerdoc" {
		t.Fatalf("got %dx%d from %q", img.Width, img.Height, img.From)
	}
	for i, c := range cells {
		got := img.Cells[i/w][i%w]
		want := c
		if want.Char == 0 {
			want.Char = ' '
		}
		if got != want {
			t.Fatalf("cell %d = %+v, want %+v", i, got, want)
		}
	}
}

func TestWireColorIsSelfInverse(t *testing.T) {
	if WireColor(1) != 4 || WireColor(4) != 1 || WireColor(3) != 6 || WireColor(9) != 12 || WireColor(200) != 200 {
		t.Fatal("palette swap table is wrong")
	}
	for i := 0; i < 256; i++ {
		if WireColor(WireColor(uint8(i))) != uint8(i) {
			t.Fatalf("WireColor not self-inverse at %d", i)
		}
	}
}

func TestSanitizeFrom(t *testing.T) {
	if got := SanitizeFrom("a|b c_d.e-fé" + "0123456789012345678901234567890123"); got != "abc_d.e-f01234567890123456789012" {
		t.Fatalf("got %q", got)
	}
}
