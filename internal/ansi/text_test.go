package ansi

import "testing"

func TestToCP437(t *testing.T) {
	for _, tc := range []struct {
		in   string
		keep bool
		want string
	}{
		{"plain ascii", false, "plain ascii"},
		{"café", false, "caf\x82"},
		{"don’t “quote” — ok…", false, `don't "quote" - ok...`},
		{"½ ░▒▓ █", false, "\xAB \xB0\xB1\xB2 \xDB"},
		{"hi 👍🏽 ❤️", false, "hi ?? ?"},
		{"e\u0301", false, "e"},
		{"\x1b[2Jboom\x07\x08", false, "[2Jboom"},
		{"a\tb\r\nc", false, "a bc"},
		{"\x01rred\x01n text", false, "red text"},
		{"\x01wname\x01n here", true, "\x01wname\x01n here"},
		{"\x02jBob is here.", true, "\x02jBob is here."},
		{"\x02jBob", false, "jBob"},
		{"raw \x82 byte", false, "raw \x82 byte"},
		{"c1 \u0082", false, "c1 \x82"},
		{"smiley ☺ house ⌂", false, "smiley ? house ?"},
		{"日本", false, "??"},
	} {
		if got := ToCP437(tc.in, tc.keep); got != tc.want {
			t.Errorf("ToCP437(%q, %v) = %q, want %q", tc.in, tc.keep, got, tc.want)
		}
	}
}
