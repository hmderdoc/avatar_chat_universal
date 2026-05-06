package ansi

import (
	"testing"
)

func TestDecodeChars(t *testing.T) {
	cases := []struct {
		in       []byte
		wantType KeyType
		wantRune rune
		wantCons int
	}{
		{[]byte("a"), KeyChar, 'a', 1},
		{[]byte("Z"), KeyChar, 'Z', 1},
		{[]byte{0x09}, KeyTab, 0, 1},
		{[]byte{0x0A}, KeyEnter, 0, 1},
		{[]byte{0x0D}, KeyEnter, 0, 1},
		{[]byte{0x08}, KeyBackspace, 0, 1},
		{[]byte{0x7F}, KeyBackspace, 0, 1},
		{[]byte{0x01}, KeyChar, 1, 1}, // ctrl-A
	}
	for _, c := range cases {
		k, n, ok := tryDecode(c.in, false)
		if !ok {
			t.Errorf("tryDecode(%q) failed", c.in)
			continue
		}
		if k.Type != c.wantType || n != c.wantCons {
			t.Errorf("tryDecode(%q): got type=%d cons=%d, want type=%d cons=%d", c.in, k.Type, n, c.wantType, c.wantCons)
		}
		if c.wantType == KeyChar && k.Rune != c.wantRune {
			t.Errorf("tryDecode(%q): rune=%q, want %q", c.in, k.Rune, c.wantRune)
		}
	}
}

func TestDecodeArrowsCSI(t *testing.T) {
	cases := []struct {
		in   []byte
		want KeyType
	}{
		{[]byte("\x1b[A"), KeyUp},
		{[]byte("\x1b[B"), KeyDown},
		{[]byte("\x1b[C"), KeyRight},
		{[]byte("\x1b[D"), KeyLeft},
		{[]byte("\x1b[H"), KeyHome},
		{[]byte("\x1b[F"), KeyEnd},
		{[]byte("\x1b[5~"), KeyPgUp},
		{[]byte("\x1b[6~"), KeyPgDn},
		{[]byte("\x1b[3~"), KeyDelete},
		{[]byte("\x1b[15~"), KeyF5},
		{[]byte("\x1b[24~"), KeyF12},
	}
	for _, c := range cases {
		k, n, ok := tryDecode(c.in, false)
		if !ok {
			t.Errorf("tryDecode(%q): incomplete", c.in)
			continue
		}
		if k.Type != c.want {
			t.Errorf("tryDecode(%q): got type=%d, want %d", c.in, k.Type, c.want)
		}
		if n != len(c.in) {
			t.Errorf("tryDecode(%q): consumed %d, want %d", c.in, n, len(c.in))
		}
	}
}

func TestDecodeSS3Variants(t *testing.T) {
	cases := []struct {
		in   []byte
		want KeyType
	}{
		{[]byte("\x1bOA"), KeyUp},
		{[]byte("\x1bOD"), KeyLeft},
		{[]byte("\x1bOP"), KeyF1},
		{[]byte("\x1bOS"), KeyF4},
	}
	for _, c := range cases {
		k, n, ok := tryDecode(c.in, false)
		if !ok || k.Type != c.want || n != 3 {
			t.Errorf("tryDecode(%q): got type=%d cons=%d ok=%v, want %d 3 true", c.in, k.Type, n, ok, c.want)
		}
	}
}

func TestDecodeIncompleteCSIWaits(t *testing.T) {
	_, _, ok := tryDecode([]byte("\x1b["), false)
	if ok {
		t.Errorf("partial CSI should not decode")
	}
	_, _, ok = tryDecode([]byte("\x1b[1"), false)
	if ok {
		t.Errorf("partial CSI with param should not decode")
	}
}

func TestDecodeBareEscWaitsThenFlushes(t *testing.T) {
	// Without flush: ESC alone is incomplete.
	_, _, ok := tryDecode([]byte("\x1b"), false)
	if ok {
		t.Errorf("bare ESC should not decode without flush")
	}
	// With flush: ESC alone decodes to KeyEsc.
	k, n, ok := tryDecode([]byte("\x1b"), true)
	if !ok || k.Type != KeyEsc || n != 1 {
		t.Errorf("flushed bare ESC: type=%d n=%d ok=%v", k.Type, n, ok)
	}
}
