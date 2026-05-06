package ansi

import "testing"

func TestParseCursorReport(t *testing.T) {
	cases := []struct {
		in       string
		wantRows int
		wantCols int
		ok       bool
	}{
		{"\x1b[25;80R", 25, 80, true},
		{"\x1b[50;132R", 50, 132, true},
		{"\x1b[60;180R", 60, 180, true},
		{"junk before \x1b[24;80R and after", 24, 80, true},
		{"\x1b[0;80R", 0, 0, false},
		{"\x1b[25;0R", 0, 0, false},
		{"\x1b[25;80", 0, 0, false}, // missing R
		{"\x1b[?25h", 0, 0, false},  // unrelated CSI
		{"", 0, 0, false},
	}
	for _, c := range cases {
		r, col, ok := parseCursorReport([]byte(c.in))
		if ok != c.ok || r != c.wantRows || col != c.wantCols {
			t.Errorf("parseCursorReport(%q) = (%d,%d,%v), want (%d,%d,%v)",
				c.in, r, col, ok, c.wantRows, c.wantCols, c.ok)
		}
	}
}

func TestParseSize(t *testing.T) {
	cases := []struct {
		in       string
		wantRows int
		wantCols int
		ok       bool
	}{
		{"\x1b[8;25;80t", 25, 80, true},
		{"\x1b[8;50;132t", 50, 132, true},
		{"junk before \x1b[8;24;80t and after", 24, 80, true},
		{"\x1b[8;25;80", 0, 0, false},     // truncated, no t
		{"\x1b[8;0;80t", 0, 0, false},     // zero rows rejected
		{"\x1b[8;25;0t", 0, 0, false},     // zero cols rejected
		{"\x1b[?25h", 0, 0, false},        // unrelated CSI
		{"", 0, 0, false},
	}
	for _, c := range cases {
		r, col, ok := parseSize([]byte(c.in))
		if ok != c.ok || r != c.wantRows || col != c.wantCols {
			t.Errorf("parseSize(%q) = (%d,%d,%v), want (%d,%d,%v)",
				c.in, r, col, ok, c.wantRows, c.wantCols, c.ok)
		}
	}
}
