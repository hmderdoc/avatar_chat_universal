package ansi

import "testing"

func TestSGRBasicColors(t *testing.T) {
	cases := []struct {
		attr Attr
		want string
	}{
		{LightGray, "\x1b[0;37;40m"},                  // default fg, no bg
		{White, "\x1b[0;1;37;40m"},                    // bright white = bold + 37
		{Red, "\x1b[0;31;40m"},                        // CGA red maps to ANSI 31
		{LightRed, "\x1b[0;1;31;40m"},                 // bright red (bit 3)
		{LightGray | BgBlue, "\x1b[0;37;44m"},         // bg blue
		{Yellow | BgRed, "\x1b[0;1;33;41m"},           // bright yellow on red
		{LightGray | Blink, "\x1b[0;5;37;40m"},        // blink bit
		{White | BgBlue | Blink, "\x1b[0;1;5;37;44m"}, // all flags
	}
	for _, c := range cases {
		got := c.attr.SGR()
		if got != c.want {
			t.Errorf("Attr(0x%02x).SGR() = %q, want %q", c.attr, got, c.want)
		}
	}
}

func TestFGBGComposition(t *testing.T) {
	a := Attr(0)
	a = FG(a, LightGreen)
	a = BG(a, Red)
	if (a & 0x0F) != LightGreen {
		t.Errorf("fg = %d, want %d", a&0x0F, LightGreen)
	}
	if ((a >> 4) & 7) != Red {
		t.Errorf("bg = %d, want %d", (a>>4)&7, Red)
	}
}
